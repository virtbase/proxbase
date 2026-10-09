package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/virtbase/proxbase/internal/answer"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/image"
	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/serial"
	"golang.org/x/sync/errgroup"
)

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
	names := make([]string, len(todo))
	for i, n := range todo {
		names[i] = n.Name
	}
	c.step("installing Proxmox VE %s on %s (logs: %s)", img.Version, strings.Join(names, ", "), c.Dir.Log("<node>-install.log"))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range todo {
		g.Go(func() error {
			t0 := time.Now()
			if err := c.install(gctx, img, n); err != nil {
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

func (c *Cluster) install(ctx context.Context, img *image.Image, n config.Node) error {
	if _, running := qemu.Running(c.pidfile(n)); running {
		return fmt.Errorf("a VM is already running (pidfile %s)", c.pidfile(n))
	}
	m := c.machine(n)
	if err := qemuImg(m.RootDisk, n.Spec.RootDisk.Size); err != nil {
		return err
	}
	for i, d := range m.DataDisks {
		if err := qemuImg(d, n.Spec.DataDisks[i].Size); err != nil {
			return err
		}
	}
	ansDir := c.Dir.Disk(n.Name + "-answer")
	if err := os.MkdirAll(ansDir, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(ansDir)
	pub, err := c.Dir.ReadSecret(secretKey + ".pub")
	if err != nil {
		return err
	}
	ans := answer.Render(answer.Params{
		FQDN:         n.Name + "." + c.Cfg.Proxmox.Domain,
		Mailto:       "root@" + c.Cfg.Proxmox.Domain,
		Keyboard:     c.Cfg.Proxmox.Keyboard,
		Country:      c.Cfg.Proxmox.Country,
		Timezone:     c.Cfg.Proxmox.Timezone,
		RootPassword: c.password,
		SSHKeys:      append([]string{pub}, c.Cfg.Proxmox.SSHKeys...),
		MAC:          m.NATMAC,
		Filesystem:   n.Spec.RootDisk.Filesystem,
	})
	if err := os.WriteFile(filepath.Join(ansDir, "answer.toml"), []byte(ans), 0o600); err != nil {
		return err
	}
	c.cleanSockets(n)
	sock := c.Dir.Run(n.Name + "-serial.sock")
	m.Install = &qemu.Install{ISO: img.ISO, Kernel: img.Kernel, Initrd: img.Initrd, Cmdline: img.Cmdline, AnswerDir: ansDir, SerialSock: sock}
	qlog, err := os.Create(c.Dir.Log(n.Name + "-qemu.log"))
	if err != nil {
		return err
	}
	defer qlog.Close()
	cmd, err := m.StartInstall(qlog)
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	driveErr := serial.Drive(ctx, sock, c.Dir.Log(n.Name+"-install.log"), installTimeout)
	if driveErr != nil {
		_ = cmd.Process.Kill()
		<-exited
		if b, _ := os.ReadFile(c.Dir.Log(n.Name + "-qemu.log")); len(b) > 0 {
			return fmt.Errorf("%w\nQEMU: %s", driveErr, strings.TrimSpace(string(b)))
		}
		return driveErr
	}
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("QEMU exited with %w", err)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
		return errors.New("QEMU did not exit after the installation")
	}
	_ = os.Remove(sock)
	return nil
}

func qemuImg(path, size string) error {
	_ = os.Remove(path)
	out, err := exec.Command("qemu-img", "create", "-q", "-f", "qcow2", path, size).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img create %s: %w: %s", path, err, out)
	}
	return nil
}

// cleanSockets removes stale sockets of a stopped node (QEMU refuses to bind them).
func (c *Cluster) cleanSockets(n config.Node) {
	for _, net := range c.Cfg.Networks {
		_ = os.Remove(c.nodeSock(n, net.Name))
	}
	_ = os.Remove(c.qmp(n))
	_ = os.Remove(c.Dir.Run(n.Name + "-serial.sock"))
}

// bootAll starts every node and waits for SSH.
func (c *Cluster) bootAll(ctx context.Context) error {
	c.step("booting nodes")
	for _, n := range c.Cfg.NodeList() {
		if _, ok := qemu.Running(c.pidfile(n)); ok {
			continue
		}
		c.cleanSockets(n)
		if err := c.machine(n).Start(); err != nil {
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
