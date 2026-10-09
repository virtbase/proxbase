package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/golden"
	"github.com/virtbase/proxbase/internal/state"
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
			users, err := baseUsers()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "KEY\tISO\tROOT\tCREATED\tUSED BY")
			for _, m := range images {
				fmt.Fprintf(w, "%s\t%s\t%s %s\t%s\t%v\n", m.Key, m.ISO, m.Filesystem, m.Size, m.Created.Local().Format(time.DateTime), users[m.Key])
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
			users, err := baseUsers()
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

// baseUsers maps base image keys to the clusters whose disks are overlays of them.
func baseUsers() (map[string][]string, error) {
	names, err := state.List()
	if err != nil {
		return nil, err
	}
	users := map[string][]string{}
	for _, name := range names {
		st, err := state.ForCluster(name).Load()
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, n := range st.Nodes {
			if n.Base != "" && !seen[n.Base] {
				seen[n.Base] = true
				users[n.Base] = append(users[n.Base], name)
			}
		}
	}
	return users, nil
}
