package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/state"
)

func envCmd() *cobra.Command {
	var format, export string
	cmd := &cobra.Command{
		Use:   "env [name]",
		Short: "Print API endpoint and credentials for clients (shell, json, terraform)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := openCluster(args)
			if err != nil {
				return err
			}
			env, err := c.Env()
			if err != nil {
				return err
			}
			endpoint, token := env.Endpoint, env.APIToken
			shell := func(key, ca, prefix string) string {
				return prefix + fmt.Sprintf("export PROXMOX_VE_ENDPOINT=%q\n", endpoint) +
					fmt.Sprintf("export PROXMOX_VE_API_TOKEN='%s'\n", token) +
					"export PROXMOX_VE_INSECURE=true  # the cluster CA is not in your trust store; curl --cacert $PROXBASE_CA_CERT works\n" +
					"export PROXMOX_VE_SSH_USERNAME=root\n" +
					fmt.Sprintf("export PROXBASE_SSH_KEY=%s\n", key) +
					fmt.Sprintf("export PROXBASE_CA_CERT=%s\n", ca) +
					fmt.Sprintf("export PROXBASE_ROOT_PASSWORD='%s'\n", c.Password())
			}
			if export != "" {
				return exportEnv(c.KeyPath(), c.CAPath(), token, export, shell(`"$d/id_ed25519"`, `"$d/pve-root-ca.pem"`,
					"# proxbase env --export; source this file. Paths are relative to its folder.\n"+
						`d=$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)`+"\n"))
			}
			switch format {
			case "shell":
				fmt.Print(shell(fmt.Sprintf("%q", c.KeyPath()), fmt.Sprintf("%q", c.CAPath()), ""))
				return nil
			case "json":
				env.RootPassword = c.Password()
				return printJSON(env)
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
	cmd.Flags().StringVar(&export, "export", "", "write id_ed25519, pve-root-ca.pem, api-token and env.sh into this directory (e.g. a mounted volume)")
	return cmd
}

// exportEnv copies the client credentials into dir for use outside the state directory.
func exportEnv(keyPath, caPath, token, dir, envSh string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"id_ed25519", key, 0o600},
		{"pve-root-ca.pem", ca, 0o644},
		{"api-token", []byte(token + "\n"), 0o600},
		{"env.sh", []byte(envSh), 0o600},
	}
	for _, f := range files {
		if err := state.WriteFileAtomic(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return err
		}
	}
	logf("wrote id_ed25519, pve-root-ca.pem, api-token and env.sh to %s", dir)
	return nil
}
