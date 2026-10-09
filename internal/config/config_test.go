package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, s string) *Cluster {
	t.Helper()
	c, err := Decode(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	c.SetDefaults()
	return c
}

func TestDefaultsValid(t *testing.T) {
	c := load(t, "name: lab\nnodes: {count: 3}\n")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	nodes := c.NodeList()
	if len(nodes) != 3 || nodes[2].Name != "pve3" {
		t.Fatalf("nodes: %+v", nodes)
	}
	if len(c.Storage.ZFS) != 1 || c.Storage.ZFS[0].Raid != "single" || c.Storage.ZFS[0].Disks[0] != "vdb" {
		t.Fatalf("zfs default: %+v", c.Storage.ZFS)
	}
	if ip, bits, _ := NodeIP(c.CorosyncNetwork().CIDR, 2); ip.String() != "10.10.10.12" || bits != 24 {
		t.Fatalf("node ip: %s/%d", ip, bits)
	}
	if ui, ssh := c.Ports(3); ui != 18003 || ssh != 18103 {
		t.Fatalf("ports %d %d", ui, ssh)
	}
}

func TestExampleValid(t *testing.T) {
	c := load(t, Example)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownField(t *testing.T) {
	if _, err := Decode(strings.NewReader("name: lab\nnodez: 3\n")); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestOverrides(t *testing.T) {
	c := load(t, "nodes:\n  count: 2\n  overrides:\n    pve2: {memory: 8G, dataDisks: []}\n")
	n := c.NodeList()
	if n[0].Spec.Memory != "4G" || n[1].Spec.Memory != "8G" {
		t.Fatalf("memory: %s %s", n[0].Spec.Memory, n[1].Spec.Memory)
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "pve2: storage.zfs tank uses vdb") {
		t.Fatalf("want missing disk error, got %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"name: Lab\n": "name:",
		"networks: [{name: a, cidr: 10.0.2.0/24}]\n":                                                  "overlaps the NAT",
		"networks: [{name: a, cidr: 10.1.0.0/24, roles: [corosync]}, {name: b, cidr: 10.1.0.0/25}]\n": "overlaps another",
		"storage: {zfs: [{name: t, raid: mirror}]}\n":                                                 "does not fit 1",
		"nodes: {defaults: {memory: 1G}}\n":                                                           "at least 2G",
		"nodes: {overrides: {pve9: {cpus: 2}}}\n":                                                     "unknown node",
		"networks: [{name: a, cidr: 10.1.0.0/28}]\nnodes: {count: 5}\n":                               "too small",
	}
	for in, want := range cases {
		err := load(t, in).Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", in, want, err)
		}
	}
}

func TestNoStorage(t *testing.T) {
	c := load(t, "nodes: {defaults: {dataDisks: []}}\n")
	if len(c.Storage.ZFS) != 0 {
		t.Fatal("no data disks must mean no default pool")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSchema(t *testing.T) {
	b, err := Schema()
	if err != nil || !strings.Contains(string(b), `"namePattern"`) {
		t.Fatalf("schema: %v", err)
	}
}

// The published schema is the committed file; it must match the code.
func TestSchemaFile(t *testing.T) {
	b, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile("../../schema/v1alpha1/cluster.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(file) != string(b)+"\n" {
		t.Fatal("schema/v1alpha1/cluster.json is out of date; run make schema")
	}
}

func TestCephDefaults(t *testing.T) {
	c := load(t, "nodes: {count: 3}\nstorage: {ceph: {enabled: true}}\n")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	ce := c.Storage.Ceph
	if len(c.Storage.ZFS) != 0 || ce.Version != "squid" || len(ce.OSDDisks) != 1 || ce.OSDDisks[0] != "vdb" || !*ce.CephFS {
		t.Fatalf("ceph defaults: %+v zfs=%v", ce, c.Storage.ZFS)
	}
	if p := ce.Pools[0]; p.Name != "ceph-vm" || p.Size != 3 || p.MinSize != 2 || p.PGNum != 32 || p.Application != "rbd" {
		t.Fatalf("pool defaults: %+v", p)
	}
	if c.Nodes.Defaults.Memory != "6G" || len(c.Warnings()) != 0 {
		t.Fatalf("memory %s, warnings %v", c.Nodes.Defaults.Memory, c.Warnings())
	}
}

func TestCephSplitDisks(t *testing.T) {
	c := load(t, "nodes: {count: 3, defaults: {memory: 4G, dataDisks: [{size: 8G}, {size: 8G}]}}\nstorage: {zfs: [{name: tank, disks: [vdb]}], ceph: {enabled: true}}\n")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := c.Storage.Ceph.OSDDisks; len(got) != 1 || got[0] != "vdc" {
		t.Fatalf("osd disks %v", got)
	}
	if len(c.Warnings()) != 3 {
		t.Fatalf("want a memory warning per node, got %v", c.Warnings())
	}
}

func TestCephValidation(t *testing.T) {
	cases := map[string]string{
		"nodes: {count: 2}\nstorage: {ceph: {enabled: true}}\n":                                                  "size 3 needs at least 3 nodes",
		"nodes: {count: 3}\nstorage: {zfs: [{name: t, disks: [vdb]}], ceph: {enabled: true, osdDisks: [vdb]}}\n": "used by ZFS pool t",
		"nodes: {count: 3}\nstorage: {ceph: {enabled: true, version: reef}}\n":                                   "squid or tentacle",
		"nodes: {count: 3}\nstorage: {ceph: {enabled: true, pools: [{name: p, pgNum: 30}]}}\n":                   "power of two",
		"nodes: {count: 3}\nstorage: {ceph: {enabled: true, osdDisks: [vdc]}}\n":                                 "uses vdc",
	}
	for in, want := range cases {
		err := load(t, in).Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", in, want, err)
		}
	}
	// Two nodes work when the pool size is lowered explicitly.
	c := load(t, "nodes: {count: 2}\nstorage: {ceph: {enabled: true, pools: [{name: p, size: 2}]}}\n")
	if err := c.Validate(); err != nil || c.Storage.Ceph.Pools[0].MinSize != 2 {
		t.Fatalf("2-node override: %v %+v", err, c.Storage.Ceph.Pools[0])
	}
}

func TestNetworkRoles(t *testing.T) {
	c := load(t, `
nodes: {count: 3}
networks:
  - {name: cluster, cidr: 10.10.10.0/24, roles: [corosync]}
  - {name: ring1, cidr: 10.10.11.0/24, roles: [corosync, migration]}
  - {name: storage, cidr: 10.10.20.0/24, mtu: 9000, roles: [ceph-public]}
  - {name: replic, cidr: 10.10.21.0/24, roles: [ceph-cluster]}
  - {name: guests, vlanAware: true, vlans: [100, 200-299]}
storage: {ceph: {enabled: true}}
`)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if links := c.CorosyncNetworks(); len(links) != 2 || links[1].Name != "ring1" {
		t.Fatalf("corosync links: %+v", links)
	}
	if pub, cl := c.CephNetworks(); pub.Name != "storage" || cl.Name != "replic" {
		t.Fatalf("ceph networks: %s %s", pub.Name, cl.Name)
	}
	if m, ok := c.RoleNetwork(RoleMigration); !ok || m.Name != "ring1" {
		t.Fatalf("migration network: %+v", m)
	}
	if g := c.Networks[4]; g.Bridge != "vmbr5" || g.VLANs[1] != "200-299" {
		t.Fatalf("guests: %+v", g)
	}
}

func TestDefaultCorosyncRole(t *testing.T) {
	c := load(t, "networks: [{name: guests}, {name: cluster, cidr: 10.9.0.0/24}]\n")
	if err := c.Validate(); err != nil || c.CorosyncNetwork().Name != "cluster" {
		t.Fatalf("%v %+v", err, c.Networks)
	}
	if pub, cl := c.CephNetworks(); pub.Name != "cluster" || cl.Name != "cluster" {
		t.Fatalf("ceph falls back to corosync: %s %s", pub.Name, cl.Name)
	}
}

func TestNetworkValidation(t *testing.T) {
	cases := map[string]string{
		"networks: [{name: a, roles: [corosync]}]\n":                                                                                                                      "roles need a cidr",
		"networks: [{name: a, cidr: 10.1.0.0/24, roles: [bogus]}]\n":                                                                                                      "unknown role",
		"networks: [{name: a, cidr: 10.1.0.0/24, vlans: [100]}]\n":                                                                                                        "need vlanAware",
		"networks: [{name: a, cidr: 10.1.0.0/24, vlanAware: true, vlans: [x]}]\n":                                                                                         "must be an ID or range",
		"networks: [{name: a, cidr: 10.1.0.0/24, roles: [ceph-public]}]\n":                                                                                                "storage.ceph is not enabled",
		"networks: [{name: a, cidr: 10.1.0.0/24, roles: [corosync, migration]}, {name: b, cidr: 10.2.0.0/24, roles: [migration]}]\n":                                      "more than once",
		"networks: [{name: a, cidr: 10.1.0.0/24, roles: [corosync]}, {name: b, cidr: 10.2.0.0/24, roles: [corosync]}, {name: c, cidr: 10.3.0.0/24, roles: [corosync]}]\n": "one or two networks",
	}
	for in, want := range cases {
		err := load(t, in).Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", in, want, err)
		}
	}
}

func TestRemovedNodes(t *testing.T) {
	c := load(t, "nodes: {count: 3, removed: [2]}\n")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	n := c.NodeList()
	if len(n) != 3 || n[0].Name != "pve1" || n[1].Name != "pve3" || n[2].Name != "pve4" || n[2].Index != 4 {
		t.Fatalf("nodes: %+v", n)
	}
	if ui, _ := c.Ports(n[1].Index); ui != 18003 {
		t.Fatalf("pve3 must keep its port, got %d", ui)
	}
}

func TestTwoNodeCephWarning(t *testing.T) {
	c := load(t, "nodes: {count: 2}\nstorage: {ceph: {enabled: true, pools: [{name: p, size: 2}]}}\n")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "I/O stops") {
		t.Fatalf("warnings: %v", w)
	}
}

func TestBindAddress(t *testing.T) {
	if c := load(t, ""); c.Access.BindAddress != "127.0.0.1" {
		t.Fatalf("default bind address %q", c.Access.BindAddress)
	}
	if err := load(t, "access: {bindAddress: 0.0.0.0}\n").Validate(); err != nil {
		t.Fatal(err)
	}
	if err := load(t, "access: {bindAddress: localhost}\n").Validate(); err == nil || !strings.Contains(err.Error(), "IPv4 address") {
		t.Fatalf("want bind address error, got %v", err)
	}
}

func TestExamples(t *testing.T) {
	files, _ := filepath.Glob("../../examples/*.yaml")
	files = append(files, "../../examples/compose/cluster.yaml")
	if len(files) < 5 {
		t.Fatalf("found only %d example files", len(files))
	}
	for _, f := range files {
		c, err := Load(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		c.SetDefaults()
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
