package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func envCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "env [name]",
		Short: "Print API endpoint and credentials for clients (shell, json, terraform)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			token, err := c.Token()
			if err != nil {
				return fmt.Errorf("cluster %s has no API token yet (create not finished or access.apiToken: false)", c.Cfg.Name)
			}
			first := c.St.Nodes[0]
			endpoint := fmt.Sprintf("https://127.0.0.1:%d/", first.UIPort)
			switch format {
			case "shell":
				fmt.Printf("export PROXMOX_VE_ENDPOINT=%q\n", endpoint)
				fmt.Printf("export PROXMOX_VE_API_TOKEN='%s'\n", token)
				fmt.Printf("export PROXMOX_VE_INSECURE=true  # the cluster CA is not in your trust store; curl --cacert %s works\n", c.CAPath())
				fmt.Printf("export PROXMOX_VE_SSH_USERNAME=root\n")
				fmt.Printf("export PROXBASE_SSH_KEY=%q\n", c.KeyPath())
				fmt.Printf("export PROXBASE_CA_CERT=%q\n", c.CAPath())
				fmt.Printf("export PROXBASE_ROOT_PASSWORD='%s'\n", c.Password())
				return nil
			case "json":
				type node struct {
					Name    string `json:"name"`
					API     string `json:"api"`
					SSHHost string `json:"sshHost"`
					SSHPort int    `json:"sshPort"`
				}
				out := struct {
					Endpoint     string `json:"endpoint"`
					APIToken     string `json:"apiToken"`
					TokenID      string `json:"tokenId"`
					TokenSecret  string `json:"tokenSecret"`
					CACert       string `json:"caCert"`
					SSHUser      string `json:"sshUser"`
					SSHKey       string `json:"sshKey"`
					RootPassword string `json:"rootPassword"`
					Nodes        []node `json:"nodes"`
				}{Endpoint: endpoint, APIToken: token, CACert: c.CAPath(), SSHUser: "root", SSHKey: c.KeyPath(), RootPassword: c.Password()}
				out.TokenID, out.TokenSecret, _ = strings.Cut(token, "=")
				for _, n := range c.St.Nodes {
					out.Nodes = append(out.Nodes, node{Name: n.Name, API: fmt.Sprintf("https://127.0.0.1:%d/", n.UIPort), SSHHost: "127.0.0.1", SSHPort: n.SSHPort})
				}
				return printJSON(out)
			case "terraform":
				fmt.Printf(`provider "proxmox" {
  endpoint  = %q
  api_token = %q
  insecure  = true # certificates are signed by the cluster CA: %s

  ssh {
    agent       = false
    username    = "root"
    private_key = file(%q)
`, endpoint, token, c.CAPath(), c.KeyPath())
				for _, n := range c.St.Nodes {
					fmt.Printf("\n    node {\n      name    = %q\n      address = \"127.0.0.1\"\n      port    = %d\n    }\n", n.Name, n.SSHPort)
				}
				fmt.Println("  }\n}")
				return nil
			}
			return fmt.Errorf("--format must be shell, json or terraform")
		},
	}
	cmd.Flags().StringVar(&format, "format", "shell", "shell|json|terraform (bpg/proxmox provider)")
	return cmd
}
