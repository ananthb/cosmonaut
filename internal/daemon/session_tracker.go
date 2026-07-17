package daemon

import (
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SessionTracker ref-counts live codespace SSH sessions across the whole
// daemon. When the count transitions from 0→1 it engages the inhibitor; when
// it drops back to 0 it releases.
//
// Each call to TrackSession launches a background scan for ssh processes
// matching the given alias and watches their exit via kernel events
// (pidfd on Linux, kqueue on macOS). Reconnections after the initial
// scan window are not tracked: the next Cosmonaut launch re-scans.
type SessionTracker struct {
	mu        sync.Mutex
	count     int
	mode      string
	inhibitor Inhibitor
	watched   map[int]struct{}
}

func newSessionTracker(mode string) *SessionTracker {
	return &SessionTracker{
		mode:      mode,
		inhibitor: newInhibitor(mode),
		watched:   map[int]struct{}{},
	}
}

// SetMode hot-swaps the inhibit mode. If sessions are currently tracked,
// the inhibitor is re-engaged with the new mode.
func (s *SessionTracker) SetMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == s.mode {
		return
	}
	s.mode = mode
	_ = s.inhibitor.Release()
	s.inhibitor = newInhibitor(mode)
	if s.count > 0 {
		if err := s.inhibitor.Engage(s.mode); err != nil {
			log.Printf("session tracker: engage after mode change: %v", err)
		}
	}
}

// Stop releases any held inhibitor; watcher goroutines keep running but
// their decrement calls become no-ops.
func (s *SessionTracker) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.inhibitor.Release()
	s.inhibitor = noopInhibitor{}
	s.count = 0
}

func (s *SessionTracker) addPid(pid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watched[pid]; ok {
		return false
	}
	s.watched[pid] = struct{}{}
	s.count++
	if s.count == 1 {
		if err := s.inhibitor.Engage(s.mode); err != nil {
			log.Printf("session tracker: engage: %v", err)
		}
	}
	return true
}

func (s *SessionTracker) removePid(pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watched[pid]; !ok {
		return
	}
	delete(s.watched, pid)
	s.count--
	if s.count == 0 {
		if err := s.inhibitor.Release(); err != nil {
			log.Printf("session tracker: release: %v", err)
		}
	}
}

// TrackSession scans for SSH processes whose command line contains the given
// alias, registers each and watches them for exit. Safe to call when mode is
// "off": it still runs but the inhibitor is a no-op.
//
// The scan retries for a short window because the launch chain (osascript →
// Terminal.app, or terminal emulator fork) means the ssh pid may not exist
// the instant LaunchRemote returns.
func (s *SessionTracker) TrackSession(alias string) {
	if alias == "" {
		return
	}
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		seen := map[int]struct{}{}
		for time.Now().Before(deadline) {
			pids := findSSHPidsByAlias(alias)
			for _, pid := range pids {
				if _, ok := seen[pid]; ok {
					continue
				}
				seen[pid] = struct{}{}
				if s.addPid(pid) {
					go s.watchExit(pid)
				}
			}
			if len(seen) > 0 {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if len(seen) == 0 {
			log.Printf("session tracker: no ssh pid found for %q within window", alias)
		}
	}()
}

func (s *SessionTracker) watchExit(pid int) {
	waitPidExit(pid)
	s.removePid(pid)
}

// findSSHPidsByAlias greps the process table for ssh processes whose command
// line contains the alias. Uses `pgrep -f` which is available on both
// macOS and Linux. The alias is regexp-quoted: aliases contain `.` (e.g.
// cs.name.github.dev), which as a metacharacter both over-matched and — a
// bigger deal for the sleep inhibitor — could latch onto an unrelated
// long-lived process and keep the machine awake indefinitely.
func findSSHPidsByAlias(alias string) []int {
	out, err := exec.Command("pgrep", "-f", `(^|/)ssh .*`+regexp.QuoteMeta(alias)).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}
