package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func nodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "node", Short: "Add or remove nodes"}
	var count int
	add := &cobra.Command{
		Use:   "add [cluster]",
		Short: "Add nodes: install, join, and set up ZFS/Ceph as configured",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			t0 := time.Now()
			if err := c.AddNodes(cmd.Context(), count); err != nil {
				return err
			}
			logf("added %d node(s) in %s", count, time.Since(t0).Round(time.Second))
			return printStatus(c.Status(cmd.Context()), "table")
		},
	}
	add.Flags().IntVar(&count, "count", 1, "number of nodes to add")
	var force bool
	remove := &cobra.Command{
		Use:   "remove [cluster] <node>",
		Short: "Remove a node: drain and destroy its Ceph daemons, delnode, delete its VM",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args[:len(args)-1])
			if err != nil {
				return err
			}
			t0 := time.Now()
			if err := c.RemoveNode(cmd.Context(), args[len(args)-1], force); err != nil {
				return err
			}
			logf("removed %s in %s", args[len(args)-1], time.Since(t0).Round(time.Second))
			return printStatus(c.Status(cmd.Context()), "table")
		},
	}
	remove.Flags().BoolVar(&force, "force", false, "remove even with guests on the node or Ceph pools left degraded")
	cmd.AddCommand(add, remove)
	return cmd
}

func snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "snapshot", Short: "Save and restore all disks of a stopped cluster"}
	op := func(use, short string, f func(c clusterOps, name string) error) *cobra.Command {
		return &cobra.Command{
			Use:   use + " [cluster] <snapshot>",
			Short: short,
			Args:  cobra.RangeArgs(1, 2),
			RunE: func(_ *cobra.Command, args []string) error {
				c, err := openCluster(args[:len(args)-1])
				if err != nil {
					return err
				}
				name := args[len(args)-1]
				t0 := time.Now()
				if err := f(c, name); err != nil {
					return err
				}
				logf("%s %s: done in %s", use, name, time.Since(t0).Round(time.Millisecond))
				return nil
			},
		}
	}
	list := &cobra.Command{
		Use:   "list [cluster]",
		Short: "List snapshots",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCREATED\tNODES")
			for _, s := range c.St.Snapshots {
				fmt.Fprintf(w, "%s\t%s\t%d\n", s.Name, s.Created.Local().Format(time.DateTime), len(s.Nodes))
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(
		op("save", "Snapshot every disk (cluster must be stopped)", clusterOps.SnapshotSave),
		op("restore", "Reset every disk to a snapshot (cluster must be stopped)", clusterOps.SnapshotRestore),
		op("delete", "Delete a snapshot", clusterOps.SnapshotDelete),
		list,
	)
	return cmd
}

type clusterOps interface {
	SnapshotSave(string) error
	SnapshotRestore(string) error
	SnapshotDelete(string) error
}
