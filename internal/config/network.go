package config

// CorosyncNetworks returns the networks for corosync link0 and (optionally) link1.
func (c *Cluster) CorosyncNetworks() []Network {
	var out []Network
	for _, n := range c.Networks {
		if n.Has(RoleCorosync) {
			out = append(out, n)
		}
	}
	return out
}

// CorosyncNetwork returns the link0 network; node names resolve to it.
func (c *Cluster) CorosyncNetwork() Network { return c.CorosyncNetworks()[0] }

// RoleNetwork returns the network with a role, if any.
func (c *Cluster) RoleNetwork(role string) (Network, bool) {
	for _, n := range c.Networks {
		if n.Has(role) {
			return n, true
		}
	}
	return Network{}, false
}
