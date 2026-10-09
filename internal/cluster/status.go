package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/virtbase/proxbase/internal/pve"
)

// NodeStatus is the state of one node.
type NodeStatus struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Online  *bool  `json:"online,omitempty"`
	IP      string `json:"ip"`
	UI      string `json:"ui"`
	SSHPort int    `json:"sshPort"`
}

// Status is the state of a cluster; pointer fields are nil when unknown.
type Status struct {
	Name     string       `json:"name"`
	Phase    string       `json:"phase"`
	Error    string       `json:"error,omitempty"`
	ISO      string       `json:"iso,omitempty"`
	Switch   bool         `json:"switch"`
	Quorate  *bool        `json:"quorate,omitempty"`
	Ceph     string       `json:"ceph,omitempty"`
	Duration string       `json:"createDuration,omitempty"`
	Faults   []string     `json:"faults,omitempty"`
	Nodes    []NodeStatus `json:"nodes"`
}

// Status reports processes and, if reachable, membership and storage health via
// the API token.
func (c *Cluster) Status(ctx context.Context) *Status {
	s := &Status{Name: c.Cfg.Name, Phase: string(c.St.Phase), Error: c.St.Error, ISO: c.St.ISO, Switch: c.net.Running(), Duration: c.St.Duration, Faults: c.faultList()}
	var api *pve.Client
	for _, n := range c.Cfg.NodeList() {
		ns := c.St.Node(n.Name)
		running := c.running(n)
		s.Nodes = append(s.Nodes, NodeStatus{
			Name: n.Name, Running: running, IP: c.corosyncIP(n),
			UI: fmt.Sprintf("https://127.0.0.1:%d", ns.UIPort), SSHPort: ns.SSHPort,
		})
		if running && api == nil {
			api = c.tokenClient(ns.UIPort)
		}
	}
	if api == nil {
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cs, members, err := api.ClusterStatus(ctx)
	if err != nil || cs == nil {
		return s
	}
	q := cs.Quorate == 1
	s.Quorate = &q
	for i := range s.Nodes {
		on := members[s.Nodes[i].Name].Online == 1
		s.Nodes[i].Online = &on
	}
	s.Ceph = c.storageHealth(ctx, api)
	return s
}
