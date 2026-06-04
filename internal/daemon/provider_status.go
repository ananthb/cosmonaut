package daemon

import (
	"time"
)

// ProviderStatus records the local-setup health of a workspace provider:
// whether its CLI is on PATH and whether the last list/auth call
// succeeded. Cached so the tray menu can show why a provider is empty
// (CLI missing, not authenticated) rather than silently hiding the
// section.
type ProviderStatus struct {
	Available bool      // CLI is on PATH
	Err       error     // last list/auth error, nil on success
	CheckedAt time.Time // when this snapshot was taken
}

// ProviderStatus returns a copy of the cached status for the given
// provider, or a zero value if none was recorded yet.
func (d *Daemon) ProviderStatus(name string) ProviderStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.providerStatus == nil {
		return ProviderStatus{}
	}
	return d.providerStatus[name]
}

func (d *Daemon) setProviderStatus(name string, status ProviderStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.providerStatus == nil {
		d.providerStatus = make(map[string]ProviderStatus)
	}
	status.CheckedAt = time.Now()
	d.providerStatus[name] = status
}
