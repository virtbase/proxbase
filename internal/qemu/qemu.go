// Package qemu builds QEMU command lines for Proxmox nodes and controls the
// processes via pidfile and QMP.
package qemu

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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

type NIC struct {
	MAC    string
	Local  string // our end of the unix datagram socket
	Switch string // the switch port socket
}

type Install struct {
	ISO, Kernel, Initrd, Cmdline string
	AnswerDir                    string // served as vvfat disk labelled PROXMOX-AIS
	SerialSock                   string
}

type Machine struct {
	Name      string // process/guest name, e.g. lab-pve1
	CPUs      int
	MemoryMiB int
	Nested    bool
	RootDisk  string
	DataDisks []string
	NATMAC    string
	UIPort    int
	SSHPort   int
	NICs      []NIC
	QMP       string
	PIDFile   string
	Console   string   // console log file (run mode)
	Install   *Install // nil = run mode
}

// MAC returns the MAC of NIC nic (0 = NAT) on node index node.
func MAC(node, nic int) string { return fmt.Sprintf("52:54:00:50:%02x:%02x", node, nic) }

func dev(format string, a ...any) []string { return []string{"-device", fmt.Sprintf(format, a...)} }

func (m *Machine) Args() []string {
	cpu := "host"
	if !m.Nested {
		cpu += ",vmx=off,svm=off"
	}
	a := []string{
		"-name", "guest=" + m.Name + ",process=" + m.Name,
		"-nodefaults", "-no-user-config",
		"-machine", "q35,accel=kvm",
		"-cpu", cpu,
		"-smp", strconv.Itoa(m.CPUs),
		"-m", strconv.Itoa(m.MemoryMiB),
		"-display", "none",
	}
	a = append(a, dev("VGA,bus=pcie.0,addr=%#x", slotVGA)...)
	a = append(a, "-drive", "if=none,id=root,file="+m.RootDisk+",format=qcow2,cache=unsafe,discard=unmap")
	a = append(a, dev("virtio-blk-pci,drive=root,bus=pcie.0,addr=%#x,bootindex=1", slotRoot)...)
	a = append(a, dev("qemu-xhci,id=usb,bus=pcie.0,addr=%#x", slotUSB)...)
	fwd := fmt.Sprintf("hostfwd=tcp:127.0.0.1:%d-:22,hostfwd=tcp:127.0.0.1:%d-:8006", m.SSHPort, m.UIPort)
	a = append(a, "-netdev", "user,id=nat,"+fwd)
	a = append(a, dev("virtio-net-pci,netdev=nat,mac=%s,bus=pcie.0,addr=%#x", m.NATMAC, slotNAT)...)
	for i, n := range m.NICs {
		a = append(a, "-netdev", fmt.Sprintf("dgram,id=net%d,local.type=unix,local.path=%s,remote.type=unix,remote.path=%s", i, n.Local, n.Switch))
		a = append(a, dev("virtio-net-pci,netdev=net%d,mac=%s,bus=pcie.0,addr=%#x", i, n.MAC, slotNICBase+i)...)
	}
	a = append(a, "-object", "rng-random,id=rng,filename=/dev/urandom")
	a = append(a, dev("virtio-rng-pci,rng=rng,bus=pcie.0,addr=%#x", slotRNG)...)
	for i, d := range m.DataDisks {
		a = append(a, "-drive", fmt.Sprintf("if=none,id=data%d,file=%s,format=qcow2,cache=unsafe,discard=unmap", i, d))
		a = append(a, dev("virtio-blk-pci,drive=data%d,bus=pcie.0,addr=%#x", i, slotData+i)...)
	}
	a = append(a, "-qmp", "unix:"+m.QMP+",server=on,wait=off", "-pidfile", m.PIDFile)

	if in := m.Install; in != nil {
		a = append(a,
			"-drive", "if=none,id=cd,media=cdrom,readonly=on,file="+in.ISO,
			"-device", "ide-cd,drive=cd,bus=ide.0",
			"-drive", "if=none,id=ais,format=raw,read-only=on,file.driver=vvfat,file.dir="+in.AnswerDir+",file.label=PROXMOX-AIS",
			"-device", "usb-storage,bus=usb.0,drive=ais,removable=on",
			"-kernel", in.Kernel, "-initrd", in.Initrd, "-append", in.Cmdline,
			"-chardev", "socket,id=ser0,path="+in.SerialSock+",server=on,wait=on",
			"-serial", "chardev:ser0",
			"-no-reboot",
		)
	} else {
		a = append(a,
			"-chardev", "file,id=ser0,path="+m.Console+",append=on",
			"-serial", "chardev:ser0",
			"-daemonize",
		)
	}
	return a
}

// StartInstall starts the installer in the foreground; it is killed if proxbase dies.
func (m *Machine) StartInstall(stderr io.Writer) (*exec.Cmd, error) {
	cmd := exec.Command(Binary, m.Args()...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// Start boots the installed node as a daemon.
func (m *Machine) Start() error {
	if _, ok := Running(m.PIDFile); ok {
		return nil
	}
	out, err := exec.Command(Binary, m.Args()...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("start %s: %w: %s", m.Name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Running reports whether the process in pidfile is alive and really ours.
func Running(pidfile string) (int, bool) {
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
