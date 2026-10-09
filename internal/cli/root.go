// Package cli implements the proxbase command line.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/state"
)

// Set via -ldflags "-X github.com/virtbase/proxbase/internal/cli.version=...".
var version = "dev"

func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := &cobra.Command{
		Use:           "proxbase",
		Short:         "Proxmox VE clusters in QEMU/KVM, for labs and CI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(createCmd(), listCmd(), statusCmd(), startCmd(), stopCmd(), destroyCmd(),
		upCmd(), nodeCmd(), snapshotCmd(), sshCmd(), consoleCmd(),
		envCmd(), doctorCmd(), configCmd(), versionCmd(), switchCmd())
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

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
	return cluster.Open(clusterName(args), logf)
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
		Use:    cluster.SwitchCommand + " <cluster>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   func(_ *cobra.Command, args []string) error { return cluster.RunSwitch(args[0]) },
	}
}
