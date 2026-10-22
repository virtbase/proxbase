package cluster

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/state"
)

func newConfig(t *testing.T, name string, nodes, portBase int) *config.Cluster {
	t.Helper()
	cfg := &config.Cluster{Name: name, Nodes: config.Nodes{Count: nodes}, Access: config.Access{PortBase: portBase}}
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// saveCluster writes the state directory of a cluster with cfg.
func saveCluster(t *testing.T, cfg *config.Cluster) {
	t.Helper()
	d := state.ForCluster(cfg.Name)
	if err := d.Init(); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(&state.State{Name: cfg.Name, Phase: state.PhaseReady}); err != nil {
		t.Fatal(err)
	}
}

func TestAssignPortBase(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	saveCluster(t, newConfig(t, "a", 1, 18000)) // stopped, still owns its range
	saveCluster(t, newConfig(t, "b", 1, 19100)) // its ports overlap the range of 19000
	// Something else listens on the first SSH port of 20000.
	l, err := net.Listen("tcp", "127.0.0.1:20101")
	if err != nil {
		t.Skipf("port 20101: %v", err)
	}
	defer l.Close()

	cfg := newConfig(t, "new", 3, 0)
	if err := AssignPortBase(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Access.PortBase != 21000 {
		t.Errorf("port base %d, want 21000 (18000 taken, 19000 overlaps b, 20000 busy)", cfg.Access.PortBase)
	}
	cfg = newConfig(t, "fixed", 1, 30000)
	if err := AssignPortBase(cfg); err != nil || cfg.Access.PortBase != 30000 {
		t.Errorf("explicit port base changed to %d (%v)", cfg.Access.PortBase, err)
	}
}

func TestOverlaps(t *testing.T) {
	taken := []int{18000, 25100}
	for base, want := range map[int]bool{18000: true, 18199: true, 18200: false, 17801: true, 17800: false, 25000: true, 26000: false} {
		if got := overlaps(base, taken); got != want {
			t.Errorf("overlaps(%d) = %v, want %v", base, got, want)
		}
	}
}

func TestCapacityWarnings(t *testing.T) {
	h := HostResources{MemAvailableMiB: 8 << 10, DiskFreeMiB: 10 << 10}
	tests := []struct {
		name          string
		mem, disk     int
		host          HostResources
		wantMem, want bool
	}{
		{name: "fits", mem: 8 << 10, disk: 10 << 10, host: h},
		{name: "memory short", mem: 12 << 10, disk: 4 << 10, host: h, wantMem: true},
		{name: "disk short", mem: 4 << 10, disk: 12 << 10, host: h, want: true},
		{name: "host unknown", mem: 64 << 10, disk: 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(capacityWarnings(tt.mem, tt.disk, tt.host), "\n")
			if strings.Contains(got, "memory") != tt.wantMem || strings.Contains(got, "of disk") != tt.want {
				t.Errorf("capacityWarnings = %q", got)
			}
		})
	}
}

func TestNewPlan(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := newConfig(t, "lab", 3, 0)
	p, err := NewPlan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.PortBase == 0 || cfg.Access.PortBase != p.PortBase || len(p.Nodes) != 3 || p.ISOCached {
		t.Fatalf("plan %+v", p)
	}
	n := p.Nodes[2]
	if n.Name != "pve3" || n.IP != "10.10.10.13" || n.UI != "https://127.0.0.1:"+strconv.Itoa(p.PortBase+3) || n.SSHPort != p.PortBase+103 {
		t.Errorf("node %+v", n)
	}
	if p.MemoryMiB != 3*4096 || p.DiskMiB != 3*64<<10 {
		t.Errorf("memory %d, disk %d", p.MemoryMiB, p.DiskMiB)
	}
}

func TestRootStorage(t *testing.T) {
	for fs, want := range map[string]string{"zfs": "local-zfs", "btrfs": "local-btrfs", "ext4": "local-lvm", "xfs": "local-lvm"} {
		if got := rootStorage(fs); got != want {
			t.Errorf("rootStorage(%s) = %s, want %s", fs, got, want)
		}
	}
}
