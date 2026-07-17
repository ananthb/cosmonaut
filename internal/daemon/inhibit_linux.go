package daemon

import (
	"log"
	"os/exec"
	"sync"
	"syscall"
)

// systemdInhibitInhibitor holds a logind inhibitor lock via `systemd-inhibit`.
// The inhibited command is `sleep infinity`; we kill it to release the lock.
type systemdInhibitInhibitor struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

func (s *systemdInhibitInhibitor) Engage(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return nil
	}
	what := "sleep"
	if mode == "sleep+shutdown" {
		what = "sleep:shutdown"
	}
	cmd := exec.Command(
		"systemd-inhibit",
		"--what="+what,
		"--who=Cosmonaut",
		"--why=codespace session active",
		"--mode=block",
		"sleep", "infinity",
	)
	cmd.SysProcAttr = daemonChildSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	return nil
}

func (s *systemdInhibitInhibitor) Release() error {
	s.mu.Lock()
	cmd := s.cmd
	s.cmd = nil
	s.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("inhibitor: systemd-inhibit exit: %v", err)
		}
	}()
	return nil
}

func platformNewInhibitor() Inhibitor { return &systemdInhibitInhibitor{} }
