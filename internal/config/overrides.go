package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Overrides change a cluster file from command line flags or tool parameters.
// Nil fields keep the file's value.
type Overrides struct {
	Name        string
	Nodes, CPUs *int
	Memory      *string
	Disk        *string
	DataDisks   *string // "2x32G", "32G,64G" or "none"
	Storage     *string // zfs, ceph or none
	Version     *string
	BindAddress *string
	Golden      bool
}

// Apply sets the overrides on c, then defaults, and validates the result.
func (o Overrides) Apply(c *Cluster) error {
	if o.Name != "" {
		c.Name = o.Name
	}
	d := &c.Nodes.Defaults
	if o.Nodes != nil {
		c.Nodes.Count = *o.Nodes
	}
	if o.CPUs != nil {
		d.CPUs = *o.CPUs
	}
	if o.Memory != nil {
		d.Memory = *o.Memory
	}
	if o.Disk != nil {
		d.RootDisk.Size = *o.Disk
	}
	if o.Version != nil {
		c.Proxmox.Version = *o.Version
	}
	if o.Golden {
		c.Proxmox.Golden = true
	}
	if o.BindAddress != nil {
		c.Access.BindAddress = *o.BindAddress
	}
	if o.DataDisks != nil {
		d.DataDisks = ParseDisks(*o.DataDisks)
		c.Storage.ZFS = nil // re-derive the default pool from the new disks
	}
	if o.Storage != nil {
		switch *o.Storage {
		case "zfs":
			if len(d.DataDisks) == 0 && d.DataDisks != nil {
				return fmt.Errorf("storage zfs needs data disks")
			}
			c.Storage.Ceph = nil
		case "ceph":
			c.Storage.ZFS = []ZFSPool{}
			if c.Storage.Ceph == nil {
				c.Storage.Ceph = &Ceph{}
			}
			c.Storage.Ceph.Enabled = true
		case "none":
			c.Storage.ZFS = []ZFSPool{}
			c.Storage.Ceph = nil
		default:
			return fmt.Errorf("storage must be zfs, ceph or none")
		}
	}
	c.SetDefaults()
	if err := c.Validate(); err != nil {
		return fmt.Errorf("invalid cluster configuration:\n%w", err)
	}
	return nil
}

var multiDiskRe = regexp.MustCompile(`^([0-9]+)x([0-9]+[MGT])$`)

// ParseDisks parses "2x32G", "32G,64G" or "none" (validation checks the sizes).
func ParseDisks(s string) []Disk {
	disks := []Disk{}
	if s == "none" || s == "0" || s == "" {
		return disks
	}
	if m := multiDiskRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		for range min(n, 9) { // more than 8 fails validation
			disks = append(disks, Disk{Size: m[2]})
		}
		return disks
	}
	for _, size := range strings.Split(s, ",") {
		disks = append(disks, Disk{Size: strings.TrimSpace(size)})
	}
	return disks
}
