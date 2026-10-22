// Package golden caches installed base images so new nodes skip the installer.
//
// A base image is an installed root disk, one per key (ISO, root filesystem and
// size, installer locale, baked Ceph release). Nodes use qcow2 overlays on top
// of it. The image is booted once to bake it (package firstboot), but powers off
// before any Proxmox VE service starts, so everything Proxmox VE generates on
// first boot (cluster CA, auth key, node certificate, root SSH key) is unique per
// node. What the installer wrote (host name, SSH host keys, machine-id) is
// replaced on each clone's first boot by the first-boot hook, before networking
// starts. Base images built before the hook existed are personalized over SSH
// instead (Personalize), which costs a reboot.
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
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/virtbase/proxbase/internal/answer"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/firstboot"
	"github.com/virtbase/proxbase/internal/image"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/vm"
)

// Hostname is the name nodes have before personalization.
const Hostname = "golden"

const format = "2" // bump when the build or personalization changes

const bakeTimeout = 20 * time.Minute

// Builder installs and bakes base images.
type Builder interface {
	vm.Installer
	vm.Booter
}

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
	Ceph       string    `json:"ceph,omitempty"` // baked Ceph release
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
		n.Spec.RootDisk.Size, cfg.Proxmox.Keyboard, cfg.Proxmox.Country, cfg.Proxmox.Timezone, cephRelease(cfg)}, "\n")))
	return hex.EncodeToString(h[:8])
}

// cephRelease is the Ceph release baked into base images, if Ceph is used.
func cephRelease(cfg *config.Cluster) string {
	if !cfg.CephEnabled() {
		return ""
	}
	return cfg.Storage.Ceph.Version
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
// aptEnv is put before the bake script's apt commands (see storage.Host.AptEnv).
func Ensure(ctx context.Context, inst Builder, cfg *config.Cluster, n config.Node, img *image.Image, aptEnv string, logf func(string, ...any)) (Base, error) {
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
	what := fmt.Sprintf("Proxmox VE %s, %s %s", img.Version, n.Spec.RootDisk.Filesystem, n.Spec.RootDisk.Size)
	if c := cephRelease(cfg); c != "" {
		what += ", Ceph " + c
	}
	logf("building base image %s (%s); later creates reuse it", key, what)
	tmp := filepath.Join(imagesDir(), key+".tmp")
	_ = os.RemoveAll(tmp)
	if err := build(ctx, inst, cfg, n, img, key, tmp, aptEnv, logf); err != nil {
		_ = os.RemoveAll(tmp)
		return Base{}, err
	}
	dir := filepath.Join(imagesDir(), key)
	if err := os.Rename(tmp, dir); err != nil {
		return Base{}, err
	}
	return Base{Key: key, Dir: dir}, nil
}

func build(ctx context.Context, inst Builder, cfg *config.Cluster, n config.Node, img *image.Image, key, dir, aptEnv string, logf func(string, ...any)) error {
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
	hook, err := firstboot.Serve()
	if err != nil {
		return err
	}
	defer hook.Close()
	ans := answer.Render(answer.Params{
		FQDN: Hostname + ".proxbase.invalid", Mailto: "root@proxbase.invalid",
		Keyboard: cfg.Proxmox.Keyboard, Country: cfg.Proxmox.Country, Timezone: cfg.Proxmox.Timezone,
		RootPassword: answer.Password(24), SSHKeys: []string{pub}, MAC: spec.NATMAC,
		Filesystem: n.Spec.RootDisk.Filesystem, FirstBootURL: hook.URL,
	})
	if err := os.WriteFile(filepath.Join(dir, "answer", "answer.toml"), []byte(ans), 0o600); err != nil {
		return err
	}
	media := vm.Media{ISO: img.ISO, Kernel: img.Kernel, Initrd: img.Initrd, Cmdline: img.Cmdline, AnswerDir: filepath.Join(dir, "answer")}
	if err := inst.Install(ctx, spec, media); err != nil {
		return fmt.Errorf("base image: %w", err)
	}
	_ = os.RemoveAll(filepath.Join(dir, "answer"))
	ceph := cephRelease(cfg)
	if err := bake(ctx, inst, spec, ceph, aptEnv, logf); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(dir, "run"))
	meta, err := json.MarshalIndent(Meta{Key: key, ISO: filepath.Base(img.ISO), Filesystem: n.Spec.RootDisk.Filesystem, Size: n.Spec.RootDisk.Size, Ceph: ceph, Created: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o644)
}

// bake boots the installed image once with the bake script; the first-boot hook
// runs it and powers off, reporting the result on the serial console.
func bake(ctx context.Context, b vm.Booter, spec vm.Spec, ceph, aptEnv string, logf func(string, ...any)) error {
	if ceph != "" {
		logf("baking base image: installing Ceph %s", ceph)
	}
	script := filepath.Join(spec.RunDir, "bake.sh")
	if err := os.WriteFile(script, []byte(aptEnv+firstboot.Bake(ceph, spec.NATMAC)), 0o600); err != nil {
		return err
	}
	spec.Files = []vm.File{{Name: firstboot.FileBake, Path: script}}
	console, err := b.BootOnce(ctx, spec, bakeTimeout)
	if err != nil {
		return fmt.Errorf("bake base image: %w", err)
	}
	if !strings.Contains(string(console), firstboot.BakeOK) {
		return fmt.Errorf("bake base image failed; serial console:\n%s", lastLines(string(console), 30))
	}
	return nil
}

// ansiRe matches terminal control sequences (GRUB draws its menu with them).
var ansiRe = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07]*\x07|[()][0-9A-Za-z])`)

// lastLines returns the last n non-empty lines of console output, indented.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(ansiRe.ReplaceAllString(strings.ReplaceAll(s, "\r", ""), ""), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "  " + strings.Join(lines, "\n  ")
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
// Users maps base image keys to the clusters whose disks are overlays of them.
func Users() (map[string][]string, error) {
	names, err := state.List()
	if err != nil {
		return nil, err
	}
	users := map[string][]string{}
	for _, name := range names {
		st, err := state.ForCluster(name).Load()
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, n := range st.Nodes {
			if n.Base != "" && !seen[n.Base] {
				seen[n.Base] = true
				users[n.Base] = append(users[n.Base], name)
			}
		}
	}
	return users, nil
}

func Remove(key string) error {
	_ = os.Remove(filepath.Join(imagesDir(), key+".lock"))
	return os.RemoveAll(filepath.Join(imagesDir(), key))
}
