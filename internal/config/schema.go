package config

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// Schema returns the JSON schema of the cluster file.
func Schema() ([]byte, error) {
	r := &jsonschema.Reflector{FieldNameTag: "yaml", RequiredFromJSONSchemaTags: true}
	s := r.Reflect(&Cluster{})
	s.ID = "https://proxbase.virtbase.com/schema/v1alpha1/cluster.json"
	s.Title = "Proxbase cluster"
	return json.MarshalIndent(s, "", "  ")
}

// Example is written by `proxbase config init`.
const Example = `# yaml-language-server: $schema=https://proxbase.virtbase.com/schema/v1alpha1/cluster.json
apiVersion: proxbase.virtbase.com/v1alpha1
kind: Cluster
name: lab
proxmox:
  version: "9.2"            # newest 9.2-N ISO, or an exact release like 9.2-1
  timezone: UTC
  keyboard: en-us
  country: us
  domain: proxbase.internal
  upgrade: false            # apt dist-upgrade on every node before clustering
  sshKeys: []               # extra public keys for root
nodes:
  count: 3
  namePattern: pve{n}
  defaults:
    cpus: 4
    memory: 4G
    nested: true
    rootDisk: {size: 32G, filesystem: zfs}
    dataDisks:              # vdb, vdc, ... inside the node
      - size: 32G
  overrides: {}             # e.g. pve1: {memory: 8G}
networks:                   # vmbr0 is always the NAT uplink (web UI and SSH forwards)
  - name: cluster
    cidr: 10.10.10.0/24     # node n gets .1n (pve1 = .11); omit for an L2-only network
    bridge: vmbr1
    roles: [corosync]       # corosync (up to two: link0, link1), ceph-public, ceph-cluster, migration
  # - name: guests
  #   bridge: vmbr2
  #   vlanAware: true
  #   vlans: ["100", "200-299"]
storage:
  zfs:
    - name: tank
      raid: single
      disks: [vdb]
  # Ceph instead of ZFS (or on other data disks); needs 3+ nodes and ~6G per node.
  # Shortcut: proxbase create --storage ceph
  # ceph:
  #   enabled: true
  #   version: squid          # or tentacle
  #   osdDisks: [vdb]         # default: data disks not used by ZFS
  #   pools:
  #     - {name: ceph-vm, size: 3, minSize: 2, pgNum: 32, application: rbd}
  #   cephfs: true            # shared storage for ISOs, templates, backups, snippets
access:
  apiToken: true            # proxbase@pve!api with Administrator on /
  portBase: 18000           # pveN: UI 127.0.0.1:18000+N, SSH 127.0.0.1:18100+N
`
