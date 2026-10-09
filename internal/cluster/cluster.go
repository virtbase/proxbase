// Package cluster orchestrates Proxmox VE clusters: install, boot, configure and
// tear down. Every step checks the current state first, so create can be re-run.
//
// The parts are pluggable: nodes run on a vm.Runtime (QEMU), internal networks
// come from network.Switch, and data storage from storage backends (ZFS, Ceph).
package cluster

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/virtbase/proxbase/internal/answer"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/network"
	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
	"github.com/virtbase/proxbase/internal/storage"
	"github.com/virtbase/proxbase/internal/storage/ceph"
	"github.com/virtbase/proxbase/internal/storage/zfs"
	"github.com/virtbase/proxbase/internal/vm"
)

const (
	secretPassword = "root-password"
	secretKey      = "id_ed25519"
	secretToken    = "api-token"
	secretCA       = "pve-root-ca.pem"

	// TokenUser owns the API token proxbase creates.
	TokenUser = "proxbase@pve"
)

// Logf reports progress.
type Logf func(format string, a ...any)

// Cluster is one cluster with its configuration, state and secrets.
type Cluster struct {
	Dir state.Dir
	Cfg *config.Cluster
	St  *state.State
	Log Logf

	start    time.Time
	signer   ssh.Signer
	password string
	runtime  vm.Runtime
	net      network.Switch
}

func newCluster(d state.Dir, cfg *config.Cluster, st *state.State, logf Logf) (*Cluster, error) {
	c := &Cluster{
		Dir: d, Cfg: cfg, St: st, Log: logf,
		start:   time.Now(),
		runtime: qemu.Runtime{},
		net:     network.Switch{Dir: d, Cluster: cfg.Name},
	}
	return c, c.loadSecrets()
}

// Open loads an existing cluster.
func Open(name string, logf Logf) (*Cluster, error) {
	d := state.ForCluster(name)
	st, err := d.Load()
	if err != nil {
		return nil, err
	}
	cfg, err := d.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", d.Config(), err)
	}
	return newCluster(d, cfg, st, logf)
}

// initialize creates the directory, secrets and state for a new cluster.
func initialize(cfg *config.Cluster, logf Logf) (*Cluster, error) {
	d := state.ForCluster(cfg.Name)
	if err := d.Init(); err != nil {
		return nil, err
	}
	if err := d.SaveConfig(cfg); err != nil {
		return nil, err
	}
	if err := d.WriteSecret(secretPassword, []byte(answer.Password(24))); err != nil {
		return nil, err
	}
	if err := writeKeyPair(d, "proxbase@"+cfg.Name); err != nil {
		return nil, err
	}
	st := &state.State{Name: cfg.Name, Phase: state.PhaseCreating, CreatedAt: time.Now().UTC()}
	for _, n := range cfg.NodeList() {
		ui, sshPort := cfg.Ports(n.Index)
		st.Nodes = append(st.Nodes, &state.NodeState{Name: n.Name, Index: n.Index, UIPort: ui, SSHPort: sshPort})
	}
	if err := d.Save(st); err != nil {
		return nil, err
	}
	return newCluster(d, cfg, st, logf)
}

// writeKeyPair generates the ed25519 key nodes trust for root logins.
func writeKeyPair(d state.Dir, comment string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return err
	}
	if err := d.WriteSecret(secretKey, pem.EncodeToMemory(block)); err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment + "\n"
	return d.WriteSecret(secretKey+".pub", []byte(authorized))
}

func (c *Cluster) loadSecrets() error {
	pw, err := c.Dir.ReadSecret(secretPassword)
	if err != nil {
		return err
	}
	c.password = pw
	key, err := os.ReadFile(c.Dir.Secret(secretKey))
	if err != nil {
		return err
	}
	c.signer, err = ssh.ParsePrivateKey(key)
	return err
}

// Password is the root password of every node.
func (c *Cluster) Password() string { return c.password }

// KeyPath is the private key for root SSH logins.
func (c *Cluster) KeyPath() string { return c.Dir.Secret(secretKey) }

// CAPath is the exported cluster CA certificate.
func (c *Cluster) CAPath() string { return c.Dir.Secret(secretCA) }

// Token is the API token as "user@realm!id=secret".
func (c *Cluster) Token() (string, error) {
	s, err := c.Dir.ReadSecret(secretToken)
	return strings.TrimSpace(s), err
}

// ConsoleSocket is the serial console socket of a node.
func (c *Cluster) ConsoleSocket(name string) (string, error) {
	for _, n := range c.Cfg.NodeList() {
		if n.Name == name {
			return qemu.ConsoleSocket(c.spec(n)), nil
		}
	}
	return "", fmt.Errorf("cluster %s has no node %q", c.Cfg.Name, name)
}

func (c *Cluster) step(format string, a ...any) {
	c.Log("[%5.0fs] %s", time.Since(c.start).Seconds(), fmt.Sprintf(format, a...))
}

func (c *Cluster) save() error { return c.Dir.Save(c.St) }

// storages returns the configured storage backends in setup order.
func (c *Cluster) storages() []storage.Backend {
	var out []storage.Backend
	if b := zfs.New(c.Cfg); b != nil {
		out = append(out, b)
	}
	if b := ceph.New(c.Cfg); b != nil {
		out = append(out, b)
	}
	return out
}
