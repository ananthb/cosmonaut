package daemon

import (
	"log"
	"os/exec"
	"sync"
	"syscall"
)

// caffeinateInhibitor holds an idle-sleep assertion via `caffeinate -i`.
// macOS does not expose a user-space shutdown inhibitor, so "sleep+shutdown"
// degrades to sleep-only (documented in docs/config.md).
type caffeinateInhibitor struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

func (c *caffeinateInhibitor) Engage(mode string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return nil
	}
	cmd := exec.Command("caffeinate", "-i")
	cmd.SysProcAttr = daemonChildSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	return nil
}

func (c *caffeinateInhibitor) Release() error {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("inhibitor: caffeinate exit: %v", err)
		}
	}()
	return nil
}

func platformNewInhibitor() Inhibitor { return &caffeinateInhibitor{} }
