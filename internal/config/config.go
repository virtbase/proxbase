// Package config defines the cluster file format (proxbase.virtbase.com/v1alpha1, kind Cluster),
// its defaults and validation.
package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "proxbase.virtbase.com/v1alpha1"
	Kind       = "Cluster"

	DefaultMirror  = "https://enterprise.proxmox.com/iso"
	DefaultVersion = "9.2"
)

type Cluster struct {
	APIVersion string    `yaml:"apiVersion" json:"apiVersion" jsonschema:"enum=proxbase.virtbase.com/v1alpha1"`
	Kind       string    `yaml:"kind" json:"kind" jsonschema:"enum=Cluster"`
	Name       string    `yaml:"name" json:"name" jsonschema:"pattern=^[a-z][a-z0-9-]*$,maxLength=15"`
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
	Upgrade  bool     `yaml:"upgrade,omitempty" json:"upgrade,omitempty" jsonschema:"description=Run apt dist-upgrade on every node before clustering"`
	Golden   bool     `yaml:"goldenImage,omitempty" json:"goldenImage,omitempty" jsonschema:"description=Clone nodes from a cached installed base image instead of running the installer"`
}

type Nodes struct {
	Count       int                 `yaml:"count" json:"count,omitempty" jsonschema:"minimum=1,maximum=16"`
	NamePattern string              `yaml:"namePattern" json:"namePattern,omitempty" jsonschema:"description=Node name; {n} is replaced by the node number"`
	Defaults    NodeSpec            `yaml:"defaults" json:"defaults,omitempty"`
	Overrides   map[string]NodeSpec `yaml:"overrides,omitempty" json:"overrides,omitempty" jsonschema:"description=Per-node settings keyed by node name"`
	Removed     []int               `yaml:"removed,omitempty" json:"removed,omitempty" jsonschema:"description=Node numbers removed with proxbase node remove; the others keep their addresses"`
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

// Network roles. A cluster may have two corosync networks (link0 and link1).
const (
	RoleCorosync    = "corosync"
	RoleCephPublic  = "ceph-public"
	RoleCephCluster = "ceph-cluster"
	RoleMigration   = "migration"
)

type Network struct {
	Name      string   `yaml:"name" json:"name"`
	CIDR      string   `yaml:"cidr,omitempty" json:"cidr,omitempty" jsonschema:"description=IPv4 prefix; node n gets .1n. Leave empty for an L2-only network (bridge without node IPs)"`
	Bridge    string   `yaml:"bridge" json:"bridge,omitempty"`
	VLANAware bool     `yaml:"vlanAware,omitempty" json:"vlanAware,omitempty"`
	VLANs     []string `yaml:"vlans,omitempty" json:"vlans,omitempty" jsonschema:"description=VLAN IDs or ranges allowed on a VLAN-aware bridge (default 2-4094)"`
	MTU       int      `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	Roles     []string `yaml:"roles,omitempty" json:"roles,omitempty" jsonschema:"enum=corosync,enum=ceph-public,enum=ceph-cluster,enum=migration"`
}

func (n Network) Has(role string) bool { return slices.Contains(n.Roles, role) }

type Storage struct {
	ZFS  []ZFSPool `yaml:"zfs" json:"zfs,omitempty"`
	Ceph *Ceph     `yaml:"ceph,omitempty" json:"ceph,omitempty"`
}

type Ceph struct {
	Enabled  bool       `yaml:"enabled" json:"enabled"`
	Version  string     `yaml:"version" json:"version,omitempty" jsonschema:"enum=squid,enum=tentacle"`
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
	APIToken    *bool  `yaml:"apiToken" json:"apiToken,omitempty" jsonschema:"description=Create API token proxbase@pve!api (default true)"`
	PortBase    int    `yaml:"portBase" json:"portBase,omitempty" jsonschema:"description=Node n gets UI port portBase+n and SSH port portBase+100+n"`
	BindAddress string `yaml:"bindAddress,omitempty" json:"bindAddress,omitempty" jsonschema:"description=Host address the UI/SSH forwards listen on (default 127.0.0.1; 0.0.0.0 in the container image)"`
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
