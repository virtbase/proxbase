// Package ceph installs and configures a hyperconverged Ceph cluster on the nodes:
// monitors, managers, one OSD per data disk, RBD pools and CephFS, registered as
// cluster storage.
package ceph

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/retry"
	"github.com/virtbase/proxbase/internal/storage"
)

const (
	fsName     = "cephfs"
	fsContent  = "iso,vztmpl,backup,snippets,import"
	rbdContent = "images,rootdir"
	maxMons    = 3
)

// Backend is the Ceph storage backend.
type Backend struct {
	cfg             config.Ceph
	public, private config.Network
	size, minSize   int
}

var (
	_ storage.Backend        = (*Backend)(nil)
	_ storage.Waiter         = (*Backend)(nil)
	_ storage.StopPreparer   = (*Backend)(nil)
	_ storage.NodeRemover    = (*Backend)(nil)
	_ storage.HealthReporter = (*Backend)(nil)
)

// New returns the backend if Ceph is enabled, or nil.
func New(cfg *config.Cluster) *Backend {
	if !cfg.CephEnabled() {
		return nil
	}
	b := &Backend{cfg: *cfg.Storage.Ceph}
	b.public, b.private = cfg.CephNetworks()
	b.size, b.minSize = cfg.CephFSSize()
	return b
}

// Setup brings Ceph to the configured state; every step checks first.
func (b *Backend) Setup(ctx context.Context, h storage.Host) error {
	nodes := h.Nodes()
	if err := b.install(ctx, h); err != nil {
		return err
	}
	apis := map[string]*pve.Client{}
	for _, n := range nodes {
		api, err := h.API(ctx, n.Name)
		if err != nil {
			return err
		}
		apis[n.Name] = api
	}
	first := nodes[0].Name
	api := apis[first]
	if err := b.init(ctx, h, api, first); err != nil {
		return err
	}

	// Monitors (up to three), a manager on every node; one at a time.
	var mons []named
	if err := retry.Do(ctx, 2*time.Minute, 2*time.Second, func() error { return api.Get(ctx, "/nodes/"+first+"/ceph/mon", &mons) }); err != nil {
		return fmt.Errorf("list ceph mon: %w", err)
	}
	for _, n := range nodes {
		if !hasName(mons, n.Name) && len(mons) < maxMons {
			if err := daemon(ctx, h, apis[n.Name], api, n.Name, "mon"); err != nil {
				return err
			}
			mons = append(mons, named{Name: n.Name})
		}
		if err := daemon(ctx, h, apis[n.Name], api, n.Name, "mgr"); err != nil {
			return err
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		g.Go(func() error { return createOSDs(gctx, h, apis[n.Name], n.Name, b.cfg.OSDDisks) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	if err := createPools(ctx, h, api, first, b.cfg.Pools); err != nil {
		return err
	}
	if *b.cfg.CephFS {
		for _, n := range nodes {
			if err := daemon(ctx, h, apis[n.Name], api, n.Name, "mds"); err != nil {
				return err
			}
		}
		if err := createFS(ctx, h, api, first); err != nil {
			return err
		}
	}
	return b.Wait(ctx, h)
}

// init writes ceph.conf with the public and cluster networks unless it exists.
func (b *Backend) init(ctx context.Context, h storage.Host, api *pve.Client, first string) error {
	s, err := h.SSH(ctx, first)
	if err != nil {
		return err
	}
	_, missing := s.Run("test -e /etc/pve/ceph.conf")
	s.Close()
	if missing == nil {
		return nil
	}
	h.Step("ceph: init (public %s %s, cluster %s %s)", b.public.Name, b.public.CIDR, b.private.Name, b.private.CIDR)
	form := url.Values{"network": {b.public.CIDR}, "cluster-network": {b.private.CIDR}, "size": {strconv.Itoa(b.size)}, "min_size": {strconv.Itoa(b.minSize)}}
	if err := api.Post(ctx, "/nodes/"+first+"/ceph/init", form, nil); err != nil {
		return fmt.Errorf("ceph init: %w", err)
	}
	return nil
}

// BeforeStop unmounts CephFS while the monitors are still up; otherwise the kernel
// client blocks each node's shutdown for about a minute.
func (b *Backend) BeforeStop(ctx context.Context, h storage.Host, running []string) {
	if !*b.cfg.CephFS {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var g errgroup.Group
	for _, n := range running {
		g.Go(func() error {
			s, err := h.SSH(ctx, n)
			if err != nil {
				return nil
			}
			defer s.Close()
			_, _ = s.Run("systemctl stop pvestatd; umount /mnt/pve/" + fsName + " 2>/dev/null || true")
			return nil
		})
	}
	_ = g.Wait()
}
