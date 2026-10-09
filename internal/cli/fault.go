package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/state"
)

func faultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fault",
		Short: "Inject failures: power loss, hangs, pulled cables, partitions, latency and loss",
		Long: `fault injects failures into a running cluster to test HA, fencing, Ceph and
applications. Node faults act on the VM (QEMU); network faults act on the internal
switch. "fault clear" undoes everything except killed nodes, which "start" boots
again. Stopping the cluster also clears all faults.`,
	}
	nodeOp := func(use, short string, f func(*cobra.Command, *cluster.Cluster, string) error) *cobra.Command {
		return &cobra.Command{
			Use:   use + " [cluster] <node>",
			Short: short,
			Args:  cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, node, err := clusterNode(args)
				if err != nil {
					return err
				}
				if err := f(cmd, c, node.Name); err != nil {
					return err
				}
				logf("%s %s: done", use, node.Name)
				return nil
			},
		}
	}
	kill := nodeOp("kill", "Power a node off hard (start brings it back)", func(_ *cobra.Command, c *cluster.Cluster, n string) error {
		return c.KillNode(n)
	})
	freeze := nodeOp("freeze", "Stop a node's vCPUs (the node hangs)", func(cmd *cobra.Command, c *cluster.Cluster, n string) error {
		return c.FreezeNode(cmd.Context(), n)
	})
	thaw := nodeOp("thaw", "Resume a frozen node", func(cmd *cobra.Command, c *cluster.Cluster, n string) error {
		return c.ThawNode(cmd.Context(), n)
	})

	var linkNet string
	var down, up bool
	link := nodeOp("link", "Pull (--down) or plug in (--up) a node's cable on a network", func(cmd *cobra.Command, c *cluster.Cluster, n string) error {
		if down == up {
			return fmt.Errorf("use exactly one of --down and --up")
		}
		return c.SetLink(cmd.Context(), n, linkNet, up)
	})
	link.Flags().StringVar(&linkNet, "network", "", "network name (default: corosync link0)")
	link.Flags().BoolVar(&down, "down", false, "pull the cable")
	link.Flags().BoolVar(&up, "up", false, "plug the cable in")

	var partNet string
	partition := &cobra.Command{
		Use:   "partition [cluster] <group> [<group>...]",
		Short: "Split a network: each group is a comma-separated node list; unlisted nodes form one more group",
		Example: `  proxbase fault partition lab pve3              # isolate pve3
  proxbase fault partition lab pve1,pve2 pve3     # pve1+pve2 | pve3 | the rest`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var cargs []string
			if len(args) > 1 && state.ForCluster(args[0]).Exists() {
				cargs, args = args[:1], args[1:]
			}
			c, err := openCluster(cargs)
			if err != nil {
				return err
			}
			var groups [][]string
			for _, g := range args {
				groups = append(groups, strings.Split(g, ","))
			}
			if err := c.Partition(partNet, groups); err != nil {
				return err
			}
			logf("partition applied")
			return nil
		},
	}
	partition.Flags().StringVar(&partNet, "network", "", "network name (default: corosync link0)")

	var degNet string
	var delay time.Duration
	var loss float64
	degrade := &cobra.Command{
		Use:     "degrade [cluster]",
		Short:   "Add latency and frame loss to a network",
		Example: `  proxbase fault degrade lab --network storage --delay 20ms --loss 1`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			if err := c.Degrade(degNet, delay, loss/100); err != nil {
				return err
			}
			logf("network degraded")
			return nil
		},
	}
	degrade.Flags().StringVar(&degNet, "network", "", "network name (default: corosync link0)")
	degrade.Flags().DurationVar(&delay, "delay", 0, "delay added to every frame, per direction (e.g. 20ms)")
	degrade.Flags().Float64Var(&loss, "loss", 0, "percentage of frames dropped, per direction")

	clear := &cobra.Command{
		Use:   "clear [cluster]",
		Short: "Undo all faults (killed nodes need start)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			done, err := c.ClearFaults(cmd.Context())
			for _, d := range done {
				logf("cleared %s", d)
			}
			return err
		},
	}
	cmd.AddCommand(kill, freeze, thaw, link, partition, degrade, clear)
	return cmd
}
