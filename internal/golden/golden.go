// Package golden caches installed base images so new nodes skip the installer.
//
// A base image is an installed but never booted root disk, one per key (ISO,
// root filesystem and size, installer locale). Nodes use qcow2 overlays on top of
// it. Everything Proxmox VE generates on first boot (cluster CA, auth key, node
// certificate, root SSH key) is therefore unique per node; what the installer
// wrote (host name, SSH host keys, machine-id) is replaced by Personalize.
package golden

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/virtbase/proxbase/internal/answer"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/image"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/vm"
)

// Hostname is the name nodes have before personalization.
const Hostname = "golden"

const format = "1" // bump when the build or personalization changes

// Base is a cached base image.
type Base struct {
	Key string
	Dir string
}

// Meta describes a base image.
type Meta struct {
	Key        string    `json:"key"`
	ISO        string    `json:"iso"`
	Filesystem string    `json:"filesystem"`
	Size       string    `json:"size"`
	Created    time.Time `json:"created"`
}

func imagesDir() string { return filepath.Join(state.CacheHome(), "images") }

// Disk is the installed root disk overlays are based on.
func (b Base) Disk() string { return filepath.Join(b.Dir, "root.qcow2") }

// Signer is the key that logs in to nodes before personalization.
func (b Base) Signer() (ssh.Signer, error) {
	key, err := os.ReadFile(filepath.Join(b.Dir, "id_ed25519"))
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(key)
}

// Key identifies the base image for a node.
func Key(cfg *config.Cluster, n config.Node, iso string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{format, filepath.Base(iso), n.Spec.RootDisk.Filesystem,
		n.Spec.RootDisk.Size, cfg.Proxmox.Keyboard, cfg.Proxmox.Country, cfg.Proxmox.Timezone}, "\n")))
	return hex.EncodeToString(h[:8])
}

// Open returns an existing base image.
func Open(key string) (Base, error) {
	b := Base{Key: key, Dir: filepath.Join(imagesDir(), key)}
	if _, err := os.Stat(b.Disk()); err != nil {
		return b, fmt.Errorf("base image %s: %w", key, err)
	}
	return b, nil
}

// Ensure returns the base image for node n, installing it first if needed.
func Ensure(ctx context.Context, inst vm.Installer, cfg *config.Cluster, n config.Node, img *image.Image, logf func(string, ...any)) (Base, error) {
	key := Key(cfg, n, img.ISO)
	if b, err := Open(key); err == nil {
		return b, nil
	}
	if err := os.MkdirAll(imagesDir(), 0o755); err != nil {
		return Base{}, err
	}
	lock, err := os.OpenFile(filepath.Join(imagesDir(), key+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Base{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Base{}, err
	}
	if b, err := Open(key); err == nil { // built by another create meanwhile
		return b, nil
	}
	logf("building base image %s (Proxmox VE %s, %s %s); later creates reuse it", key, img.Version, n.Spec.RootDisk.Filesystem, n.Spec.RootDisk.Size)
	tmp := filepath.Join(imagesDir(), key+".tmp")
	_ = os.RemoveAll(tmp)
	if err := build(ctx, inst, cfg, n, img, key, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return Base{}, err
	}
	dir := filepath.Join(imagesDir(), key)
	if err := os.Rename(tmp, dir); err != nil {
		return Base{}, err
	}
	return Base{Key: key, Dir: dir}, nil
}

func build(ctx context.Context, inst vm.Installer, cfg *config.Cluster, n config.Node, img *image.Image, key, dir string) error {
	for _, sub := range []string{"run", "logs", "answer"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	pub, err := writeKey(dir)
	if err != nil {
		return err
	}
	spec := vm.Spec{
		Name: Hostname, Process: "proxbase-golden-" + key, CPUs: 4, MemoryMiB: 4096, Nested: true,
		RootDisk: vm.Disk{Path: filepath.Join(dir, "root.qcow2"), Size: n.Spec.RootDisk.Size},
		NATMAC:   vm.MAC(0, 0), Bind: "127.0.0.1",
		RunDir: filepath.Join(dir, "run"), LogDir: filepath.Join(dir, "logs"),
	}
	if spec.UIPort, err = freePort(); err != nil {
		return err
	}
	if spec.SSHPort, err = freePort(); err != nil {
		return err
	}
	ans := answer.Render(answer.Params{
		FQDN: Hostname + ".proxbase.invalid", Mailto: "root@proxbase.invalid",
		Keyboard: cfg.Proxmox.Keyboard, Country: cfg.Proxmox.Country, Timezone: cfg.Proxmox.Timezone,
		RootPassword: answer.Password(24), SSHKeys: []string{pub}, MAC: spec.NATMAC,
		Filesystem: n.Spec.RootDisk.Filesystem,
	})
	if err := os.WriteFile(filepath.Join(dir, "answer", "answer.toml"), []byte(ans), 0o600); err != nil {
		return err
	}
	media := vm.Media{ISO: img.ISO, Kernel: img.Kernel, Initrd: img.Initrd, Cmdline: img.Cmdline, AnswerDir: filepath.Join(dir, "answer")}
	if err := inst.Install(ctx, spec, media); err != nil {
		return fmt.Errorf("base image: %w", err)
	}
	_ = os.RemoveAll(filepath.Join(dir, "answer"))
	_ = os.RemoveAll(filepath.Join(dir, "run"))
	meta, err := json.MarshalIndent(Meta{Key: key, ISO: filepath.Base(img.ISO), Filesystem: n.Spec.RootDisk.Filesystem, Size: n.Spec.RootDisk.Size, Created: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o644)
}

// writeKey creates the login key of nodes before personalization.
func writeKey(dir string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "proxbase-golden")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " proxbase-golden", nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// List returns all base images.
func List() ([]Meta, error) {
	entries, err := os.ReadDir(imagesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(imagesDir(), e.Name(), "meta.json"))
		if !e.IsDir() || err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// Remove deletes a base image.
func Remove(key string) error {
	_ = os.Remove(filepath.Join(imagesDir(), key+".lock"))
	return os.RemoveAll(filepath.Join(imagesDir(), key))
}
