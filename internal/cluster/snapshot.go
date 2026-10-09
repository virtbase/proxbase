package cluster

import (
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
)

var snapRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,39}$`)

// disks returns every disk image of the cluster.
func (c *Cluster) disks() []string {
	var out []string
	for _, n := range c.Cfg.NodeList() {
		out = append(out, c.rootDisk(n))
		for i := range n.Spec.DataDisks {
			out = append(out, c.dataDisk(n, i))
		}
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
		if _, ok := qemu.Running(c.pidfile(n)); ok {
			return fmt.Errorf("%s is running; snapshots need a stopped cluster (proxbase stop %s)", n.Name, c.Cfg.Name)
		}
	}
	return nil
}

func qemuSnapshot(op, name, disk string) error {
	out, err := exec.Command("qemu-img", "snapshot", op, name, disk).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img snapshot %s %s %s: %w: %s", op, name, disk, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SnapshotSave takes an internal qcow2 snapshot of every disk of the stopped cluster.
func (c *Cluster) SnapshotSave(name string) error {
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
	var done []string
	for _, d := range c.disks() {
		if err := qemuSnapshot("-c", name, d); err != nil {
			for _, u := range done {
				_ = qemuSnapshot("-d", name, u)
			}
			return err
		}
		done = append(done, d)
	}
	c.St.Snapshots = append(c.St.Snapshots, state.Snapshot{Name: name, Created: time.Now().UTC(), Nodes: c.nodeNames()})
	return c.save()
}

// SnapshotRestore resets every disk to the snapshot.
func (c *Cluster) SnapshotRestore(name string) error {
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
	for _, d := range c.disks() {
		if err := qemuSnapshot("-a", name, d); err != nil {
			return err
		}
	}
	return nil
}

// SnapshotDelete removes the snapshot from every disk.
func (c *Cluster) SnapshotDelete(name string) error {
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
	for _, d := range c.disks() {
		if err := qemuSnapshot("-d", name, d); err != nil && !strings.Contains(err.Error(), "Can't find") {
			return err
		}
	}
	c.St.Snapshots = slices.DeleteFunc(c.St.Snapshots, func(s state.Snapshot) bool { return s.Name == name })
	return c.save()
}
