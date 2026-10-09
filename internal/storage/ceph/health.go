package ceph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/retry"
	"github.com/virtbase/proxbase/internal/storage"
)

const readyTimeout = 10 * time.Minute

type status struct {
	Health struct {
		Status string                    `json:"status"`
		Checks map[string]map[string]any `json:"checks"`
	} `json:"health"`
	OSDMap struct {
		Num int `json:"num_osds"`
		Up  int `json:"num_up_osds"`
		In  int `json:"num_in_osds"`
	} `json:"osdmap"`
	PGMap struct {
		NumPGs  int `json:"num_pgs"`
		ByState []struct {
			Name  string `json:"state_name"`
			Count int    `json:"count"`
		} `json:"pgs_by_state"`
	} `json:"pgmap"`
}

// settled reports HEALTH_OK, every OSD up and in, and every placement group
// active+clean (new pools and OSDs settle while health already says OK).
func (s status) settled() bool {
	if s.OSDMap.Up != s.OSDMap.Num || s.OSDMap.In != s.OSDMap.Num {
		return false
	}
	clean := 0
	for _, st := range s.PGMap.ByState {
		if st.Name == "active+clean" {
			clean += st.Count
		}
	}
	return s.Health.Status == "HEALTH_OK" && clean == s.PGMap.NumPGs
}

func (s status) checks() string {
	names := make([]string, 0, len(s.Health.Checks))
	for k := range s.Health.Checks {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Health returns the health status with failing checks, or "" if unavailable.
func (b *Backend) Health(ctx context.Context, api *pve.Client) string {
	var st status
	if err := api.Get(ctx, "/cluster/ceph/status", &st); err != nil {
		return ""
	}
	if c := st.checks(); c != "" {
		return st.Health.Status + " (" + c + ")"
	}
	return st.Health.Status
}

// storageIDs are the storage entries Proxbase registers for Ceph.
func (b *Backend) storageIDs() []string {
	var ids []string
	for _, p := range b.cfg.Pools {
		if p.Application == "rbd" {
			ids = append(ids, p.Name)
		}
	}
	if *b.cfg.CephFS {
		ids = append(ids, fsName)
	}
	return ids
}

// storagesActive reports whether every Ceph storage is active on every node.
func (b *Backend) storagesActive(ctx context.Context, h storage.Host, api *pve.Client) error {
	for _, n := range h.Nodes() {
		for _, id := range b.storageIDs() {
			var st struct {
				Active int `json:"active"`
			}
			if err := api.Get(ctx, "/nodes/"+n.Name+"/storage/"+id+"/status", &st); err != nil {
				return err
			}
			if st.Active != 1 {
				return fmt.Errorf("storage %s inactive on %s", id, n.Name)
			}
		}
	}
	return nil
}

// Wait waits for HEALTH_OK, all PGs active+clean and all Ceph storages active on
// every node; it shows `ceph -s` if that does not happen in time.
func (b *Backend) Wait(ctx context.Context, h storage.Host) error {
	first := h.Nodes()[0].Name
	api, err := h.API(ctx, first)
	if err != nil {
		return err
	}
	var last status
	err = retry.Do(ctx, readyTimeout, 3*time.Second, func() error {
		if err := api.Get(ctx, "/cluster/ceph/status", &last); err != nil {
			return err
		}
		if !last.settled() {
			return errors.New("not settled")
		}
		return b.storagesActive(ctx, h, api)
	})
	if err == nil {
		h.Step("ceph: HEALTH_OK, %d OSDs up, %d PGs active+clean, storages active on all nodes", last.OSDMap.Num, last.PGMap.NumPGs)
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	out := ""
	if s, serr := h.SSH(ctx, first); serr == nil {
		out, _ = s.Run("ceph -s 2>&1; ceph health detail 2>&1 | head -20")
		s.Close()
	}
	return errors.Join(fmt.Errorf("ceph not ready after %s (%s %s):\n%s", readyTimeout, last.Health.Status, last.checks(), out), err)
}
