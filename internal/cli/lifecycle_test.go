package cli

import (
	"testing"

	"github.com/virtbase/proxbase/internal/cluster"
)

func TestHealthy(t *testing.T) {
	yes, no := true, false
	ok := func() *cluster.Status {
		return &cluster.Status{Phase: "ready", Quorate: &yes, Ceph: "HEALTH_OK",
			Nodes: []cluster.NodeStatus{{Name: "pve1", Running: true, Online: &yes}}}
	}
	if why := healthy(ok()); why != "" {
		t.Fatalf("healthy cluster reported %q", why)
	}
	cases := map[string]func(*cluster.Status){
		"phase creating":              func(s *cluster.Status) { s.Phase = "creating" },
		"not quorate":                 func(s *cluster.Status) { s.Quorate = &no },
		"pve1 not running or offline": func(s *cluster.Status) { s.Nodes[0].Online = nil },
		"ceph HEALTH_WARN":            func(s *cluster.Status) { s.Ceph = "HEALTH_WARN" },
	}
	for want, mutate := range cases {
		s := ok()
		mutate(s)
		if got := healthy(s); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
