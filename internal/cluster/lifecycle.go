package cluster

import (
	"bytes"
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
)

// Start boots a stopped cluster and waits for quorum.
func (c *Cluster) Start(ctx context.Context) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	for _, n := range c.St.Nodes {
		if !n.Installed {
			return fmt.Errorf("cluster was not created completely; run `proxbase create %s` to resume", c.Cfg.Name)
		}
	}
	if err := c.startSwitch(); err != nil {
		return err
	}
	if err := c.bootAll(ctx); err != nil {
		return err
	}
	if c.St.Phase != state.PhaseReady {
		return nil
	}
	if err := c.waitQuorum(ctx, 5*time.Minute); err != nil {
		return err
	}
	if err := c.waitAPI(ctx, 2*time.Minute); err != nil {
		return err
	}
	if c.Cfg.CephEnabled() {
		return c.waitCephHealth(ctx, 10*time.Minute)
	}
	return nil
}

// waitAPI waits until the token client can read the cluster status.
func (c *Cluster) waitAPI(ctx context.Context, timeout time.Duration) error {
	api := c.tokenClient(c.St.Nodes[0].UIPort)
	if api == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		_, _, err := getStatus(ctx, api)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("API not reachable after %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Stop shuts all nodes down (ACPI, then hard after timeout) and stops the switch.
func (c *Cluster) Stop(ctx context.Context, timeout time.Duration) error {
	unlock, err := c.Dir.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return c.stop(ctx, timeout)
}

func (c *Cluster) stop(ctx context.Context, timeout time.Duration) error {
	if timeout > 0 && c.Cfg.CephEnabled() && *c.Cfg.Storage.Ceph.CephFS {
		c.unmountCephFS(ctx)
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		if _, ok := qemu.Running(c.pidfile(n)); !ok {
			continue
		}
		g.Go(func() error {
			if err := qemu.Stop(gctx, c.qmp(n), c.pidfile(n), timeout); err != nil {
				return fmt.Errorf("%s: %w", n.Name, err)
			}
			c.step("%s stopped", n.Name)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for _, n := range c.Cfg.NodeList() {
		c.cleanSockets(n)
	}
	return c.stopSwitch()
}

// unmountCephFS unmounts CephFS on all running nodes while the monitors are still
// up; otherwise the kernel client blocks each node's shutdown for about a minute.
func (c *Cluster) unmountCephFS(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var g errgroup.Group
	for _, n := range c.Cfg.NodeList() {
		if _, ok := qemu.Running(c.pidfile(n)); !ok {
			continue
		}
		g.Go(func() error {
			s, err := c.ssh(ctx, n.Name)
			if err != nil {
				return nil
			}
			defer s.Close()
			_, _ = s.Run("systemctl stop pvestatd; umount /mnt/pve/" + cephFSName + " 2>/dev/null || true")
			return nil
		})
	}
	_ = g.Wait()
}

// Destroy kills everything belonging to the cluster and removes its directory.
func Destroy(name string, logf Logf) error {
	d := state.ForCluster(name)
	if _, err := os.Stat(string(d)); err != nil {
		return fmt.Errorf("cluster %q does not exist", name)
	}
	unlock, err := d.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if c, err := Open(name, logf); err == nil {
		_ = c.stop(context.Background(), 0)
	}
	killStrays(string(d))
	return os.RemoveAll(string(d))
}

// killStrays kills any process whose command line references dir (QEMU or switch
// processes left behind without a pidfile).
func killStrays(dir string) {
	self := os.Getpid()
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, p := range procs {
		pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(p)))
		b, err := os.ReadFile(p)
		if err != nil || pid == self || !bytes.Contains(b, []byte(dir+"/")) {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

type NodeStatus struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Online  *bool  `json:"online,omitempty"`
	IP      string `json:"ip"`
	UI      string `json:"ui"`
	SSHPort int    `json:"sshPort"`
}

type Status struct {
	Name     string       `json:"name"`
	Phase    string       `json:"phase"`
	Error    string       `json:"error,omitempty"`
	ISO      string       `json:"iso,omitempty"`
	Switch   bool         `json:"switch"`
	Quorate  *bool        `json:"quorate,omitempty"`
	Ceph     string       `json:"ceph,omitempty"`
	Duration string       `json:"createDuration,omitempty"`
	Nodes    []NodeStatus `json:"nodes"`
}

// Status reports processes and, if reachable, cluster membership via the API token.
func (c *Cluster) Status(ctx context.Context) *Status {
	_, sw := c.switchPID()
	s := &Status{Name: c.Cfg.Name, Phase: string(c.St.Phase), Error: c.St.Error, ISO: c.St.ISO, Switch: sw, Duration: c.St.Duration}
	var api *pve.Client
	for _, n := range c.Cfg.NodeList() {
		ns := c.St.Node(n.Name)
		_, running := qemu.Running(c.pidfile(n))
		s.Nodes = append(s.Nodes, NodeStatus{
			Name: n.Name, Running: running, IP: c.corosyncIP(n),
			UI: fmt.Sprintf("https://127.0.0.1:%d", ns.UIPort), SSHPort: ns.SSHPort,
		})
		if running && api == nil {
			api = c.tokenClient(ns.UIPort)
		}
	}
	if api == nil {
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cs, members, err := getStatus(ctx, api)
	if err != nil || cs == nil {
		return s
	}
	q := cs.Quorate == 1
	s.Quorate = &q
	for i := range s.Nodes {
		on := members[s.Nodes[i].Name].Online == 1
		s.Nodes[i].Online = &on
	}
	if c.Cfg.CephEnabled() {
		var cs cephStatus
		if err := api.Get(ctx, "/cluster/ceph/status", &cs); err == nil {
			s.Ceph = cs.Health.Status
			if checks := cs.checks(); checks != "" {
				s.Ceph += " (" + checks + ")"
			}
		}
	}
	return s
}

// tokenClient returns an API client using the token, verified against the cluster CA.
func (c *Cluster) tokenClient(port int) *pve.Client {
	tok, err := c.Token()
	if err != nil || tok == "" {
		return nil
	}
	caPEM, err := os.ReadFile(c.CAPath())
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil
	}
	cl := pve.NewClientCA(port, pool)
	cl.SetToken(tok)
	return cl
}
