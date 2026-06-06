package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// appletView identifies which sub-view is currently focused.
type appletView int

const (
	viewList appletView = iota
	viewDetail
	viewSettings
	viewCreate
)

func (v appletView) label() string {
	switch v {
	case viewList:
		return "Workspaces"
	case viewDetail:
		return "Detail"
	case viewSettings:
		return "Settings"
	case viewCreate:
		return "Create"
	}
	return ""
}

// pollTickInterval is the background refresh cadence for the applet poller.
// Matches the GUI's 15m backstop; the user can also trigger an immediate
// poll with `r` from the list view.
const pollTickInterval = 15 * time.Minute

// pollDoneMsg is dispatched when a Poll() call finishes, so the applet can
// re-render with fresh workspace data.
type pollDoneMsg struct{ result PollResult }

// pollTickMsg fires on the backstop ticker.
type pollTickMsg struct{}

// flashMsg shows a transient status line at the bottom of the applet for
// flashTTL. Useful for "Deleted X", "Port forward started", etc.
type flashMsg struct {
	text string
	err  bool
}

type flashExpireMsg struct{ seq int }

const flashTTL = 4 * time.Second

// switchViewMsg requests a view change. Sub-views emit it to navigate (e.g.
// list emits switchViewMsg{viewDetail, ws} when the user presses Enter).
type switchViewMsg struct {
	view      appletView
	workspace *provider.Workspace
}

// reloadMsg asks the foreground view to rebuild itself from current data.
// Used after a delete, port-forward toggle, etc. so the model picks up new
// state without a full re-render of unrelated sub-views.
type reloadMsg struct{}

// AppletModel is the top-level Bubbletea model. It owns the data layer and
// the currently-mounted sub-view, and routes Update / View calls to it.
type AppletModel struct {
	data *AppletData

	view     appletView
	list     listModel
	detail   detailModel
	settings settingsModel
	create   createModel

	width  int
	height int

	flash    string
	flashErr bool
	flashSeq int

	err error
}

// NewAppletModel constructs the top-level model. Pass the shared data
// layer; the model will trigger an initial poll on Init.
func NewAppletModel(data *AppletData) AppletModel {
	m := AppletModel{
		data: data,
		view: viewList,
	}
	m.list = newListModel(data)
	m.settings = newSettingsModel(data)
	return m
}

func (m AppletModel) Init() tea.Cmd {
	return tea.Batch(
		m.list.Init(),
		m.pollCmd(),
		tea.Tick(pollTickInterval, func(time.Time) tea.Msg { return pollTickMsg{} }),
	)
}

// pollCmd returns a tea.Cmd that runs a poll in the background and dispatches
// pollDoneMsg when it finishes.
func (m AppletModel) pollCmd() tea.Cmd {
	return func() tea.Msg {
		return pollDoneMsg{result: m.data.Poll()}
	}
}

func (m AppletModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Forward to all sub-views so they can lay out.
		listM, c1 := m.list.update(msg, m.data)
		m.list = listM
		detailM, c2 := m.detail.update(msg, m.data)
		m.detail = detailM
		settingsM, c3 := m.settings.update(msg, m.data)
		m.settings = settingsM
		return m, tea.Batch(c1, c2, c3)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			m = m.cycleView()
			return m, m.activeInit()
		case "shift+tab":
			m = m.cycleViewBack()
			return m, m.activeInit()
		}

	case pollTickMsg:
		// Schedule the next tick + kick a fresh poll. Don't re-block the
		// foreground; the poller serializes itself internally.
		return m, tea.Batch(
			m.pollCmd(),
			tea.Tick(pollTickInterval, func(time.Time) tea.Msg { return pollTickMsg{} }),
		)

	case pollDoneMsg:
		// Sub-views read directly from data on render; just re-issue a
		// reload so the list rebuilds its row index.
		listM, cmd := m.list.update(reloadMsg{}, m.data)
		m.list = listM
		return m, cmd

	case flashMsg:
		m.flash = msg.text
		m.flashErr = msg.err
		m.flashSeq++
		seq := m.flashSeq
		return m, tea.Tick(flashTTL, func(time.Time) tea.Msg {
			return flashExpireMsg{seq: seq}
		})

	case flashExpireMsg:
		if msg.seq == m.flashSeq {
			m.flash = ""
		}
		return m, nil

	case switchViewMsg:
		m.view = msg.view
		var cmd tea.Cmd
		switch msg.view {
		case viewDetail:
			if msg.workspace != nil {
				m.detail = newDetailModel(m.data, *msg.workspace)
				cmd = m.detail.Init()
			}
		case viewList:
			cmd = m.list.Init()
		case viewSettings:
			cmd = m.settings.Init()
		case viewCreate:
			m.create = newCreateModel(m.data)
			cmd = m.create.Init()
		}
		return m, cmd
	}

	// Forward everything else to the active sub-view.
	var cmd tea.Cmd
	switch m.view {
	case viewList:
		m.list, cmd = m.list.update(msg, m.data)
	case viewDetail:
		m.detail, cmd = m.detail.update(msg, m.data)
	case viewSettings:
		m.settings, cmd = m.settings.update(msg, m.data)
	case viewCreate:
		m.create, cmd = m.create.update(msg, m.data)
	}
	return m, cmd
}

func (m AppletModel) View() string {
	header := m.renderHeader()
	var body string
	switch m.view {
	case viewList:
		body = m.list.view(m.data, m.width, m.height-headerLines-footerLines)
	case viewDetail:
		body = m.detail.view(m.data, m.width, m.height-headerLines-footerLines)
	case viewSettings:
		body = m.settings.view(m.data, m.width, m.height-headerLines-footerLines)
	case viewCreate:
		body = m.create.view(m.data, m.width, m.height-headerLines-footerLines)
	}
	footer := m.renderFooter()
	return strings.Join([]string{header, body, footer}, "\n")
}

// headerLines / footerLines are the row budgets reserved for the persistent
// chrome so sub-views can clamp their own content.
const (
	headerLines = 2
	footerLines = 2
)

func (m AppletModel) renderHeader() string {
	tabs := []string{}
	for _, v := range []appletView{viewList, viewDetail, viewSettings} {
		label := v.label()
		if v == viewDetail && m.detail.workspace.Name == "" {
			// Don't show Detail tab when no workspace is selected.
			continue
		}
		if v == m.view {
			tabs = append(tabs, tabActiveStyle.Render(" "+label+" "))
		} else {
			tabs = append(tabs, tabIdleStyle.Render(" "+label+" "))
		}
	}
	bar := strings.Join(tabs, dimStyle.Render(" · "))
	title := titleStyle.Render("cosmonaut")
	right := dimStyle.Render("tab: switch  ?: help  q: quit")
	gap := strings.Repeat(" ", maxInt(1, m.width-lipgloss.Width(title)-lipgloss.Width(bar)-lipgloss.Width(right)-2))
	return title + "  " + bar + gap + right + "\n" + dimStyle.Render(strings.Repeat("─", maxInt(0, m.width)))
}

func (m AppletModel) renderFooter() string {
	if m.flash != "" {
		if m.flashErr {
			return "\n" + errorStyle.Render("✗ "+m.flash)
		}
		return "\n" + successStyle.Render("✓ "+m.flash)
	}
	return "\n" + dimStyle.Render(" ")
}

func (m AppletModel) cycleView() AppletModel {
	switch m.view {
	case viewList:
		if m.detail.workspace.Name != "" {
			m.view = viewDetail
		} else {
			m.view = viewSettings
		}
	case viewDetail:
		m.view = viewSettings
	case viewSettings:
		m.view = viewList
	}
	return m
}

func (m AppletModel) cycleViewBack() AppletModel {
	switch m.view {
	case viewList:
		m.view = viewSettings
	case viewSettings:
		if m.detail.workspace.Name != "" {
			m.view = viewDetail
		} else {
			m.view = viewList
		}
	case viewDetail:
		m.view = viewList
	}
	return m
}

func (m AppletModel) activeInit() tea.Cmd {
	switch m.view {
	case viewList:
		return m.list.Init()
	case viewDetail:
		return m.detail.Init()
	case viewSettings:
		return m.settings.Init()
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// styles shared across views — kept in one place so the applet has a
// consistent visual identity.
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true)
	tabActiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true)
	tabIdleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))
	captionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Bold(true)
	stateOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stateWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stateBad  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func stateColor(state string) lipgloss.Style {
	switch strings.ToLower(state) {
	case "available", "started", "ready", "running", "connected":
		return stateOK
	case "starting", "pending":
		return stateWarn
	case "stopped":
		return dimStyle
	}
	return stateBad
}

func stateIconChar(state string) string {
	switch strings.ToLower(state) {
	case "available", "started", "ready", "running", "connected":
		return "●"
	case "starting", "pending":
		return "◐"
	}
	return "○"
}

// RunApplet starts the persistent TUI applet using the given data layer.
// Blocks until the user quits.
func RunApplet(data *AppletData) error {
	model := NewAppletModel(data)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// emitFlash returns a tea.Cmd that pushes a transient status banner.
func emitFlash(text string, isErr bool) tea.Cmd {
	return func() tea.Msg {
		return flashMsg{text: text, err: isErr}
	}
}

// switchTo returns a tea.Cmd that navigates to a different view.
func switchTo(v appletView, ws *provider.Workspace) tea.Cmd {
	return func() tea.Msg {
		return switchViewMsg{view: v, workspace: ws}
	}
}

