// Package cli implements the proxbase command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/network"
	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/state"
)

// Set via -ldflags "-X github.com/virtbase/proxbase/internal/cli.version=...".
var version = "dev"

// sink receives progress and messages; --progress selects text or JSON lines.
var sink = progress.Text(os.Stderr)

// Execute runs the command line and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var format string
	root := &cobra.Command{
		Use:           "proxbase",
		Short:         "Proxmox VE clusters in QEMU/KVM, for labs and CI",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			switch format {
			case "text":
			case "json":
				sink = progress.JSON(os.Stderr)
			default:
				return fmt.Errorf("--progress must be text or json")
			}
			return nil
		},
	}
	root.PersistentFlags().StringVar(&format, "progress", "text", "progress and messages on stderr: text or json (one JSON object per line)")
	root.AddCommand(createCmd(), listCmd(), statusCmd(), startCmd(), stopCmd(), destroyCmd(),
		upCmd(), nodeCmd(), snapshotCmd(), faultCmd(), imageCmd(), sshCmd(), consoleCmd(),
		envCmd(), doctorCmd(), configCmd(), versionCmd(), switchCmd(), mcpCmd())
	t0 := time.Now()
	err := root.ExecuteContext(ctx)
	e := progress.Event{Time: time.Now(), Type: progress.TypeDone, Elapsed: time.Since(t0).Seconds()}
	if err != nil {
		e.Type, e.Error = progress.TypeError, err.Error()
		if h, ok := errors.AsType[*hintError](err); ok {
			e.Hint, e.Log = h.hint, h.log
		}
	}
	sink(e)
	if err != nil {
		return 1
	}
	return 0
}

// hintError adds what to do next and where details are logged to an error.
type hintError struct {
	err       error
	hint, log string
}

func (e *hintError) Error() string { return e.err.Error() }
func (e *hintError) Unwrap() error { return e.err }

func logf(format string, a ...any) { sink.Logf(format, a...) }

// clusterName picks the argument, else "default", else the only existing cluster.
func clusterName(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	if names, _ := state.List(); len(names) == 1 && !state.ForCluster("default").Exists() {
		return names[0]
	}
	return "default"
}

func openCluster(args []string) (*cluster.Cluster, error) {
	return cluster.Open(clusterName(args), sink)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func outputFlag(cmd *cobra.Command, p *string) {
	cmd.Flags().StringVarP(p, "output", "o", "table", "output format: table|json")
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { fmt.Println("proxbase", version) },
	}
}

func switchCmd() *cobra.Command {
	return &cobra.Command{
		Use:    network.Command + " <cluster>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   func(_ *cobra.Command, args []string) error { return network.Run(args[0]) },
	}
}
