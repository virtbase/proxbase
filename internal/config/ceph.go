package config

import (
	"fmt"
	"slices"
)

func (c *Cluster) setCephDefaults() {
	ce := c.Storage.Ceph
	if ce.Version == "" {
		ce.Version = "squid"
	}
	if ce.Network == "" {
		ce.Network = c.CorosyncNetwork().Name
	}
	if ce.OSDDisks == nil {
		for i := range c.Nodes.Defaults.DataDisks {
			dev := DataDiskDevice(i)
			if !slices.ContainsFunc(c.Storage.ZFS, func(z ZFSPool) bool { return slices.Contains(z.Disks, dev) }) {
				ce.OSDDisks = append(ce.OSDDisks, dev)
			}
		}
	}
	if ce.Pools == nil {
		ce.Pools = []CephPool{{Name: "ceph-vm"}}
	}
	for i := range ce.Pools {
		p := &ce.Pools[i]
		if p.Size == 0 {
			p.Size = 3
		}
		if p.MinSize == 0 {
			p.MinSize = min(2, p.Size)
		}
		if p.PGNum == 0 {
			p.PGNum = 32
		}
		if p.Application == "" {
			p.Application = "rbd"
		}
	}
	if ce.CephFS == nil {
		ce.CephFS = ptr(true)
	}
}

// CephNetwork returns the network carrying Ceph traffic.
func (c *Cluster) CephNetwork() Network {
	for _, n := range c.Networks {
		if n.Name == c.Storage.Ceph.Network {
			return n
		}
	}
	return c.CorosyncNetwork()
}

// CephFSSize is the replication of the CephFS pools: that of the first pool.
func (c *Cluster) CephFSSize() (size, minSize int) {
	p := c.Storage.Ceph.Pools[0]
	return p.Size, p.MinSize
}

func hasDisk(n Node, dev string) bool {
	for i := range n.Spec.DataDisks {
		if DataDiskDevice(i) == dev {
			return true
		}
	}
	return false
}

func (c *Cluster) validateCeph(zfsDisks map[string]string) []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf("storage.ceph: "+format, a...)) }
	ce := c.Storage.Ceph
	if ce.Version != "squid" && ce.Version != "tentacle" {
		add("version %q must be squid or tentacle", ce.Version)
	}
	if !slices.ContainsFunc(c.Networks, func(n Network) bool { return n.Name == ce.Network }) {
		add("network %q is not defined in networks", ce.Network)
	}
	if len(ce.OSDDisks) == 0 {
		add("no OSD disks (add nodes.defaults.dataDisks)")
	}
	for _, d := range ce.OSDDisks {
		if pool, ok := zfsDisks[d]; ok {
			add("disk %s is used by ZFS pool %s", d, pool)
		}
	}
	if len(ce.Pools) == 0 {
		add("at least one pool is required")
	}
	names := map[string]bool{}
	for _, p := range ce.Pools {
		if !nodeRe.MatchString(p.Name) || names[p.Name] || p.Name == "cephfs" {
			add("pool name %q is invalid or duplicate", p.Name)
		}
		names[p.Name] = true
		if p.Size < 1 || p.Size > 7 || p.MinSize < 1 || p.MinSize > p.Size {
			add("pool %s: need 1 <= minSize <= size <= 7", p.Name)
		}
		if p.Size > c.Nodes.Count {
			add("pool %s: size %d needs at least %d nodes (set pools[].size to at most %d for smaller clusters)", p.Name, p.Size, p.Size, c.Nodes.Count)
		}
		if p.PGNum < 8 || p.PGNum > 4096 || p.PGNum&(p.PGNum-1) != 0 {
			add("pool %s: pgNum must be a power of two between 8 and 4096", p.Name)
		}
		if p.Application != "rbd" && p.Application != "cephfs" && p.Application != "rgw" {
			add("pool %s: application must be rbd, cephfs or rgw", p.Name)
		}
	}
	return errs
}

// Warnings returns non-fatal configuration advice.
func (c *Cluster) Warnings() []string {
	var out []string
	if c.CephEnabled() {
		for _, n := range c.NodeList() {
			if mem, err := MemoryMiB(n.Spec.Memory); err == nil && mem < 6144 {
				out = append(out, fmt.Sprintf("node %s has %s memory; Ceph (mon, mgr, mds, OSD) wants at least 6G", n.Name, n.Spec.Memory))
			}
		}
	}
	return out
}
