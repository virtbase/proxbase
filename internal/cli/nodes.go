package cli

import (
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
