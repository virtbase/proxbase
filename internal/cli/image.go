package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/golden"
)

func imageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "image", Short: "Manage cached base images (create --golden)"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List base images and the clusters using them",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			images, err := golden.List()
			if err != nil {
				return err
			}
			users, err := golden.Users()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "KEY\tISO\tROOT\tCEPH\tCREATED\tUSED BY")
			for _, m := range images {
				ceph := m.Ceph
				if ceph == "" {
					ceph = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s %s\t%s\t%s\t%v\n", m.Key, m.ISO, m.Filesystem, m.Size, ceph, m.Created.Local().Format(time.DateTime), users[m.Key])
			}
			return w.Flush()
		},
	}
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Delete base images no cluster uses",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			images, err := golden.List()
			if err != nil {
				return err
			}
			users, err := golden.Users()
			if err != nil {
				return err
			}
			for _, m := range images {
				if len(users[m.Key]) > 0 {
					logf("keeping %s (used by %v)", m.Key, users[m.Key])
					continue
				}
				if err := golden.Remove(m.Key); err != nil {
					return err
				}
				logf("deleted %s", m.Key)
			}
			return nil
		},
	}
	cmd.AddCommand(list, prune)
	return cmd
}
