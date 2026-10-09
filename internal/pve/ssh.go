package pve

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSH runs commands as root on a node through its forwarded port.
type SSH struct {
	Port    int
	Signer  ssh.Signer
	HostKey string // base64 wire format; empty = trust on first use
	client  *ssh.Client
}

// Dial connects and returns the host key seen (for trust on first use).
func (s *SSH) Dial(ctx context.Context) (string, error) {
	var seen string
	cfg := &ssh.ClientConfig{
		User:    "root",
		Auth:    []ssh.AuthMethod{ssh.PublicKeys(s.Signer)},
		Timeout: 10 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = base64.StdEncoding.EncodeToString(key.Marshal())
			if s.HostKey != "" && seen != s.HostKey {
				return fmt.Errorf("SSH host key of 127.0.0.1:%d changed", s.Port)
			}
			return nil
		},
	}
	addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return "", err
	}
	s.client = ssh.NewClient(c, chans, reqs)
	return seen, nil
}

func (s *SSH) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Run executes a shell script (passed on stdin to bash) and returns stdout.
func (s *SSH) Run(script string) (string, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdin = strings.NewReader(script)
	sess.Stdout, sess.Stderr = &stdout, &stderr
	if err := sess.Run("bash -euo pipefail -s"); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
