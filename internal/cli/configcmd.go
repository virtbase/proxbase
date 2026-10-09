package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/config"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Work with cluster files"}
	var force bool
	initCmd := &cobra.Command{
		Use:   "init [file]",
		Short: "Write an example cluster file (default cluster.yaml)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := "cluster.yaml"
			if len(args) > 0 {
				path = args[0]
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
			if force {
				flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			f, err := os.OpenFile(path, flags, 0o644)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.WriteString(config.Example)
			if err == nil {
				logf("wrote %s", path)
			}
			return err
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	validate := &cobra.Command{
		Use:   "validate <file>",
		Short: "Check a cluster file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.Load(args[0])
			if err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}
			cfg.SetDefaults()
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("%s:\n%w", args[0], err)
			}
			fmt.Printf("%s is valid (%d nodes)\n", args[0], cfg.Nodes.Count)
			return nil
		},
	}
	schema := &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON schema of the cluster file",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			b, err := config.Schema()
			if err != nil {
				return err
			}
			fmt.Println(string(b))
			return nil
		},
	}
	cmd.AddCommand(initCmd, validate, schema)
	return cmd
}
