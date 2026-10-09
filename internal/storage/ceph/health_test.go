package ceph

import (
	"encoding/json"
	"testing"

	"github.com/virtbase/proxbase/internal/config"
)

func TestSettled(t *testing.T) {
	parse := func(s string) status {
		var st status
		if err := json.Unmarshal([]byte(s), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	ok := `{"health":{"status":"HEALTH_OK"},"osdmap":{"num_osds":3,"num_up_osds":3,"num_in_osds":3},"pgmap":{"num_pgs":73,"pgs_by_state":[{"state_name":"active+clean","count":73}]}}`
	if !parse(ok).settled() {
		t.Fatal("healthy cluster not settled")
	}
	for name, s := range map[string]string{
		"peering":  `{"health":{"status":"HEALTH_OK"},"osdmap":{"num_osds":3,"num_up_osds":3,"num_in_osds":3},"pgmap":{"num_pgs":73,"pgs_by_state":[{"state_name":"active+clean","count":46},{"state_name":"creating+peering","count":27}]}}`,
		"osd down": `{"health":{"status":"HEALTH_OK"},"osdmap":{"num_osds":4,"num_up_osds":3,"num_in_osds":4},"pgmap":{"num_pgs":73,"pgs_by_state":[{"state_name":"active+clean","count":73}]}}`,
		"warn":     `{"health":{"status":"HEALTH_WARN","checks":{"OSD_DOWN":{}}},"osdmap":{"num_osds":3,"num_up_osds":3,"num_in_osds":3},"pgmap":{"num_pgs":73,"pgs_by_state":[{"state_name":"active+clean","count":73}]}}`,
	} {
		if parse(s).settled() {
			t.Errorf("%s: must not be settled", name)
		}
	}
	if c := parse(`{"health":{"checks":{"B":{},"A":{}}}}`).checks(); c != "A, B" {
		t.Fatalf("checks %q", c)
	}
}

func TestCheckRemove(t *testing.T) {
	b := &Backend{cfg: config.Ceph{Pools: []config.CephPool{{Name: "ceph-vm", Size: 3}}}}
	if err := b.CheckRemove(3, false); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckRemove(2, false); err == nil {
		t.Fatal("size 3 on 2 nodes must be refused")
	}
	if err := b.CheckRemove(2, true); err != nil {
		t.Fatal("--force must allow it")
	}
}
