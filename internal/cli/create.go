package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/config"
)

type createOpts struct {
	file, memory, disk, dataDisks, storage, version, output, bind, snapshot, owner string
	nodes, cpus                                                                    int
	dryRun, golden                                                                 bool
	ttl                                                                            time.Duration
}

// lease is the owner (by default the user) and lifetime the flags ask for.
func (o *createOpts) lease() cluster.Lease {
	owner := o.owner
	if owner == "" {
		owner = os.Getenv("USER")
	}
	return cluster.Lease{Owner: owner, TTL: o.ttl}
}

// saveBaseline saves the live snapshot name of a created cluster, unless name
// is empty or the snapshot exists (a create that is run again).
func saveBaseline(ctx context.Context, c *cluster.Cluster, name string) error {
	if name == "" || c.St.Snapshot(name) != nil {
		return nil
	}
	t0 := time.Now()
	if err := c.SnapshotSaveLive(ctx, name); err != nil {
		return fmt.Errorf("save snapshot %s: %w", name, err)
	}
	logf("live snapshot %s saved in %s", name, time.Since(t0).Round(time.Second))
	return nil
}

func createCmd() *cobra.Command {
	var o createOpts
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a cluster from a file and/or flags (re-run to resume a failed create)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := o.resolve(cmd, args)
			if err != nil {
				return err
			}
			if o.dryRun {
				plan, err := cluster.NewPlan(cfg)
				if err != nil {
					return err
				}
				logPlan(plan)
				if o.output == "json" {
					return printJSON(cfg)
				}
				b, err := cfg.YAML()
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(b)
				return err
			}
			if o.snapshot != "" {
				if err := cluster.CheckSnapshotName(o.snapshot); err != nil {
					return err
				}
			}
			c, err := cluster.Create(cmd.Context(), cfg, sink, o.lease())
			if err != nil {
				return createError(c, err)
			}
			logf("created in %s", c.St.Duration)
			if err := saveBaseline(cmd.Context(), c, o.snapshot); err != nil {
				return err
			}
			return printStatus(c.Status(cmd.Context()), o.output)
		},
	}
	o.addFlags(cmd)
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the resolved cluster file and exit")
	cmd.Flags().StringVar(&o.snapshot, "snapshot", "", "save a live snapshot with this name when the cluster is ready (kept if it exists)")
	cmd.Flags().StringVarP(&o.output, "output", "o", "table", "output format: table|json (with --dry-run: yaml|json)")
	return cmd
}

// logPlan summarizes what a create would set up.
func logPlan(p *cluster.Plan) {
	last := p.Nodes[len(p.Nodes)-1]
	logf("plan: %d node(s), %.1f GiB memory, %d GiB of thin disks, web UI from %s, SSH from port %d; ISO %s",
		len(p.Nodes), float64(p.MemoryMiB)/1024, p.DiskMiB>>10, p.Nodes[0].UI, p.Nodes[0].SSHPort, map[bool]string{true: "cached", false: "to download"}[p.ISOCached])
	if len(p.Nodes) > 1 {
		logf("plan: last node %s at %s, SSH port %d", last.Name, last.UI, last.SSHPort)
	}
	for _, w := range p.Warnings {
		logf("warning: %s", w)
	}
}

// createError tells how to go on after a failed create of an initialized cluster.
func createError(c *cluster.Cluster, err error) error {
	if c == nil {
		return err
	}
	name := c.Cfg.Name
	return &hintError{err: err, log: c.Dir.Log(""),
		hint: fmt.Sprintf("Re-run `proxbase create %s` to resume, or `proxbase destroy %s` to start over", name, name)}
}

// addFlags registers the flags that override fields of the cluster file.
func (o *createOpts) addFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVarP(&o.file, "file", "f", "", "cluster file (YAML)")
	f.IntVar(&o.nodes, "nodes", 0, "number of nodes")
	f.IntVar(&o.cpus, "cpus", 0, "vCPUs per node")
	f.StringVar(&o.memory, "memory", "", "memory per node, e.g. 4G")
	f.StringVar(&o.disk, "disk", "", "root disk size, e.g. 32G")
	f.StringVar(&o.dataDisks, "data-disks", "", "data disks per node: 2x32G, 32G,64G or none")
	f.StringVar(&o.storage, "storage", "", "data storage on the data disks: zfs, ceph or none")
	f.StringVar(&o.version, "pve-version", "", "Proxmox VE ISO version, e.g. 9.2 or 9.2-1")
	f.BoolVar(&o.golden, "golden", false, "clone nodes from a cached base image instead of installing each (faster)")
	f.StringVar(&o.bind, "bind-address", "", "address for the UI/SSH forwards (default $PROXBASE_BIND_ADDRESS or 127.0.0.1)")
	f.DurationVar(&o.ttl, "ttl", 0, "lifetime: proxbase gc deletes the cluster this long after the create (e.g. 2h; default: none)")
	f.StringVar(&o.owner, "owner", "", "who owns the cluster, e.g. a user or an agent (default $USER)")
}

func (o *createOpts) resolve(cmd *cobra.Command, args []string) (*config.Cluster, error) {
	cfg := &config.Cluster{}
	if o.file != "" {
		var err error
		if cfg, err = config.Load(o.file); err != nil {
			return nil, fmt.Errorf("%s: %w", o.file, err)
		}
	}
	f := cmd.Flags()
	ov := config.Overrides{Golden: o.golden}
	if len(args) > 0 {
		ov.Name = args[0]
	}
	set := func(flag string, dst **string, v *string) {
		if f.Changed(flag) {
			*dst = v
		}
	}
	if f.Changed("nodes") {
		ov.Nodes = &o.nodes
	}
	if f.Changed("cpus") {
		ov.CPUs = &o.cpus
	}
	set("memory", &ov.Memory, &o.memory)
	set("disk", &ov.Disk, &o.disk)
	set("data-disks", &ov.DataDisks, &o.dataDisks)
	set("storage", &ov.Storage, &o.storage)
	set("pve-version", &ov.Version, &o.version)
	set("bind-address", &ov.BindAddress, &o.bind)
	if env := os.Getenv("PROXBASE_BIND_ADDRESS"); ov.BindAddress == nil && cfg.Access.BindAddress == "" && env != "" {
		ov.BindAddress = &env
	}
	if err := ov.Apply(cfg); err != nil {
		return nil, err
	}
	for _, w := range cfg.Warnings() {
		logf("warning: %s", w)
	}
	return cfg, nil
}
