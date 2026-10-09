// Package vm describes node virtual machines independently of the hypervisor and
// defines what a node runtime must provide. QEMU (package qemu) implements it.
package vm

import (
	"context"
	"fmt"
	"time"
)

// Disk is a disk image and its size for creation (e.g. "32G").
type Disk struct {
	Path string
	Size string
}

// NIC connects the node to an internal network through a unix datagram socket pair.
type NIC struct {
	MAC    string
	Local  string // the node's end
	Remote string // the network's end (switch port)
}

// Spec is everything a runtime needs to run one node.
type Spec struct {
	Name      string // node name; also the prefix of its files in RunDir and LogDir
	Process   string // process/guest name, unique on the host (e.g. lab-pve1)
	CPUs      int
	MemoryMiB int
	Nested    bool
	RootDisk  Disk
	DataDisks []Disk
	NATMAC    string // NIC on the NAT uplink (vmbr0)
	Bind      string // host address of the web UI and SSH forwards
	UIPort    int
	SSHPort   int
	NICs      []NIC
	RunDir    string // pidfile, control and console sockets
	LogDir    string // console, installer and runtime logs
}

// Disks returns the root disk followed by the data disks.
func (s Spec) Disks() []Disk { return append([]Disk{s.RootDisk}, s.DataDisks...) }

// Media is what the unattended installer boots from.
type Media struct {
	ISO, Kernel, Initrd, Cmdline string
	AnswerDir                    string // directory with answer.toml
}

// MAC returns the MAC address of NIC nic (0 = NAT uplink) on node number node.
func MAC(node, nic int) string { return fmt.Sprintf("52:54:00:50:%02x:%02x", node, nic) }

// Installer creates a node's disks and installs Proxmox VE onto them.
type Installer interface {
	Install(ctx context.Context, s Spec, m Media) error
}

// Cloner creates a node's disks from an installed base image instead of installing.
type Cloner interface {
	Clone(ctx context.Context, s Spec, base string) error
}

// Runner starts and stops installed nodes.
type Runner interface {
	Start(ctx context.Context, s Spec) error
	// Stop shuts the node down cleanly, powering it off hard after timeout (0: at once).
	Stop(ctx context.Context, s Spec, timeout time.Duration) error
	Running(s Spec) bool
}

// SnapshotOp is an operation on disk snapshots.
type SnapshotOp int

const (
	SnapshotSave SnapshotOp = iota
	SnapshotRestore
	SnapshotDelete
)

// Snapshotter manages snapshots of the disks of stopped nodes.
type Snapshotter interface {
	Snapshot(ctx context.Context, op SnapshotOp, name string, disks []Disk) error
}

// Runtime is a complete node runtime.
type Runtime interface {
	Installer
	Runner
	Snapshotter
}

// FaultInjector simulates failures of running nodes.
type FaultInjector interface {
	Kill(s Spec) error                                           // power loss
	Freeze(ctx context.Context, s Spec) error                    // stop all vCPUs
	Thaw(ctx context.Context, s Spec) error                      // resume after Freeze
	SetLink(ctx context.Context, s Spec, nic int, up bool) error // carrier of internal NIC nic (0-based)
}
