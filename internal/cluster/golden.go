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
	if !ok {
		return errors.New("the node runtime cannot clone base images")
	}
	t0 := time.Now()
	var names []string
	for _, n := range todo {
		base, err := golden.Ensure(ctx, c.runtime, c.Cfg, n, img, c.step)
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

// personalizeAll gives cloned nodes their own identity (host name, SSH host keys,
// machine-id, password, authorized keys) and reboots them.
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
	// A previous run may have personalized the node already (golden key gone).
	if s, err := c.dial(ctx, name, c.signer, ""); err == nil {
		out, rerr := s.Run("cat " + golden.IdentityMarker + " 2>/dev/null || true")
		s.Close()
		if rerr == nil && strings.TrimSpace(out) == name {
			done()
			return c.finishIdentity(ctx, name)
		}
	}
	s, err := c.waitSSH(ctx, name, 5*time.Minute) // golden key while pending
	if err != nil {
		return err
	}
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
	err = retry.Do(ctx, 5*time.Minute, 5*time.Second, func() error {
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
