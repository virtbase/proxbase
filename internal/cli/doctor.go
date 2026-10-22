package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/doctor"
)

func doctorCmd() *cobra.Command {
	var portBase int
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that this host can run proxbase clusters",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			checks := doctor.Run(portBase)
			for _, c := range checks {
				fmt.Printf("%-5s %-12s %s\n", c.Status, c.Name, c.Detail)
			}
			if doctor.Failed(checks) {
				return errors.New("some checks failed")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&portBase, "port-base", 18000, "access.portBase to check")
	return cmd
}
