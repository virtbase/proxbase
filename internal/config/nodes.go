package config

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// MemoryMiB parses "4G" or "512M".
func MemoryMiB(s string) (int, error) {
	m := memRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid memory %q (want e.g. 4G or 512M)", s)
	}
	v, _ := strconv.Atoi(m[1])
	if m[2] == "G" {
		v *= 1024
	}
	return v, nil
}

// MaxNodeIndex is the highest node number in use.
func (c *Cluster) MaxNodeIndex() int { return c.Nodes.Count + len(c.Nodes.Removed) }

// NodeName returns the name of node number i.
func (c *Cluster) NodeName(i int) string {
	return strings.ReplaceAll(c.Nodes.NamePattern, "{n}", strconv.Itoa(i))
}

// NodeList resolves names and per-node specs (defaults merged with overrides).
func (c *Cluster) NodeList() []Node {
	out := make([]Node, 0, c.Nodes.Count)
	for i := 1; i <= c.MaxNodeIndex(); i++ {
		if slices.Contains(c.Nodes.Removed, i) {
			continue
		}
		name := c.NodeName(i)
		spec := c.Nodes.Defaults
		spec.DataDisks = append([]Disk(nil), spec.DataDisks...)
		if o, ok := c.Nodes.Overrides[name]; ok {
			if o.CPUs != 0 {
				spec.CPUs = o.CPUs
			}
			if o.Memory != "" {
				spec.Memory = o.Memory
			}
			if o.Nested != nil {
				spec.Nested = o.Nested
			}
			if o.RootDisk.Size != "" {
				spec.RootDisk.Size = o.RootDisk.Size
			}
			if o.RootDisk.Filesystem != "" {
				spec.RootDisk.Filesystem = o.RootDisk.Filesystem
			}
			if o.DataDisks != nil {
				spec.DataDisks = o.DataDisks
			}
		}
		out = append(out, Node{Index: i, Name: name, Spec: spec})
	}
	return out
}

// NodeIP returns the address of node index n (1-based) in a network: .11, .12, ...
func NodeIP(cidr string, n int) (netip.Addr, int, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	a := p.Masked().Addr()
	for i := 0; i < 10+n; i++ {
		a = a.Next()
	}
	if !p.Contains(a.Next()) { // the last address is broadcast
		return netip.Addr{}, 0, fmt.Errorf("network %s too small for node %d", cidr, n)
	}
	return a, p.Bits(), nil
}

// Ports returns the host UI and SSH ports of node index n.
func (c *Cluster) Ports(n int) (ui, ssh int) {
	return c.Access.PortBase + n, c.Access.PortBase + 100 + n
}
