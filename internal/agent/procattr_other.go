//go:build !linux

package agent

import "syscall"

func childProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
