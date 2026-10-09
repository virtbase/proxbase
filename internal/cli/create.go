package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/config"
)

type createOpts struct {
	file, memory, disk, dataDisks, storage, version, output, bind string
	nodes, cpus                                                   int
	dryRun, golden                                                bool
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
			c, err := cluster.Create(cmd.Context(), cfg, sink)
			if err != nil {
				return createError(c, err)
			}
			logf("created in %s", c.St.Duration)
			return printStatus(c.Status(cmd.Context()), o.output)
		},
	}
	o.addFlags(cmd)
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the resolved cluster file and exit")
	cmd.Flags().StringVarP(&o.output, "output", "o", "table", "output format: table|json (with --dry-run: yaml|json)")
	return cmd
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
