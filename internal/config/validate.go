package config

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,14}$`)
	nodeRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	memRe    = regexp.MustCompile(`^([0-9]+)([MG])$`)
	sizeRe   = regexp.MustCompile(`^[0-9]+[MGT]$`)
	bridgeRe = regexp.MustCompile(`^vmbr[0-9]{1,4}$`)
	vlanRe   = regexp.MustCompile(`^[0-9]{1,4}(-[0-9]{1,4})?$`)
	versRe   = regexp.MustCompile(`^[0-9]+\.[0-9]+(-[0-9]+)?$`)
	minDisks = map[string]int{"single": 1, "mirror": 2, "raid10": 4, "raidz": 3, "raidz2": 4, "raidz3": 5}
)

// Validate checks a defaulted config and returns all problems at once.
func (c *Cluster) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if c.APIVersion != APIVersion {
		add("apiVersion: must be %s", APIVersion)
	}
	if c.Kind != Kind {
		add("kind: must be %s", Kind)
	}
	if !nameRe.MatchString(c.Name) {
		add("name: %q must match %s", c.Name, nameRe)
	}
	if !versRe.MatchString(c.Proxmox.Version) {
		add("proxmox.version: %q must look like 9.2 or 9.2-1", c.Proxmox.Version)
	}
	if !strings.HasPrefix(c.Proxmox.Mirror, "https://") && !strings.HasPrefix(c.Proxmox.Mirror, "http://") {
		add("proxmox.mirror: must be an http(s) URL")
	}
	if c.Nodes.Count < 1 || c.Nodes.Count > 16 || c.MaxNodeIndex() > 32 {
		add("nodes.count: must be between 1 and 16")
	}
	if !strings.Contains(c.Nodes.NamePattern, "{n}") {
		add("nodes.namePattern: must contain {n}")
	}

	nodes := c.NodeList()
	names := map[string]bool{}
	for _, n := range nodes {
		names[n.Name] = true
		p := fmt.Sprintf("node %s: ", n.Name)
		if !nodeRe.MatchString(n.Name) {
			add("%sinvalid hostname", p)
		}
		if n.Spec.CPUs < 1 || n.Spec.CPUs > 64 {
			add("%scpus must be between 1 and 64", p)
		}
		if mem, err := MemoryMiB(n.Spec.Memory); err != nil {
			add("%s%v", p, err)
		} else if mem < 2048 {
			add("%smemory must be at least 2G", p)
		}
		if !sizeRe.MatchString(n.Spec.RootDisk.Size) {
			add("%srootDisk.size %q must look like 32G", p, n.Spec.RootDisk.Size)
		}
		switch n.Spec.RootDisk.Filesystem {
		case "zfs", "ext4", "xfs", "btrfs":
		default:
			add("%srootDisk.filesystem %q must be zfs, ext4, xfs or btrfs", p, n.Spec.RootDisk.Filesystem)
		}
		if len(n.Spec.DataDisks) > 8 {
			add("%sat most 8 data disks", p)
		}
		for i, d := range n.Spec.DataDisks {
			if !sizeRe.MatchString(d.Size) {
				add("%sdataDisks[%d].size %q must look like 32G", p, i, d.Size)
			}
			if d.Filesystem != "" {
				add("%sdataDisks[%d].filesystem is only valid on rootDisk", p, i)
			}
		}
		if c.CephEnabled() {
			for _, dev := range c.Storage.Ceph.OSDDisks {
				if !hasDisk(n, dev) {
					add("%sstorage.ceph uses %s, but the node has %d data disk(s)", p, dev, len(n.Spec.DataDisks))
				}
			}
		}
		for _, z := range c.Storage.ZFS {
			for _, dev := range z.Disks {
				found := false
				for i := range n.Spec.DataDisks {
					found = found || DataDiskDevice(i) == dev
				}
				if !found {
					add("%sstorage.zfs %s uses %s, but the node has %d data disk(s)", p, z.Name, dev, len(n.Spec.DataDisks))
				}
			}
		}
	}
	for name := range c.Nodes.Overrides {
		if !names[name] {
			add("nodes.overrides: unknown node %q", name)
		}
	}

	if len(c.Networks) > 8 {
		add("networks: at most 8")
	}
	seen := map[string]bool{}
	roles := map[string]int{}
	var prefixes []netip.Prefix
	for i, n := range c.Networks {
		p := fmt.Sprintf("networks[%d] (%s): ", i, n.Name)
		if !nodeRe.MatchString(n.Name) || len(n.Name) > 12 {
			add("%sname must be a short lowercase identifier", p)
		}
		if seen["n:"+n.Name] || seen["b:"+n.Bridge] {
			add("%sduplicate name or bridge", p)
		}
		seen["n:"+n.Name], seen["b:"+n.Bridge] = true, true
		if !bridgeRe.MatchString(n.Bridge) || n.Bridge == "vmbr0" {
			add("%sbridge %q must be vmbrN with N > 0 (vmbr0 is the NAT uplink)", p, n.Bridge)
		}
		if n.MTU != 0 && (n.MTU < 576 || n.MTU > 9000) {
			add("%smtu must be between 576 and 9000", p)
		}
		for _, r := range n.Roles {
			switch r {
			case RoleCorosync, RoleCephPublic, RoleCephCluster, RoleMigration:
				roles[r]++
			default:
				add("%sunknown role %q", p, r)
			}
		}
		if len(n.VLANs) > 0 && !n.VLANAware {
			add("%svlans need vlanAware: true", p)
		}
		for _, v := range n.VLANs {
			if !vlanRe.MatchString(v) {
				add("%svlan %q must be an ID or range like 100 or 200-299", p, v)
			}
		}
		if n.CIDR == "" {
			if len(n.Roles) > 0 {
				add("%sroles need a cidr (L2-only networks have no node addresses)", p)
			}
			continue
		}
		pfx, err := netip.ParsePrefix(n.CIDR)
		if err != nil || !pfx.Addr().Is4() {
			add("%scidr %q must be an IPv4 prefix", p, n.CIDR)
			continue
		}
		if _, _, err := NodeIP(n.CIDR, c.MaxNodeIndex()); err != nil {
			add("%s%v", p, err)
		}
		if pfx.Overlaps(netip.MustParsePrefix("10.0.2.0/24")) {
			add("%scidr overlaps the NAT network 10.0.2.0/24", p)
		}
		for _, q := range prefixes {
			if q.Overlaps(pfx) {
				add("%scidr overlaps another network", p)
			}
		}
		prefixes = append(prefixes, pfx)
	}
	if roles[RoleCorosync] < 1 || roles[RoleCorosync] > 2 {
		add("networks: one or two networks need the corosync role (link0, link1)")
	}
	for _, r := range []string{RoleCephPublic, RoleCephCluster, RoleMigration} {
		if roles[r] > 1 {
			add("networks: role %s is assigned more than once", r)
		}
	}
	if (roles[RoleCephPublic] > 0 || roles[RoleCephCluster] > 0) && !c.CephEnabled() {
		add("networks: ceph roles are set but storage.ceph is not enabled")
	}

	pools := map[string]bool{}
	for i, z := range c.Storage.ZFS {
		p := fmt.Sprintf("storage.zfs[%d] (%s): ", i, z.Name)
		if !nodeRe.MatchString(z.Name) || z.Name == "rpool" {
			add("%sinvalid pool name", p)
		}
		if pools[z.Name] {
			add("%sduplicate pool name", p)
		}
		pools[z.Name] = true
		min, ok := minDisks[z.Raid]
		if !ok {
			add("%sraid %q must be one of single, mirror, raid10, raidz, raidz2, raidz3", p, z.Raid)
		} else if len(z.Disks) < min || (z.Raid == "single" && len(z.Disks) != 1) || (z.Raid == "raid10" && len(z.Disks)%2 != 0) {
			add("%sraid %s does not fit %d disk(s)", p, z.Raid, len(z.Disks))
		}
	}
	used := map[string]string{}
	for _, z := range c.Storage.ZFS {
		for _, d := range z.Disks {
			if other, ok := used[d]; ok && other != z.Name {
				add("storage.zfs: disk %s used by %s and %s", d, other, z.Name)
			}
			used[d] = z.Name
		}
	}

	if c.CephEnabled() {
		errs = append(errs, c.validateCeph(used)...)
	}

	if c.Access.PortBase < 1024 || c.Access.PortBase+100+c.MaxNodeIndex() > 65535 {
		add("access.portBase: must be between 1024 and %d", 65535-100-c.MaxNodeIndex())
	}
	if a, err := netip.ParseAddr(c.Access.BindAddress); err != nil || !a.Is4() {
		add("access.bindAddress: %q must be an IPv4 address", c.Access.BindAddress)
	}
	for _, r := range c.Nodes.Removed {
		if r < 1 || r > c.MaxNodeIndex() {
			add("nodes.removed: %d is not a node number", r)
		}
	}
	return errors.Join(errs...)
}
