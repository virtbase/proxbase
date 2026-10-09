// Package serial drives the Proxmox installer over the QEMU serial socket: on a
// stock ISO the auto-installer stops in a debug shell, where we fetch the answer
// file from the PROXMOX-AIS partition and exit to start the installation.
package serial

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const (
	shellHint  = "and enter 'exit'"
	fetchCmd   = "proxmox-fetch-answer partition PROXMOX-AIS >/run/automatic-installer-answers && exit\n"
	failMarker = "Installation failed"
)

// ErrTimeout is returned when the install did not finish in time.
var ErrTimeout = errors.New("install timed out")

// Drive connects to sock, logs all output to logPath and answers the debug shell.
// It returns nil when the VM closes the console (power-off after install).
func Drive(ctx context.Context, sock, logPath string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var conn net.Conn
	for {
		var err error
		if conn, err = net.Dial("unix", sock); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("connect serial console %s: %w", sock, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()

	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()

	var tail []byte
	buf := make([]byte, 64<<10)
	sent, finished := false, false
	var sawHint time.Time
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := conn.Read(buf)
		if n > 0 {
			_, _ = log.Write(buf[:n])
			tail = append(tail, buf[:n]...)
			if len(tail) > 16<<10 {
				tail = tail[len(tail)-16<<10:]
			}
		}
		if !sent && sawHint.IsZero() && bytes.Contains(tail, []byte(shellHint)) {
			sawHint = time.Now()
		}
		// Send once bash printed its prompt (or 5s after the hint as a fallback).
		if !sent && !sawHint.IsZero() && (bytes.HasSuffix(bytes.TrimRight(tail, " "), []byte("#")) || time.Since(sawHint) > 5*time.Second) {
			if _, err := conn.Write([]byte(fetchCmd)); err != nil {
				return err
			}
			fmt.Fprintf(log, "\n[proxbase] sent: %s", fetchCmd)
			sent = true
		}
		finished = finished || bytes.Contains(tail, []byte("Installation finished"))
		if bytes.Contains(tail, []byte(failMarker)) {
			time.Sleep(time.Second)
			return fmt.Errorf("installer reported failure, see %s:\n%s", logPath, lastLines(tail, 15))
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && ctx.Err() == nil {
				continue
			}
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return fmt.Errorf("%w after %s, see %s:\n%s", ErrTimeout, timeout, logPath, lastLines(tail, 15))
				}
				return ctx.Err()
			}
			// EOF: QEMU exited.
			if !finished {
				return fmt.Errorf("VM exited before the installation finished, see %s:\n%s", logPath, lastLines(tail, 15))
			}
			return nil
		}
	}
}

func lastLines(b []byte, n int) string {
	s := strings.ReplaceAll(string(b), "\r", "")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "  " + strings.Join(lines, "\n  ")
}
