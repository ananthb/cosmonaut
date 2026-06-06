package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTrayTestDaemon constructs a Daemon stripped of Fyne dependencies,
// wired with a controllable nowFunc and an applyTrayMenuFunc that just
// counts calls. The returned cleanup stops any in-flight rebuild timer
// so leftover goroutines don't race subsequent tests.
func newTrayTestDaemon(t *testing.T) (*Daemon, *atomic.Int32, func(time.Time), func()) {
	t.Helper()
	d := &Daemon{}
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	d.nowFunc = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	setNow := func(tm time.Time) {
		mu.Lock()
		defer mu.Unlock()
		now = tm
	}
	var applies atomic.Int32
	d.applyTrayMenuFunc = func() {
		applies.Add(1)
	}
	cleanup := func() {
		d.mu.Lock()
		if d.rebuildTimer != nil {
			d.rebuildTimer.Stop()
			d.rebuildTimer = nil
		}
		d.mu.Unlock()
	}
	return d, &applies, setNow, cleanup
}

// stopTimerLocked stops any armed rebuild timer so the real wall clock
// doesn't fire flushPendingRebuild mid-test. The test drives flush
// manually via callFlush.
func stopTimer(d *Daemon) {
	d.mu.Lock()
	if d.rebuildTimer != nil {
		d.rebuildTimer.Stop()
		d.rebuildTimer = nil
	}
	d.mu.Unlock()
}

func TestRebuildTrayMenu_DeferredWithinCooldown(t *testing.T) {
	d, applies, setNow, cleanup := newTrayTestDaemon(t)
	defer cleanup()

	// First apply goes through immediately (no prior apply, no tray open).
	d.rebuildTrayMenu()
	if got := applies.Load(); got != 1 {
		t.Fatalf("first rebuild: applies = %d, want 1", got)
	}
	stopTimer(d)

	// Second rebuild 1s later (still inside the 2s cooldown) must defer.
	setNow(d.now().Add(1 * time.Second))
	d.rebuildTrayMenu()
	if got := applies.Load(); got != 1 {
		t.Fatalf("rebuild within cooldown: applies = %d, want 1", got)
	}
	d.mu.Lock()
	if !d.pendingRebuild {
		d.mu.Unlock()
		t.Fatalf("rebuild within cooldown: pendingRebuild = false, want true")
	}
	d.mu.Unlock()
	stopTimer(d)

	// Advance past the cooldown and flush — apply now fires.
	setNow(d.now().Add(2 * time.Second))
	d.flushPendingRebuild()
	if got := applies.Load(); got != 2 {
		t.Fatalf("flush after cooldown: applies = %d, want 2", got)
	}
}

func TestRebuildTrayMenu_DeferredWithinInteractionWindow(t *testing.T) {
	d, applies, setNow, cleanup := newTrayTestDaemon(t)
	defer cleanup()

	// Simulate the user opening the tray; the interaction window now
	// covers the next 30 seconds.
	d.mu.Lock()
	d.trayOpenedAt = d.now()
	d.mu.Unlock()

	// Move past the cooldown so it's not the gate under test.
	setNow(d.now().Add(5 * time.Second))

	d.rebuildTrayMenu()
	if got := applies.Load(); got != 0 {
		t.Fatalf("rebuild within interaction window: applies = %d, want 0", got)
	}
	d.mu.Lock()
	if !d.pendingRebuild {
		d.mu.Unlock()
		t.Fatalf("rebuild within interaction window: pendingRebuild = false, want true")
	}
	d.mu.Unlock()
	stopTimer(d)

	// A second rebuild while still gated is idempotent.
	d.rebuildTrayMenu()
	if got := applies.Load(); got != 0 {
		t.Fatalf("second rebuild within window: applies = %d, want 0", got)
	}
	stopTimer(d)

	// Still inside the 30s window — flushing finds the gate still up,
	// re-arms the timer, and does not apply.
	setNow(d.now().Add(20 * time.Second))
	d.flushPendingRebuild()
	if got := applies.Load(); got != 0 {
		t.Fatalf("flush still inside window: applies = %d, want 0", got)
	}
	d.mu.Lock()
	if !d.pendingRebuild {
		d.mu.Unlock()
		t.Fatalf("flush still inside window: pendingRebuild cleared prematurely")
	}
	d.mu.Unlock()
	stopTimer(d)

	// Move past the interaction window and flush — apply fires.
	setNow(d.now().Add(15 * time.Second))
	d.flushPendingRebuild()
	if got := applies.Load(); got != 1 {
		t.Fatalf("flush after interaction window: applies = %d, want 1", got)
	}
}

func TestRebuildTrayMenu_OnTrayOpenedDoesNotFlush(t *testing.T) {
	d, applies, _, cleanup := newTrayTestDaemon(t)
	defer cleanup()

	// Seed a pending rebuild as if a poll had completed earlier.
	d.mu.Lock()
	d.pendingRebuild = true
	d.lastApplyAt = d.now().Add(-1 * time.Hour) // cooldown well clear
	d.mu.Unlock()

	d.onTrayOpened()
	if got := applies.Load(); got != 0 {
		t.Fatalf("onTrayOpened applied immediately: applies = %d, want 0", got)
	}
	d.mu.Lock()
	if !d.pendingRebuild {
		d.mu.Unlock()
		t.Fatalf("onTrayOpened cleared pendingRebuild: want it preserved")
	}
	if d.trayOpenedAt.IsZero() {
		d.mu.Unlock()
		t.Fatalf("onTrayOpened did not stamp trayOpenedAt")
	}
	d.mu.Unlock()
	stopTimer(d)
}

func TestRebuildTrayMenu_AppliesWhenBothGatesClear(t *testing.T) {
	d, applies, setNow, cleanup := newTrayTestDaemon(t)
	defer cleanup()

	// First call: no prior apply, no tray open. Both gates are clear.
	d.rebuildTrayMenu()
	if got := applies.Load(); got != 1 {
		t.Fatalf("first rebuild: applies = %d, want 1", got)
	}
	stopTimer(d)

	// Advance past both gates and rebuild again.
	setNow(d.now().Add(31 * time.Second))
	d.rebuildTrayMenu()
	if got := applies.Load(); got != 2 {
		t.Fatalf("rebuild after gates clear: applies = %d, want 2", got)
	}
	d.mu.Lock()
	if d.pendingRebuild {
		d.mu.Unlock()
		t.Fatalf("apply path left pendingRebuild=true")
	}
	d.mu.Unlock()
	stopTimer(d)
}
