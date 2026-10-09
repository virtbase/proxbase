package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/pve"
)

const (
	cephFSName    = "cephfs"
	cephFSContent = "iso,vztmpl,backup,snippets,import"
	cephRBD       = "images,rootdir"
)

// setupCeph installs and configures Ceph; every step checks the current state first.
func (c *Cluster) setupCeph(ctx context.Context) error {
	if !c.Cfg.CephEnabled() {
		return nil
	}
	ce := c.Cfg.Storage.Ceph
	nodes := c.Cfg.NodeList()
	if err := c.installCeph(ctx, ce.Version); err != nil {
		return err
	}
	apis := map[string]*pve.Client{}
	for _, n := range nodes {
		api, err := c.api(ctx, n.Name)
		if err != nil {
			return err
		}
		apis[n.Name] = api
	}
	first := nodes[0].Name
	api := apis[first]

	s, err := c.waitSSH(ctx, first, time.Minute)
	if err != nil {
		return err
	}
	_, missing := s.Run("test -e /etc/pve/ceph.conf")
	s.Close()
	if missing != nil {
		public, cluster := c.Cfg.CephNetworks()
		size, minSize := c.Cfg.CephFSSize()
		c.step("ceph: init (public %s %s, cluster %s %s)", public.Name, public.CIDR, cluster.Name, cluster.CIDR)
		form := url.Values{"network": {public.CIDR}, "cluster-network": {cluster.CIDR}, "size": {strconv.Itoa(size)}, "min_size": {strconv.Itoa(minSize)}}
		if err := api.Post(ctx, "/nodes/"+first+"/ceph/init", form, nil); err != nil {
			return fmt.Errorf("ceph init: %w", err)
		}
	}

	// Three monitors (fewer on smaller clusters), a manager on every node; one at a time.
	var mons []named
	if err := retry(ctx, func() error { return api.Get(ctx, "/nodes/"+first+"/ceph/mon", &mons) }); err != nil {
		return fmt.Errorf("list ceph mon: %w", err)
	}
	for _, n := range nodes {
		if !hasName(mons, n.Name) && len(mons) < 3 {
			if err := c.cephDaemon(ctx, apis[n.Name], api, n.Name, "mon"); err != nil {
				return err
			}
			mons = append(mons, named{Name: n.Name})
		}
		if err := c.cephDaemon(ctx, apis[n.Name], api, n.Name, "mgr"); err != nil {
			return err
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		g.Go(func() error { return c.createOSDs(gctx, apis[n.Name], n.Name, ce.OSDDisks) })
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if err := c.createCephPools(ctx, api, first, ce.Pools); err != nil {
		return err
	}
	if *ce.CephFS {
		for _, n := range nodes {
			if err := c.cephDaemon(ctx, apis[n.Name], api, n.Name, "mds"); err != nil {
				return err
			}
		}
		if err := c.createCephFS(ctx, api, first); err != nil {
			return err
		}
	}
	return c.waitCephHealth(ctx, 10*time.Minute)
}

func (c *Cluster) installCeph(ctx context.Context, version string) error {
	// Ceph keys since 19.2.6 (aes256k) are rejected by the libpve-storage-perl on
	// the 9.2 ISO ("Not a proper rbd authentication file"); newer versions accept them.
	script := aptWait + fmt.Sprintf(`
export DEBIAN_FRONTEND=noninteractive
log=/var/log/proxbase-ceph-install.log
if ! [ -x /usr/bin/ceph-mon ] || ! ceph-mon --version 2>/dev/null | grep -q ' %[1]s '; then
	if ! { yes || true; } | pveceph install --repository no-subscription --version %[1]s >$log 2>&1; then
		tail -n 20 $log >&2; exit 1
	fi
	echo installed
fi
before=$(dpkg-query -W -f '${Version}' libpve-storage-perl)
if ! $apt install -y --only-upgrade libpve-storage-perl >>$log 2>&1; then
	tail -n 20 $log >&2; exit 1
fi
if [ "$before" != "$(dpkg-query -W -f '${Version}' libpve-storage-perl)" ]; then
	systemctl restart pvedaemon pveproxy pvestatd
	echo "upgraded libpve-storage-perl $before -> $(dpkg-query -W -f '${Version}' libpve-storage-perl)"
fi
`, version)
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			s, err := c.waitSSH(gctx, n.Name, time.Minute)
			if err != nil {
				return err
			}
			defer s.Close()
			t0 := time.Now()
			out, err := s.Run(script)
			if err != nil {
				return fmt.Errorf("%s: pveceph install: %w", n.Name, err)
			}
			if strings.Contains(out, "installed") {
				c.step("%s: ceph %s installed in %s", n.Name, version, time.Since(t0).Round(time.Second))
			}
			if i := strings.Index(out, "upgraded "); i >= 0 {
				c.step("%s: %s", n.Name, strings.TrimSpace(out[i:]))
			}
			return nil
		})
	}
	return g.Wait()
}

// postTask POSTs and waits for the task if the call returned a UPID.
func postTask(ctx context.Context, api *pve.Client, node, path string, form url.Values, timeout time.Duration) error {
	var upid string
	if err := api.Post(ctx, path, form, &upid); err != nil {
		return err
	}
	if upid == "" {
		return nil
	}
	return api.WaitTask(ctx, node, upid, timeout)
}

// cephDaemon creates a mon, mgr or mds named after the node unless it exists.
func (c *Cluster) cephDaemon(ctx context.Context, api, first *pve.Client, node, kind string) error {
	var list []named
	if err := retry(ctx, func() error { return first.Get(ctx, "/nodes/"+node+"/ceph/"+kind, &list) }); err != nil {
		return fmt.Errorf("list ceph %s: %w", kind, err)
	}
	if hasName(list, node) {
		return nil
	}
	c.step("%s: ceph %s", node, kind)
	if err := postTask(ctx, api, node, "/nodes/"+node+"/ceph/"+kind+"/"+node, url.Values{}, 3*time.Minute); err != nil {
		return fmt.Errorf("%s: create ceph %s: %w", node, kind, err)
	}
	if kind != "mon" {
		return nil
	}
	// Monitors re-elect after a new one joins; wait until it is in quorum.
	return retry(ctx, func() error {
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

// retry runs f until it succeeds or two minutes passed; Ceph calls fail briefly
// while monitors elect.
func retry(ctx context.Context, f func() error) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := f()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Cluster) createOSDs(ctx context.Context, api *pve.Client, node string, disks []string) error {
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
		c.step("%s: ceph osd on %s", node, path)
		if err := postTask(ctx, api, node, "/nodes/"+node+"/ceph/osd", url.Values{"dev": {path}}, 5*time.Minute); err != nil {
			return fmt.Errorf("%s: create osd on %s: %w", node, path, err)
		}
	}
	return nil
}

func (c *Cluster) createCephPools(ctx context.Context, api *pve.Client, node string, pools []config.CephPool) error {
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
		c.step("ceph: pool %s (size %d/%d, %d PGs)", p.Name, p.Size, p.MinSize, p.PGNum)
		form := url.Values{
			"name": {p.Name}, "size": {strconv.Itoa(p.Size)}, "min_size": {strconv.Itoa(p.MinSize)},
			"pg_num": {strconv.Itoa(p.PGNum)}, "pg_autoscale_mode": {"off"}, "application": {p.Application},
			"add_storages": {strconv.Itoa(boolInt(p.Application == "rbd"))},
		}
		if err := postTask(ctx, api, node, "/nodes/"+node+"/ceph/pool", form, 3*time.Minute); err != nil {
			return fmt.Errorf("create ceph pool %s: %w", p.Name, err)
		}
		if p.Application == "rbd" {
			if err := api.Put(ctx, "/storage/"+p.Name, url.Values{"content": {cephRBD}}, nil); err != nil {
				return fmt.Errorf("set content of storage %s: %w", p.Name, err)
			}
		}
	}
	return nil
}

func (c *Cluster) createCephFS(ctx context.Context, api *pve.Client, node string) error {
	var fs []named
	if err := api.Get(ctx, "/nodes/"+node+"/ceph/fs", &fs); err != nil {
		return err
	}
	if len(fs) == 0 {
		c.step("ceph: cephfs %s", cephFSName)
		form := url.Values{"pg_num": {"32"}, "add-storage": {"1"}}
		if err := postTask(ctx, api, node, "/nodes/"+node+"/ceph/fs/"+cephFSName, form, 5*time.Minute); err != nil {
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
		return s.Storage == cephFSName
	}) {
		form := url.Values{"storage": {cephFSName}, "type": {"cephfs"}, "fs-name": {cephFSName}, "content": {cephFSContent}}
		if err := api.Post(ctx, "/storage", form, nil); err != nil {
			return fmt.Errorf("add storage %s: %w", cephFSName, err)
		}
	}
	if err := api.Put(ctx, "/storage/"+cephFSName, url.Values{"content": {cephFSContent}}, nil); err != nil {
		return fmt.Errorf("set content of storage %s: %w", cephFSName, err)
	}
	return nil
}

type named struct {
	Name string `json:"name"`
}

func hasName(list []named, name string) bool {
	return slices.ContainsFunc(list, func(n named) bool { return n.Name == name })
}

type diskInfo struct {
	DevPath string   `json:"devpath"`
	Used    string   `json:"used"`
	OSDs    []string `json:"osdid-list"`
}

type cephStatus struct {
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
func (s cephStatus) settled() bool {
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

func (s cephStatus) checks() string {
	names := make([]string, 0, len(s.Health.Checks))
	for k := range s.Health.Checks {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// cephStorages are the storage IDs Proxbase registers for Ceph.
func (c *Cluster) cephStorages() []string {
	var ids []string
	for _, p := range c.Cfg.Storage.Ceph.Pools {
		if p.Application == "rbd" {
			ids = append(ids, p.Name)
		}
	}
	if *c.Cfg.Storage.Ceph.CephFS {
		ids = append(ids, cephFSName)
	}
	return ids
}

// storagesActive reports whether every Ceph storage is active on every node.
func (c *Cluster) storagesActive(ctx context.Context, api *pve.Client) error {
	for _, n := range c.Cfg.NodeList() {
		for _, id := range c.cephStorages() {
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

// waitCephHealth waits for HEALTH_OK, all PGs active+clean and all Ceph storages
// active on every node; it shows `ceph -s` if that does not happen in time.
func (c *Cluster) waitCephHealth(ctx context.Context, timeout time.Duration) error {
	first := c.Cfg.NodeList()[0].Name
	api, err := c.api(ctx, first)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	var last cephStatus
	for {
		err := api.Get(ctx, "/cluster/ceph/status", &last)
		if err == nil && last.settled() {
			if err = c.storagesActive(ctx, api); err == nil {
				c.step("ceph: HEALTH_OK, %d OSDs up, %d PGs active+clean, storages active on all nodes", last.OSDMap.Num, last.PGMap.NumPGs)
				return nil
			}
		}
		if time.Now().After(deadline) {
			out := ""
			if s, err := c.waitSSH(ctx, first, time.Minute); err == nil {
				out, _ = s.Run("ceph -s 2>&1; ceph health detail 2>&1 | head -20")
				s.Close()
			}
			return errors.Join(fmt.Errorf("ceph not ready after %s (%s %s):\n%s", timeout, last.Health.Status, last.checks(), out), err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
