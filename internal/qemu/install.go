package qemu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/virtbase/proxbase/internal/serial"
	"github.com/virtbase/proxbase/internal/vm"
)

const installTimeout = 20 * time.Minute

var _ vm.Cloner = Runtime{}

// Clone creates the root disk as a qcow2 overlay of base and fresh data disks.
func (Runtime) Clone(ctx context.Context, s vm.Spec, base string) error {
	if _, ok := running(pidfile(s)); ok {
		return fmt.Errorf("a VM is already running (pidfile %s)", pidfile(s))
	}
	for _, d := range s.DataDisks {
		if err := createDisk(ctx, d); err != nil {
			return err
		}
	}
	return createOverlay(ctx, s.RootDisk, base)
}

// Install creates fresh disks and runs the unattended installer in the foreground,
// answering its debug shell over the serial console. QEMU powers off when done.
func (Runtime) Install(ctx context.Context, s vm.Spec, m vm.Media) error {
	if _, ok := running(pidfile(s)); ok {
		return fmt.Errorf("a VM is already running (pidfile %s)", pidfile(s))
	}
	for _, d := range s.Disks() {
		if err := createDisk(ctx, d); err != nil {
			return err
		}
	}
	cleanSockets(s)
	qlog, err := os.Create(qemuLog(s))
	if err != nil {
		return err
	}
	defer qlog.Close()
	cmd := exec.Command(Binary, args(s, &m)...)
	cmd.SysProcAttr = installProcAttr()
	cmd.Stdout, cmd.Stderr = qlog, qlog
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	if err := serial.Drive(ctx, serialSock(s), installLog(s), installTimeout); err != nil {
		_ = cmd.Process.Kill()
		<-exited
		if b, _ := os.ReadFile(qemuLog(s)); len(b) > 0 {
			return fmt.Errorf("%w\nQEMU: %s", err, strings.TrimSpace(string(b)))
		}
		return err
	}
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("QEMU exited with %w", err)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
		return errors.New("QEMU did not exit after the installation")
	}
	_ = os.Remove(serialSock(s))
	return nil
}
