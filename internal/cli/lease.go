package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
)

func ttlCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ttl [cluster] <duration|none>",
		Short: "Set how long a cluster may live: proxbase gc deletes it once expired",
		Long: `ttl sets the cluster's expiry to the duration from now (e.g. 2h, 90m), or removes
it with none. proxbase gc deletes expired clusters; nothing deletes them by itself.`,
		Example: `  proxbase ttl lab 4h
  proxbase ttl lab none`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			var ttl time.Duration
			if v := args[len(args)-1]; v != "none" {
				var err error
				if ttl, err = time.ParseDuration(v); err != nil || ttl <= 0 {
					return fmt.Errorf("%q is not a positive duration like 2h, or none", v)
				}
			}
			c, err := openCluster(args[:len(args)-1])
			if err != nil {
				return err
			}
			if err := c.SetExpiry(ttl); err != nil {
				return err
			}
			if ttl == 0 {
				logf("cluster %s does not expire", c.Cfg.Name)
				return nil
			}
			logf("cluster %s expires at %s", c.Cfg.Name, c.St.ExpiresAt.Local().Format(time.DateTime))
			return nil
		},
	}
}

func gcCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete expired clusters (see create --ttl and proxbase ttl)",
		Long: `gc lists the clusters whose lifetime has ended and, with --yes, destroys them
with their disks, snapshots and background job files. Clusters with a running
create or node change are left alone. Run it from cron or a CI job.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			res, err := cluster.GC(time.Now(), !yes, sink)
			if err != nil {
				return err
			}
			for _, s := range res.Skipped {
				logf("skipped %s", s)
			}
			switch {
			case len(res.Deleted) == 0:
				logf("no expired clusters")
			case yes:
				logf("deleted %v", res.Deleted)
			default:
				logf("expired: %v; run proxbase gc --yes to delete them", res.Deleted)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "delete the expired clusters (without it, only list them)")
	return cmd
}

// expiry describes when a cluster expires, relative to now.
func expiry(s *cluster.Status, now time.Time) string {
	if s.Expires == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, s.Expires)
	if err != nil {
		return s.Expires
	}
	left := t.Sub(now)
	switch {
	case left <= 0:
		return "expired"
	case left < time.Minute:
		return "in " + left.Round(time.Second).String()
	}
	return "in " + left.Round(time.Minute).String()
}
