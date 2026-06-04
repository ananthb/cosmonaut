package daemon

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/history"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

const maxSubmenuCodespaces = 5

// buildTrayMenu constructs the system tray menu from config, history,
// and cached codespace state.
func (d *Daemon) buildTrayMenu() *fyne.Menu {
	var items []*fyne.MenuItem

	if githubItem := d.githubCodespacesMenu(); githubItem != nil {
		items = append(items, githubItem)
	}
	if coderItem := d.coderWorkspaceMenu(); coderItem != nil {
		items = append(items, coderItem)
	}

	// Open previous / launch.
	hist := history.Load()
	if len(items) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
	}
	if len(hist.Entries) > 0 {
		items = append(items, fyne.NewMenuItem("Open previous", func() {
			go d.hotkeyActionPrevious()
		}))
	}
	items = append(items, fyne.NewMenuItem("Launch...", func() {
		go d.showGUI()
	}))

	// Preferences.
	items = append(items, fyne.NewMenuItemSeparator())
	items = append(items, d.preferencesMenuItem())

	// Quit.
	items = append(items, fyne.NewMenuItemSeparator())
	items = append(items, fyne.NewMenuItem("Quit", func() {
		d.Stop()
	}))

	return fyne.NewMenu("cosmonaut", items...)
}

func (d *Daemon) githubCodespacesMenu() *fyne.MenuItem {
	all := d.Codespaces()
	configured := d.Cfg == nil || d.Cfg.IsGitHubConfigured()
	if len(all) == 0 && !configured {
		return nil
	}

	repos := codespace.UniqueRepos(all)
	hist := history.Load()
	repos = hist.SortRepos(repos)

	items := make([]*fyne.MenuItem, 0, len(repos)+2)

	if status := d.ProviderStatus(provider.NameGitHub); !status.CheckedAt.IsZero() {
		if msg := githubStatusMessage(status); msg != "" {
			items = append(items, disabledMenuItem(msg))
			items = append(items, fyne.NewMenuItemSeparator())
		}
	}

	if len(repos) == 0 {
		items = append(items, disabledMenuItem("No codespaces"))
		root := fyne.NewMenuItem("Codespaces", nil)
		root.ChildMenu = fyne.NewMenu("", items...)
		return root
	}

	for _, repo := range repos {
		repo := repo
		args := d.targetNameForRepo(repo)
		if args == "" {
			args = repo
		}
		item := fyne.NewMenuItem(repo, func() {
			go d.showGUI(args)
		})
		if sub := d.codespaceSubmenu(repo, args); sub != nil {
			item.ChildMenu = sub
		}
		items = append(items, item)
	}

	root := fyne.NewMenuItem("Codespaces", nil)
	root.ChildMenu = fyne.NewMenu("", items...)
	return root
}

// githubStatusMessage returns a short human-readable summary of the
// GitHub CLI local-setup state. Empty when everything is healthy.
func githubStatusMessage(status ProviderStatus) string {
	if !status.Available {
		return "gh CLI not installed"
	}
	if status.Err == nil {
		return ""
	}
	msg := strings.ToLower(status.Err.Error())
	switch {
	case strings.Contains(msg, `needs the "codespace" scope`):
		return "gh token missing codespace scope"
	case strings.Contains(msg, "not logged"),
		strings.Contains(msg, "authentication"),
		strings.Contains(msg, "auth status"):
		return "Not authenticated (run `gh auth login`)"
	default:
		return "Codespaces unavailable"
	}
}

func (d *Daemon) coderWorkspaceMenu() *fyne.MenuItem {
	workspaces := filterWorkspacesByProvider(d.Workspaces(), provider.NameCoder)
	configured := d.Cfg != nil && d.Cfg.IsCoderConfigured()
	if len(workspaces) == 0 && !configured {
		return nil
	}

	sort.Slice(workspaces, func(i, j int) bool {
		oi, oj := stateOrder(workspaces[i].State), stateOrder(workspaces[j].State)
		if oi != oj {
			return oi < oj
		}
		return workspaceLabel(workspaces[i]) < workspaceLabel(workspaces[j])
	})

	items := make([]*fyne.MenuItem, 0, len(workspaces)+3)

	if status := d.ProviderStatus(provider.NameCoder); !status.CheckedAt.IsZero() {
		if msg := coderStatusMessage(status); msg != "" {
			items = append(items, disabledMenuItem(msg))
			items = append(items, fyne.NewMenuItemSeparator())
		}
	}

	if len(workspaces) == 0 {
		items = append(items, disabledMenuItem("No Coder workspaces"))
		items = append(items, fyne.NewMenuItem("Create new...", func() {
			go d.showGUI()
		}))
		item := fyne.NewMenuItem("Coder", nil)
		item.ChildMenu = fyne.NewMenu("", items...)
		return item
	}
	for _, ws := range workspaces {
		ws := ws
		label := fmt.Sprintf("%s %s", stateIcon(ws.State), ws.Name)
		item := fyne.NewMenuItem(label, func() {
			_, resolvedName := guiTargetForCoderWorkspace(d.Cfg, ws)
			go d.showGUI("--workspace", ws.Name, "--provider", provider.NameCoder, resolvedName)
		})
		item.ChildMenu = d.coderWorkspaceActionsMenu(ws)
		items = append(items, item)
	}
	item := fyne.NewMenuItem("Coder", nil)
	item.ChildMenu = fyne.NewMenu("", items...)
	return item
}

// coderStatusMessage returns a short human-readable summary of the
// Coder local-setup state. Empty when everything is healthy.
func coderStatusMessage(status ProviderStatus) string {
	if !status.Available {
		return "Coder CLI not installed"
	}
	if status.Err == nil {
		return ""
	}
	msg := status.Err.Error()
	switch {
	case strings.Contains(strings.ToLower(msg), "not authenticated"),
		strings.Contains(msg, "coder login"):
		return "Not authenticated (run `coder login`)"
	default:
		return "Coder unavailable"
	}
}

func (d *Daemon) coderWorkspaceActionsMenu(ws provider.Workspace) *fyne.Menu {
	target, resolvedName := guiTargetForCoderWorkspace(d.Cfg, ws)
	deleteItem := fyne.NewMenuItem("Delete workspace...", func() {
		go d.showGUI("--delete", "--workspace", ws.Name, "--provider", provider.NameCoder, resolvedName)
	})
	if !d.canDeleteWorkspace(provider.NameCoder) {
		deleteItem.Disabled = true
	}
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open in editor", func() {
			go d.showGUI("--workspace", ws.Name, "--provider", provider.NameCoder, resolvedName)
		}),
		fyne.NewMenuItem("Workspace settings...", func() {
			go d.showGUI("--detail", "--workspace", ws.Name, "--provider", provider.NameCoder, resolvedName)
		}),
		deleteItem,
		fyne.NewMenuItemSeparator(),
	}
	items = append(items, fyne.NewMenuItem("Forward port...", func() {
		go d.showGUI("--port-forward", "--workspace", ws.Name, "--provider", provider.NameCoder, resolvedName)
	}))
	items = append(items, fyne.NewMenuItemSeparator())
	if target.Coder == nil || len(target.Coder.PortForwards) == 0 {
		items = append(items, disabledMenuItem("No configured ports"))
		return fyne.NewMenu("", items...)
	}
	for _, pf := range target.Coder.PortForwards {
		pf := pf
		remotePort := pf.RemotePort
		localPort := pf.LocalPort
		if localPort == 0 {
			localPort = remotePort
		}
		item := fyne.NewMenuItem("Port "+coderPortForwardLabel(pf), nil)
		item.ChildMenu = d.coderPortActionsMenu(ws.Name, pf)
		items = append(items, item)
	}
	return fyne.NewMenu("", items...)
}

func (d *Daemon) coderPortActionsMenu(workspaceName string, pf config.PortForward) *fyne.Menu {
	remotePort := pf.RemotePort
	localPort := pf.LocalPort
	if localPort == 0 {
		localPort = remotePort
	}
	protocol := normalizePortForwardProtocol(pf.Protocol)

	var items []*fyne.MenuItem
	if d.forwards != nil && d.forwards.IsActiveProtocol(provider.NameCoder, workspaceName, protocol, remotePort, localPort) {
		items = append(items, fyne.NewMenuItem(fmt.Sprintf("Stop localhost %d", localPort), func() {
			d.stopWorkspacePortForward(provider.NameCoder, workspaceName, protocol, remotePort, localPort)
		}))
	} else {
		items = append(items, fyne.NewMenuItem(fmt.Sprintf("Forward localhost %d", localPort), func() {
			go func() {
				if err := d.startWorkspacePortForward(provider.NameCoder, workspaceName, protocol, remotePort, localPort); err != nil {
					d.notify(err.Error())
				}
			}()
		}))
	}
	return fyne.NewMenu("", items...)
}

func coderPortForwardLabel(pf config.PortForward) string {
	if pf.Label != "" {
		return fmt.Sprintf("%s (%d)", pf.Label, pf.RemotePort)
	}
	protocol := normalizePortForwardProtocol(pf.Protocol)
	if protocol != "tcp" {
		return fmt.Sprintf("%d (%s)", pf.RemotePort, protocol)
	}
	return fmt.Sprintf("%d", pf.RemotePort)
}

// codespaceSubmenu builds a submenu showing codespaces for a repo.
// Returns nil if the repo has no codespaces.
func (d *Daemon) codespaceSubmenu(repo, launchArgs string) *fyne.Menu {
	all := d.Codespaces()
	repoCS := codespace.FilterByRepo(all, repo)
	if len(repoCS) == 0 {
		return nil
	}

	// Sort: Available/Starting first, then others, alphabetically within groups.
	sort.Slice(repoCS, func(i, j int) bool {
		oi, oj := stateOrder(repoCS[i].State), stateOrder(repoCS[j].State)
		if oi != oj {
			return oi < oj
		}
		return csLabel(repoCS[i]) < csLabel(repoCS[j])
	})

	var items []*fyne.MenuItem
	limit := min(maxSubmenuCodespaces, len(repoCS))
	for _, cs := range repoCS[:limit] {
		label := fmt.Sprintf("%s %s", stateIcon(cs.State), csLabel(cs))
		item := fyne.NewMenuItem(label, func() {
			go d.showGUI("--workspace", cs.Name, "--provider", "github", launchArgs)
		})
		item.ChildMenu = d.codespaceActionsMenu(cs, launchArgs)
		items = append(items, item)
	}

	if len(repoCS) > maxSubmenuCodespaces {
		items = append(items, fyne.NewMenuItemSeparator())
		items = append(items, fyne.NewMenuItem("Show all...", func() {
			go d.showGUI(launchArgs)
		}))
	}

	return fyne.NewMenu("", items...)
}

func (d *Daemon) codespaceActionsMenu(cs codespace.Codespace, launchArgs string) *fyne.Menu {
	deleteItem := fyne.NewMenuItem("Delete codespace...", func() {
		go d.showGUI("--delete", "--workspace", cs.Name, "--provider", provider.NameGitHub, launchArgs)
	})
	if !d.canDeleteWorkspace(provider.NameGitHub) {
		deleteItem.Disabled = true
	}
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open in editor", func() {
			go d.showGUI("--workspace", cs.Name, "--provider", "github", launchArgs)
		}),
		fyne.NewMenuItem("Workspace settings...", func() {
			go d.showGUI("--detail", "--workspace", cs.Name, "--provider", "github", launchArgs)
		}),
		fyne.NewMenuItem("Refresh ports", func() {
			d.refreshPortsAsync(cs.Name, nil)
		}),
		fyne.NewMenuItem("Forward port...", func() {
			go d.showGUI("--port-forward", "--workspace", cs.Name, "--provider", provider.NameGitHub, launchArgs)
		}),
		deleteItem,
		fyne.NewMenuItemSeparator(),
	}

	entry := d.ensurePorts(cs.Name)
	switch {
	case entry.Loading:
		items = append(items, disabledMenuItem("Loading ports..."))
	case entry.Err != nil:
		items = append(items, disabledMenuItem("Ports unavailable"))
	case len(entry.Ports) == 0:
		items = append(items, disabledMenuItem("No forwarded ports"))
	default:
		for _, port := range entry.Ports {
			port := port
			item := fyne.NewMenuItem("Port "+codespace.PortLabel(port), nil)
			item.ChildMenu = d.portActionsMenu(cs.Name, port)
			items = append(items, item)
		}
	}

	return fyne.NewMenu("", items...)
}

func (d *Daemon) portActionsMenu(codespaceName string, port codespace.Port) *fyne.Menu {
	var items []*fyne.MenuItem
	if port.BrowseURL == "" {
		items = append(items, disabledMenuItem("No browse URL"))
	} else {
		items = append(items, fyne.NewMenuItem("Open URL", func() {
			d.openURL(port.BrowseURL)
		}))
		items = append(items, fyne.NewMenuItem("Copy URL", func() {
			d.copyText(port.BrowseURL)
		}))
	}

	items = append(items, fyne.NewMenuItemSeparator())
	remotePort := port.SourcePort
	localPort := port.SourcePort
	if d.forwards != nil && d.forwards.IsActive(provider.NameGitHub, codespaceName, remotePort, localPort) {
		items = append(items, fyne.NewMenuItem(fmt.Sprintf("Stop localhost %d", localPort), func() {
			d.stopLocalPortForward(codespaceName, remotePort, localPort)
		}))
	} else {
		items = append(items, fyne.NewMenuItem(fmt.Sprintf("Forward localhost %d:%d", remotePort, localPort), func() {
			go func() {
				if err := d.startLocalPortForward(codespaceName, remotePort, localPort); err != nil {
					d.notify(err.Error())
				}
			}()
		}))
	}

	return fyne.NewMenu("", items...)
}

func disabledMenuItem(label string) *fyne.MenuItem {
	item := fyne.NewMenuItem(label, nil)
	item.Disabled = true
	return item
}

// stateOrder returns a sort key for codespace states (lower = first).
func stateOrder(state string) int {
	switch state {
	case "Available", "Started", "ready", "running", "connected":
		return 0
	case "Starting", "starting", "pending":
		return 1
	case "Stopped", "stopped":
		return 2
	default:
		return 3
	}
}

// stateIcon returns a Unicode indicator for a codespace state.
func stateIcon(state string) string {
	switch state {
	case "Available", "Started", "ready", "running", "connected":
		return "●"
	case "Starting", "starting", "pending":
		return "◐"
	default:
		return "○"
	}
}

// csLabel returns a short display label for a codespace.
func csLabel(cs codespace.Codespace) string {
	name := cs.DisplayName
	if name == "" {
		name = cs.Name
	}
	if cs.GitStatus != nil {
		ref := cs.GitStatus.Ref
		if ref == "" {
			ref = cs.GitStatus.Branch
		}
		if ref != "" {
			return fmt.Sprintf("%s (%s)", name, ref)
		}
	}
	return name
}

// targetNameForRepo returns the config target name for a repo, or empty string.
func (d *Daemon) targetNameForRepo(repo string) string {
	if d.Cfg == nil {
		return ""
	}
	for name, t := range d.Cfg.Targets {
		if t.Repository == repo {
			return name
		}
	}
	return ""
}

// preferencesMenuItem opens the preferences window.
func (d *Daemon) preferencesMenuItem() *fyne.MenuItem {
	return fyne.NewMenuItem("Preferences...", func() {
		go d.showPreferences()
	})
}

// rebuildTrayMenu rebuilds and replaces the system tray menu.
// Safe to call from any goroutine.
func (d *Daemon) rebuildTrayMenu() {
	if d.app == nil {
		return
	}
	fyne.Do(func() {
		if desk, ok := d.app.(desktop.App); ok {
			desk.SetSystemTrayMenu(d.buildTrayMenu())
		}
	})
}

// openFile opens a file with the OS default handler.
func openFile(path string) {
	_ = exec.Command("open", path).Run()
}
