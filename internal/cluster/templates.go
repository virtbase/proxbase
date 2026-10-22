package cluster

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/guestimage"
	"github.com/virtbase/proxbase/internal/remote"
	"github.com/virtbase/proxbase/internal/state"
)

const (
	containerDir = "/var/lib/vz/template/cache/" // of storage local
	importDir    = "/var/lib/vz/import/"
	aplIndex     = "/var/lib/pve-manager/apl-info/download.proxmox.com"
)

// ensureTemplates provides the configured templates: container templates on
// every node, VM templates imported from cloud images on the first node or on
// each. It skips what exists, so it also completes templates on added nodes.
func (c *Cluster) ensureTemplates(ctx context.Context) error {
	t := c.Cfg.Templates
	if len(t.VMs) == 0 && len(t.Containers) == 0 {
		return nil
	}
	nodes := c.Cfg.NodeList()
	for _, name := range t.Containers {
		if err := c.ensureContainerTemplate(ctx, nodes, name); err != nil {
			return err
		}
	}
	for _, v := range t.VMs {
		img, err := guestimage.CloudImage(ctx, v, state.CacheHome(), c.step)
		if err != nil {
			return fmt.Errorf("template %s: %w", v.Name, err)
		}
		targets := nodes[:1]
		if v.PerNode {
			targets = nodes
		}
		g, gctx := errgroup.WithContext(ctx)
		for _, n := range targets {
			vmid := v.VMID
			if v.PerNode {
				vmid += n.Index - 1
			}
			g.Go(func() error { return c.ensureVMTemplate(gctx, n, v, vmid, img) })
		}
		if err := g.Wait(); err != nil {
			return err
		}
	}
	return nil
}

// ensureContainerTemplate copies the newest container template called name to
// every node's local storage, through the host cache.
func (c *Cluster) ensureContainerTemplate(ctx context.Context, nodes []config.Node, name string) error {
	ct, err := c.containerEntry(ctx, nodes[0].Name, name)
	if err != nil {
		return err
	}
	img, err := guestimage.ContainerImage(ctx, ct, state.CacheHome(), c.step)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		g.Go(func() error { return c.uploadOnce(gctx, n.Name, containerDir+img.Name, img) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	c.step("container template %s on %d node(s)", img.Name, len(nodes))
	return nil
}

// containerEntry finds a template in the appliance index, which the node
// downloads and checks the signature of. Without network access it uses the
// entry of the last create.
func (c *Cluster) containerEntry(ctx context.Context, node, name string) (guestimage.Container, error) {
	r, err := c.Exec(ctx, node, "pveam update >/dev/null 2>&1; cat "+aplIndex)
	if err != nil {
		return guestimage.Container{}, err
	}
	if r.ExitCode == 0 {
		index, err := guestimage.ParseIndex(strings.NewReader(r.Stdout))
		if err != nil {
			return guestimage.Container{}, err
		}
		if ct, ok := guestimage.Newest(index, name); ok {
			_ = guestimage.SaveContainer(state.CacheHome(), ct)
			return ct, nil
		}
		return guestimage.Container{}, fmt.Errorf("container template %q is not in the appliance index (see pveam available --section system)", name)
	}
	if ct, ok := guestimage.LastContainer(state.CacheHome(), name); ok {
		return ct, nil
	}
	return guestimage.Container{}, fmt.Errorf("container template %s: %s has no appliance index (pveam update needs internet access)", name, node)
}

// ensureVMTemplate imports img as VM template vmid on node n, unless it exists.
func (c *Cluster) ensureVMTemplate(ctx context.Context, n config.Node, v config.VMTemplate, vmid int, img *guestimage.Image) error {
	r, err := c.Exec(ctx, n.Name, fmt.Sprintf("qm config %d 2>/dev/null", vmid))
	if err != nil {
		return err
	}
	if r.ExitCode == 0 {
		if !strings.Contains(r.Stdout, "\ntemplate: 1") && !strings.HasPrefix(r.Stdout, "template: 1") {
			return fmt.Errorf("template %s: VM %d exists on %s and is not a template", v.Name, vmid, n.Name)
		}
		return nil
	}
	t0 := time.Now()
	file := importDir + img.Name
	if err := c.uploadOnce(ctx, n.Name, file, img); err != nil {
		return err
	}
	storage := v.Storage
	if storage == "" {
		storage = rootStorage(n.Spec.RootDisk.Filesystem)
	}
	script := fmt.Sprintf(`set -e
trap 'rm -f %[8]s' EXIT
qm create %[1]d --name %[2]s --memory %[3]d --cores %[4]d --net0 virtio,bridge=vmbr0 --ostype l26 \
  --scsihw virtio-scsi-single --scsi0 %[5]s:0,import-from=local:import/%[6]s,discard=on \
  --ide2 %[5]s:cloudinit --serial0 socket --vga serial0 --agent enabled=1 --boot order=scsi0 >/dev/null || { qm destroy %[1]d --purge >/dev/null 2>&1; exit 1; }
%[7]s
qm template %[1]d`, vmid, remote.Quote(v.Name), v.Memory, v.Cores, remote.Quote(storage), remote.Quote(img.Name), resize(vmid, v.DiskSize), remote.Quote(file))
	if r, err = c.Exec(ctx, n.Name, script); err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("template %s on %s: %s", v.Name, n.Name, strings.TrimSpace(r.Stderr))
	}
	c.step("%s: VM template %s (%d) in %s", n.Name, v.Name, vmid, time.Since(t0).Round(time.Second))
	return nil
}

func resize(vmid int, size string) string {
	if size == "" {
		return ""
	}
	return fmt.Sprintf("qm disk resize %d scsi0 %s >/dev/null", vmid, remote.Quote(size))
}

// rootStorage is the storage the installer creates for guest disks on a root
// disk with the file system fs.
func rootStorage(fs string) string {
	switch fs {
	case "zfs":
		return "local-zfs"
	case "btrfs":
		return "local-btrfs"
	}
	return "local-lvm"
}

// uploadOnce copies img to p on node unless a file of its size is there.
func (c *Cluster) uploadOnce(ctx context.Context, node, p string, img *guestimage.Image) error {
	r, err := c.Exec(ctx, node, "stat -c %s -- "+remote.Quote(p)+" 2>/dev/null")
	if err != nil {
		return err
	}
	if size, err := strconv.ParseInt(strings.TrimSpace(r.Stdout), 10, 64); err == nil && size == img.Size {
		return nil
	}
	f, err := os.Open(img.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.PutFile(ctx, node, p, f, 0o644)
}
