package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/nodesetup"
	"github.com/virtbase/proxbase/internal/remote"
	"github.com/virtbase/proxbase/internal/retry"
)

// postInstall configures host names, repositories and bridges on every node and,
// if configured, upgrades them (rebooting into a new kernel).
func (c *Cluster) postInstall(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			s, err := c.waitSSH(gctx, n.Name, time.Minute)
			if err != nil {
				return err
			}
			defer s.Close()
			out, err := s.Run(nodesetup.Script(c.Cfg, n))
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
			out, err = s.Run(nodesetup.Upgrade)
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
func (c *Cluster) rebootForKernel(ctx context.Context, name string, s *remote.SSH) error {
	out, err := s.Run(nodesetup.PendingKernel)
	if err != nil || strings.TrimSpace(out) == "" {
		return err
	}
	kernel, bootID, _ := strings.Cut(strings.TrimSpace(out), " ")
	c.step("%s: rebooting into kernel %s", name, kernel)
	if _, err := s.Run(nodesetup.Reboot); err != nil {
		return err
	}
	var running string
	err = retry.Do(ctx, 5*time.Minute, 5*time.Second, func() error {
		ns, err := c.ssh(ctx, name)
		if err != nil {
			return err
		}
		defer ns.Close()
		id, err := ns.Run(nodesetup.BootID)
		if err != nil {
			return err
		}
		if strings.HasPrefix(id, bootID) {
			return errors.New("not rebooted yet")
		}
		running = strings.TrimSpace(strings.SplitN(id, "\n", 2)[1])
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s did not come back after the reboot: %w", name, err)
	}
	c.step("%s: running %s", name, running)
	return nil
}
