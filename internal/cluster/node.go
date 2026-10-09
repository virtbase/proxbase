package cluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/golden"
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/remote"
	"github.com/virtbase/proxbase/internal/retry"
	"github.com/virtbase/proxbase/internal/storage"
	"github.com/virtbase/proxbase/internal/vm"
)

// spec describes node n for the runtime.
func (c *Cluster) spec(n config.Node) vm.Spec {
	mem, _ := config.MemoryMiB(n.Spec.Memory)
	ns := c.St.Node(n.Name)
	s := vm.Spec{
		Name:      n.Name,
		Process:   c.Cfg.Name + "-" + n.Name,
		CPUs:      n.Spec.CPUs,
		MemoryMiB: mem,
		Nested:    *n.Spec.Nested,
		RootDisk:  vm.Disk{Path: c.Dir.Disk(n.Name + "-root.qcow2"), Size: n.Spec.RootDisk.Size},
		NATMAC:    vm.MAC(n.Index, 0),
		Bind:      c.Cfg.Access.BindAddress,
		NICs:      c.net.NICs(c.Cfg, n),
		RunDir:    c.Dir.Path("run"),
		LogDir:    c.Dir.Path("logs"),
	}
	if ns != nil {
		s.UIPort, s.SSHPort = ns.UIPort, ns.SSHPort
	}
	for i, d := range n.Spec.DataDisks {
		s.DataDisks = append(s.DataDisks, vm.Disk{Path: c.Dir.Disk(fmt.Sprintf("%s-data%d.qcow2", n.Name, i)), Size: d.Size})
	}
	return s
}

func (c *Cluster) running(n config.Node) bool { return c.runtime.Running(c.spec(n)) }

func nodeIP(n config.Node, net config.Network) string {
	ip, _, _ := config.NodeIP(net.CIDR, n.Index)
	return ip.String()
}

func (c *Cluster) corosyncIP(n config.Node) string { return nodeIP(n, c.Cfg.CorosyncNetwork()) }

// dial connects with a given key; an empty hostKey accepts any host key.
func (c *Cluster) dial(ctx context.Context, name string, signer ssh.Signer, hostKey string) (*remote.SSH, error) {
	s := &remote.SSH{Port: c.St.Node(name).SSHPort, Signer: signer, HostKey: hostKey}
	if _, err := s.Dial(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// ssh connects to a node, trusting its host key on first use. Nodes cloned from
// a base image are reached with the base image's key until personalized.
func (c *Cluster) ssh(ctx context.Context, name string) (*remote.SSH, error) {
	ns := c.St.Node(name)
	if ns.Pending {
		base, err := golden.Open(ns.Base)
		if err != nil {
			return nil, retry.Permanent(err)
		}
		signer, err := base.Signer()
		if err != nil {
			return nil, retry.Permanent(err)
		}
		return c.dial(ctx, name, signer, "")
	}
	s := &remote.SSH{Port: ns.SSHPort, Signer: c.signer, HostKey: ns.HostKey}
	seen, err := s.Dial(ctx)
	if err != nil {
		return nil, err
	}
	if ns.HostKey == "" {
		ns.HostKey = seen
	}
	return s, nil
}

// waitSSH waits until a node accepts SSH logins; a changed host key ends the wait.
func (c *Cluster) waitSSH(ctx context.Context, name string, timeout time.Duration) (*remote.SSH, error) {
	var s *remote.SSH
	err := retry.Do(ctx, timeout, 2*time.Second, func() error {
		var err error
		s, err = c.ssh(ctx, name)
		if err != nil && strings.Contains(err.Error(), "host key") {
			return retry.Permanent(err)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("%s: SSH not reachable: %w", name, err)
	}
	return s, nil
}

// api returns a root@pam API client for a node, pinned to its current certificate.
// The certificate is read again on every attempt: after a join, pveproxy switches
// to the new cluster-signed certificate with a delay.
func (c *Cluster) api(ctx context.Context, name string) (*pve.Client, error) {
	s, err := c.waitSSH(ctx, name, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var cl *pve.Client
	err = retry.Do(ctx, 3*time.Minute, 2*time.Second, func() error {
		certPEM, err := s.Run("cat /etc/pve/local/pve-ssl.pem")
		if err != nil {
			return fmt.Errorf("read certificate: %w", err)
		}
		fp, err := pve.Fingerprint(certPEM)
		if err != nil {
			return err
		}
		cl = pve.NewClient(c.St.Node(name).UIPort, fp)
		return cl.Login(ctx, "root@pam", c.password)
	})
	if err != nil {
		return nil, fmt.Errorf("%s: API login: %w", name, err)
	}
	return cl, nil
}

// tokenClient returns an API client using the token, verified against the cluster
// CA, or nil if the cluster has no token yet.
func (c *Cluster) tokenClient(port int) *pve.Client {
	tok, err := c.Token()
	if err != nil || tok == "" {
		return nil
	}
	caPEM, err := os.ReadFile(c.CAPath())
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil
	}
	cl := pve.NewClientCA(port, pool)
	cl.SetToken(tok)
	return cl
}

// host gives storage backends access to the cluster.
type host struct{ c *Cluster }

var _ storage.Host = host{}

func (h host) Nodes() []config.Node { return h.c.Cfg.NodeList() }

func (h host) API(ctx context.Context, node string) (*pve.Client, error) { return h.c.api(ctx, node) }

func (h host) SSH(ctx context.Context, node string) (*remote.SSH, error) {
	return h.c.waitSSH(ctx, node, time.Minute)
}

func (h host) Step(format string, a ...any) { h.c.step(format, a...) }
