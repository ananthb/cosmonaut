//go:build !linux

package daemon

import "syscall"

// daemonChildSysProcAttr returns the SysProcAttr for helper children the
// daemon must never orphan. See sysproc_linux.go for the full rationale;
// this platform has no Pdeathsig equivalent, so the SIGTERM/SIGINT handler
// in Daemon.Run is the only orphan protection beyond normal cleanup.
func daemonChildSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
