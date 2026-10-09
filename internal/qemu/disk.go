package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/virtbase/proxbase/internal/vm"
)

// createDisk replaces d with an empty qcow2 image.
func createDisk(ctx context.Context, d vm.Disk) error {
	_ = os.Remove(d.Path)
	out, err := exec.CommandContext(ctx, "qemu-img", "create", "-q", "-f", "qcow2", d.Path, d.Size).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img create %s: %w: %s", d.Path, err, out)
	}
	return nil
}

// createOverlay replaces d with a qcow2 overlay backed by base.
func createOverlay(ctx context.Context, d vm.Disk, base string) error {
	_ = os.Remove(d.Path)
	out, err := exec.CommandContext(ctx, "qemu-img", "create", "-q", "-f", "qcow2", "-F", "qcow2", "-b", base, d.Path, d.Size).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img create %s on %s: %w: %s", d.Path, base, err, out)
	}
	return nil
}

// Snapshot saves, restores or deletes an internal qcow2 snapshot on every disk.
// A failed save removes the snapshot from the disks already done.
func (Runtime) Snapshot(ctx context.Context, op vm.SnapshotOp, name string, disks []vm.Disk) error {
	flag := map[vm.SnapshotOp]string{vm.SnapshotSave: "-c", vm.SnapshotRestore: "-a", vm.SnapshotDelete: "-d"}[op]
	var done []vm.Disk
	for _, d := range disks {
		err := qemuSnapshot(ctx, flag, name, d.Path)
		if op == vm.SnapshotDelete && err != nil && strings.Contains(err.Error(), "Can't find") {
			err = nil
		}
		if err != nil {
			if op == vm.SnapshotSave {
				for _, u := range done {
					_ = qemuSnapshot(ctx, "-d", name, u.Path)
				}
			}
			return err
		}
		done = append(done, d)
	}
	return nil
}

func qemuSnapshot(ctx context.Context, flag, name, disk string) error {
	out, err := exec.CommandContext(ctx, "qemu-img", "snapshot", flag, name, disk).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img snapshot %s %s %s: %w: %s", flag, name, disk, err, strings.TrimSpace(string(out)))
	}
	return nil
}
