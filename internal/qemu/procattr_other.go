//go:build !linux

package qemu

import "syscall"

func installProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
