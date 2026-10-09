package cli

import (
	"context"
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/state"
)

func upCmd() *cobra.Command {
	var o createOpts
	var stopTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "up [name]",
		Short: "Create, resume or start a cluster and run in the foreground; stop it on SIGTERM/SIGINT",
		Long: `up is meant for containers: it creates the cluster (or resumes a failed create, or
starts an existing one), then waits. On SIGTERM or SIGINT it shuts all nodes down
cleanly and exits. An existing cluster keeps its stored configuration.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := o.resolve(cmd, args)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, runErr := run(ctx, cfg.Name, func() (*cluster.Cluster, error) { return cluster.Create(ctx, cfg, logf) })
			if c == nil {
				return runErr
			}
			if runErr == nil {
				logf("cluster %s is up; waiting for SIGTERM", c.Cfg.Name)
				_ = printStatus(c.Status(ctx), "table")
				<-ctx.Done()
			} else if ctx.Err() == nil {
				logf("error: %v", runErr)
				logf("waiting for SIGTERM; restart the container to retry")
				<-ctx.Done()
			}
			logf("stopping cluster %s", c.Cfg.Name)
			stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout+30*time.Second)
			defer cancel()
			return c.Stop(stopCtx, stopTimeout)
		},
	}
	o.addFlags(cmd)
	cmd.Flags().DurationVar(&stopTimeout, "stop-timeout", 2*time.Minute, "time for a clean node shutdown on SIGTERM before powering off hard")
	return cmd
}

// run starts an existing ready cluster or creates/resumes one.
func run(ctx context.Context, name string, create func() (*cluster.Cluster, error)) (*cluster.Cluster, error) {
	if state.ForCluster(name).Exists() {
		c, err := cluster.Open(name, logf)
		if err != nil {
			return nil, err
		}
		if c.St.Phase == state.PhaseReady {
			logf("starting existing cluster %s", name)
			return c, c.Start(ctx)
		}
	}
	c, err := create()
	if c == nil && err == nil {
		err = errors.New("create returned no cluster")
	}
	return c, err
}
