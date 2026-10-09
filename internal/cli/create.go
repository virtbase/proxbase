package cli

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

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
			c, err := cluster.Create(cmd.Context(), cfg, logf)
			if err != nil {
				if c != nil {
					return fmt.Errorf("%w\n\nRe-run `proxbase create %s` to resume, or `proxbase destroy %s` to start over", err, cfg.Name, cfg.Name)
				}
				return err
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

var multiDiskRe = regexp.MustCompile(`^([0-9]+)x([0-9]+[MGT])$`)

func (o *createOpts) resolve(cmd *cobra.Command, args []string) (*config.Cluster, error) {
	cfg := &config.Cluster{}
	if o.file != "" {
		var err error
		if cfg, err = config.Load(o.file); err != nil {
			return nil, fmt.Errorf("%s: %w", o.file, err)
		}
	}
	if len(args) > 0 {
		cfg.Name = args[0]
	}
	f := cmd.Flags()
	d := &cfg.Nodes.Defaults
	if f.Changed("nodes") {
		cfg.Nodes.Count = o.nodes
	}
	if f.Changed("cpus") {
		d.CPUs = o.cpus
	}
	if f.Changed("memory") {
		d.Memory = o.memory
	}
	if f.Changed("disk") {
		d.RootDisk.Size = o.disk
	}
	if f.Changed("pve-version") {
		cfg.Proxmox.Version = o.version
	}
	if o.golden {
		cfg.Proxmox.Golden = true
	}
	if f.Changed("bind-address") {
		cfg.Access.BindAddress = o.bind
	} else if env := os.Getenv("PROXBASE_BIND_ADDRESS"); cfg.Access.BindAddress == "" && env != "" {
		cfg.Access.BindAddress = env
	}
	if f.Changed("data-disks") {
		disks, err := parseDisks(o.dataDisks)
		if err != nil {
			return nil, err
		}
		d.DataDisks = disks
		cfg.Storage.ZFS = nil // re-derive the default pool from the new disks
	}
	if f.Changed("storage") {
		switch o.storage {
		case "zfs":
			if len(d.DataDisks) == 0 && d.DataDisks != nil {
				return nil, fmt.Errorf("--storage zfs needs data disks")
			}
			cfg.Storage.Ceph = nil
		case "ceph":
			cfg.Storage.ZFS = []config.ZFSPool{}
			if cfg.Storage.Ceph == nil {
				cfg.Storage.Ceph = &config.Ceph{}
			}
			cfg.Storage.Ceph.Enabled = true
		case "none":
			cfg.Storage.ZFS = []config.ZFSPool{}
			cfg.Storage.Ceph = nil
		default:
			return nil, fmt.Errorf("--storage must be zfs, ceph or none")
		}
	}
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid cluster configuration:\n%w", err)
	}
	for _, w := range cfg.Warnings() {
		logf("warning: %s", w)
	}
	return cfg, nil
}

func parseDisks(s string) ([]config.Disk, error) {
	disks := []config.Disk{}
	if s == "none" || s == "0" || s == "" {
		return disks, nil
	}
	if m := multiDiskRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		for i := 0; i < n; i++ {
			disks = append(disks, config.Disk{Size: m[2]})
		}
		return disks, nil
	}
	for _, size := range strings.Split(s, ",") {
		disks = append(disks, config.Disk{Size: strings.TrimSpace(size)})
	}
	return disks, nil
}
