package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func tokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage scoped API tokens: one role on one path, for least-privilege tests",
		Long: `token manages API tokens of proxbase@pve with privilege separation: a token may only do
what its role allows on its path, e.g. PVEAuditor on / or PVEVMAdmin on /vms/100.
The cluster's main token (proxbase@pve!api, Administrator) is not affected.`,
	}
	var role, path string
	var noPropagate bool
	create := &cobra.Command{
		Use:   "create [cluster] <name>",
		Short: "Create (or replace) a token and print its value (tokenId=secret)",
		Example: `  proxbase token create lab readonly --role PVEAuditor
  proxbase token create lab vm100 --role PVEVMAdmin --path /vms/100
  eval "$(proxbase env lab --token readonly)"`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args[:len(args)-1])
			if err != nil {
				return err
			}
			t, err := c.CreateToken(cmd.Context(), args[len(args)-1], role, path, !noPropagate)
			if err != nil {
				return err
			}
			logf("token %s has %s on %s", t.ID, t.Role, t.Path)
			fmt.Println(t.Value)
			return nil
		},
	}
	create.Flags().StringVar(&role, "role", "PVEAuditor", "role, e.g. PVEAuditor, PVEVMAdmin, PVEDatastoreUser, PVEPoolAdmin")
	create.Flags().StringVar(&path, "path", "/", "ACL path the role applies to, e.g. /, /vms/100, /storage/tank, /pool/ci")
	create.Flags().BoolVar(&noPropagate, "no-propagate", false, "the role applies to path only, not below it")
	var output string
	list := &cobra.Command{
		Use:   "list [cluster]",
		Short: "List the scoped tokens with their role and path",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			tokens, err := c.Tokens(cmd.Context())
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(tokens)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TOKEN\tROLE\tPATH\tPROPAGATE")
			for _, t := range tokens {
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\n", t.ID, t.Role, t.Path, t.Propagate)
			}
			return w.Flush()
		},
	}
	outputFlag(list, &output)
	del := &cobra.Command{
		Use:   "delete [cluster] <name>",
		Short: "Delete a scoped token and its grant",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openCluster(args[:len(args)-1])
			if err != nil {
				return err
			}
			name := args[len(args)-1]
			if err := c.DeleteToken(cmd.Context(), name); err != nil {
				return err
			}
			logf("deleted token %s", name)
			return nil
		},
	}
	cmd.AddCommand(create, list, del)
	return cmd
}
