package ceph

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/retry"
	"github.com/virtbase/proxbase/internal/storage"
)

type named struct {
	Name string `json:"name"`
}

func hasName(list []named, name string) bool {
	return slices.ContainsFunc(list, func(n named) bool { return n.Name == name })
}

// daemon creates a mon, mgr or mds named after the node unless it exists. API
// calls fail briefly while monitors elect, so reads are retried.
func daemon(ctx context.Context, h storage.Host, api, first *pve.Client, node, kind string) error {
	var list []named
	if err := retry.Do(ctx, 2*time.Minute, 2*time.Second, func() error { return first.Get(ctx, "/nodes/"+node+"/ceph/"+kind, &list) }); err != nil {
		return fmt.Errorf("list ceph %s: %w", kind, err)
	}
	if hasName(list, node) {
		return nil
	}
	h.Step("%s: ceph %s", node, kind)
	if err := api.PostTask(ctx, node, "/nodes/"+node+"/ceph/"+kind+"/"+node, url.Values{}, 3*time.Minute); err != nil {
		return fmt.Errorf("%s: create ceph %s: %w", node, kind, err)
	}
	if kind != "mon" {
		return nil
	}
	// Monitors re-elect after a new one joins; wait until it is in quorum.
	return retry.Do(ctx, 2*time.Minute, 2*time.Second, func() error {
		var st struct {
			Quorum []string `json:"quorum_names"`
		}
		if err := first.Get(ctx, "/cluster/ceph/status", &st); err != nil {
			return err
		}
		if !slices.Contains(st.Quorum, node) {
			return fmt.Errorf("mon.%s not in quorum %v", node, st.Quorum)
		}
		return nil
	})
}

type diskInfo struct {
	DevPath string   `json:"devpath"`
	Used    string   `json:"used"`
	OSDs    []string `json:"osdid-list"`
}

// createOSDs creates an OSD on every listed disk that has none yet.
func createOSDs(ctx context.Context, h storage.Host, api *pve.Client, node string, disks []string) error {
	var list []diskInfo
	if err := api.Get(ctx, "/nodes/"+node+"/disks/list", &list); err != nil {
		return err
	}
	for _, dev := range disks {
		path := "/dev/" + dev
		i := slices.IndexFunc(list, func(d diskInfo) bool { return d.DevPath == path })
		switch {
		case i < 0:
			return fmt.Errorf("%s: disk %s not found", node, path)
		case len(list[i].OSDs) > 0:
			continue
		case list[i].Used != "":
			return fmt.Errorf("%s: disk %s is in use (%s)", node, path, list[i].Used)
		}
		h.Step("%s: ceph osd on %s", node, path)
		if err := api.PostTask(ctx, node, "/nodes/"+node+"/ceph/osd", url.Values{"dev": {path}}, 5*time.Minute); err != nil {
			return fmt.Errorf("%s: create osd on %s: %w", node, path, err)
		}
	}
	return nil
}

// createPools creates missing pools; RBD pools become storage with explicit content.
func createPools(ctx context.Context, h storage.Host, api *pve.Client, node string, pools []config.CephPool) error {
	var existing []struct {
		Name string `json:"pool_name"`
	}
	if err := api.Get(ctx, "/nodes/"+node+"/ceph/pool", &existing); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range existing {
		have[e.Name] = true
	}
	for _, p := range pools {
		if have[p.Name] {
			continue
		}
		h.Step("ceph: pool %s (size %d/%d, %d PGs)", p.Name, p.Size, p.MinSize, p.PGNum)
		rbd := p.Application == "rbd"
		form := url.Values{
			"name": {p.Name}, "size": {strconv.Itoa(p.Size)}, "min_size": {strconv.Itoa(p.MinSize)},
			"pg_num": {strconv.Itoa(p.PGNum)}, "pg_autoscale_mode": {"off"}, "application": {p.Application},
			"add_storages": {map[bool]string{true: "1", false: "0"}[rbd]},
		}
		if err := api.PostTask(ctx, node, "/nodes/"+node+"/ceph/pool", form, 3*time.Minute); err != nil {
			return fmt.Errorf("create ceph pool %s: %w", p.Name, err)
		}
		if rbd {
			if err := api.Put(ctx, "/storage/"+p.Name, url.Values{"content": {rbdContent}}, nil); err != nil {
				return fmt.Errorf("set content of storage %s: %w", p.Name, err)
			}
		}
	}
	return nil
}

// createFS creates CephFS and its storage entry unless they exist.
func createFS(ctx context.Context, h storage.Host, api *pve.Client, node string) error {
	var fs []named
	if err := api.Get(ctx, "/nodes/"+node+"/ceph/fs", &fs); err != nil {
		return err
	}
	if len(fs) == 0 {
		h.Step("ceph: cephfs %s", fsName)
		form := url.Values{"pg_num": {"32"}, "add-storage": {"1"}}
		if err := api.PostTask(ctx, node, "/nodes/"+node+"/ceph/fs/"+fsName, form, 5*time.Minute); err != nil {
			return fmt.Errorf("create cephfs: %w", err)
		}
	}
	var storages []struct {
		Storage string `json:"storage"`
	}
	if err := api.Get(ctx, "/storage", &storages); err != nil {
		return err
	}
	if !slices.ContainsFunc(storages, func(s struct {
		Storage string `json:"storage"`
	}) bool {
		return s.Storage == fsName
	}) {
		form := url.Values{"storage": {fsName}, "type": {"cephfs"}, "fs-name": {fsName}, "content": {fsContent}}
		if err := api.Post(ctx, "/storage", form, nil); err != nil {
			return fmt.Errorf("add storage %s: %w", fsName, err)
		}
	}
	if err := api.Put(ctx, "/storage/"+fsName, url.Values{"content": {fsContent}}, nil); err != nil {
		return fmt.Errorf("set content of storage %s: %w", fsName, err)
	}
	return nil
}
