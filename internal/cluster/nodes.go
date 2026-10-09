package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/storage"
)

// AddNodes adds count nodes (reusing numbers of removed nodes first) and runs the
// create steps again, which install, join and configure only what is missing.
func (c *Cluster) AddNodes(ctx context.Context, count int) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if c.St.Phase != state.PhaseReady {
		return fmt.Errorf("cluster is %s; finish it with `proxbase create %s` first", c.St.Phase, c.Cfg.Name)
	}
	before := map[string]bool{}
	for _, n := range c.Cfg.NodeList() {
		before[n.Name] = true
	}
	for range count {
		if len(c.Cfg.Nodes.Removed) > 0 {
			slices.Sort(c.Cfg.Nodes.Removed)
			c.Cfg.Nodes.Removed = c.Cfg.Nodes.Removed[1:]
		}
		c.Cfg.Nodes.Count++
	}
	if err := c.Cfg.Validate(); err != nil {
		return fmt.Errorf("cannot add %d node(s):\n%w", count, err)
	}
	var added []config.Node
	for _, n := range c.Cfg.NodeList() {
		if !before[n.Name] {
			added = append(added, n)
		}
	}
	if err := preflight(c.Cfg, added); err != nil {
		return err
	}
	for _, n := range added {
		ui, ssh := c.Cfg.Ports(n.Index)
		c.St.Nodes = append(c.St.Nodes, &state.NodeState{Name: n.Name, Index: n.Index, UIPort: ui, SSHPort: ssh})
	}
	slices.SortFunc(c.St.Nodes, func(a, b *state.NodeState) int { return a.Index - b.Index })
	if err := c.Dir.SaveConfig(c.Cfg); err != nil {
		return err
	}
	c.St.Phase = state.PhaseCreating
	if err := c.save(); err != nil {
		return err
	}
	if err := c.net.Reload(c.Cfg); err != nil {
		return err
	}
	if err := c.create(ctx); err != nil {
		c.St.Phase, c.St.Error = state.PhaseFailed, err.Error()
		_ = c.save()
		return fmt.Errorf("%w\n\nRe-run `proxbase create %s` to finish adding the node(s)", err, c.Cfg.Name)
	}
	c.St.Phase, c.St.Error = state.PhaseReady, ""
	return c.save()
}

// RemoveNode takes a node out of storage and the cluster, then deletes its VM.
func (c *Cluster) RemoveNode(ctx context.Context, name string, force bool) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if c.St.Phase != state.PhaseReady {
		return fmt.Errorf("cluster is %s; node remove needs a ready cluster", c.St.Phase)
	}
	nodes := c.Cfg.NodeList()
	i := slices.IndexFunc(nodes, func(n config.Node) bool { return n.Name == name })
	if i < 0 {
		return fmt.Errorf("cluster %s has no node %q", c.Cfg.Name, name)
	}
	if len(nodes) == 1 {
		return errors.New("cannot remove the last node; use destroy")
	}
	node := nodes[i]
	anchor := slices.Delete(slices.Clone(nodes), i, i+1)[0]
	if !c.running(node) {
		return fmt.Errorf("%s is not running; start the cluster first", name)
	}
	var removers []storage.NodeRemover
	for _, b := range c.storages() {
		if r, ok := b.(storage.NodeRemover); ok {
			if err := r.CheckRemove(len(nodes)-1, force); err != nil {
				return err
			}
			removers = append(removers, r)
		}
	}
	aapi, err := c.api(ctx, anchor.Name)
	if err != nil {
		return err
	}
	if !force {
		if err := c.checkNoGuests(ctx, name); err != nil {
			return err
		}
	}
	// Ceph before ZFS: removers run in reverse setup order.
	for _, r := range slices.Backward(removers) {
		if err := r.RemoveNode(ctx, host{c}, name, force); err != nil {
			return err
		}
	}

	c.step("%s: shutting down", name)
	spec := c.spec(node)
	if err := c.runtime.Stop(ctx, spec, 2*time.Minute); err != nil {
		return err
	}
	c.step("%s: removing from the cluster", name)
	if err := aapi.Delete(ctx, "/cluster/config/nodes/"+name); err != nil && !pve.IsStatus(err, 500) {
		return fmt.Errorf("delnode %s: %w", name, err)
	}
	if s, err := c.waitSSH(ctx, anchor.Name, time.Minute); err == nil {
		_, _ = s.Run("rm -rf /etc/pve/nodes/" + name)
		s.Close()
	}
	for _, d := range spec.Disks() {
		_ = os.Remove(d.Path)
	}

	c.Cfg.Nodes.Count--
	c.Cfg.Nodes.Removed = append(c.Cfg.Nodes.Removed, node.Index)
	c.St.Nodes = slices.DeleteFunc(c.St.Nodes, func(n *state.NodeState) bool { return n.Name == name })
	if err := c.Dir.SaveConfig(c.Cfg); err != nil {
		return err
	}
	if err := c.save(); err != nil {
		return err
	}
	if err := c.net.Reload(c.Cfg); err != nil {
		return err
	}
	if err := c.postInstall(ctx); err != nil { // rewrites /etc/hosts
		return err
	}
	if err := c.waitQuorum(ctx, 3*time.Minute); err != nil {
		return err
	}
	return c.setupStorage(ctx) // e.g. restores three Ceph monitors
}

// checkNoGuests fails if VMs or containers are still on the node.
func (c *Cluster) checkNoGuests(ctx context.Context, name string) error {
	api, err := c.api(ctx, name)
	if err != nil {
		return err
	}
	for _, kind := range []string{"qemu", "lxc"} {
		var guests []struct {
			VMID int `json:"vmid"`
		}
		if err := api.Get(ctx, "/nodes/"+name+"/"+kind, &guests); err != nil {
			return err
		}
		if len(guests) > 0 {
			return fmt.Errorf("%s still has %d %s guest(s); migrate or remove them, or use --force", name, len(guests), kind)
		}
	}
	return nil
}
