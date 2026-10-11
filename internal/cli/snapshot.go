package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
)

func snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Save and restore a whole cluster",
		Long: `snapshot saves and restores all disks of a cluster at once.

A snapshot of a stopped cluster holds its disks; after a restore, start the
cluster as usual. A live snapshot (save --live) is taken of the running cluster
and also holds the memory of every node; restoring it starts the cluster in that
state within seconds, without booting.`,
	}
	op := func(use, short string, f func(*cluster.Cluster, context.Context, string) error) *cobra.Command {
		return &cobra.Command{
			Use:   use + " [cluster] <snapshot>",
			Short: short,
			Args:  cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := openCluster(args[:len(args)-1])
				if err != nil {
					return err
				}
				name := args[len(args)-1]
				t0 := time.Now()
				if err := f(c, cmd.Context(), name); err != nil {
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
			fmt.Fprintln(w, "NAME\tCREATED\tNODES\tLIVE")
			for _, s := range c.St.Snapshots {
				fmt.Fprintf(w, "%s\t%s\t%d\t%v\n", s.Name, s.Created.Local().Format(time.DateTime), len(s.Nodes), s.Live)
			}
			return w.Flush()
		},
	}
	var live bool
	save := op("save", "Snapshot every disk (cluster stopped), or with --live the running cluster", func(c *cluster.Cluster, ctx context.Context, name string) error {
		if live {
			return c.SnapshotSaveLive(ctx, name)
		}
		return c.SnapshotSave(ctx, name)
	})
	save.Flags().BoolVar(&live, "live", false, "snapshot the running cluster, memory included; restoring it resumes the cluster")
	cmd.AddCommand(
		save,
		op("restore", "Reset every disk to a snapshot (cluster must be stopped); a live snapshot also starts the cluster", (*cluster.Cluster).SnapshotRestore),
		op("delete", "Delete a snapshot", (*cluster.Cluster).SnapshotDelete),
		list,
	)
	return cmd
}
