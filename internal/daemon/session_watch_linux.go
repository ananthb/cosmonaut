package daemon

import (
	"log"

	"golang.org/x/sys/unix"
)

// waitPidExit blocks until the given pid exits. Uses pidfd_open(2) +
// poll(2) for event-driven notification (no polling of /proc). Falls
// back to a kill(0) poll on errors (older kernels).
func waitPidExit(pid int) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		log.Printf("session watch: pidfd_open pid=%d: %v", pid, err)
		fallbackWaitPid(pid)
		return
	}
	defer func() { _ = unix.Close(fd) }()

	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		_, err := unix.Poll(fds, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			log.Printf("session watch: poll pid=%d: %v", pid, err)
			fallbackWaitPid(pid)
			return
		}
		return
	}
}
