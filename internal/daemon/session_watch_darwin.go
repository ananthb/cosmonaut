package daemon

import (
	"log"
	"syscall"
)

// waitPidExit blocks until the given pid exits. Uses kqueue's EVFILT_PROC
// with NOTE_EXIT for event-driven notification (no polling). Falls back to
// a simple kill(0) poll on errors.
func waitPidExit(pid int) {
	kq, err := syscall.Kqueue()
	if err != nil {
		log.Printf("session watch: kqueue: %v", err)
		fallbackWaitPid(pid)
		return
	}
	defer func() {
		if err := syscall.Close(kq); err != nil {
			log.Printf("session watch: kqueue close: %v", err)
		}
	}()

	ev := syscall.Kevent_t{
		Ident:  uint64(pid),
		Filter: syscall.EVFILT_PROC,
		Flags:  syscall.EV_ADD | syscall.EV_ENABLE | syscall.EV_ONESHOT,
		Fflags: syscall.NOTE_EXIT,
	}
	if _, err := syscall.Kevent(kq, []syscall.Kevent_t{ev}, nil, nil); err != nil {
		log.Printf("session watch: kevent register pid=%d: %v", pid, err)
		fallbackWaitPid(pid)
		return
	}

	out := make([]syscall.Kevent_t, 1)
	for {
		n, err := syscall.Kevent(kq, nil, out, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			log.Printf("session watch: kevent wait pid=%d: %v", pid, err)
			fallbackWaitPid(pid)
			return
		}
		if n > 0 {
			return
		}
	}
}
