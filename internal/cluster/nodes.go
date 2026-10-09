package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
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
	if err := c.reloadSwitch(); err != nil {
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

// RemoveNode takes a node out of Ceph and the cluster, then deletes its VM.
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
	remaining := slices.Delete(slices.Clone(nodes), i, i+1)
	if _, running := qemu.Running(c.pidfile(node)); !running {
		return fmt.Errorf("%s is not running; start the cluster first", name)
	}
	if c.Cfg.CephEnabled() {
		for _, p := range c.Cfg.Storage.Ceph.Pools {
			if p.Size > len(remaining) && !force {
				return fmt.Errorf("ceph pool %s has size %d; %d remaining nodes cannot hold all copies (use --force to accept a degraded pool)", p.Name, p.Size, len(remaining))
			}
		}
	}
	anchor, err := c.api(ctx, remaining[0].Name)
	if err != nil {
		return err
	}
	napi, err := c.api(ctx, name)
	if err != nil {
		return err
	}
	if !force {
		for _, kind := range []string{"qemu", "lxc"} {
			var guests []struct {
				VMID int `json:"vmid"`
			}
			if err := napi.Get(ctx, "/nodes/"+name+"/"+kind, &guests); err != nil {
				return err
			}
			if len(guests) > 0 {
				return fmt.Errorf("%s still has %d %s guest(s); migrate or remove them, or use --force", name, len(guests), kind)
			}
		}
	}
	if c.Cfg.CephEnabled() {
		if err := c.removeCeph(ctx, anchor, napi, remaining[0].Name, name, force); err != nil {
			return err
		}
	}

	// Take the node out of the ZFS storage definitions while it is still a member.
	var rest []string
	for _, n := range remaining {
		rest = append(rest, n.Name)
	}
	for _, z := range c.Cfg.Storage.ZFS {
		if err := anchor.Put(ctx, "/storage/"+z.Name, url.Values{"nodes": {strings.Join(rest, ",")}}, nil); err != nil {
			return fmt.Errorf("update storage %s: %w", z.Name, err)
		}
	}

	c.step("%s: shutting down", name)
	if err := qemu.Stop(ctx, c.qmp(node), c.pidfile(node), 2*time.Minute); err != nil {
		return err
	}
	c.step("%s: removing from the cluster", name)
	if err := anchor.Delete(ctx, "/cluster/config/nodes/"+name); err != nil && !pve.IsStatus(err, 500) {
		return fmt.Errorf("delnode %s: %w", name, err)
	}
	if s, err := c.waitSSH(ctx, remaining[0].Name, time.Minute); err == nil {
		_, _ = s.Run("rm -rf /etc/pve/nodes/" + name)
		s.Close()
	}

	c.cleanSockets(node)
	for _, p := range []string{c.rootDisk(node), c.pidfile(node)} {
		_ = os.Remove(p)
	}
	for j := range node.Spec.DataDisks {
		_ = os.Remove(c.dataDisk(node, j))
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
	if err := c.reloadSwitch(); err != nil {
		return err
	}
	if err := c.postInstall(ctx); err != nil { // rewrites /etc/hosts
		return err
	}
	if err := c.waitQuorum(ctx, 3*time.Minute); err != nil {
		return err
	}
	if c.Cfg.CephEnabled() {
		return c.setupCeph(ctx) // restores three monitors where possible
	}
	return nil
}

// removeCeph drains and destroys the node's OSDs, then its MDS, manager and monitor.
func (c *Cluster) removeCeph(ctx context.Context, anchor, napi *pve.Client, anchorName, name string, force bool) error {
	s, err := c.waitSSH(ctx, anchorName, time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()
	osds, err := hostOSDs(ctx, s, name)
	if err != nil {
		return err
	}
	if len(osds) > 0 {
		c.step("%s: ceph osd out %s, waiting for data to move", name, strings.Join(osds, " "))
		if _, err := s.Run("ceph osd out " + strings.Join(osds, " ")); err != nil {
			return err
		}
		// safe-to-destroy succeeds once no PG depends on these OSDs any more.
		if !force {
			script := fmt.Sprintf("for i in $(seq 900); do ceph osd safe-to-destroy %s >/dev/null 2>&1 && exit 0; sleep 2; done; ceph osd safe-to-destroy %s", strings.Join(osds, " "), strings.Join(osds, " "))
			if _, err := s.Run(script); err != nil {
				return fmt.Errorf("osds %s did not drain: %w", strings.Join(osds, " "), err)
			}
		}
	}
	ns, err := c.waitSSH(ctx, name, time.Minute)
	if err != nil {
		return err
	}
	defer ns.Close()
	for _, id := range osds {
		if _, err := ns.Run("systemctl stop ceph-osd@" + id); err != nil {
			return err
		}
		c.step("%s: ceph osd.%s destroyed", name, id)
		if err := deleteTask(ctx, napi, name, "/nodes/"+name+"/ceph/osd/"+id+"?cleanup=1"); err != nil {
			return fmt.Errorf("destroy osd.%s: %w", id, err)
		}
	}
	for _, kind := range []string{"mds", "mgr", "mon"} {
		var list []named
		if err := retry(ctx, func() error { return anchor.Get(ctx, "/nodes/"+name+"/ceph/"+kind, &list) }); err != nil {
			return err
		}
		if !hasName(list, name) {
			continue
		}
		c.step("%s: ceph %s destroyed", name, kind)
		if err := deleteTask(ctx, napi, name, "/nodes/"+name+"/ceph/"+kind+"/"+name); err != nil {
			return fmt.Errorf("destroy %s.%s: %w", kind, name, err)
		}
	}
	// Never shut a node down while Ceph still knows OSDs on it.
	left, err := hostOSDs(ctx, s, name)
	if err != nil {
		return err
	}
	if len(left) > 0 {
		return fmt.Errorf("%s still has OSDs %v; not shutting it down", name, left)
	}
	_, err = s.Run("ceph osd crush remove " + name + " 2>/dev/null || true")
	return err
}

// hostOSDs lists the OSDs of a host: those under its CRUSH bucket and those whose
// metadata names it (a new OSD may not be placed in CRUSH yet).
func hostOSDs(ctx context.Context, s *pve.SSH, host string) ([]string, error) {
	var tree struct {
		Nodes []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Children []int  `json:"children"`
		} `json:"nodes"`
	}
	var meta []struct {
		ID       int    `json:"id"`
		Hostname string `json:"hostname"`
	}
	err := retry(ctx, func() error {
		out, err := s.Run("ceph osd tree -f json")
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(out), &tree); err != nil {
			return err
		}
		if out, err = s.Run("ceph osd metadata -f json"); err != nil {
			return err
		}
		return json.Unmarshal([]byte(out), &meta)
	})
	if err != nil {
		return nil, fmt.Errorf("list ceph osds: %w", err)
	}
	set := map[int]bool{}
	for _, n := range tree.Nodes {
		if n.Type == "host" && n.Name == host {
			for _, id := range n.Children {
				set[id] = true
			}
		}
	}
	for _, m := range meta {
		if m.Hostname == host {
			set[m.ID] = true
		}
	}
	var ids []string
	for id := range set {
		ids = append(ids, strconv.Itoa(id))
	}
	slices.Sort(ids)
	return ids, nil
}

// deleteTask DELETEs and waits for the task if one was started.
func deleteTask(ctx context.Context, api *pve.Client, node, path string) error {
	var upid string
	if err := api.Do(ctx, "DELETE", path, nil, &upid); err != nil {
		return err
	}
	if upid == "" {
		return nil
	}
	return api.WaitTask(ctx, node, upid, 5*time.Minute)
}
