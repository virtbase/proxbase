package cluster

import "testing"

func TestStatusProblem(t *testing.T) {
	yes, no := true, false
	ok := func() *Status {
		return &Status{Phase: "ready", Quorate: &yes, Ceph: "HEALTH_OK",
			Nodes: []NodeStatus{{Name: "pve1", Running: true, Online: &yes}}}
	}
	if why := ok().Problem(); why != "" {
		t.Fatalf("healthy cluster reported %q", why)
	}
	cases := map[string]func(*Status){
		"phase creating":              func(s *Status) { s.Phase = "creating" },
		"not quorate":                 func(s *Status) { s.Quorate = &no },
		"pve1 not running or offline": func(s *Status) { s.Nodes[0].Online = nil },
		"ceph HEALTH_WARN":            func(s *Status) { s.Ceph = "HEALTH_WARN" },
	}
	for want, mutate := range cases {
		s := ok()
		mutate(s)
		if got := s.Problem(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
