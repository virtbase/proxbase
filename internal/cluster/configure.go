package cluster

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/pve"
	"golang.org/x/sync/errgroup"
)

func (c *Cluster) postInstall(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			s, err := c.waitSSH(gctx, n.Name, time.Minute)
			if err != nil {
				return err
			}
			defer s.Close()
			out, err := s.Run(c.postInstallScript(n))
			if err != nil {
				return fmt.Errorf("%s: post-install: %w", n.Name, err)
			}
			if strings.Contains(out, "changed") {
				c.step("%s: hosts, repositories and bridges configured", n.Name)
			}
			if !c.Cfg.Proxmox.Upgrade {
				return nil
			}
			t0 := time.Now()
			out, err = s.Run(upgradeScript)
			if err != nil {
				return fmt.Errorf("%s: dist-upgrade: %w", n.Name, err)
			}
			c.step("%s: dist-upgrade in %s: %s", n.Name, time.Since(t0).Round(time.Second), strings.TrimSpace(out))
			return c.rebootForKernel(gctx, n.Name, s)
		})
	}
	return g.Wait()
}

// rebootForKernel reboots a node whose newest installed kernel is not running
// and waits until it is back (new boot ID, SSH up).
func (c *Cluster) rebootForKernel(ctx context.Context, name string, s *pve.SSH) error {
	out, err := s.Run(`new=$(ls /boot/vmlinuz-* | sed 's#^/boot/vmlinuz-##' | sort -V | tail -n 1)
if [ "$new" != "$(uname -r)" ]; then echo "$new $(cat /proc/sys/kernel/random/boot_id)"; fi`)
	if err != nil || strings.TrimSpace(out) == "" {
		return err
	}
	kernel, bootID, _ := strings.Cut(strings.TrimSpace(out), " ")
	c.step("%s: rebooting into kernel %s", name, kernel)
	if _, err := s.Run("systemd-run --on-active=2 systemctl reboot >/dev/null"); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		ns, err := c.ssh(ctx, name)
		if err != nil {
			continue
		}
		id, err := ns.Run("cat /proc/sys/kernel/random/boot_id; uname -r")
		ns.Close()
		if err == nil && !strings.HasPrefix(id, bootID) {
			c.step("%s: running %s", name, strings.TrimSpace(strings.SplitN(id, "\n", 2)[1]))
			return nil
		}
	}
	return fmt.Errorf("%s did not come back after the reboot", name)
}

// links returns the corosync link parameters (link0, link1) of a node.
func (c *Cluster) links(n config.Node) url.Values {
	v := url.Values{}
	for i, net := range c.Cfg.CorosyncNetworks() {
		v.Set(fmt.Sprintf("link%d", i), c.nodeIP(n, net))
	}
	return v
}

func describeLinks(v url.Values) string {
	s := "link0 " + v.Get("link0")
	if l1 := v.Get("link1"); l1 != "" {
		s += ", link1 " + l1
	}
	return s
}

type clusterStatus struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Online  int    `json:"online"`
	Quorate int    `json:"quorate"`
	Nodes   int    `json:"nodes"`
	IP      string `json:"ip"`
}

func getStatus(ctx context.Context, cl *pve.Client) (cluster *clusterStatus, nodes map[string]clusterStatus, err error) {
	var entries []clusterStatus
	if err := cl.Get(ctx, "/cluster/status", &entries); err != nil {
		return nil, nil, err
	}
	nodes = map[string]clusterStatus{}
	for i, e := range entries {
		switch e.Type {
		case "cluster":
			cluster = &entries[i]
		case "node":
			nodes[e.Name] = e
		}
	}
	return cluster, nodes, nil
}

// formCluster creates the cluster on the first node and joins the others via the API.
func (c *Cluster) formCluster(ctx context.Context) error {
	nodes := c.Cfg.NodeList()
	first := nodes[0]
	api, err := c.api(ctx, first.Name)
	if err != nil {
		return err
	}
	cs, _, err := getStatus(ctx, api)
	if err != nil {
		return err
	}
	if cs == nil {
		form := c.links(first)
		form.Set("clustername", c.Cfg.Name)
		c.step("creating cluster %s on %s (%s)", c.Cfg.Name, first.Name, describeLinks(form))
		var upid string
		if err := api.Post(ctx, "/cluster/config", form, &upid); err != nil {
			return fmt.Errorf("create cluster: %w", err)
		}
		if err := api.WaitTask(ctx, first.Name, upid, 2*time.Minute); err != nil {
			return fmt.Errorf("create cluster: %w", err)
		}
		// pmxcfs restarts; log in again.
		if api, err = c.api(ctx, first.Name); err != nil {
			return err
		}
	}
	s, err := c.waitSSH(ctx, first.Name, time.Minute)
	if err != nil {
		return err
	}
	certPEM, err := s.Run("cat /etc/pve/nodes/" + first.Name + "/pve-ssl.pem")
	s.Close()
	if err != nil {
		return err
	}
	fp, err := pve.Fingerprint(certPEM)
	if err != nil {
		return err
	}
	for _, n := range nodes[1:] {
		_, members, err := getStatus(ctx, api)
		if err != nil {
			return err
		}
		if _, ok := members[n.Name]; ok {
			continue
		}
		form := c.links(n)
		c.step("joining %s (%s)", n.Name, describeLinks(form))
		napi, err := c.api(ctx, n.Name)
		if err != nil {
			return err
		}
		form.Set("hostname", c.corosyncIP(first))
		form.Set("password", c.password)
		form.Set("fingerprint", fp)
		// The cluster refuses joins until it is quorate.
		if err := c.waitMember(ctx, api, first.Name, nil, "", 2*time.Minute); err != nil {
			return err
		}
		var upid string
		if err := napi.Post(ctx, "/cluster/config/join", form, &upid); err != nil {
			return fmt.Errorf("join %s: %w", n.Name, err)
		}
		if err := c.waitMember(ctx, api, n.Name, napi, upid, 3*time.Minute); err != nil {
			return err
		}
	}
	if m, ok := c.Cfg.RoleNetwork(config.RoleMigration); ok {
		if err := api.Put(ctx, "/cluster/options", url.Values{"migration": {"type=secure,network=" + m.CIDR}}, nil); err != nil {
			return fmt.Errorf("set migration network: %w", err)
		}
	}
	return nil
}

// waitMember waits until node is listed online in a quorate cluster. If a join
// task is given, its failure ends the wait early.
func (c *Cluster) waitMember(ctx context.Context, api *pve.Client, node string, joiner *pve.Client, upid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		cs, members, err := getStatus(ctx, api)
		if err == nil && cs != nil && cs.Quorate == 1 && members[node].Online == 1 {
			return nil
		}
		last = err
		if joiner != nil {
			if done, terr := joiner.TaskDone(ctx, node, upid); done && terr != nil {
				return fmt.Errorf("join %s: %w", node, terr)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	err := fmt.Errorf("%s did not come online in the cluster within %s", node, timeout)
	if last != nil {
		err = fmt.Errorf("%w: %w", err, last)
	}
	return err
}

var votesRe = regexp.MustCompile(`Total votes:\s+(\d+)`)

// waitQuorum waits until every node is quorate, sees all votes and can write /etc/pve.
func (c *Cluster) waitQuorum(ctx context.Context, timeout time.Duration) error {
	want := c.Cfg.Nodes.Count
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			deadline := time.Now().Add(timeout)
			var last string
			for {
				s, err := c.waitSSH(gctx, n.Name, time.Minute)
				if err != nil {
					return err
				}
				out, err := s.Run(`pvecm status 2>&1 || true
f=/etc/pve/.proxbase-write-test; if echo ok > $f 2>/dev/null; then rm -f $f; echo WRITABLE; fi`)
				s.Close()
				m := votesRe.FindStringSubmatch(out)
				if err == nil && regexp.MustCompile(`Quorate:\s+Yes`).MatchString(out) && m != nil && m[1] == strconv.Itoa(want) && strings.Contains(out, "WRITABLE") {
					return nil
				}
				last = out
				if time.Now().After(deadline) {
					return fmt.Errorf("%s: no quorum after %s:\n%s", n.Name, timeout, last)
				}
				select {
				case <-gctx.Done():
					return gctx.Err()
				case <-time.After(2 * time.Second):
				}
			}
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	c.step("cluster quorate: %d/%d votes, /etc/pve writable on every node", want, want)
	return nil
}

func (c *Cluster) createZFS(ctx context.Context) error {
	if len(c.Cfg.Storage.ZFS) == 0 {
		return nil
	}
	var first *pve.Client
	var names []string
	for _, n := range c.Cfg.NodeList() {
		names = append(names, n.Name)
		api, err := c.api(ctx, n.Name)
		if err != nil {
			return err
		}
		if first == nil {
			first = api
		}
		var pools []struct {
			Name string `json:"name"`
		}
		if err := api.Get(ctx, "/nodes/"+n.Name+"/disks/zfs", &pools); err != nil {
			return err
		}
		for _, z := range c.Cfg.Storage.ZFS {
			exists := false
			for _, p := range pools {
				exists = exists || p.Name == z.Name
			}
			if exists {
				continue
			}
			devs := make([]string, len(z.Disks))
			for i, d := range z.Disks {
				devs[i] = "/dev/" + d
			}
			c.step("%s: creating ZFS pool %s (%s on %s)", n.Name, z.Name, z.Raid, strings.Join(z.Disks, ", "))
			var upid string
			form := url.Values{"name": {z.Name}, "raidlevel": {z.Raid}, "devices": {strings.Join(devs, ",")}, "ashift": {"12"}, "add_storage": {"0"}}
			if err := api.Post(ctx, "/nodes/"+n.Name+"/disks/zfs", form, &upid); err != nil {
				return fmt.Errorf("%s: create zfs pool %s: %w", n.Name, z.Name, err)
			}
			if err := api.WaitTask(ctx, n.Name, upid, 2*time.Minute); err != nil {
				return fmt.Errorf("%s: create zfs pool %s: %w", n.Name, z.Name, err)
			}
		}
	}
	var storages []struct {
		Storage string `json:"storage"`
	}
	if err := first.Get(ctx, "/storage", &storages); err != nil {
		return err
	}
	for _, z := range c.Cfg.Storage.ZFS {
		exists := slices.ContainsFunc(storages, func(s struct {
			Storage string `json:"storage"`
		}) bool {
			return s.Storage == z.Name
		})
		nodes := strings.Join(names, ",")
		if exists {
			// Keep the node list current after node add/remove.
			if err := first.Put(ctx, "/storage/"+z.Name, url.Values{"nodes": {nodes}}, nil); err != nil {
				return fmt.Errorf("update storage %s: %w", z.Name, err)
			}
			continue
		}
		form := url.Values{"storage": {z.Name}, "type": {"zfspool"}, "pool": {z.Name}, "content": {"images,rootdir"}, "sparse": {"1"}, "nodes": {nodes}}
		if err := first.Post(ctx, "/storage", form, nil); err != nil {
			return fmt.Errorf("add storage %s: %w", z.Name, err)
		}
		c.step("storage %s (zfspool) added for all nodes", z.Name)
	}
	return nil
}

func (c *Cluster) ensureToken(ctx context.Context) error {
	api, err := c.api(ctx, c.Cfg.NodeList()[0].Name)
	if err != nil {
		return err
	}
	if tok, err := c.Token(); err == nil && tok != "" {
		probe := *api
		probe.SetToken(tok)
		if probe.Get(ctx, "/version", &struct{}{}) == nil {
			return nil
		}
	}
	var users []struct {
		UserID string `json:"userid"`
	}
	if err := api.Get(ctx, "/access/users", &users); err != nil {
		return err
	}
	found := false
	for _, u := range users {
		found = found || u.UserID == TokenUser
	}
	if !found {
		if err := api.Post(ctx, "/access/users", url.Values{"userid": {TokenUser}, "comment": {"proxbase"}}, nil); err != nil {
			return fmt.Errorf("create user %s: %w", TokenUser, err)
		}
	}
	if err := api.Put(ctx, "/access/acl", url.Values{"path": {"/"}, "roles": {"Administrator"}, "users": {TokenUser}}, nil); err != nil {
		return fmt.Errorf("grant Administrator to %s: %w", TokenUser, err)
	}
	tokenPath := "/access/users/" + url.PathEscape(TokenUser) + "/token/api"
	if err := api.Delete(ctx, tokenPath); err != nil && !pve.IsStatus(err, 500) && !pve.IsStatus(err, 404) {
		return err
	}
	var tok struct {
		FullID string `json:"full-tokenid"`
		Value  string `json:"value"`
	}
	if err := api.Post(ctx, tokenPath, url.Values{"privsep": {"0"}, "comment": {"proxbase"}}, &tok); err != nil {
		return fmt.Errorf("create API token: %w", err)
	}
	c.step("API token %s created", tok.FullID)
	return c.Dir.WriteSecret(secretToken, []byte(tok.FullID+"="+tok.Value+"\n"))
}

func (c *Cluster) exportCA(ctx context.Context) error {
	s, err := c.waitSSH(ctx, c.Cfg.NodeList()[0].Name, time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()
	ca, err := s.Run("cat /etc/pve/pve-root-ca.pem")
	if err != nil {
		return err
	}
	return c.Dir.WriteSecret(secretCA, []byte(ca))
}
