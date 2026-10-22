package cluster

import (
	"fmt"
	"net"
	"strconv"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/doctor"
	"github.com/virtbase/proxbase/internal/image"
	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/state"
)

// Plan is what a create would set up and need, worked out without writing anything.
type Plan struct {
	Name      string        `json:"name"`
	PortBase  int           `json:"portBase"`
	Nodes     []PlanNode    `json:"nodes"`
	MemoryMiB int           `json:"memoryMiB" jsonschema:"memory of all nodes; idle nodes return about half to the host"`
	DiskMiB   int           `json:"diskMiB" jsonschema:"size of all node disks; the images are thin and start at a few GiB"`
	ISOCached bool          `json:"isoCached" jsonschema:"the ISO is cached, so the create needs no download"`
	Host      HostResources `json:"host"`
	Warnings  []string      `json:"warnings,omitempty"`
}

// PlanNode is one node of a plan.
type PlanNode struct {
	Name    string   `json:"name"`
	CPUs    int      `json:"cpus"`
	Memory  string   `json:"memory"`
	Disks   []string `json:"disks" jsonschema:"sizes, root disk first"`
	IP      string   `json:"ip" jsonschema:"on the corosync network"`
	UI      string   `json:"ui"`
	SSHPort int      `json:"sshPort"`
}

// HostResources is what this host has free for clusters.
type HostResources struct {
	MemAvailableMiB int `json:"memAvailableMiB"`
	DiskFreeMiB     int `json:"diskFreeMiB" jsonschema:"in the file system holding cluster state"`
}

const (
	// nodeDiskMiB is the disk a node uses on the host after a create.
	nodeDiskMiB = 4 << 10
	// isoMiB is the space the ISO and its extracted files take.
	isoMiB = 1800
	// portRange is the span of ports a cluster may use above its base: SSH
	// ports at base+101 up to base+132 for 32 node numbers.
	portRange = 200
)

// NewPlan works out what creating cfg would set up, assigning a port base if
// cfg has none.
func NewPlan(cfg *config.Cluster) (*Plan, error) {
	if err := AssignPortBase(cfg); err != nil {
		return nil, err
	}
	nodes := cfg.NodeList()
	p := &Plan{Name: cfg.Name, PortBase: cfg.Access.PortBase, MemoryMiB: memoryMiB(nodes), ISOCached: image.Cached(cfg.Proxmox.Version, state.CacheHome())}
	for _, n := range nodes {
		pn := PlanNode{Name: n.Name, CPUs: n.Spec.CPUs, Memory: n.Spec.Memory, IP: nodeIP(n, cfg.CorosyncNetwork())}
		for _, d := range append([]config.Disk{n.Spec.RootDisk}, n.Spec.DataDisks...) {
			size, err := config.DiskMiB(d.Size)
			if err != nil {
				return nil, err
			}
			p.DiskMiB += size
			pn.Disks = append(pn.Disks, d.Size)
		}
		ui, ssh := cfg.Ports(n.Index)
		pn.UI, pn.SSHPort = fmt.Sprintf("https://127.0.0.1:%d", ui), ssh
		p.Nodes = append(p.Nodes, pn)
	}
	p.Host = hostResources()
	p.Warnings = append(cfg.Warnings(), capacityWarnings(p.MemoryMiB, needDiskMiB(len(nodes), p.ISOCached), p.Host)...)
	return p, nil
}

// AssignPortBase gives a new cluster without access.portBase the first range
// from config.DefaultPortBase that no other cluster uses, running or not, and
// whose ports are free.
func AssignPortBase(cfg *config.Cluster) error {
	if cfg.Access.PortBase != 0 {
		return nil
	}
	taken, err := otherPortBases(cfg.Name)
	if err != nil {
		return err
	}
	for base := config.DefaultPortBase; base+100+cfg.MaxNodeIndex() <= 65535; base += config.PortBaseStep {
		if overlaps(base, taken) || !portsFree(cfg, base) {
			continue
		}
		cfg.Access.PortBase = base
		return nil
	}
	return fmt.Errorf("no free port range from %d; set access.portBase", config.DefaultPortBase)
}

// otherPortBases returns the port bases of the clusters other than name.
func otherPortBases(name string) ([]int, error) {
	names, err := state.List()
	if err != nil {
		return nil, err
	}
	var bases []int
	for _, n := range names {
		if n == name {
			continue
		}
		if cfg, err := state.ForCluster(n).LoadConfig(); err == nil {
			bases = append(bases, cfg.Access.PortBase)
		}
	}
	return bases, nil
}

func overlaps(base int, taken []int) bool {
	for _, b := range taken {
		if base < b+portRange && b < base+portRange {
			return true
		}
	}
	return false
}

// portsFree reports whether the UI and SSH ports of every node of cfg are free
// with the given base.
func portsFree(cfg *config.Cluster, base int) bool {
	for _, n := range cfg.NodeList() {
		for _, port := range []int{base + n.Index, base + 100 + n.Index} {
			l, err := net.Listen("tcp", net.JoinHostPort(cfg.Access.BindAddress, strconv.Itoa(port)))
			if err != nil {
				return false
			}
			l.Close()
		}
	}
	return true
}

// checkCapacity logs what the host lacks to run nodes, as warnings.
func checkCapacity(cfg *config.Cluster, nodes []config.Node, p progress.Sink) {
	isoCached := image.Cached(cfg.Proxmox.Version, state.CacheHome())
	for _, w := range capacityWarnings(memoryMiB(nodes), needDiskMiB(len(nodes), isoCached), hostResources()) {
		p.Logf("warning: %s", w)
	}
}

// memoryMiB is the memory of nodes; the specs are validated.
func memoryMiB(nodes []config.Node) int {
	total := 0
	for _, n := range nodes {
		mem, _ := config.MemoryMiB(n.Spec.Memory)
		total += mem
	}
	return total
}

func hostResources() HostResources {
	var h HostResources
	h.MemAvailableMiB, _ = doctor.MemAvailableMiB()
	h.DiskFreeMiB, _, _ = doctor.DiskFreeMiB(state.DataHome())
	return h
}

// needDiskMiB estimates the host disk a create of nodes takes.
func needDiskMiB(nodes int, isoCached bool) int {
	need := nodes * nodeDiskMiB
	if !isoCached {
		need += isoMiB
	}
	return need
}

// capacityWarnings compares what nodes need with what the host has. Shortages
// are warnings: nodes return unused memory to the host and disks are thin, so
// clusters often fit anyway.
func capacityWarnings(memMiB, diskMiB int, h HostResources) []string {
	var out []string
	if h.MemAvailableMiB > 0 && memMiB > h.MemAvailableMiB {
		out = append(out, fmt.Sprintf("the nodes have %s of memory and the host %s available: idle nodes return about half, but under load the host may swap or kill nodes",
			gib(memMiB), gib(h.MemAvailableMiB)))
	}
	if h.DiskFreeMiB > 0 && diskMiB > h.DiskFreeMiB {
		out = append(out, fmt.Sprintf("the create needs about %s of disk and %s is free in %s", gib(diskMiB), gib(h.DiskFreeMiB), state.DataHome()))
	}
	return out
}

func gib(mib int) string { return fmt.Sprintf("%.1f GiB", float64(mib)/1024) }
