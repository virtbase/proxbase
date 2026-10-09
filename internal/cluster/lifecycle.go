package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/retry"
	"github.com/virtbase/proxbase/internal/state"
)

// Start boots a stopped cluster and waits for quorum, the API and storage.
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
	if err := c.net.Start(c.Cfg); err != nil {
		return err
	}
	if err := c.bootAll(ctx); err != nil {
		return err
	}
	c.St.Faults = slices.DeleteFunc(c.St.Faults, func(f state.Fault) bool { return f.Kind == FaultKill })
	if err := c.save(); err != nil {
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
	return c.waitStorage(ctx)
}

// waitAPI waits until the token client can read the cluster status.
func (c *Cluster) waitAPI(ctx context.Context, timeout time.Duration) error {
	api := c.tokenClient(c.St.Nodes[0].UIPort)
	if api == nil {
		return nil
	}
	err := retry.Do(ctx, timeout, time.Second, func() error {
		_, _, err := api.ClusterStatus(ctx)
		return err
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("API not reachable after %s: %w", timeout, err)
	}
	return err
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
	var running []string
	for _, n := range c.Cfg.NodeList() {
		if c.running(n) {
			running = append(running, n.Name)
		}
	}
	if timeout > 0 && len(running) > 0 {
		c.prepareStop(ctx, running)
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		if !c.running(n) {
			continue
		}
		g.Go(func() error {
			if err := c.runtime.Stop(gctx, c.spec(n), timeout); err != nil {
				return fmt.Errorf("%s: %w", n.Name, err)
			}
			c.step("%s stopped", n.Name)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	// Injected faults do not survive a shutdown.
	if len(c.St.Faults) > 0 {
		c.St.Faults = nil
		if err := c.save(); err != nil {
			return err
		}
	}
	if err := c.net.Stop(); err != nil {
		return err
	}
	return c.net.DropFaults()
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
