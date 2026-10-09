package cluster

import (
	"fmt"
	"strings"
)

// NodeEndpoint is how clients reach one node.
type NodeEndpoint struct {
	Name    string `json:"name"`
	API     string `json:"api"`
	SSHHost string `json:"sshHost"`
	SSHPort int    `json:"sshPort"`
}

// Env is what API and SSH clients need to use the cluster.
type Env struct {
	Endpoint     string         `json:"endpoint"`
	APIToken     string         `json:"apiToken"`
	TokenID      string         `json:"tokenId"`
	TokenSecret  string         `json:"tokenSecret"`
	CACert       string         `json:"caCert"`
	SSHUser      string         `json:"sshUser"`
	SSHKey       string         `json:"sshKey"`
	RootPassword string         `json:"rootPassword,omitempty"`
	Nodes        []NodeEndpoint `json:"nodes"`
}

// Env returns endpoints and credentials, without the root password.
func (c *Cluster) Env() (*Env, error) {
	token, err := c.Token()
	if err != nil || token == "" {
		return nil, fmt.Errorf("cluster %s has no API token yet (create not finished or access.apiToken: false)", c.Cfg.Name)
	}
	e := &Env{
		Endpoint: fmt.Sprintf("https://127.0.0.1:%d/", c.St.Nodes[0].UIPort),
		APIToken: token, CACert: c.CAPath(), SSHUser: "root", SSHKey: c.KeyPath(),
	}
	e.TokenID, e.TokenSecret, _ = strings.Cut(token, "=")
	for _, n := range c.St.Nodes {
		e.Nodes = append(e.Nodes, NodeEndpoint{Name: n.Name, API: fmt.Sprintf("https://127.0.0.1:%d/", n.UIPort), SSHHost: "127.0.0.1", SSHPort: n.SSHPort})
	}
	return e, nil
}
