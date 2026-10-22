// Package remote runs commands on nodes over SSH.
package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// HandshakeTimeout bounds the SSH handshake of one Dial.
const HandshakeTimeout = 3 * time.Second

// SSH runs commands as root on a node through its forwarded port on 127.0.0.1.
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
	// QEMU's port forward accepts at once even while the guest is still booting,
	// then retries its own connection to the guest only every 12s. Without a
	// deadline the handshake waits for that retry instead of failing fast so
	// the caller can dial again.
	deadline := time.Now().Add(HandshakeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return "", err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return "", err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		c.Close()
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

// ExecResult is the outcome of a command; ExitCode is -1 if it did not exit normally.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// Exec runs a command line through the login shell. A non-zero exit is not an
// error; a cancelled ctx kills the command.
func (s *SSH) Exec(ctx context.Context, command string) (ExecResult, error) {
	var stdout, stderr bytes.Buffer
	code, err := s.run(ctx, command, nil, &stdout, &stderr)
	return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}, err
}

// Stream runs a command line with stdin read from in (if not nil) and stdout
// written to out. A non-zero exit is an error with the command's stderr.
func (s *SSH) Stream(ctx context.Context, command string, in io.Reader, out io.Writer) error {
	var stderr bytes.Buffer
	code, err := s.run(ctx, command, in, out, &stderr)
	if err == nil && code != 0 {
		err = fmt.Errorf("exit status %d: %s", code, strings.TrimSpace(stderr.String()))
	}
	return err
}

// run runs command and returns its exit code; -1 if it did not exit normally.
func (s *SSH) run(ctx context.Context, command string, in io.Reader, stdout, stderr io.Writer) (int, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return -1, fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close()
	sess.Stdin, sess.Stdout, sess.Stderr = in, stdout, stderr
	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		// Best effort: closing the session ends Run even if the signal is not supported.
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		<-done //nolint:errcheck // killed, the error is superseded by ctx.Err()
		return -1, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	if exit, ok := errors.AsType[*ssh.ExitError](err); ok {
		return exit.ExitStatus(), nil
	}
	return -1, fmt.Errorf("run command: %w", err)
}

// Quote quotes s as one word for a POSIX shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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
