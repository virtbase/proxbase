package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/answer"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/firstboot"
	"github.com/virtbase/proxbase/internal/image"
	"github.com/virtbase/proxbase/internal/vm"
)

// installAll installs every node that is not installed yet, in parallel.
func (c *Cluster) installAll(ctx context.Context, img *image.Image) error {
	var todo []config.Node
	for _, n := range c.Cfg.NodeList() {
		if !c.St.Node(n.Name).Installed {
			todo = append(todo, n)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	if c.Cfg.Proxmox.Golden {
		return c.cloneAll(ctx, img, todo)
	}
	names := make([]string, len(todo))
	for i, n := range todo {
		names[i] = n.Name
	}
	c.step("installing Proxmox VE %s on %s (logs: %s)", img.Version, strings.Join(names, ", "), c.Dir.Log("<node>-install.log"))
	hook, err := firstboot.Serve()
	if err != nil {
		return err
	}
	defer hook.Close()
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range todo {
		g.Go(func() error {
			t0 := time.Now()
			if err := c.install(gctx, img, n, hook.URL); err != nil {
				return fmt.Errorf("%s: %w", n.Name, err)
			}
			mu.Lock()
			defer mu.Unlock()
			c.St.Node(n.Name).Installed = true
			c.step("%s installed in %s", n.Name, time.Since(t0).Round(time.Second))
			return c.save()
		})
	}
	return g.Wait()
}

// install writes the node's answer file and runs the unattended installer.
// The installed system runs the first-boot hook at hookURL.
func (c *Cluster) install(ctx context.Context, img *image.Image, n config.Node, hookURL string) error {
	ansDir := c.Dir.Disk(n.Name + "-answer")
	if err := os.MkdirAll(ansDir, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(ansDir)
	pub, err := c.Dir.ReadSecret(secretKey + ".pub")
	if err != nil {
		return err
	}
	spec := c.spec(n)
	ans := answer.Render(answer.Params{
		FQDN:         n.Name + "." + c.Cfg.Proxmox.Domain,
		Mailto:       "root@" + c.Cfg.Proxmox.Domain,
		Keyboard:     c.Cfg.Proxmox.Keyboard,
		Country:      c.Cfg.Proxmox.Country,
		Timezone:     c.Cfg.Proxmox.Timezone,
		RootPassword: c.password,
		SSHKeys:      append([]string{pub}, c.Cfg.Proxmox.SSHKeys...),
		MAC:          spec.NATMAC,
		Filesystem:   n.Spec.RootDisk.Filesystem,
		FirstBootURL: hookURL,
	})
	if err := os.WriteFile(filepath.Join(ansDir, "answer.toml"), []byte(ans), 0o600); err != nil {
		return err
	}
	return c.runtime.Install(ctx, spec, vm.Media{ISO: img.ISO, Kernel: img.Kernel, Initrd: img.Initrd, Cmdline: img.Cmdline, AnswerDir: ansDir})
}

// startNode starts node n. A node cloned from a base image that has no identity yet
// gets it as a guest file, which the first-boot hook applies.
func (c *Cluster) startNode(ctx context.Context, n config.Node) error {
	spec := c.spec(n)
	if !c.St.Node(n.Name).Pending || c.runtime.Running(spec) {
		return c.runtime.Start(ctx, spec)
	}
	pub, err := c.Dir.ReadSecret(secretKey + ".pub")
	if err != nil {
		return err
	}
	keys := append([]string{pub}, c.Cfg.Proxmox.SSHKeys...)
	path := filepath.Join(spec.RunDir, n.Name+"-identity")
	if err := os.WriteFile(path, firstboot.Identity(n.Name, c.Cfg.Proxmox.Domain, c.password, keys), 0o600); err != nil {
		return err
	}
	// QEMU reads the file at start; it holds the root password, so drop it then.
	defer os.Remove(path)
	spec.Files = append(spec.Files, vm.File{Name: firstboot.FileIdentity, Path: path})
	return c.runtime.Start(ctx, spec)
}

// bootAll starts every node and waits for SSH.
func (c *Cluster) bootAll(ctx context.Context) error {
	c.step("booting nodes")
	for _, n := range c.Cfg.NodeList() {
		if err := c.startNode(ctx, n); err != nil {
			return err
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range c.Cfg.NodeList() {
		g.Go(func() error {
			s, err := c.waitSSH(gctx, n.Name, 5*time.Minute)
			if err != nil {
				return err
			}
			return s.Close()
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	c.step("all nodes reachable via SSH")
	return c.save()
}
