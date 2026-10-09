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
	cmd := &cobra.Command{Use: "snapshot", Short: "Save and restore all disks of a stopped cluster"}
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
			fmt.Fprintln(w, "NAME\tCREATED\tNODES")
			for _, s := range c.St.Snapshots {
				fmt.Fprintf(w, "%s\t%s\t%d\n", s.Name, s.Created.Local().Format(time.DateTime), len(s.Nodes))
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(
		op("save", "Snapshot every disk (cluster must be stopped)", (*cluster.Cluster).SnapshotSave),
		op("restore", "Reset every disk to a snapshot (cluster must be stopped)", (*cluster.Cluster).SnapshotRestore),
		op("delete", "Delete a snapshot", (*cluster.Cluster).SnapshotDelete),
		list,
	)
	return cmd
}
