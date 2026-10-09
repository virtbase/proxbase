package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/vm"
)

// Start boots an installed node as a daemon.
func (Runtime) Start(ctx context.Context, s vm.Spec) error {
	if _, ok := running(pidfile(s)); ok {
		return nil
	}
	cleanSockets(s)
	out, err := exec.CommandContext(ctx, Binary, args(s, nil)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("start %s: %w: %s", s.Process, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Running reports whether the node's QEMU process is alive.
func (Runtime) Running(s vm.Spec) bool {
	_, ok := running(pidfile(s))
	return ok
}

// Stop shuts a node down: ACPI powerdown, QMP quit after timeout, then SIGKILL.
func (Runtime) Stop(ctx context.Context, s vm.Spec, timeout time.Duration) error {
	if err := stop(ctx, qmpSocket(s), pidfile(s), timeout); err != nil {
		return err
	}
	cleanSockets(s)
	return nil
}

// cleanSockets removes stale sockets of a stopped node (QEMU refuses to bind them).
func cleanSockets(s vm.Spec) {
	for _, n := range s.NICs {
		_ = os.Remove(n.Local)
	}
	for _, p := range []string{qmpSocket(s), console(s), serialSock(s)} {
		_ = os.Remove(p)
	}
}

// running reports whether the process in pidfile is alive and really ours.
func running(pidfile string) (int, bool) {
	b, err := os.ReadFile(pidfile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || !strings.Contains(string(cmdline), pidfile) {
		return 0, false
	}
	return pid, true
}
