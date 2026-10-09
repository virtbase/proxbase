package config

import "fmt"

// SetDefaults fills every unset field.
func (c *Cluster) SetDefaults() {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&c.APIVersion, APIVersion)
	def(&c.Kind, Kind)
	def(&c.Name, "default")
	p := &c.Proxmox
	def(&p.Version, DefaultVersion)
	def(&p.Mirror, DefaultMirror)
	def(&p.Timezone, "UTC")
	def(&p.Keyboard, "en-us")
	def(&p.Country, "us")
	def(&p.Domain, "proxbase.internal")

	n := &c.Nodes
	if n.Count == 0 {
		n.Count = 1
	}
	def(&n.NamePattern, "pve{n}")
	d := &n.Defaults
	if d.CPUs == 0 {
		d.CPUs = 4
	}
	if c.CephEnabled() {
		def(&d.Memory, "6G")
	}
	def(&d.Memory, "4G")
	if d.Nested == nil {
		d.Nested = ptr(true)
	}
	def(&d.RootDisk.Size, "32G")
	def(&d.RootDisk.Filesystem, "zfs")
	if d.DataDisks == nil {
		d.DataDisks = []Disk{{Size: "32G"}}
	}

	if len(c.Networks) == 0 {
		c.Networks = []Network{{Name: "cluster", CIDR: "10.10.10.0/24", Roles: []string{RoleCorosync}}}
	}
	for i := range c.Networks {
		def(&c.Networks[i].Bridge, fmt.Sprintf("vmbr%d", i+1))
	}
	if len(c.CorosyncNetworks()) == 0 {
		for i := range c.Networks {
			if c.Networks[i].CIDR != "" {
				c.Networks[i].Roles = append(c.Networks[i].Roles, RoleCorosync)
				break
			}
		}
	}

	if c.Storage.ZFS == nil && len(d.DataDisks) > 0 && !c.CephEnabled() {
		c.Storage.ZFS = []ZFSPool{{Name: "tank"}}
	}
	for i := range c.Storage.ZFS {
		z := &c.Storage.ZFS[i]
		if z.Disks == nil {
			for j := range d.DataDisks {
				z.Disks = append(z.Disks, DataDiskDevice(j))
			}
		}
		if z.Raid == "" {
			z.Raid = map[bool]string{true: "single", false: "mirror"}[len(z.Disks) <= 1]
		}
	}

	if c.CephEnabled() {
		c.setCephDefaults()
	}

	if c.Access.APIToken == nil {
		c.Access.APIToken = ptr(true)
	}
	if c.Access.PortBase == 0 {
		c.Access.PortBase = 18000
	}
	def(&c.Access.BindAddress, "127.0.0.1")
}

// DataDiskDevice returns the guest device name of data disk i (0-based).
func DataDiskDevice(i int) string { return "vd" + "bcdefghi"[i:i+1] }
