// Port-forward dialogs (ad-hoc + configured) and the config mutations
// backing the Coder port-forward list.
package daemon

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

func (uw *unifiedWindow) showCoderPortDialog(ws provider.Workspace, target config.Target, targetName string, index int, existing *config.PortForward) {
	labelEntry := widget.NewEntry()
	labelEntry.PlaceHolder = "app"
	localEntry := widget.NewEntry()
	localEntry.PlaceHolder = "8080"
	remoteEntry := widget.NewEntry()
	remoteEntry.PlaceHolder = "3000"
	protocolSelect := widget.NewSelect([]string{"tcp", "udp"}, nil)
	protocolSelect.Selected = "tcp"
	if existing != nil {
		labelEntry.SetText(existing.Label)
		if existing.LocalPort > 0 {
			localEntry.SetText(strconv.Itoa(existing.LocalPort))
		}
		if existing.RemotePort > 0 {
			remoteEntry.SetText(strconv.Itoa(existing.RemotePort))
		}
		protocolSelect.Selected = normalizePortForwardProtocol(existing.Protocol)
	}

	items := []*widget.FormItem{
		widget.NewFormItem("Label", labelEntry),
		widget.NewFormItem("Local port", localEntry),
		widget.NewFormItem("Remote port", remoteEntry),
		widget.NewFormItem("Protocol", protocolSelect),
	}
	title := "Add Coder port forward"
	if existing != nil {
		title = "Edit Coder port forward"
	}
	dialog.ShowForm(title, "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		remotePort, err := strconv.Atoi(strings.TrimSpace(remoteEntry.Text))
		if err != nil || remotePort <= 0 || remotePort > 65535 {
			dialog.ShowError(fmt.Errorf("remote port must be between 1 and 65535"), uw.win)
			return
		}
		localPort := remotePort
		if strings.TrimSpace(localEntry.Text) != "" {
			localPort, err = strconv.Atoi(strings.TrimSpace(localEntry.Text))
			if err != nil || localPort <= 0 || localPort > 65535 {
				dialog.ShowError(fmt.Errorf("local port must be between 1 and 65535"), uw.win)
				return
			}
		}
		protocol := normalizePortForwardProtocol(protocolSelect.Selected)
		if protocol != "tcp" && protocol != "udp" {
			protocol = "tcp"
		}

		pf := config.PortForward{
			Label:      strings.TrimSpace(labelEntry.Text),
			LocalPort:  localPort,
			RemotePort: remotePort,
			Protocol:   protocol,
		}
		var saveErr error
		if existing == nil {
			saveErr = uw.addCoderPortForward(targetName, ws, target, pf)
		} else {
			saveErr = uw.updateCoderPortForward(targetName, index, pf)
		}
		if saveErr != nil {
			dialog.ShowError(saveErr, uw.win)
			return
		}
		uw.showCoderWorkspaceDetail(ws)
	}, uw.win)
}

// showAdHocPortForwardDialog prompts for remote/local port (+ protocol for
// providers that support UDP) and starts a one-off workspace port forward.
// The forward is not saved to config — it lives for the daemon's lifetime.
func (uw *unifiedWindow) showAdHocPortForwardDialog(providerName, workspaceName string, onStarted func()) {
	remoteEntry := widget.NewEntry()
	remoteEntry.PlaceHolder = "3000"
	localEntry := widget.NewEntry()
	localEntry.PlaceHolder = "same as remote"

	items := []*widget.FormItem{
		widget.NewFormItem("Remote port", remoteEntry),
		widget.NewFormItem("Local port", localEntry),
	}
	var protocolSelect *widget.Select
	if providerName == provider.NameCoder {
		protocolSelect = widget.NewSelect([]string{"tcp", "udp"}, nil)
		protocolSelect.Selected = "tcp"
		items = append(items, widget.NewFormItem("Protocol", protocolSelect))
	}

	title := fmt.Sprintf("Forward port — %s", workspaceName)
	dialog.ShowForm(title, "Forward", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		remotePort, err := strconv.Atoi(strings.TrimSpace(remoteEntry.Text))
		if err != nil || remotePort <= 0 || remotePort > 65535 {
			dialog.ShowError(fmt.Errorf("remote port must be between 1 and 65535"), uw.win)
			return
		}
		localPort := remotePort
		if strings.TrimSpace(localEntry.Text) != "" {
			localPort, err = strconv.Atoi(strings.TrimSpace(localEntry.Text))
			if err != nil || localPort <= 0 || localPort > 65535 {
				dialog.ShowError(fmt.Errorf("local port must be between 1 and 65535"), uw.win)
				return
			}
		}
		protocol := "tcp"
		if protocolSelect != nil {
			protocol = normalizePortForwardProtocol(protocolSelect.Selected)
		}
		go func() {
			if err := uw.daemon.startWorkspacePortForward(providerName, workspaceName, protocol, remotePort, localPort); err != nil {
				fyne.Do(func() { dialog.ShowError(err, uw.win) })
				return
			}
			if onStarted != nil {
				fyne.Do(onStarted)
			}
		}()
	}, uw.win)
}

func (uw *unifiedWindow) addCoderPortForward(targetName string, ws provider.Workspace, target config.Target, pf config.PortForward) error {
	if uw.daemon.Cfg == nil {
		return fmt.Errorf("no config is loaded, so port forwards cannot be saved")
	}
	targetName = strings.TrimSpace(targetName)
	if targetName == "" {
		targetName = ws.Name
	}
	uw.daemon.Cfg.UpdateTarget(targetName, func(t *config.Target, exists bool) {
		if !exists {
			*t = target
		}
		*t = applyWorkspaceDefaults(*t, ws)
		if t.Coder == nil {
			t.Coder = &config.CoderTargetConfig{}
		}
		t.Coder.WorkspaceName = ws.Name
		t.Coder.PortForwards = append(t.Coder.PortForwards, pf)
	})
	uw.daemon.persistConfig()
	return nil
}

func (uw *unifiedWindow) updateCoderPortForward(targetName string, index int, pf config.PortForward) error {
	if uw.daemon.Cfg == nil {
		return fmt.Errorf("no config is loaded, so port forwards cannot be saved")
	}
	var opErr error
	uw.daemon.Cfg.UpdateTarget(targetName, func(t *config.Target, exists bool) {
		if !exists || t.Coder == nil {
			opErr = fmt.Errorf("coder target %q was not found in config", targetName)
			return
		}
		if index < 0 || index >= len(t.Coder.PortForwards) {
			opErr = fmt.Errorf("port forward no longer exists")
			return
		}
		t.Coder.PortForwards[index] = pf
	})
	if opErr != nil {
		return opErr
	}
	uw.daemon.persistConfig()
	return nil
}

func (uw *unifiedWindow) removeCoderPortForward(targetName string, index int) error {
	if uw.daemon.Cfg == nil {
		return fmt.Errorf("no config is loaded, so port forwards cannot be saved")
	}
	var opErr error
	uw.daemon.Cfg.UpdateTarget(targetName, func(t *config.Target, exists bool) {
		if !exists || t.Coder == nil {
			opErr = fmt.Errorf("coder target %q was not found in config", targetName)
			return
		}
		if index < 0 || index >= len(t.Coder.PortForwards) {
			opErr = fmt.Errorf("port forward no longer exists")
			return
		}
		t.Coder.PortForwards = append(t.Coder.PortForwards[:index], t.Coder.PortForwards[index+1:]...)
	})
	if opErr != nil {
		return opErr
	}
	uw.daemon.persistConfig()
	return nil
}

func coderPortTargetName(cfg *config.Config, ws provider.Workspace, fallback string) string {
	if cfg != nil {
		for name, target := range cfg.TargetsSnapshot() {
			if target.Coder != nil && target.Coder.WorkspaceName == ws.Name {
				return name
			}
		}
	}
	if strings.TrimSpace(fallback) != "" && fallback == ws.Name {
		return fallback
	}
	return ws.Name
}
