package cluster

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

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

// SnapshotSave takes a snapshot of every disk of the stopped cluster.
func (c *Cluster) SnapshotSave(ctx context.Context, name string) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if !snapRe.MatchString(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	if c.St.Phase != state.PhaseReady {
		return fmt.Errorf("cluster is %s; snapshots need a ready cluster", c.St.Phase)
	}
	if c.St.Snapshot(name) != nil {
		return fmt.Errorf("snapshot %q exists", name)
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
	return c.runtime.Snapshot(ctx, vm.SnapshotRestore, name, c.disks())
}

// SnapshotDelete removes the snapshot from every disk.
func (c *Cluster) SnapshotDelete(ctx context.Context, name string) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if c.St.Snapshot(name) == nil {
		return fmt.Errorf("no snapshot %q", name)
	}
	if err := c.requireStopped(); err != nil {
		return err
	}
	if err := c.runtime.Snapshot(ctx, vm.SnapshotDelete, name, c.disks()); err != nil {
		return err
	}
	c.St.Snapshots = slices.DeleteFunc(c.St.Snapshots, func(s state.Snapshot) bool { return s.Name == name })
	return c.save()
}
