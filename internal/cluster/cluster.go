// Package cluster orchestrates Proxmox VE clusters: install, boot, configure and
// tear down. Every step checks the current state first, so create can be re-run.
package cluster

import (
	"context"
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
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/qemu"
	"github.com/virtbase/proxbase/internal/state"
)

const (
	secretPassword = "root-password"
	secretKey      = "id_ed25519"
	secretToken    = "api-token"
	secretCA       = "pve-root-ca.pem"

	TokenUser = "proxbase@pve"
	TokenID   = TokenUser + "!api"
)

type Logf func(format string, a ...any)

type Cluster struct {
	Dir   state.Dir
	Cfg   *config.Cluster
	St    *state.State
	Log   Logf
	start time.Time

	signer   ssh.Signer
	password string
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
	c := &Cluster{Dir: d, Cfg: cfg, St: st, Log: logf, start: time.Now()}
	return c, c.loadSecrets()
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
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "proxbase@"+cfg.Name)
	if err != nil {
		return nil, err
	}
	if err := d.WriteSecret(secretKey, pem.EncodeToMemory(block)); err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " proxbase@" + cfg.Name + "\n"
	if err := d.WriteSecret(secretKey+".pub", []byte(authorized)); err != nil {
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
	c := &Cluster{Dir: d, Cfg: cfg, St: st, Log: logf, start: time.Now()}
	return c, c.loadSecrets()
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

func (c *Cluster) Password() string { return c.password }
func (c *Cluster) KeyPath() string  { return c.Dir.Secret(secretKey) }
func (c *Cluster) CAPath() string   { return c.Dir.Secret(secretCA) }
func (c *Cluster) Token() (string, error) {
	s, err := c.Dir.ReadSecret(secretToken)
	return strings.TrimSpace(s), err
}

func (c *Cluster) step(format string, a ...any) {
	c.Log("[%5.0fs] %s", time.Since(c.start).Seconds(), fmt.Sprintf(format, a...))
}

func (c *Cluster) save() error { return c.Dir.Save(c.St) }

func (c *Cluster) rootDisk(n config.Node) string { return c.Dir.Disk(n.Name + "-root.qcow2") }
func (c *Cluster) dataDisk(n config.Node, i int) string {
	return c.Dir.Disk(fmt.Sprintf("%s-data%d.qcow2", n.Name, i))
}
func (c *Cluster) nodeSock(n config.Node, net string) string {
	return c.Dir.Run(n.Name + "-" + net + ".sock")
}
func (c *Cluster) switchSock(net string) string { return c.Dir.Run("sw-" + net + ".sock") }
func (c *Cluster) pidfile(n config.Node) string { return c.Dir.Run(n.Name + ".pid") }
func (c *Cluster) qmp(n config.Node) string     { return c.Dir.Run(n.Name + ".qmp") }

func (c *Cluster) machine(n config.Node) *qemu.Machine {
	mem, _ := config.MemoryMiB(n.Spec.Memory)
	ns := c.St.Node(n.Name)
	m := &qemu.Machine{
		Name:       c.Cfg.Name + "-" + n.Name,
		CPUs:       n.Spec.CPUs,
		MemoryMiB:  mem,
		Nested:     *n.Spec.Nested,
		RootDisk:   c.rootDisk(n),
		NATMAC:     qemu.MAC(n.Index, 0),
		Bind:       c.Cfg.Access.BindAddress,
		UIPort:     ns.UIPort,
		SSHPort:    ns.SSHPort,
		QMP:        c.qmp(n),
		PIDFile:    c.pidfile(n),
		Console:    c.Dir.Run(n.Name + "-console.sock"),
		ConsoleLog: c.Dir.Log(n.Name + "-console.log"),
	}
	for i := range n.Spec.DataDisks {
		m.DataDisks = append(m.DataDisks, c.dataDisk(n, i))
	}
	for i, net := range c.Cfg.Networks {
		m.NICs = append(m.NICs, qemu.NIC{MAC: qemu.MAC(n.Index, i+1), Local: c.nodeSock(n, net.Name), Switch: c.switchSock(net.Name)})
	}
	return m
}

// ssh connects to a node, trusting its host key on first use.
func (c *Cluster) ssh(ctx context.Context, name string) (*pve.SSH, error) {
	ns := c.St.Node(name)
	s := &pve.SSH{Port: ns.SSHPort, Signer: c.signer, HostKey: ns.HostKey}
	seen, err := s.Dial(ctx)
	if err != nil {
		return nil, err
	}
	if ns.HostKey == "" {
		ns.HostKey = seen
	}
	return s, nil
}

// waitSSH waits until a node accepts SSH logins.
func (c *Cluster) waitSSH(ctx context.Context, name string, timeout time.Duration) (*pve.SSH, error) {
	deadline := time.Now().Add(timeout)
	for {
		s, err := c.ssh(ctx, name)
		if err == nil {
			return s, nil
		}
		if strings.Contains(err.Error(), "host key") || time.Now().After(deadline) {
			return nil, fmt.Errorf("%s: SSH not reachable: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// api returns a root@pam API client for a node, pinned to its current certificate.
func (c *Cluster) api(ctx context.Context, name string) (*pve.Client, error) {
	s, err := c.waitSSH(ctx, name, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	certPEM, err := s.Run("cat /etc/pve/local/pve-ssl.pem")
	if err != nil {
		return nil, fmt.Errorf("%s: read certificate: %w", name, err)
	}
	fp, err := pve.Fingerprint(certPEM)
	if err != nil {
		return nil, err
	}
	cl := pve.NewClient(c.St.Node(name).UIPort, fp)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		err = cl.Login(ctx, "root@pam", c.password)
		if err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s: API login: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Cluster) nodeIP(n config.Node, net config.Network) string {
	ip, _, _ := config.NodeIP(net.CIDR, n.Index)
	return ip.String()
}

func (c *Cluster) corosyncIP(n config.Node) string { return c.nodeIP(n, c.Cfg.CorosyncNetwork()) }
