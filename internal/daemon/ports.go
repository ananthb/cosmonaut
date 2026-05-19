package daemon

import (
	"fmt"
	"log"
	"net/url"
	"time"

	"fyne.io/fyne/v2"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

type portCacheEntry struct {
	Ports     []codespace.Port
	Err       error
	Loading   bool
	FetchedAt time.Time
}

func (d *Daemon) ensurePorts(codespaceName string) portCacheEntry {
	return d.ensurePortsWithCallback(codespaceName, nil)
}

func (d *Daemon) ensurePortsWithCallback(codespaceName string, done func()) portCacheEntry {
	if codespaceName == "" {
		return portCacheEntry{}
	}

	start := false
	d.mu.Lock()
	if d.portCache == nil {
		d.portCache = make(map[string]portCacheEntry)
	}
	entry, ok := d.portCache[codespaceName]
	if !ok {
		entry = portCacheEntry{Loading: true}
		d.portCache[codespaceName] = entry
		start = true
	}
	d.mu.Unlock()

	if start {
		d.refreshPortsAsync(codespaceName, done)
	}
	return entry
}

func (d *Daemon) refreshPortsAsync(codespaceName string, done func()) {
	if codespaceName == "" {
		return
	}

	d.setPortCacheLoading(codespaceName)
	go func() {
		ports, err := codespace.ListPorts(d.Runner, codespaceName)
		d.setPortCacheResult(codespaceName, ports, err)
		if err != nil {
			log.Printf("ports: list %s: %v", codespaceName, err)
		}
		d.rebuildTrayMenu()
		if done != nil {
			fyne.Do(done)
		}
	}()
}

func (d *Daemon) setPortCacheLoading(codespaceName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.portCache == nil {
		d.portCache = make(map[string]portCacheEntry)
	}
	entry := d.portCache[codespaceName]
	entry.Loading = true
	entry.Err = nil
	d.portCache[codespaceName] = entry
}

func (d *Daemon) setPortCacheResult(codespaceName string, ports []codespace.Port, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.portCache == nil {
		d.portCache = make(map[string]portCacheEntry)
	}
	cp := make([]codespace.Port, len(ports))
	copy(cp, ports)
	d.portCache[codespaceName] = portCacheEntry{
		Ports:     cp,
		Err:       err,
		Loading:   false,
		FetchedAt: time.Now(),
	}
}

func (d *Daemon) copyText(text string) {
	if d.app == nil || text == "" {
		return
	}
	d.app.Clipboard().SetContent(text)
	d.notify("Copied to clipboard")
}

func (d *Daemon) openURL(raw string) {
	if d.app == nil || raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		d.notify(fmt.Sprintf("Invalid URL: %s", raw))
		return
	}
	if err := d.app.OpenURL(u); err != nil {
		d.notify(fmt.Sprintf("Opening URL failed: %v", err))
	}
}

func (d *Daemon) notify(msg string) {
	if msg == "" {
		return
	}
	if d.app != nil {
		d.app.SendNotification(&fyne.Notification{
			Title:   "cosmonaut",
			Content: msg,
		})
		return
	}
	sendNotification(msg)
}

func (d *Daemon) startLocalPortForward(codespaceName string, remotePort, localPort int) error {
	return d.startWorkspacePortForward(provider.NameGitHub, codespaceName, "tcp", remotePort, localPort)
}

func (d *Daemon) startWorkspacePortForward(providerName, workspaceName, protocol string, remotePort, localPort int) error {
	if d.forwards == nil {
		d.forwards = newPortForwardManager()
	}
	if err := d.forwards.StartProtocol(providerName, workspaceName, protocol, remotePort, localPort); err != nil {
		return err
	}
	d.notify(fmt.Sprintf("Forwarding localhost:%d to %s:%d", localPort, workspaceName, remotePort))
	d.rebuildTrayMenu()
	return nil
}

func (d *Daemon) stopLocalPortForward(codespaceName string, remotePort, localPort int) {
	d.stopWorkspacePortForward(provider.NameGitHub, codespaceName, "tcp", remotePort, localPort)
}

func (d *Daemon) stopWorkspacePortForward(providerName, workspaceName, protocol string, remotePort, localPort int) {
	if d.forwards == nil {
		return
	}
	if d.forwards.StopProtocol(providerName, workspaceName, protocol, remotePort, localPort) {
		d.notify(fmt.Sprintf("Stopped localhost:%d forward", localPort))
		d.rebuildTrayMenu()
	}
}
