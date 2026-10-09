package qemu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// qmpConn is a minimal QEMU Machine Protocol client.
type qmpConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialQMP(path string) (*qmpConn, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	q := &qmpConn{conn: conn, r: bufio.NewReader(conn)}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := q.r.ReadBytes('\n'); err != nil { // greeting
		conn.Close()
		return nil, err
	}
	if _, err := q.Execute("qmp_capabilities", nil); err != nil {
		conn.Close()
		return nil, err
	}
	return q, nil
}

func (q *qmpConn) Close() error { return q.conn.Close() }

// Execute runs a command with optional arguments and returns its result.
func (q *qmpConn) Execute(cmd string, args any) (json.RawMessage, error) {
	_ = q.conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := map[string]any{"execute": cmd}
	if args != nil {
		req["arguments"] = args
	}
	if err := json.NewEncoder(q.conn).Encode(req); err != nil {
		return nil, err
	}
	for {
		line, err := q.r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var msg struct {
			Return json.RawMessage        `json:"return"`
			Error  *struct{ Desc string } `json:"error"`
			Event  string                 `json:"event"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, err
		}
		if msg.Event != "" {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("qmp %s: %s", cmd, msg.Error.Desc)
		}
		return msg.Return, nil
	}
}

// qmpExec connects, runs one command and disconnects.
func qmpExec(path, cmd string, args any) error {
	q, err := dialQMP(path)
	if err != nil {
		return err
	}
	defer q.Close()
	_, err = q.Execute(cmd, args)
	return err
}

func stop(ctx context.Context, qmpPath, pidfile string, timeout time.Duration) error {
	pid, ok := running(pidfile)
	if !ok {
		return nil
	}
	if timeout > 0 && qmpExec(qmpPath, "system_powerdown", nil) == nil && waitExit(ctx, pid, timeout) {
		return nil
	}
	_ = qmpExec(qmpPath, "quit", nil)
	if waitExit(ctx, pid, 10*time.Second) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	if !waitExit(ctx, pid, 5*time.Second) {
		return fmt.Errorf("process %d did not exit", pid)
	}
	_ = os.Remove(pidfile)
	return nil
}

func waitExit(ctx context.Context, pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
	return syscall.Kill(pid, 0) == syscall.ESRCH
}
