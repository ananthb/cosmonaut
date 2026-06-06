package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/doctor"
	"github.com/linuskendall/cosmonaut/internal/terminal"
)

// settingsSection identifies which group of rows has focus.
type settingsSection int

const (
	secHealth settingsSection = iota
	secAuth
	secEditor
	secDaemon
	secTarget
	secActions
)

// settingsModel mirrors the GUI's Preferences page: health checks (with
// inline fix), GitHub auth, editor entry, daemon options, default-target
// options, and an "edit config file" action.
type settingsModel struct {
	section settingsSection

	healthCursor int
	editorInput  textinput.Model
	daemonField  int // 0=hotkeyAction, 1=inhibitSleep
	daemonHotkey int
	daemonSleep  int
	targetField  int // 0=autoStop, 1=preWarm
	targetStop   int
	targetWarm   int
}

func newSettingsModel(d *AppletData) settingsModel {
	m := settingsModel{}
	ed := textinput.New()
	ed.Placeholder = "zed (default)"
	ed.CharLimit = 80
	ed.Width = 40
	m.editorInput = ed
	m.syncFromConfig(d.Config())
	return m
}

func (m *settingsModel) syncFromConfig(cfg *config.Config) {
	m.editorInput.SetValue(cfg.Editor)

	hotkeyActions := []string{"picker", "previous", "default"}
	sleepActions := []string{"off", "sleep", "sleep+shutdown"}
	dm := cfg.Daemon
	if dm == nil {
		dm = &config.DaemonConfig{}
	}
	m.daemonHotkey = indexOf(hotkeyActions, defaultStr(dm.HotkeyAction, "picker"))
	m.daemonSleep = indexOf(sleepActions, defaultStr(dm.InhibitSleep, "off"))

	stops := []string{"off", "15m", "30m", "1h"}
	warms := []string{"off", "08:00", "09:00", "10:00"}
	if cfg.DefaultTarget != "" {
		t := cfg.Targets[cfg.DefaultTarget]
		m.targetStop = indexOf(stops, defaultStr(t.AutoStop, "off"))
		m.targetWarm = indexOf(warms, defaultStr(t.PreWarm, "off"))
	}
}

func indexOf(opts []string, v string) int {
	for i, o := range opts {
		if o == v {
			return i
		}
	}
	return 0
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (m settingsModel) Init() tea.Cmd { return nil }

func (m settingsModel) update(msg tea.Msg, d *AppletData) (settingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg, d)
	}
	return m, nil
}

func (m settingsModel) handleKey(msg tea.KeyMsg, d *AppletData) (settingsModel, tea.Cmd) {
	key := msg.String()

	// secEditor is a text input — most keys go to the textinput model so
	// the user can type freely. Navigation keys (tab/shift+tab/esc) still
	// move focus, persisting the input value before they do.
	if m.section == secEditor {
		switch key {
		case "esc":
			d.Config().Editor = strings.TrimSpace(m.editorInput.Value())
			cmd := m.persistAndAck(d)
			return m, tea.Batch(cmd, switchTo(viewList, nil))
		case "tab":
			d.Config().Editor = strings.TrimSpace(m.editorInput.Value())
			cmd := m.persistAndAck(d)
			m.section = (m.section + 1) % 6
			m.editorInput.Blur()
			return m, cmd
		case "shift+tab":
			d.Config().Editor = strings.TrimSpace(m.editorInput.Value())
			cmd := m.persistAndAck(d)
			m.section = (m.section + 5) % 6
			m.editorInput.Blur()
			return m, cmd
		default:
			if !m.editorInput.Focused() {
				m.editorInput.Focus()
			}
			var cmd tea.Cmd
			m.editorInput, cmd = m.editorInput.Update(msg)
			return m, cmd
		}
	}

	switch key {
	case "esc":
		return m, switchTo(viewList, nil)
	case "tab":
		m.section = (m.section + 1) % 6
		return m, nil
	case "shift+tab":
		m.section = (m.section + 5) % 6
		return m, nil
	case "up", "k":
		m = m.moveCursor(-1, d)
	case "down", "j":
		m = m.moveCursor(1, d)
	case "left", "h":
		m = m.cycleValue(-1, d)
		return m, m.persistAndAck(d)
	case "right", "l":
		m = m.cycleValue(1, d)
		return m, m.persistAndAck(d)
	case "enter", " ":
		return m.activate(d)
	}
	return m, nil
}

func (m settingsModel) moveCursor(delta int, d *AppletData) settingsModel {
	switch m.section {
	case secHealth:
		n := len(doctor.Catalog(d.ListErr))
		if n == 0 {
			return m
		}
		m.healthCursor = wrapDetail(m.healthCursor+delta, n)
	case secDaemon:
		m.daemonField = wrapDetail(m.daemonField+delta, 2)
	case secTarget:
		m.targetField = wrapDetail(m.targetField+delta, 2)
	}
	return m
}

// cycleValue moves through enum values (left/right) for the focused field.
// For health/auth/actions sections — which are list/button-based — this is
// a no-op; up/down + enter handle navigation there.
func (m settingsModel) cycleValue(delta int, d *AppletData) settingsModel {
	switch m.section {
	case secEditor:
		// Editor is a free-text field — left/right has no enum to cycle.
	case secDaemon:
		if d.Config().Daemon == nil {
			d.Config().Daemon = &config.DaemonConfig{}
		}
		switch m.daemonField {
		case 0:
			actions := []string{"picker", "previous", "default"}
			m.daemonHotkey = wrapDetail(m.daemonHotkey+delta, len(actions))
			d.Config().Daemon.HotkeyAction = actions[m.daemonHotkey]
		case 1:
			modes := []string{"off", "sleep", "sleep+shutdown"}
			m.daemonSleep = wrapDetail(m.daemonSleep+delta, len(modes))
			d.Config().Daemon.InhibitSleep = modes[m.daemonSleep]
		}
	case secTarget:
		if d.Config().DefaultTarget == "" {
			return m
		}
		t := d.Config().Targets[d.Config().DefaultTarget]
		switch m.targetField {
		case 0:
			stops := []string{"off", "15m", "30m", "1h"}
			m.targetStop = wrapDetail(m.targetStop+delta, len(stops))
			if stops[m.targetStop] == "off" {
				t.AutoStop = ""
			} else {
				t.AutoStop = stops[m.targetStop]
			}
		case 1:
			warms := []string{"off", "08:00", "09:00", "10:00"}
			m.targetWarm = wrapDetail(m.targetWarm+delta, len(warms))
			if warms[m.targetWarm] == "off" {
				t.PreWarm = ""
			} else {
				t.PreWarm = warms[m.targetWarm]
			}
		}
		d.Config().Targets[d.Config().DefaultTarget] = t
	}
	return m
}

func (m settingsModel) persistAndAck(d *AppletData) tea.Cmd {
	switch m.section {
	case secEditor, secDaemon, secTarget:
		if err := d.PersistConfig(); err != nil {
			return emitFlash("save: "+err.Error(), true)
		}
		return emitFlash("Saved", false)
	}
	return nil
}

func (m settingsModel) activate(d *AppletData) (settingsModel, tea.Cmd) {
	switch m.section {
	case secHealth:
		checks := doctor.Catalog(d.ListErr)
		if m.healthCursor >= len(checks) {
			return m, nil
		}
		c := checks[m.healthCursor]
		if c.Status() == nil {
			return m, emitFlash("already passing", false)
		}
		if c.HasInProcessFix() {
			return m, func() tea.Msg {
				if err := c.Fix(); err != nil {
					return flashMsg{text: "fix: " + err.Error(), err: true}
				}
				return flashMsg{text: "Fix applied — re-check on next render"}
			}
		}
		if c.HasTerminalFix() {
			cmd := c.FixCommand()
			return m, func() tea.Msg {
				terminal.OpenCommandInTerminal(cmd + `; echo; echo "Press enter to close"; read _`)
				return flashMsg{text: "Fix command opened in terminal"}
			}
		}
	case secAuth:
		return m.toggleAuth(d)
	case secActions:
		return m, func() tea.Msg {
			openFile(d.ConfigPath())
			return flashMsg{text: "Opened config in default editor"}
		}
	}
	return m, nil
}

func (m settingsModel) toggleAuth(d *AppletData) (settingsModel, tea.Cmd) {
	runner := codespace.DefaultGHRunner{}
	authed := codespace.EnsureGHAuth(runner) == nil
	if authed {
		return m, func() tea.Msg {
			_, err := runner.Run([]string{"auth", "logout", "--hostname", "github.com"})
			if err != nil {
				return flashMsg{text: "gh auth logout: " + err.Error(), err: true}
			}
			return flashMsg{text: "Logged out"}
		}
	}
	return m, func() tea.Msg {
		terminal.OpenCommandInTerminal(`gh auth login --web --hostname github.com; echo; echo "Press enter to close"; read _`)
		return flashMsg{text: "Login flow opened in terminal"}
	}
}

// ── Rendering ───────────────────────────────────────────────────────

func (m settingsModel) view(d *AppletData, width, height int) string {
	var b strings.Builder
	b.WriteString(captionStyle.Render("SETTINGS") + "\n\n")

	b.WriteString(m.renderHealth(d) + "\n")
	b.WriteString(m.renderAuth(d) + "\n")
	b.WriteString(m.renderEditor(d) + "\n")
	b.WriteString(m.renderDaemon(d) + "\n")
	b.WriteString(m.renderTarget(d) + "\n")
	b.WriteString(m.renderActions(d) + "\n")

	b.WriteString("\n" + dimStyle.Render("tab/shift+tab section  ↑/↓ row  ←/→ value  enter run  esc back"))
	return clampHeight(b.String(), height, width)
}

func (m settingsModel) sectionHeader(name string, sec settingsSection) string {
	if m.section == sec {
		return selectedStyle.Render(name)
	}
	return captionStyle.Render(name)
}

func (m settingsModel) renderHealth(d *AppletData) string {
	checks := doctor.Catalog(d.ListErr)
	var lines []string
	lines = append(lines, m.sectionHeader("HEALTH", secHealth))
	var failing, passing []doctor.Check
	for _, c := range checks {
		if c.Status() == nil {
			passing = append(passing, c)
		} else {
			failing = append(failing, c)
		}
	}
	idx := 0
	for _, c := range failing {
		mark := stateBad.Render("✗")
		if c.Status() != nil && c.Status().Severity == doctor.SeverityWarning {
			mark = stateWarn.Render("!")
		}
		cursor := "  "
		if m.section == secHealth && idx == m.healthCursor {
			cursor = cursorStyle.Render("> ")
		}
		title := c.Title
		if m.section == secHealth && idx == m.healthCursor {
			title = selectedStyle.Render(title)
		}
		lines = append(lines, fmt.Sprintf("%s%s %s", cursor, mark, title))
		if c.Status() != nil {
			lines = append(lines, "      "+dimStyle.Render(c.Status().Summary))
		}
		idx++
	}
	if len(failing) == 0 {
		lines = append(lines, "  "+stateOK.Render("✓ ")+dimStyle.Render(fmt.Sprintf("All OK (%d checks)", len(passing))))
	} else if len(passing) > 0 {
		lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("(%d other checks passing)", len(passing))))
	}
	return strings.Join(lines, "\n")
}

func (m settingsModel) renderAuth(d *AppletData) string {
	_ = d
	runner := codespace.DefaultGHRunner{}
	authed := codespace.EnsureGHAuth(runner) == nil
	state := stateBad.Render("not authenticated")
	if authed {
		state = stateOK.Render("authenticated")
	}
	header := m.sectionHeader("AUTH", secAuth)
	hint := "enter to log " + map[bool]string{true: "out", false: "in"}[authed]
	return fmt.Sprintf("%s\n  GitHub: %s  %s", header, state, dimStyle.Render(hint))
}

func (m settingsModel) renderEditor(_ *AppletData) string {
	header := m.sectionHeader("EDITOR", secEditor)
	return fmt.Sprintf("%s\n  %s\n  %s", header, m.editorInput.View(),
		dimStyle.Render("any binary on PATH; empty = zed default"))
}

func (m settingsModel) renderDaemon(d *AppletData) string {
	header := m.sectionHeader("DAEMON", secDaemon)
	actions := []string{"picker", "previous", "default"}
	modes := []string{"off", "sleep", "sleep+shutdown"}
	rows := [][2]string{
		{"Hotkey action", actions[m.daemonHotkey]},
		{"Inhibit sleep", modes[m.daemonSleep]},
	}
	var lines []string
	lines = append(lines, header)
	for i, r := range rows {
		cursor := "  "
		if m.section == secDaemon && i == m.daemonField {
			cursor = cursorStyle.Render("> ")
		}
		label := r[0]
		value := r[1]
		if m.section == secDaemon && i == m.daemonField {
			value = selectedStyle.Render("‹ " + value + " ›")
			label = selectedStyle.Render(label)
		}
		lines = append(lines, fmt.Sprintf("%s%s  %s", cursor, padRight(label, 16), value))
	}
	return strings.Join(lines, "\n")
}

func (m settingsModel) renderTarget(d *AppletData) string {
	header := m.sectionHeader("DEFAULT TARGET", secTarget)
	cfg := d.Config()
	if cfg.DefaultTarget == "" {
		return header + "\n  " + dimStyle.Render("(no defaultTarget set in config)")
	}
	stops := []string{"off", "15m", "30m", "1h"}
	warms := []string{"off", "08:00", "09:00", "10:00"}
	rows := [][2]string{
		{"Auto-stop", stops[m.targetStop]},
		{"Pre-warm", warms[m.targetWarm]},
	}
	var lines []string
	lines = append(lines, header+dimStyle.Render(" — "+cfg.DefaultTarget))
	for i, r := range rows {
		cursor := "  "
		if m.section == secTarget && i == m.targetField {
			cursor = cursorStyle.Render("> ")
		}
		label, value := r[0], r[1]
		if m.section == secTarget && i == m.targetField {
			value = selectedStyle.Render("‹ " + value + " ›")
			label = selectedStyle.Render(label)
		}
		lines = append(lines, fmt.Sprintf("%s%s  %s", cursor, padRight(label, 16), value))
	}
	return strings.Join(lines, "\n")
}

func (m settingsModel) renderActions(d *AppletData) string {
	header := m.sectionHeader("ACTIONS", secActions)
	cursor := "  "
	label := "Edit config file"
	if m.section == secActions {
		cursor = cursorStyle.Render("> ")
		label = selectedStyle.Render(label)
	}
	return fmt.Sprintf("%s\n%s%s  %s", header, cursor, label, dimStyle.Render(d.ConfigPath()))
}

// openFile asks the OS to open path with its default handler. Used by the
// "Edit config file" action — the user's $EDITOR or default JSON viewer
// takes it from there.
func openFile(path string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", path).Start()
	case "linux":
		_ = exec.Command("xdg-open", path).Start()
	}
}
