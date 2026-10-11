package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/golden"
	"github.com/virtbase/proxbase/internal/image"
	"github.com/virtbase/proxbase/internal/nodesetup"
	"github.com/virtbase/proxbase/internal/retry"
	"github.com/virtbase/proxbase/internal/vm"
)

// cloneAll creates the nodes' disks from base images instead of installing.
func (c *Cluster) cloneAll(ctx context.Context, img *image.Image, todo []config.Node) error {
	cloner, ok := c.runtime.(vm.Cloner)
	builder, ok2 := c.runtime.(golden.Builder)
	if !ok || !ok2 {
		return errors.New("the node runtime cannot build or clone base images")
	}
	t0 := time.Now()
	var names []string
	for _, n := range todo {
		base, err := golden.Ensure(ctx, builder, c.Cfg, n, img, c.aptEnv(), c.step)
		if err != nil {
			return err
		}
		if err := cloner.Clone(ctx, c.spec(n), base.Disk()); err != nil {
			return fmt.Errorf("%s: %w", n.Name, err)
		}
		ns := c.St.Node(n.Name)
		ns.Installed, ns.Base, ns.Pending, ns.HostKey = true, base.Key, true, ""
		names = append(names, n.Name)
	}
	c.step("%s cloned from base image in %s", strings.Join(names, ", "), time.Since(t0).Round(time.Second))
	return c.save()
}

// personalizeAll completes the identity of cloned nodes (host name, SSH host
// keys, machine-id, password, authorized keys). Nodes from current base images
// received it on their first boot; nodes from base images built before the
// first-boot hook are personalized over SSH and rebooted.
func (c *Cluster) personalizeAll(ctx context.Context) error {
	pub, err := c.Dir.ReadSecret(secretKey + ".pub")
	if err != nil {
		return err
	}
	keys := append([]string{strings.TrimSpace(pub)}, c.Cfg.Proxmox.SSHKeys...)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		if !c.St.Node(n.Name).Pending {
			continue
		}
		g.Go(func() error {
			if err := c.personalize(gctx, n.Name, keys, &mu); err != nil {
				return fmt.Errorf("%s: personalize: %w", n.Name, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	return c.save()
}

func (c *Cluster) personalize(ctx context.Context, name string, keys []string, mu *sync.Mutex) error {
	ns := c.St.Node(name)
	done := func() {
		mu.Lock()
		ns.Pending, ns.HostKey = false, ""
		mu.Unlock()
	}
	// The cluster key works once the node has its identity (first-boot hook or a
	// previous run); until then only the base image's key does.
	s, err := c.waitSSH(ctx, name, 5*time.Minute)
	if err != nil {
		return err
	}
	out, err := s.Run("cat " + golden.IdentityMarker + " 2>/dev/null || true")
	if err != nil {
		s.Close()
		return err
	}
	if strings.TrimSpace(out) == name {
		s.Close()
		done()
		return c.finishIdentity(ctx, name)
	}
	// A base image from before the first-boot hook: personalize over SSH.
	bootID, err := s.Run("cat /proc/sys/kernel/random/boot_id")
	if err == nil {
		_, err = s.Run(golden.Personalize(name, c.Cfg.Proxmox.Domain, c.password, keys))
	}
	s.Close()
	if err != nil {
		return err
	}
	done()
	// Rebooting: wait for the new boot with the cluster key and the new host key.
	err = retry.Do(ctx, 5*time.Minute, time.Second, func() error {
		ns, err := c.dial(ctx, name, c.signer, "")
		if err != nil {
			return err
		}
		defer ns.Close()
		id, err := ns.Run(nodesetup.BootID)
		if err != nil {
			return err
		}
		if strings.HasPrefix(id, strings.TrimSpace(bootID)) {
			return errors.New("not rebooted yet")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("did not come back after the reboot: %w", err)
	}
	return c.finishIdentity(ctx, name)
}

func (c *Cluster) finishIdentity(ctx context.Context, name string) error {
	s, err := c.waitSSH(ctx, name, 2*time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()
	if _, err := s.Run(golden.Finish(name)); err != nil {
		return err
	}
	c.step("%s: personalized", name)
	return nil
}
