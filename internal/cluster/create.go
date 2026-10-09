package cluster

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/image"
	"github.com/virtbase/proxbase/internal/network"
	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
)

// Create builds a cluster, or resumes an unfinished create of the same name.
func Create(ctx context.Context, cfg *config.Cluster, p progress.Sink) (*Cluster, error) {
	d := state.ForCluster(cfg.Name)
	var c *Cluster
	var err error
	if d.Exists() {
		if c, err = Open(cfg.Name, p); err != nil {
			return nil, err
		}
		if c.St.Phase == state.PhaseReady {
			return nil, fmt.Errorf("cluster %q already exists", cfg.Name)
		}
		c.Progress.Logf("resuming unfinished create of %q (using its stored %s)", cfg.Name, d.Config())
	} else {
		if err := preflight(cfg, cfg.NodeList()); err != nil {
			return nil, err
		}
		if c, err = initialize(cfg, p); err != nil {
			return nil, err
		}
	}
	unlock, err := d.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	c.St.Phase, c.St.Error = state.PhaseCreating, ""
	if err := c.create(ctx); err != nil {
		c.St.Phase, c.St.Error = state.PhaseFailed, err.Error()
		_ = c.save()
		return c, err
	}
	now := time.Now().UTC()
	c.St.Phase, c.St.ReadyAt = state.PhaseReady, &now
	c.St.Duration = time.Since(c.start).Round(time.Second).String()
	return c, c.save()
}

// create runs all steps; each one skips what is already done.
func (c *Cluster) create(ctx context.Context) error {
	c.step("cluster %s: %d node(s), state in %s", c.Cfg.Name, c.Cfg.Nodes.Count, c.Dir)
	img, err := image.Ensure(ctx, c.Cfg.Proxmox.Mirror, c.Cfg.Proxmox.Version, state.CacheHome(), c.step)
	if err != nil {
		return err
	}
	c.St.ISO = filepath.Base(img.ISO)
	if err := c.save(); err != nil {
		return err
	}
	steps := []func(context.Context) error{
		func(context.Context) error { return c.net.Start(c.Cfg) },
		func(ctx context.Context) error { return c.installAll(ctx, img) },
		c.bootAll,
		c.personalizeAll,
		c.postInstall,
		c.formCluster,
		func(ctx context.Context) error { return c.waitQuorum(ctx, 5*time.Minute) },
		c.setupStorage,
		c.ensureToken,
		c.exportCA,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	c.step("cluster %s is ready", c.Cfg.Name)
	return nil
}

const unixPathMax = 107

// preflight catches host problems for new nodes before anything is written.
func preflight(cfg *config.Cluster, nodes []config.Node) error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("KVM is required: %w (run `proxbase doctor`)", err)
	}
	f.Close()
	for _, bin := range []string{qemu.Binary, "qemu-img"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH (install QEMU)", bin)
		}
	}
	sw := network.Switch{Dir: state.ForCluster(cfg.Name), Cluster: cfg.Name}
	for _, n := range nodes {
		for _, p := range sw.SocketPaths(cfg, n) {
			if len(p) > unixPathMax {
				return fmt.Errorf("socket path %s is too long (%d > %d bytes); use a shorter XDG_DATA_HOME or names", p, len(p), unixPathMax)
			}
		}
		ui, ssh := cfg.Ports(n.Index)
		for _, port := range []int{ui, ssh} {
			l, err := net.Listen("tcp", net.JoinHostPort(cfg.Access.BindAddress, strconv.Itoa(port)))
			if err != nil {
				return fmt.Errorf("port %d for %s is in use; set access.portBase: %w", port, n.Name, err)
			}
			l.Close()
		}
	}
	return nil
}
