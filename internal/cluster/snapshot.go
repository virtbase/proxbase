package cluster

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/vm"
)

var snapRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,39}$`)

// disks returns every disk image of the cluster.
func (c *Cluster) disks() []vm.Disk {
	var out []vm.Disk
	for _, n := range c.Cfg.NodeList() {
		out = append(out, c.spec(n).Disks()...)
	}
	return out
}

func (c *Cluster) nodeNames() []string {
	var out []string
	for _, n := range c.Cfg.NodeList() {
		out = append(out, n.Name)
	}
	return out
}

// requireStopped makes sure no node runs; snapshots of running disks are not consistent.
func (c *Cluster) requireStopped() error {
	for _, n := range c.Cfg.NodeList() {
		if c.running(n) {
			return fmt.Errorf("%s is running; snapshots need a stopped cluster (proxbase stop %s)", n.Name, c.Cfg.Name)
		}
	}
	return nil
}

// CheckSnapshotName validates the name of a snapshot.
func CheckSnapshotName(name string) error {
	if !snapRe.MatchString(name) {
		return fmt.Errorf("invalid snapshot name %q: letters, digits, '.', '_' and '-', at most 40", name)
	}
	return nil
}

// checkNewSnapshot validates the name of a snapshot about to be saved.
func (c *Cluster) checkNewSnapshot(name string) error {
	if err := CheckSnapshotName(name); err != nil {
		return err
	}
	if c.St.Phase != state.PhaseReady {
		return fmt.Errorf("cluster is %s; snapshots need a ready cluster", c.St.Phase)
	}
	if c.St.Snapshot(name) != nil {
		return fmt.Errorf("snapshot %q exists", name)
	}
	return nil
}

// SnapshotSave takes a snapshot of every disk of the stopped cluster.
func (c *Cluster) SnapshotSave(ctx context.Context, name string) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := c.checkNewSnapshot(name); err != nil {
		return err
	}
	if err := c.requireStopped(); err != nil {
		return err
	}
	if err := c.runtime.Snapshot(ctx, vm.SnapshotSave, name, c.disks()); err != nil {
		return err
	}
	c.St.Snapshots = append(c.St.Snapshots, state.Snapshot{Name: name, Created: time.Now().UTC(), Nodes: c.nodeNames()})
	return c.save()
}

// SnapshotSaveLive snapshots the running cluster with its memory. Every node is
// paused until all snapshots are taken, so they show one moment of the cluster.
func (c *Cluster) SnapshotSaveLive(ctx context.Context, name string) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := c.checkNewSnapshot(name); err != nil {
		return err
	}
	live, ok := c.runtime.(vm.LiveSnapshotter)
	if !ok {
		return errors.New("the node runtime cannot take live snapshots")
	}
	if len(c.St.Faults) > 0 {
		return fmt.Errorf("cluster %s has active faults; clear them first (proxbase fault clear %s)", c.Cfg.Name, c.Cfg.Name)
	}
	nodes := c.Cfg.NodeList()
	for _, n := range nodes {
		if !c.running(n) {
			return fmt.Errorf("%s is not running; live snapshots need a running cluster (proxbase start %s)", n.Name, c.Cfg.Name)
		}
	}
	var paused []config.Node
	defer func() {
		for _, n := range paused {
			if err := live.Resume(context.WithoutCancel(ctx), c.spec(n)); err != nil {
				c.step("%s: resume failed: %v", n.Name, err)
			}
		}
	}()
	for _, n := range nodes {
		if err := live.Pause(ctx, c.spec(n)); err != nil {
			return fmt.Errorf("%s: pause: %w", n.Name, err)
		}
		paused = append(paused, n)
	}
	var mu sync.Mutex
	var saved []config.Node
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		g.Go(func() error {
			if err := live.SaveLive(gctx, c.spec(n), name); err != nil {
				return err
			}
			mu.Lock()
			saved = append(saved, n)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		for _, n := range saved {
			_ = live.DeleteLive(context.WithoutCancel(ctx), c.spec(n), name)
		}
		return err
	}
	c.St.Snapshots = append(c.St.Snapshots, state.Snapshot{Name: name, Created: time.Now().UTC(), Nodes: c.nodeNames(), Live: true})
	return c.save()
}

// SnapshotRestore resets every disk to the snapshot.
func (c *Cluster) SnapshotRestore(ctx context.Context, name string) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	snap := c.St.Snapshot(name)
	if snap == nil {
		return fmt.Errorf("no snapshot %q", name)
	}
	if !slices.Equal(snap.Nodes, c.nodeNames()) {
		return fmt.Errorf("snapshot %q was taken with nodes %s, the cluster now has %s", name, strings.Join(snap.Nodes, ","), strings.Join(c.nodeNames(), ","))
	}
	if err := c.requireStopped(); err != nil {
		return err
	}
	if snap.Live {
		return c.resume(ctx, name)
	}
	return c.runtime.Snapshot(ctx, vm.SnapshotRestore, name, c.disks())
}

// resume starts every node in the state of live snapshot name. The nodes load
// it paused and are resumed together, so the cluster continues as one; their
// clocks, which stood still in the snapshot, are set to the host's time.
func (c *Cluster) resume(ctx context.Context, name string) error {
	live, ok := c.runtime.(vm.LiveSnapshotter)
	if !ok {
		return errors.New("the node runtime cannot start from live snapshots")
	}
	if err := c.net.Start(c.Cfg); err != nil {
		return err
	}
	nodes := c.Cfg.NodeList()
	for i, n := range nodes {
		spec := c.spec(n)
		spec.LoadSnapshot = name
		if err := c.runtime.Start(ctx, spec); err != nil {
			for _, started := range nodes[:i] {
				_ = c.runtime.Stop(context.WithoutCancel(ctx), c.spec(started), 0)
			}
			return fmt.Errorf("%s: start from snapshot %s: %w", n.Name, name, err)
		}
	}
	for _, n := range nodes {
		if err := live.Resume(ctx, c.spec(n)); err != nil {
			return fmt.Errorf("%s: resume: %w", n.Name, err)
		}
	}
	c.step("%d node(s) resumed from snapshot %s", len(nodes), name)
	if err := c.syncClocks(ctx); err != nil {
		return err
	}
	if err := c.waitQuorum(ctx, 5*time.Minute); err != nil {
		return err
	}
	if err := c.waitAPI(ctx, 2*time.Minute); err != nil {
		return err
	}
	return c.waitStorage(ctx)
}

// syncClocks sets every node's clock to the host's time.
func (c *Cluster) syncClocks(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			s, err := c.waitSSH(gctx, n.Name, time.Minute)
			if err != nil {
				return err
			}
			defer s.Close()
			now := time.Now()
			script := fmt.Sprintf("date -u -s @%d.%03d >/dev/null\nchronyc -a makestep >/dev/null 2>&1 || true\n", now.Unix(), now.Nanosecond()/1e6)
			if _, err := s.Run(script); err != nil {
				return fmt.Errorf("%s: set clock: %w", n.Name, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// SnapshotDelete removes the snapshot from every disk.
func (c *Cluster) SnapshotDelete(ctx context.Context, name string) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	snap := c.St.Snapshot(name)
	if snap == nil {
		return fmt.Errorf("no snapshot %q", name)
	}
	if err := c.deleteSnapshot(ctx, snap); err != nil {
		return err
	}
	c.St.Snapshots = slices.DeleteFunc(c.St.Snapshots, func(s state.Snapshot) bool { return s.Name == name })
	return c.save()
}

// deleteSnapshot removes a snapshot from the disks: through the running nodes
// for a live snapshot of a running cluster, otherwise from the stopped disks.
func (c *Cluster) deleteSnapshot(ctx context.Context, snap *state.Snapshot) error {
	live, ok := c.runtime.(vm.LiveSnapshotter)
	nodes := c.Cfg.NodeList()
	allRunning := !slices.ContainsFunc(nodes, func(n config.Node) bool { return !c.running(n) })
	if !snap.Live || !ok || !allRunning {
		if err := c.requireStopped(); err != nil {
			return err
		}
		return c.runtime.Snapshot(ctx, vm.SnapshotDelete, snap.Name, c.disks())
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		g.Go(func() error { return live.DeleteLive(gctx, c.spec(n), snap.Name) })
	}
	return g.Wait()
}
