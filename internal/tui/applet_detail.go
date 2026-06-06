package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/editor"
	"github.com/linuskendall/cosmonaut/internal/provider"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
	"github.com/linuskendall/cosmonaut/internal/terminal"
)

// detailFocus identifies which group of inputs has keyboard focus inside
// the detail view. Tab cycles between them.
type detailFocus int

const (
	focusActions detailFocus = iota
	focusOptions
	focusPorts
)

// detailModel renders the per-workspace detail page. It owns the SSH
// option toggles and the inline delete confirmation; ports are read from
// the shared cache.
type detailModel struct {
	workspace provider.Workspace

	focus         detailFocus
	actionsCursor int // 0=Open, 1=SSH, 2=Delete
	optionsCursor int // 0=ControlMaster, 1=tmux
	portsCursor   int // index into displayed ports

	confirmDelete bool
}

func newDetailModel(_ *AppletData, ws provider.Workspace) detailModel {
	return detailModel{workspace: ws}
}

func (m detailModel) Init() tea.Cmd { return nil }

func (m detailModel) update(msg tea.Msg, d *AppletData) (detailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case reloadMsg:
		// Re-fetch the latest workspace snapshot if the data layer has it.
		for _, latest := range d.Workspaces() {
			if latest.Provider == m.workspace.Provider && latest.Name == m.workspace.Name {
				m.workspace = latest
				return m, nil
			}
		}
		return m, nil
	case tea.KeyMsg:
		if m.confirmDelete {
			return m.handleConfirmDelete(msg, d)
		}
		return m.handleKey(msg, d)
	}
	return m, nil
}

func (m detailModel) handleKey(msg tea.KeyMsg, d *AppletData) (detailModel, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		return m, switchTo(viewList, nil)
	case "tab":
		// Let the top-level handler swap views, but cycle focus first if
		// there's more than one group on this view.
		m.focus = (m.focus + 1) % 3
		return m, nil
	case "up", "k":
		m.moveCursor(-1, d)
	case "down", "j":
		m.moveCursor(1, d)
	case "enter", " ":
		return m.activate(d)
	case "r":
		// Refresh the ports cache for GitHub codespaces (no-op for Coder).
		if m.workspace.Provider == provider.NameGitHub {
			name := m.workspace.Name
			return m, func() tea.Msg {
				d.RefreshPorts(name)
				return reloadMsg{}
			}
		}
	}
	return m, nil
}

func (m detailModel) handleConfirmDelete(msg tea.KeyMsg, d *AppletData) (detailModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		ws := m.workspace
		m.confirmDelete = false
		return m, tea.Batch(
			func() tea.Msg {
				if err := d.DeleteWorkspace(ws.Provider, ws.Name); err != nil {
					return flashMsg{text: fmt.Sprintf("delete %s: %v", ws.Name, err), err: true}
				}
				go func() { d.Poll() }()
				return flashMsg{text: fmt.Sprintf("Deleted %s", ws.Name)}
			},
			switchTo(viewList, nil),
		)
	case "n", "N", "esc":
		m.confirmDelete = false
	}
	return m, nil
}

// moveCursor moves the cursor within the active focus group, wrapping.
func (m *detailModel) moveCursor(delta int, d *AppletData) {
	switch m.focus {
	case focusActions:
		m.actionsCursor = wrapDetail(m.actionsCursor+delta, 3)
	case focusOptions:
		m.optionsCursor = wrapDetail(m.optionsCursor+delta, 2)
	case focusPorts:
		n := m.visiblePortCount(d)
		if n == 0 {
			return
		}
		m.portsCursor = wrapDetail(m.portsCursor+delta, n)
	}
}

func wrapDetail(i, n int) int {
	if n <= 0 {
		return 0
	}
	return ((i % n) + n) % n
}

// activate runs the action under the cursor of the focused group.
func (m detailModel) activate(d *AppletData) (detailModel, tea.Cmd) {
	switch m.focus {
	case focusActions:
		switch m.actionsCursor {
		case 0:
			return m, m.openInEditor(d)
		case 1:
			return m, m.openSSHShell(d)
		case 2:
			if d.canDeleteReason(m.workspace.Provider) != "" {
				return m, emitFlash("delete unavailable: "+d.canDeleteReason(m.workspace.Provider), true)
			}
			m.confirmDelete = true
		}
	case focusOptions:
		cfg := d.Config()
		switch m.optionsCursor {
		case 0:
			cur := cfg.WorkspaceSSHControlMaster(m.workspace.Provider, m.workspace.Name)
			next := !cur
			cfg.SetWorkspaceSSHControlMaster(m.workspace.Provider, m.workspace.Name, &next)
			if err := d.PersistConfig(); err != nil {
				return m, emitFlash("save: "+err.Error(), true)
			}
			// Rewrite the on-disk conf so the new setting takes effect
			// without waiting for the next PrepareSSH call.
			m.applySSHOptionsAsync(d)
			return m, emitFlash(fmt.Sprintf("ControlMaster %s", onOff(next)), false)
		case 1:
			cur := cfg.WorkspaceSSHTmux(m.workspace.Provider, m.workspace.Name)
			next := !cur
			cfg.SetWorkspaceSSHTmux(m.workspace.Provider, m.workspace.Name, &next)
			if err := d.PersistConfig(); err != nil {
				return m, emitFlash("save: "+err.Error(), true)
			}
			return m, emitFlash(fmt.Sprintf("tmux %s", onOff(next)), false)
		}
	case focusPorts:
		return m.activatePort(d)
	}
	return m, nil
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

func (m detailModel) applySSHOptionsAsync(d *AppletData) {
	go func() {
		paths := sshconfig.ResolvePaths()
		confPath := paths.WorkspaceConfigPath(m.workspace.Provider, m.workspace.Name)
		opts := sshconfig.ManagedExtrasOptions{
			ControlMaster: d.Config().WorkspaceSSHControlMaster(m.workspace.Provider, m.workspace.Name),
		}
		_, _ = sshconfig.RefreshManagedExtras(confPath, opts)
	}()
}

func (m detailModel) openInEditor(d *AppletData) tea.Cmd {
	return func() tea.Msg {
		paths := sshconfig.ResolvePaths()
		alias, ok := sshconfig.ReadExistingWorkspaceAlias(paths, m.workspace.Provider, m.workspace.Name)
		if !ok {
			// Need to prepare SSH first.
			mgr, err := d.ManagerForProvider(m.workspace.Provider)
			if err != nil {
				return flashMsg{text: err.Error(), err: true}
			}
			ws := m.workspace
			if _, err := mgr.StartWorkspace(&ws); err != nil {
				return flashMsg{text: err.Error(), err: true}
			}
			if err := mgr.EnsureReachable(&ws); err != nil {
				return flashMsg{text: err.Error(), err: true}
			}
			opts := sshconfig.ManagedExtrasOptions{
				ControlMaster: d.Config().WorkspaceSSHControlMaster(ws.Provider, ws.Name),
			}
			alias, err = mgr.PrepareSSH(paths, &ws, opts)
			if err != nil {
				return flashMsg{text: err.Error(), err: true}
			}
		}
		edName := d.Config().Editor
		ed, err := editor.ForName(edName)
		if err != nil {
			return flashMsg{text: err.Error(), err: true}
		}
		workspacePath := m.guessWorkspacePath()
		if err := ed.LaunchRemote(alias, workspacePath); err != nil {
			return flashMsg{text: err.Error(), err: true}
		}
		return flashMsg{text: fmt.Sprintf("Launched %s", ed.Name())}
	}
}

func (m detailModel) openSSHShell(d *AppletData) tea.Cmd {
	return func() tea.Msg {
		paths := sshconfig.ResolvePaths()
		alias, ok := sshconfig.ReadExistingWorkspaceAlias(paths, m.workspace.Provider, m.workspace.Name)
		if !ok {
			return flashMsg{text: "no SSH config yet — open in editor first", err: true}
		}
		useTmux := d.Config().WorkspaceSSHTmux(m.workspace.Provider, m.workspace.Name)
		go terminal.OpenSSHInTerminal(alias, m.guessWorkspacePath(), useTmux)
		return flashMsg{text: fmt.Sprintf("Opening shell to %s", alias)}
	}
}

func (m detailModel) guessWorkspacePath() string {
	if m.workspace.Provider == provider.NameCoder {
		return "/workspaces/" + m.workspace.Name
	}
	if m.workspace.Repository != "" {
		parts := strings.SplitN(m.workspace.Repository, "/", 2)
		return "/workspaces/" + parts[len(parts)-1]
	}
	return "/workspaces/" + m.workspace.Name
}

func (m detailModel) activatePort(d *AppletData) (detailModel, tea.Cmd) {
	if m.workspace.Provider != provider.NameGitHub {
		return m, nil
	}
	entry := d.PortCache(m.workspace.Name)
	if m.portsCursor >= len(entry.Ports) {
		return m, nil
	}
	p := entry.Ports[m.portsCursor]
	csName := m.workspace.Name
	if d.PortForwards().IsActive(provider.NameGitHub, csName, p.SourcePort, p.SourcePort) {
		d.PortForwards().Stop(provider.NameGitHub, csName, p.SourcePort, p.SourcePort)
		return m, emitFlash(fmt.Sprintf("Stopped localhost:%d", p.SourcePort), false)
	}
	return m, func() tea.Msg {
		if err := d.PortForwards().Start(provider.NameGitHub, csName, p.SourcePort, p.SourcePort); err != nil {
			return flashMsg{text: "forward: " + err.Error(), err: true}
		}
		return flashMsg{text: fmt.Sprintf("Forwarding localhost:%d", p.SourcePort)}
	}
}

func (m detailModel) visiblePortCount(d *AppletData) int {
	if m.workspace.Provider != provider.NameGitHub {
		return 0
	}
	entry := d.PortCache(m.workspace.Name)
	return len(entry.Ports)
}

// ── Rendering ────────────────────────────────────────────────────────

func (m detailModel) view(d *AppletData, width, height int) string {
	var b strings.Builder

	ws := m.workspace
	// Header
	stateText := stateColor(ws.State).Render(strings.ToUpper(ws.State))
	title := ws.DisplayName
	if title == "" {
		title = ws.Name
	}
	fmt.Fprintf(&b, "%s %s\n", stateText, titleStyle.Render(title))
	subtitle := dimStyle.Render(ws.Provider)
	if ws.Repository != "" {
		subtitle += dimStyle.Render("  ⌂ " + ws.Repository)
	}
	if ws.Branch != "" {
		subtitle += dimStyle.Render("  ⎇ " + ws.Branch)
	}
	b.WriteString(subtitle + "\n\n")

	// Actions
	b.WriteString(m.renderActionGroup(d) + "\n")

	// Info table
	b.WriteString(captionStyle.Render("INFO") + "\n")
	infoRows := m.infoRows(d)
	for _, r := range infoRows {
		fmt.Fprintf(&b, "  %s  %s\n", dimStyle.Render(padRight(r[0], 14)), r[1])
	}
	b.WriteString("\n")

	// SSH options
	b.WriteString(m.renderOptionsGroup(d) + "\n")

	// Ports section
	b.WriteString(m.renderPortsGroup(d) + "\n")

	if m.confirmDelete {
		b.WriteString("\n" + errorStyle.Render(fmt.Sprintf("Delete %s? (y/N) ", ws.Name)) + "\n")
	}

	b.WriteString("\n")
	hint := "tab cycle group  ↑/↓ move  enter toggle/run  r refresh ports  esc back"
	b.WriteString(dimStyle.Render(hint))

	return clampHeight(b.String(), height, width)
}

func (m detailModel) renderActionGroup(d *AppletData) string {
	header := captionStyle.Render("ACTIONS")
	if m.focus == focusActions {
		header = selectedStyle.Render("ACTIONS")
	}
	actions := []string{"Open in editor", "SSH shell", "Delete"}
	if reason := d.canDeleteReason(m.workspace.Provider); reason != "" {
		actions[2] = "Delete — " + reason
	}
	var lines []string
	lines = append(lines, header)
	for i, label := range actions {
		cursor := "  "
		if m.focus == focusActions && i == m.actionsCursor {
			cursor = cursorStyle.Render("> ")
		}
		if m.focus == focusActions && i == m.actionsCursor {
			label = selectedStyle.Render(label)
		}
		if i == 2 && d.canDeleteReason(m.workspace.Provider) != "" {
			label = dimStyle.Render(label)
		}
		lines = append(lines, cursor+label)
	}
	return strings.Join(lines, "\n")
}

func (m detailModel) renderOptionsGroup(d *AppletData) string {
	header := captionStyle.Render("SSH OPTIONS")
	if m.focus == focusOptions {
		header = selectedStyle.Render("SSH OPTIONS")
	}
	cfg := d.Config()
	cm := cfg.WorkspaceSSHControlMaster(m.workspace.Provider, m.workspace.Name)
	tx := cfg.WorkspaceSSHTmux(m.workspace.Provider, m.workspace.Name)
	rows := []struct {
		label string
		state bool
		hint  string
	}{
		{"Persistent SSH (ControlMaster)", cm, "Multiplex extra sessions over one TCP connection — instant reconnects."},
		{"Wrap shell in tmux", tx, "SSH button and `cosmonaut shell` attach to a persistent tmux session."},
	}
	var lines []string
	lines = append(lines, header)
	for i, r := range rows {
		cursor := "  "
		if m.focus == focusOptions && i == m.optionsCursor {
			cursor = cursorStyle.Render("> ")
		}
		box := "[ ]"
		if r.state {
			box = stateOK.Render("[x]")
		}
		line := fmt.Sprintf("%s%s %s", cursor, box, r.label)
		if m.focus == focusOptions && i == m.optionsCursor {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, line)
		lines = append(lines, "      "+dimStyle.Render(r.hint))
	}
	return strings.Join(lines, "\n")
}

func (m detailModel) renderPortsGroup(d *AppletData) string {
	header := captionStyle.Render("PORTS")
	if m.focus == focusPorts {
		header = selectedStyle.Render("PORTS")
	}
	var lines []string
	lines = append(lines, header)
	if m.workspace.Provider != provider.NameGitHub {
		lines = append(lines, "  "+dimStyle.Render("(port forwarding shown for GitHub codespaces)"))
		return strings.Join(lines, "\n")
	}
	entry := d.EnsurePortsCache(m.workspace.Name, nil)
	switch {
	case entry.Loading:
		lines = append(lines, "  "+dimStyle.Render("loading..."))
	case entry.Err != nil:
		lines = append(lines, "  "+errorStyle.Render("ports unavailable: "+entry.Err.Error()))
	case len(entry.Ports) == 0:
		lines = append(lines, "  "+dimStyle.Render("no forwarded ports"))
	default:
		for i, p := range entry.Ports {
			cursor := "  "
			if m.focus == focusPorts && i == m.portsCursor {
				cursor = cursorStyle.Render("> ")
			}
			active := d.PortForwards().IsActive(provider.NameGitHub, m.workspace.Name, p.SourcePort, p.SourcePort)
			marker := "○"
			if active {
				marker = stateOK.Render("●")
			}
			label := codespace.PortLabel(p)
			line := fmt.Sprintf("%s%s  %s", cursor, marker, label)
			if m.focus == focusPorts && i == m.portsCursor {
				line = selectedStyle.Render(line)
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func (m detailModel) infoRows(d *AppletData) [][2]string {
	ws := m.workspace
	rows := [][2]string{}
	rows = append(rows, [2]string{"Name", ws.Name})
	if ws.MachineName != "" {
		rows = append(rows, [2]string{"Machine", ws.MachineName})
	}
	if ws.CreatedAt != "" {
		rows = append(rows, [2]string{"Created", ws.CreatedAt})
	}
	if ws.LastUsedAt != "" {
		rows = append(rows, [2]string{"Last used", ws.LastUsedAt})
	}
	var alias string
	switch ws.Provider {
	case provider.NameGitHub:
		alias = fmt.Sprintf("cs.%s.github.dev", ws.Name)
	case provider.NameCoder:
		alias = fmt.Sprintf("%s.coder", ws.Name)
	}
	rows = append(rows, [2]string{"SSH host", alias})
	rows = append(rows, [2]string{"Path", m.guessWorkspacePath()})
	_ = d
	return rows
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}
