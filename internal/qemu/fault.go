package qemu

import (
	"context"
	"fmt"
	"syscall"

	"github.com/virtbase/proxbase/internal/vm"
)

var _ vm.FaultInjector = Runtime{}

// Kill ends the node's QEMU process at once, like pulling the power cord.
func (Runtime) Kill(s vm.Spec) error {
	pid, ok := running(pidfile(s))
	if !ok {
		return fmt.Errorf("%s is not running", s.Name)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// Freeze stops all vCPUs; the node hangs but keeps its memory.
func (r Runtime) Freeze(ctx context.Context, s vm.Spec) error { return r.Pause(ctx, s) }

// Thaw resumes a frozen node.
func (r Runtime) Thaw(ctx context.Context, s vm.Spec) error { return r.Resume(ctx, s) }

// SetLink sets the carrier of internal NIC nic, as if its cable was pulled.
func (Runtime) SetLink(_ context.Context, s vm.Spec, nic int, up bool) error {
	if nic < 0 || nic >= len(s.NICs) {
		return fmt.Errorf("%s has no internal NIC %d", s.Name, nic)
	}
	return qmpExec(qmpSocket(s), "set_link", map[string]any{"name": fmt.Sprintf("net%d", nic), "up": up})
}
