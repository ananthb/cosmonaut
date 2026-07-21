//go:build linux

package daemon

import "syscall"

// daemonChildSysProcAttr returns the SysProcAttr for helper children the
// daemon must never orphan (inhibitors, port forwards). Setpgid gives the
// child its own process group so terminal-generated signals against a
// foreground `cosmonaut applet` run don't kill it before our own cleanup
// gets a chance to release things in order — but on its own that means a
// SIGKILLed or crashed daemon leaves the child running forever, most
// dangerously a block-mode systemd-inhibit holding a sleep/shutdown lock.
// Pdeathsig closes that hole: the kernel delivers SIGTERM to the child the
// moment its parent dies, no matter how the parent died.
func daemonChildSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
