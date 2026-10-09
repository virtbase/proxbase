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

// QMP is a minimal QEMU Machine Protocol client.
type QMP struct {
	conn net.Conn
	r    *bufio.Reader
}

func DialQMP(path string) (*QMP, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	q := &QMP{conn: conn, r: bufio.NewReader(conn)}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := q.r.ReadBytes('\n'); err != nil { // greeting
		conn.Close()
		return nil, err
	}
	if _, err := q.Execute("qmp_capabilities"); err != nil {
		conn.Close()
		return nil, err
	}
	return q, nil
}

func (q *QMP) Close() error { return q.conn.Close() }

func (q *QMP) Execute(cmd string) (json.RawMessage, error) {
	_ = q.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(q.conn).Encode(map[string]string{"execute": cmd}); err != nil {
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

func qmpCommand(path, cmd string) error {
	q, err := DialQMP(path)
	if err != nil {
		return err
	}
	defer q.Close()
	_, err = q.Execute(cmd)
	return err
}

// Stop shuts a node down: ACPI powerdown, then QMP quit after timeout, then SIGKILL.
func Stop(ctx context.Context, qmpPath, pidfile string, timeout time.Duration) error {
	pid, ok := Running(pidfile)
	if !ok {
		return nil
	}
	if timeout > 0 && qmpCommand(qmpPath, "system_powerdown") == nil && waitExit(ctx, pid, timeout) {
		return nil
	}
	_ = qmpCommand(qmpPath, "quit")
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
