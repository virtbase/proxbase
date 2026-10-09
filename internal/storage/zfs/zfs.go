// Package zfs creates ZFS pools on the data disks of every node and registers
// them as cluster storage.
package zfs

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/storage"
)

// Backend is the ZFS storage backend.
type Backend struct {
	Pools []config.ZFSPool
}

var (
	_ storage.Backend     = (*Backend)(nil)
	_ storage.NodeRemover = (*Backend)(nil)
)

// New returns the backend for the configured pools, or nil if there are none.
func New(cfg *config.Cluster) *Backend {
	if len(cfg.Storage.ZFS) == 0 {
		return nil
	}
	return &Backend{Pools: cfg.Storage.ZFS}
}

// Setup creates missing pools on every node and the cluster storage entries,
// keeping their node lists current.
func (b *Backend) Setup(ctx context.Context, h storage.Host) error {
	nodes := h.Nodes()
	var names []string
	for _, n := range nodes {
		names = append(names, n.Name)
		if err := b.createPools(ctx, h, n.Name); err != nil {
			return err
		}
	}
	return b.registerStorage(ctx, h, nodes[0].Name, names, true)
}

func (b *Backend) createPools(ctx context.Context, h storage.Host, node string) error {
	api, err := h.API(ctx, node)
	if err != nil {
		return err
	}
	var pools []struct {
		Name string `json:"name"`
	}
	if err := api.Get(ctx, "/nodes/"+node+"/disks/zfs", &pools); err != nil {
		return err
	}
	for _, z := range b.Pools {
		if slices.ContainsFunc(pools, func(p struct {
			Name string `json:"name"`
		}) bool {
			return p.Name == z.Name
		}) {
			continue
		}
		devs := make([]string, len(z.Disks))
		for i, d := range z.Disks {
			devs[i] = "/dev/" + d
		}
		h.Step("%s: creating ZFS pool %s (%s on %s)", node, z.Name, z.Raid, strings.Join(z.Disks, ", "))
		form := url.Values{"name": {z.Name}, "raidlevel": {z.Raid}, "devices": {strings.Join(devs, ",")}, "ashift": {"12"}, "add_storage": {"0"}}
		if err := api.PostTask(ctx, node, "/nodes/"+node+"/disks/zfs", form, 2*time.Minute); err != nil {
			return fmt.Errorf("%s: create zfs pool %s: %w", node, z.Name, err)
		}
	}
	return nil
}

// registerStorage adds missing storage entries (if add) and sets the node lists.
func (b *Backend) registerStorage(ctx context.Context, h storage.Host, anchor string, nodes []string, add bool) error {
	api, err := h.API(ctx, anchor)
	if err != nil {
		return err
	}
	var storages []struct {
		Storage string `json:"storage"`
	}
	if err := api.Get(ctx, "/storage", &storages); err != nil {
		return err
	}
	list := strings.Join(nodes, ",")
	for _, z := range b.Pools {
		exists := slices.ContainsFunc(storages, func(s struct {
			Storage string `json:"storage"`
		}) bool {
			return s.Storage == z.Name
		})
		if exists {
			if err := api.Put(ctx, "/storage/"+z.Name, url.Values{"nodes": {list}}, nil); err != nil {
				return fmt.Errorf("update storage %s: %w", z.Name, err)
			}
			continue
		}
		if !add {
			continue
		}
		form := url.Values{"storage": {z.Name}, "type": {"zfspool"}, "pool": {z.Name}, "content": {"images,rootdir"}, "sparse": {"1"}, "nodes": {list}}
		if err := api.Post(ctx, "/storage", form, nil); err != nil {
			return fmt.Errorf("add storage %s: %w", z.Name, err)
		}
		h.Step("storage %s (zfspool) added for all nodes", z.Name)
	}
	return nil
}

// CheckRemove always succeeds: each node's pool only holds that node's data.
func (b *Backend) CheckRemove(int, bool) error { return nil }

// RemoveNode drops node from the storage entries while it is still a member.
func (b *Backend) RemoveNode(ctx context.Context, h storage.Host, node string, _ bool) error {
	var rest []string
	for _, n := range h.Nodes() {
		if n.Name != node {
			rest = append(rest, n.Name)
		}
	}
	return b.registerStorage(ctx, h, rest[0], rest, false)
}
