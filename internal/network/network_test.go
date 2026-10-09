package network

import (
	"strings"
	"testing"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/state"
)

func TestNICs(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader("nodes: {count: 2}\nnetworks: [{name: a, cidr: 10.1.0.0/24}, {name: b}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetDefaults()
	s := Switch{Dir: state.Dir("/s"), Cluster: "lab"}
	nics := s.NICs(cfg, cfg.NodeList()[1])
	if len(nics) != 2 {
		t.Fatalf("want 2 NICs, got %d", len(nics))
	}
	if n := nics[1]; n.MAC != "52:54:00:50:02:02" || n.Local != "/s/run/pve2-b.sock" || n.Remote != "/s/run/sw-b.sock" {
		t.Fatalf("NIC: %+v", n)
	}
}

func TestPolicyPartition(t *testing.T) {
	d := state.Dir(t.TempDir())
	cfg, _ := config.Decode(strings.NewReader("nodes: {count: 3}\n"))
	cfg.SetDefaults()
	s := Switch{Dir: d, Cluster: "lab"}
	p := policy(d, "cluster", Fault{Partition: [][]string{{"pve3"}}})
	sock := func(n string) string { return nodeSocket(d, n, "cluster") }
	if p.Block(sock("pve1"), sock("pve2")) || !p.Block(sock("pve1"), sock("pve3")) || !p.Block(sock("pve3"), sock("pve2")) {
		t.Fatal("pve3 must be isolated, pve1 and pve2 connected")
	}
	if f, err := s.Faults(); err != nil || len(f) != 0 {
		t.Fatalf("no faults file must mean no faults: %v %v", f, err)
	}
}
