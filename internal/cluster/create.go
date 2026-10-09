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
	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
)

const installTimeout = 20 * time.Minute

// Create builds a cluster, or resumes an unfinished create of the same name.
func Create(ctx context.Context, cfg *config.Cluster, logf Logf) (*Cluster, error) {
	d := state.ForCluster(cfg.Name)
	var c *Cluster
	var err error
	if d.Exists() {
		if c, err = Open(cfg.Name, logf); err != nil {
			return nil, err
		}
		if c.St.Phase == state.PhaseReady {
			return nil, fmt.Errorf("cluster %q already exists", cfg.Name)
		}
		logf("resuming unfinished create of %q (using its stored %s)", cfg.Name, d.Config())
	} else {
		if err := preflight(cfg, cfg.NodeList()); err != nil {
			return nil, err
		}
		if c, err = initialize(cfg, logf); err != nil {
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

func (c *Cluster) create(ctx context.Context) error {
	c.step("cluster %s: %d node(s), state in %s", c.Cfg.Name, c.Cfg.Nodes.Count, c.Dir)
	img, err := image.Ensure(ctx, c.Cfg.Proxmox.Mirror, c.Cfg.Proxmox.Version, filepath.Join(state.CacheHome()), c.step)
	if err != nil {
		return err
	}
	c.St.ISO = filepath.Base(img.ISO)
	if err := c.save(); err != nil {
		return err
	}
	if err := c.startSwitch(); err != nil {
		return err
	}
	if err := c.installAll(ctx, img); err != nil {
		return err
	}
	if err := c.bootAll(ctx); err != nil {
		return err
	}
	if err := c.postInstall(ctx); err != nil {
		return err
	}
	if err := c.formCluster(ctx); err != nil {
		return err
	}
	if err := c.waitQuorum(ctx, 5*time.Minute); err != nil {
		return err
	}
	if err := c.createZFS(ctx); err != nil {
		return err
	}
	if err := c.setupCeph(ctx); err != nil {
		return err
	}
	if *c.Cfg.Access.APIToken {
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
	}
	if err := c.exportCA(ctx); err != nil {
		return err
	}
	c.step("cluster %s is ready", c.Cfg.Name)
	return nil
}

var unixPathMax = 107

// preflight catches host problems before anything is written.
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
	d := state.ForCluster(cfg.Name)
	for _, n := range nodes {
		for _, net := range cfg.Networks {
			if p := d.Run(n.Name + "-" + net.Name + ".sock"); len(p) > unixPathMax {
				return fmt.Errorf("socket path %s is too long (%d > %d bytes); use a shorter XDG_DATA_HOME or names", p, len(p), unixPathMax)
			}
		}
		ui, ssh := cfg.Ports(n.Index)
		for _, port := range []int{ui, ssh} {
			l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
			if err != nil {
				return fmt.Errorf("port %d for %s is in use; set access.portBase: %w", port, n.Name, err)
			}
			l.Close()
		}
	}
	return nil
}
