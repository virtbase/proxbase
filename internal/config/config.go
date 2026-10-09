// Package config defines the cluster file format (proxbase.dev/v1alpha1, kind Cluster),
// its defaults and validation.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "proxbase.dev/v1alpha1"
	Kind       = "Cluster"

	DefaultMirror  = "https://enterprise.proxmox.com/iso"
	DefaultVersion = "9.2"
)

type Cluster struct {
	APIVersion string    `yaml:"apiVersion" json:"apiVersion" jsonschema:"enum=proxbase.dev/v1alpha1"`
	Kind       string    `yaml:"kind" json:"kind" jsonschema:"enum=Cluster"`
	Name       string    `yaml:"name" json:"name" jsonschema:"pattern=^[a-z][a-z0-9-]{0,14}$"`
	Proxmox    Proxmox   `yaml:"proxmox" json:"proxmox,omitempty"`
	Nodes      Nodes     `yaml:"nodes" json:"nodes,omitempty"`
	Networks   []Network `yaml:"networks" json:"networks,omitempty"`
	Storage    Storage   `yaml:"storage" json:"storage,omitempty"`
	Access     Access    `yaml:"access" json:"access,omitempty"`
}

type Proxmox struct {
	// ISO version, either "9.2" (newest 9.2-N release) or an exact release like "9.2-1".
	Version  string   `yaml:"version" json:"version,omitempty"`
	Mirror   string   `yaml:"mirror" json:"mirror,omitempty" jsonschema:"description=Base URL holding the ISOs and SHA256SUMS"`
	Timezone string   `yaml:"timezone" json:"timezone,omitempty"`
	Keyboard string   `yaml:"keyboard" json:"keyboard,omitempty"`
	Country  string   `yaml:"country" json:"country,omitempty"`
	Domain   string   `yaml:"domain" json:"domain,omitempty"`
	SSHKeys  []string `yaml:"sshKeys,omitempty" json:"sshKeys,omitempty" jsonschema:"description=Extra public keys for root on every node"`
}

type Nodes struct {
	Count       int                 `yaml:"count" json:"count,omitempty" jsonschema:"minimum=1,maximum=16"`
	NamePattern string              `yaml:"namePattern" json:"namePattern,omitempty" jsonschema:"description=Node name; {n} is replaced by the node number"`
	Defaults    NodeSpec            `yaml:"defaults" json:"defaults,omitempty"`
	Overrides   map[string]NodeSpec `yaml:"overrides,omitempty" json:"overrides,omitempty" jsonschema:"description=Per-node settings keyed by node name"`
}

type NodeSpec struct {
	CPUs      int    `yaml:"cpus" json:"cpus,omitempty"`
	Memory    string `yaml:"memory" json:"memory,omitempty" jsonschema:"pattern=^[0-9]+[MG]$"`
	Nested    *bool  `yaml:"nested" json:"nested,omitempty"`
	RootDisk  Disk   `yaml:"rootDisk" json:"rootDisk,omitempty"`
	DataDisks []Disk `yaml:"dataDisks" json:"dataDisks,omitempty" jsonschema:"description=Extra disks; they appear as vdb, vdc, ... in the node"`
}

type Disk struct {
	Size       string `yaml:"size" json:"size,omitempty" jsonschema:"pattern=^[0-9]+[MGT]$"`
	Filesystem string `yaml:"filesystem,omitempty" json:"filesystem,omitempty" jsonschema:"enum=zfs,enum=ext4,enum=xfs,enum=btrfs,description=Root disk only"`
}

type Network struct {
	Name      string `yaml:"name" json:"name"`
	CIDR      string `yaml:"cidr" json:"cidr"`
	Bridge    string `yaml:"bridge" json:"bridge,omitempty"`
	VLANAware bool   `yaml:"vlanAware,omitempty" json:"vlanAware,omitempty"`
	MTU       int    `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	Corosync  bool   `yaml:"corosync" json:"corosync,omitempty"`
}

type Storage struct {
	ZFS  []ZFSPool `yaml:"zfs" json:"zfs,omitempty"`
	Ceph *Ceph     `yaml:"ceph,omitempty" json:"ceph,omitempty"`
}

type Ceph struct {
	Enabled  bool       `yaml:"enabled" json:"enabled"`
	Version  string     `yaml:"version" json:"version,omitempty" jsonschema:"enum=squid,enum=tentacle"`
	Network  string     `yaml:"network" json:"network,omitempty" jsonschema:"description=Name of the network for Ceph public and cluster traffic; default the corosync network"`
	OSDDisks []string   `yaml:"osdDisks" json:"osdDisks,omitempty" jsonschema:"description=Data disk devices (vdb, vdc, ...) for OSDs; default all not used by ZFS"`
	Pools    []CephPool `yaml:"pools" json:"pools,omitempty"`
	CephFS   *bool      `yaml:"cephfs" json:"cephfs,omitempty" jsonschema:"description=CephFS storage for ISOs, templates, backups and snippets (default true)"`
}

type CephPool struct {
	Name        string `yaml:"name" json:"name"`
	Size        int    `yaml:"size" json:"size,omitempty" jsonschema:"minimum=1,maximum=7"`
	MinSize     int    `yaml:"minSize" json:"minSize,omitempty" jsonschema:"minimum=1,maximum=7"`
	PGNum       int    `yaml:"pgNum" json:"pgNum,omitempty"`
	Application string `yaml:"application" json:"application,omitempty" jsonschema:"enum=rbd,enum=cephfs,enum=rgw"`
}

// CephEnabled reports whether Ceph is configured.
func (c *Cluster) CephEnabled() bool { return c.Storage.Ceph != nil && c.Storage.Ceph.Enabled }

type ZFSPool struct {
	Name  string   `yaml:"name" json:"name"`
	Raid  string   `yaml:"raid" json:"raid,omitempty" jsonschema:"enum=single,enum=mirror,enum=raid10,enum=raidz,enum=raidz2,enum=raidz3"`
	Disks []string `yaml:"disks" json:"disks,omitempty" jsonschema:"description=Data disk devices (vdb, vdc, ...); default all"`
}

type Access struct {
	APIToken *bool `yaml:"apiToken" json:"apiToken,omitempty" jsonschema:"description=Create API token proxbase@pve!api (default true)"`
	PortBase int   `yaml:"portBase" json:"portBase,omitempty" jsonschema:"description=Node n gets UI port portBase+n and SSH port portBase+100+n"`
}

// Node is a fully resolved node.
type Node struct {
	Index int // 1-based
	Name  string
	Spec  NodeSpec
}

// Load reads and strictly decodes a cluster file. It does not apply defaults.
func Load(path string) (*Cluster, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Decode(f)
}

func Decode(r io.Reader) (*Cluster, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var c Cluster
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return &c, nil
}

func (c *Cluster) YAML() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ptr[T any](v T) *T { return &v }

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
		c.Networks = []Network{{Name: "cluster", CIDR: "10.10.10.0/24", Corosync: true}}
	}
	hasCorosync := false
	for i := range c.Networks {
		def(&c.Networks[i].Bridge, fmt.Sprintf("vmbr%d", i+1))
		hasCorosync = hasCorosync || c.Networks[i].Corosync
	}
	if !hasCorosync {
		c.Networks[0].Corosync = true
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
}

// DataDiskDevice returns the guest device name of data disk i (0-based).
func DataDiskDevice(i int) string { return "vd" + "bcdefghi"[i:i+1] }

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,14}$`)
	nodeRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	memRe    = regexp.MustCompile(`^([0-9]+)([MG])$`)
	sizeRe   = regexp.MustCompile(`^[0-9]+[MGT]$`)
	bridgeRe = regexp.MustCompile(`^vmbr[0-9]{1,4}$`)
	versRe   = regexp.MustCompile(`^[0-9]+\.[0-9]+(-[0-9]+)?$`)
	minDisks = map[string]int{"single": 1, "mirror": 2, "raid10": 4, "raidz": 3, "raidz2": 4, "raidz3": 5}
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

// NodeList resolves names and per-node specs (defaults merged with overrides).
func (c *Cluster) NodeList() []Node {
	out := make([]Node, 0, c.Nodes.Count)
	for i := 1; i <= c.Nodes.Count; i++ {
		name := strings.ReplaceAll(c.Nodes.NamePattern, "{n}", strconv.Itoa(i))
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

// CorosyncNetwork returns the network carrying corosync.
func (c *Cluster) CorosyncNetwork() Network {
	for _, n := range c.Networks {
		if n.Corosync {
			return n
		}
	}
	return c.Networks[0]
}

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
	if c.Nodes.Count < 1 || c.Nodes.Count > 16 {
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
	corosync := 0
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
		if n.Corosync {
			corosync++
		}
		pfx, err := netip.ParsePrefix(n.CIDR)
		if err != nil || !pfx.Addr().Is4() {
			add("%scidr %q must be an IPv4 prefix", p, n.CIDR)
			continue
		}
		if _, _, err := NodeIP(n.CIDR, c.Nodes.Count); err != nil {
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
	if corosync != 1 {
		add("networks: exactly one network must have corosync: true")
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

	if c.Access.PortBase < 1024 || c.Access.PortBase+100+c.Nodes.Count > 65535 {
		add("access.portBase: must be between 1024 and %d", 65535-100-c.Nodes.Count)
	}
	return errors.Join(errs...)
}

// Ports returns the host UI and SSH ports of node index n.
func (c *Cluster) Ports(n int) (ui, ssh int) {
	return c.Access.PortBase + n, c.Access.PortBase + 100 + n
}
