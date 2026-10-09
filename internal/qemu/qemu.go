// Package qemu runs Proxmox VE nodes as QEMU/KVM processes. Runtime implements
// vm.Runtime: installs through the serial console, daemonized runs controlled
// via pidfile and QMP, and qcow2 disks with internal snapshots.
package qemu

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/virtbase/proxbase/internal/vm"
)

const Binary = "qemu-system-x86_64"

// Fixed PCI slots, identical for install and run so NIC names never drift.
const (
	slotVGA     = 0x01
	slotRoot    = 0x02
	slotUSB     = 0x03
	slotNAT     = 0x04
	slotNICBase = 0x05 // up to 8 segment NICs: 0x05..0x0c
	slotRNG     = 0x0e
	slotData    = 0x10 // up to 8 data disks: 0x10..0x17
)

// Runtime is the QEMU node runtime.
type Runtime struct{}

var _ vm.Runtime = Runtime{}

// Files of a node, derived from its name.
func pidfile(s vm.Spec) string    { return filepath.Join(s.RunDir, s.Name+".pid") }
func qmpSocket(s vm.Spec) string  { return filepath.Join(s.RunDir, s.Name+".qmp") }
func console(s vm.Spec) string    { return filepath.Join(s.RunDir, s.Name+"-console.sock") }
func serialSock(s vm.Spec) string { return filepath.Join(s.RunDir, s.Name+"-serial.sock") }
func consoleLog(s vm.Spec) string { return filepath.Join(s.LogDir, s.Name+"-console.log") }
func installLog(s vm.Spec) string { return filepath.Join(s.LogDir, s.Name+"-install.log") }
func qemuLog(s vm.Spec) string    { return filepath.Join(s.LogDir, s.Name+"-qemu.log") }

// ConsoleSocket is the serial console of a running node (proxbase console).
func ConsoleSocket(s vm.Spec) string { return console(s) }

func dev(format string, a ...any) []string { return []string{"-device", fmt.Sprintf(format, a...)} }

// args returns the command line; media != nil boots the installer.
func args(s vm.Spec, media *vm.Media) []string {
	cpu := "host"
	if !s.Nested {
		cpu += ",vmx=off,svm=off"
	}
	a := []string{
		"-name", "guest=" + s.Process + ",process=" + s.Process,
		"-nodefaults", "-no-user-config",
		"-machine", "q35,accel=kvm",
		"-cpu", cpu,
		"-smp", strconv.Itoa(s.CPUs),
		"-m", strconv.Itoa(s.MemoryMiB),
		"-display", "none",
	}
	a = append(a, dev("VGA,bus=pcie.0,addr=%#x", slotVGA)...)
	a = append(a, "-drive", "if=none,id=root,file="+s.RootDisk.Path+",format=qcow2,cache=unsafe,discard=unmap")
	a = append(a, dev("virtio-blk-pci,drive=root,bus=pcie.0,addr=%#x,bootindex=1", slotRoot)...)
	a = append(a, dev("qemu-xhci,id=usb,bus=pcie.0,addr=%#x", slotUSB)...)
	fwd := fmt.Sprintf("hostfwd=tcp:%[1]s:%[2]d-10.0.2.15:22,hostfwd=tcp:%[1]s:%[3]d-10.0.2.15:8006", s.Bind, s.SSHPort, s.UIPort)
	// Nested guests bridged to vmbr0 get DHCP leases from .100 on; the node is .15.
	a = append(a, "-netdev", "user,id=nat,dhcpstart=10.0.2.100,"+fwd)
	a = append(a, dev("virtio-net-pci,netdev=nat,mac=%s,bus=pcie.0,addr=%#x", s.NATMAC, slotNAT)...)
	for i, n := range s.NICs {
		a = append(a, "-netdev", fmt.Sprintf("dgram,id=net%d,local.type=unix,local.path=%s,remote.type=unix,remote.path=%s", i, n.Local, n.Remote))
		a = append(a, dev("virtio-net-pci,netdev=net%d,mac=%s,bus=pcie.0,addr=%#x", i, n.MAC, slotNICBase+i)...)
	}
	a = append(a, "-object", "rng-random,id=rng,filename=/dev/urandom")
	a = append(a, dev("virtio-rng-pci,rng=rng,bus=pcie.0,addr=%#x", slotRNG)...)
	for i, d := range s.DataDisks {
		a = append(a, "-drive", fmt.Sprintf("if=none,id=data%d,file=%s,format=qcow2,cache=unsafe,discard=unmap", i, d.Path))
		a = append(a, dev("virtio-blk-pci,drive=data%d,bus=pcie.0,addr=%#x", i, slotData+i)...)
	}
	a = append(a, "-qmp", "unix:"+qmpSocket(s)+",server=on,wait=off", "-pidfile", pidfile(s))

	if media != nil {
		a = append(a,
			"-drive", "if=none,id=cd,media=cdrom,readonly=on,file="+media.ISO,
			"-device", "ide-cd,drive=cd,bus=ide.0",
			"-drive", "if=none,id=ais,format=raw,read-only=on,file.driver=vvfat,file.dir="+media.AnswerDir+",file.label=PROXMOX-AIS",
			"-device", "usb-storage,bus=usb.0,drive=ais,removable=on",
			"-kernel", media.Kernel, "-initrd", media.Initrd, "-append", media.Cmdline,
			"-chardev", "socket,id=ser0,path="+serialSock(s)+",server=on,wait=on",
			"-serial", "chardev:ser0",
			"-no-reboot",
		)
	} else {
		a = append(a,
			"-chardev", "socket,id=ser0,path="+console(s)+",server=on,wait=off,logfile="+consoleLog(s)+",logappend=on",
			"-serial", "chardev:ser0",
			"-daemonize",
		)
	}
	return a
}
