package qemu

import "syscall"

// installProcAttr kills the installer VM if proxbase dies.
func installProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
