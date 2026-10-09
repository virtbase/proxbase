package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/remote"
	"github.com/virtbase/proxbase/internal/retry"
	"github.com/virtbase/proxbase/internal/storage"
)

// CheckRemove fails if a pool keeps more copies than nodes would remain.
func (b *Backend) CheckRemove(remaining int, force bool) error {
	for _, p := range b.cfg.Pools {
		if p.Size > remaining && !force {
			return fmt.Errorf("ceph pool %s has size %d; %d remaining nodes cannot hold all copies (use --force to accept a degraded pool)", p.Name, p.Size, remaining)
		}
	}
	return nil
}

// RemoveNode drains and destroys the node's OSDs, then its MDS, manager and
// monitor. It refuses to finish while Ceph still knows OSDs on the node.
func (b *Backend) RemoveNode(ctx context.Context, h storage.Host, name string, force bool) error {
	var anchor string
	for _, n := range h.Nodes() {
		if n.Name != name {
			anchor = n.Name
			break
		}
	}
	aapi, err := h.API(ctx, anchor)
	if err != nil {
		return err
	}
	napi, err := h.API(ctx, name)
	if err != nil {
		return err
	}
	s, err := h.SSH(ctx, anchor)
	if err != nil {
		return err
	}
	defer s.Close()
	osds, err := hostOSDs(ctx, s, name)
	if err != nil {
		return err
	}
	if len(osds) > 0 {
		ids := strings.Join(osds, " ")
		h.Step("%s: ceph osd out %s, waiting for data to move", name, ids)
		if _, err := s.Run("ceph osd out " + ids); err != nil {
			return err
		}
		// safe-to-destroy succeeds once no PG depends on these OSDs any more.
		if !force {
			script := fmt.Sprintf("for i in $(seq 900); do ceph osd safe-to-destroy %s >/dev/null 2>&1 && exit 0; sleep 2; done; ceph osd safe-to-destroy %s", ids, ids)
			if _, err := s.Run(script); err != nil {
				return fmt.Errorf("osds %s did not drain: %w", ids, err)
			}
		}
	}
	ns, err := h.SSH(ctx, name)
	if err != nil {
		return err
	}
	defer ns.Close()
	for _, id := range osds {
		if _, err := ns.Run("systemctl stop ceph-osd@" + id); err != nil {
			return err
		}
		h.Step("%s: ceph osd.%s destroyed", name, id)
		if err := napi.DeleteTask(ctx, name, "/nodes/"+name+"/ceph/osd/"+id+"?cleanup=1", 5*time.Minute); err != nil {
			return fmt.Errorf("destroy osd.%s: %w", id, err)
		}
	}
	for _, kind := range []string{"mds", "mgr", "mon"} {
		var list []named
		if err := retry.Do(ctx, 2*time.Minute, 2*time.Second, func() error { return aapi.Get(ctx, "/nodes/"+name+"/ceph/"+kind, &list) }); err != nil {
			return err
		}
		if !hasName(list, name) {
			continue
		}
		h.Step("%s: ceph %s destroyed", name, kind)
		if err := napi.DeleteTask(ctx, name, "/nodes/"+name+"/ceph/"+kind+"/"+name, 5*time.Minute); err != nil {
			return fmt.Errorf("destroy %s.%s: %w", kind, name, err)
		}
	}
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
func hostOSDs(ctx context.Context, s *remote.SSH, host string) ([]string, error) {
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
	err := retry.Do(ctx, 2*time.Minute, 2*time.Second, func() error {
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
