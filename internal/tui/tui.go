// Package tui provides interactive terminal UI components built on Bubbletea.
// It includes a filterable repository picker, a codespace selector with
// delete and back support, a work-label text input, and a generic spinner
// for long-running operations.
package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

const numberTimeout = 500 * time.Millisecond

// numberTimeoutMsg fires when the digit input window expires.
type numberTimeoutMsg struct {
	seq int
}

// escTimeoutMsg fires when the double-esc window expires.
type escTimeoutMsg struct {
	seq int
}

const escTimeout = 300 * time.Millisecond

var (
	selectedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	recommendedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	dimStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	cursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	successStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	recentStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// Status prints a colored status line to stderr.
func Status(icon, msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", successStyle.Render(icon), msg)
}

// StatusErr prints a colored error status line to stderr.
func StatusErr(icon, msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", errorStyle.Render(icon), msg)
}

// wrap wraps an index around within [0, total).
func wrap(i, total int) int {
	return ((i % total) + total) % total
}

// itemLine is the line offset from the top of the view where item 0 starts.
// "Title\n\n" = 2 lines before the first item row.
const itemLineOffset = 2

// mouseItemIndex maps a mouse Y position to a list item index, or -1.
func mouseItemIndex(y, total int) int {
	idx := y - itemLineOffset
	if idx >= 0 && idx < total {
		return idx
	}
	return -1
}

// --- Repo Selection Model ---

// RepoResult holds the outcome of the repo selection TUI.
type RepoResult struct {
	Repo string
	Quit bool
}

// RepoModel is the Bubbletea model for repository selection.
// It supports filtering by typing: the list narrows as the user types,
// and if the filter matches no existing repo, a "use <filter>" option appears.
type RepoModel struct {
	allRepos    []string // full list (codespace repos, config repos, all user repos)
	recentCount int      // how many of the leading entries are "recent"
	filtered    []int    // indices into allRepos matching the current filter
	filter      string
	cursor      int // position within filtered list (+ possible "use as-is" row)
	result      RepoResult
	done        bool
	escPending  bool // true if one esc was pressed, waiting for second
	escSeq      int
}

// NewRepoModel creates a repo selection model.
func NewRepoModel(repos []string, recentCount int) RepoModel {
	filtered := make([]int, len(repos))
	for i := range repos {
		filtered[i] = i
	}
	return RepoModel{
		allRepos:    repos,
		recentCount: recentCount,
		filtered:    filtered,
	}
}

func (m *RepoModel) refilter() {
	if m.filter == "" {
		m.filtered = make([]int, len(m.allRepos))
		for i := range m.allRepos {
			m.filtered[i] = i
		}
	} else {
		m.filtered = m.filtered[:0]
		lower := strings.ToLower(m.filter)
		for i, repo := range m.allRepos {
			if strings.Contains(strings.ToLower(repo), lower) {
				m.filtered = append(m.filtered, i)
			}
		}
	}
	if m.cursor >= m.totalChoices() {
		m.cursor = max(0, m.totalChoices()-1)
	}
}

// hasExactMatch returns true if the filter exactly matches a repo in the filtered list.
func (m RepoModel) hasExactMatch() bool {
	lower := strings.ToLower(m.filter)
	for _, idx := range m.filtered {
		if strings.ToLower(m.allRepos[idx]) == lower {
			return true
		}
	}
	return false
}

// showUseAsIs returns true when we should show a "use <filter>" option.
func (m RepoModel) showUseAsIs() bool {
	return m.filter != "" && strings.Contains(m.filter, "/") && !m.hasExactMatch()
}

func (m RepoModel) totalChoices() int {
	n := len(m.filtered)
	if m.showUseAsIs() {
		n++
	}
	if n == 0 {
		n = 1 // always at least the use-as-is row when filter has a slash
	}
	return n
}

func (m RepoModel) Init() tea.Cmd { return tea.EnableMouseCellMotion }

func (m RepoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case escTimeoutMsg:
		if msg.seq == m.escSeq {
			m.escPending = false
		}

	case tea.MouseMsg:
		total := m.totalChoices()
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.cursor = wrap(m.cursor-1, total)
		case tea.MouseButtonWheelDown:
			m.cursor = wrap(m.cursor+1, total)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionRelease {
				if idx := mouseItemIndex(msg.Y, total); idx >= 0 {
					m.cursor = idx
					return m.selectCurrent()
				}
			}
		}

	case tea.KeyMsg:
		key := msg.String()

		switch key {
		case "ctrl+c":
			m.result.Quit = true
			m.done = true
			return m, tea.Quit
		case "esc":
			if m.escPending {
				// Double esc: quit.
				m.result.Quit = true
				m.done = true
				return m, tea.Quit
			}
			// Single esc: clear filter or start double-esc timer.
			if m.filter != "" {
				m.filter = ""
				m.refilter()
			} else {
				m.escPending = true
				m.escSeq++
				seq := m.escSeq
				return m, tea.Tick(escTimeout, func(time.Time) tea.Msg {
					return escTimeoutMsg{seq: seq}
				})
			}
		case "q":
			// q quits when not typing a filter.
			if m.filter == "" {
				m.result.Quit = true
				m.done = true
				return m, tea.Quit
			}
			m.filter += key
			m.refilter()
		case "up":
			m.cursor = wrap(m.cursor-1, m.totalChoices())
		case "down":
			m.cursor = wrap(m.cursor+1, m.totalChoices())
		case "enter":
			return m.selectCurrent()
		case "backspace", "ctrl+h":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.refilter()
			}
		default:
			// Single printable character: append to filter.
			if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
				m.filter += key
				m.refilter()
			}
		}
	}
	return m, nil
}

func (m RepoModel) selectCurrent() (tea.Model, tea.Cmd) {
	if m.cursor < len(m.filtered) {
		m.result.Repo = m.allRepos[m.filtered[m.cursor]]
	} else if m.showUseAsIs() {
		m.result.Repo = m.filter
	} else {
		return m, nil // nothing to select
	}
	m.done = true
	return m, tea.Quit
}

func (m RepoModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder
	if m.filter == "" {
		b.WriteString("Select a repository (type to filter):\n\n")
	} else {
		fmt.Fprintf(&b, "Select a repository (filter: %s):\n\n", selectedStyle.Render(m.filter))
	}

	for i, repoIdx := range m.filtered {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("> ")
		}

		label := m.allRepos[repoIdx]
		if repoIdx < m.recentCount {
			label += recentStyle.Render(" (recent)")
		}

		if i == m.cursor {
			fmt.Fprintf(&b, "%s%s\n", cursor, selectedStyle.Render(label))
		} else {
			fmt.Fprintf(&b, "%s%s\n", cursor, label)
		}
	}

	if m.showUseAsIs() {
		idx := len(m.filtered)
		cursor := "  "
		if m.cursor == idx {
			cursor = cursorStyle.Render("> ")
		}
		label := fmt.Sprintf("use %s", m.filter)
		if m.cursor == idx {
			fmt.Fprintf(&b, "%s%s\n", cursor, selectedStyle.Render(label))
		} else {
			fmt.Fprintf(&b, "%s%s\n", cursor, label)
		}
	}

	if len(m.filtered) == 0 && !m.showUseAsIs() {
		b.WriteString(dimStyle.Render("  no matches: type owner/repo to use directly"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ move, type to filter, enter select, q/esc esc quit"))
	return b.String()
}

// Result returns the repo selection result.
func (m RepoModel) Result() RepoResult {
	return m.result
}

// RunRepoSelection runs the repo selection TUI.
func RunRepoSelection(repos []string, recentCount int) (string, error) {
	model := NewRepoModel(repos, recentCount)
	p := tea.NewProgram(model, tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}

	result := finalModel.(RepoModel).Result()
	if result.Quit {
		os.Exit(0)
	}
	return result.Repo, nil
}

// --- Workspace Selection Model ---

// SelectResult holds the outcome of the workspace selection TUI.
type SelectResult struct {
	Selected *provider.Workspace // nil means "create new"
	Delete   *provider.Workspace // non-nil means user wants to delete this workspace
	Quit     bool
	Back     bool // user wants to go back to repo selection
}

// SelectModel is the Bubbletea model for workspace selection.
type SelectModel struct {
	workspaces     []provider.Workspace
	target         config.Target
	dryRun         bool
	allowBack      bool // whether esc means "back" instead of "quit"
	cursor         int
	recommendedIdx int // -1 if none
	result         SelectResult
	done           bool
	repo           string
	numberBuf      string
	numberSeq      int
	escPending     bool
	escSeq         int
}

// NewSelectModel creates a selection model.
// If allowBack is true, esc/backspace signals "go back" instead of quit.
func NewSelectModel(workspaces []provider.Workspace, target config.Target, dryRun, allowBack bool) SelectModel {
	matches := provider.FindMatching(workspaces, &target)
	recommended := -1
	if len(matches) == 1 {
		for i, ws := range workspaces {
			if ws.Name == matches[0].Name {
				recommended = i
				break
			}
		}
	}

	return SelectModel{
		workspaces:     workspaces,
		target:         target,
		dryRun:         dryRun,
		allowBack:      allowBack,
		cursor:         0,
		recommendedIdx: recommended,
		repo:           target.Repository,
	}
}

func (m SelectModel) Init() tea.Cmd { return tea.EnableMouseCellMotion }

func (m SelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	totalChoices := len(m.workspaces) + 1

	switch msg := msg.(type) {
	case numberTimeoutMsg:
		if msg.seq == m.numberSeq && m.numberBuf != "" {
			m.numberBuf = ""
			return m.selectCurrent()
		}

	case escTimeoutMsg:
		if msg.seq == m.escSeq {
			m.escPending = false
			// Single esc expired: go back if allowed.
			if m.allowBack {
				m.result.Back = true
				m.done = true
				return m, tea.Quit
			}
			m.result.Quit = true
			m.done = true
			return m, tea.Quit
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.cursor = wrap(m.cursor-1, totalChoices)
		case tea.MouseButtonWheelDown:
			m.cursor = wrap(m.cursor+1, totalChoices)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionRelease {
				if idx := mouseItemIndex(msg.Y, totalChoices); idx >= 0 {
					m.cursor = idx
					return m.selectCurrent()
				}
			}
		}

	case tea.KeyMsg:
		key := msg.String()

		if len(key) != 1 || key[0] < '0' || key[0] > '9' {
			m.numberBuf = ""
		}

		switch key {
		case "ctrl+c":
			m.result.Quit = true
			m.done = true
			return m, tea.Quit
		case "esc":
			if m.escPending {
				// Double esc: always quit, even if allowBack.
				m.result.Quit = true
				m.done = true
				return m, tea.Quit
			}
			// Start double-esc timer. If it expires, go back or quit.
			m.escPending = true
			m.escSeq++
			seq := m.escSeq
			return m, tea.Tick(escTimeout, func(time.Time) tea.Msg {
				return escTimeoutMsg{seq: seq}
			})
		case "backspace", "ctrl+h":
			if m.allowBack {
				m.result.Back = true
				m.done = true
				return m, tea.Quit
			}
		case "q":
			m.result.Quit = true
			m.done = true
			return m, tea.Quit
		case "up", "k":
			m.cursor = wrap(m.cursor-1, totalChoices)
		case "down", "j":
			m.cursor = wrap(m.cursor+1, totalChoices)
		case "enter":
			m.numberBuf = ""
			return m.selectCurrent()
		case "d", "x":
			if m.cursor < len(m.workspaces) {
				ws := m.workspaces[m.cursor]
				m.result.Delete = &ws
				m.done = true
				return m, tea.Quit
			}
		default:
			if key >= "0" && key <= "9" {
				m.numberBuf += key
				if n, err := strconv.Atoi(m.numberBuf); err == nil && n >= 1 && n <= totalChoices {
					m.cursor = n - 1
				}
				m.numberSeq++
				seq := m.numberSeq
				return m, tea.Tick(numberTimeout, func(time.Time) tea.Msg {
					return numberTimeoutMsg{seq: seq}
				})
			}
		}
	}
	return m, nil
}

func (m SelectModel) selectCurrent() (tea.Model, tea.Cmd) {
	if m.cursor < len(m.workspaces) {
		ws := m.workspaces[m.cursor]
		m.result.Selected = &ws
	} else {
		m.result.Selected = nil // create new
	}
	m.done = true
	return m, tea.Quit
}

func (m SelectModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Existing workspaces found for %s:\n\n", m.repo)

	for i, ws := range m.workspaces {
		recommended := i == m.recommendedIdx
		desc := provider.DescribeWorkspace(&ws, recommended)

		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("> ")
		}

		num := fmt.Sprintf("%d. ", i+1)
		if recommended {
			desc = recommendedStyle.Render(desc)
		}

		if i == m.cursor {
			fmt.Fprintf(&b, "%s%s%s\n", cursor, selectedStyle.Render(num), desc)
		} else {
			fmt.Fprintf(&b, "%s%s%s\n", cursor, dimStyle.Render(num), desc)
		}
	}

	// "Create new" option
	createIdx := len(m.workspaces)
	cursor := "  "
	if m.cursor == createIdx {
		cursor = cursorStyle.Render("> ")
	}
	num := fmt.Sprintf("%d. ", createIdx+1)
	label := "create a new workspace"
	if m.dryRun {
		label += " (disabled by --dry-run)"
	}
	if m.cursor == createIdx {
		fmt.Fprintf(&b, "%s%s%s\n", cursor, selectedStyle.Render(num), selectedStyle.Render(label))
	} else {
		fmt.Fprintf(&b, "%s%s%s\n", cursor, dimStyle.Render(num), label)
	}

	b.WriteString("\n")
	hint := "↑/↓ move, # or enter select, d delete"
	if m.allowBack {
		hint += ", esc back, esc esc quit"
	} else {
		hint += ", q/esc quit"
	}
	if m.numberBuf != "" {
		hint = fmt.Sprintf("typing: %s…", m.numberBuf)
	}
	b.WriteString(dimStyle.Render(hint))
	return b.String()
}

// Result returns the selection result after the program exits.
func (m SelectModel) Result() SelectResult {
	return m.result
}

// --- Work Label Model ---

// WorkLabelResult holds the outcome of the work label input TUI.
type WorkLabelResult struct {
	Label string
	Quit  bool
}

// WorkLabelModel is the Bubbletea model for work label input.
type WorkLabelModel struct {
	textInput textinput.Model
	result    WorkLabelResult
	done      bool
	hint      string
}

// NewWorkLabelModel creates a work label input model.
func NewWorkLabelModel() WorkLabelModel {
	ti := textinput.New()
	ti.Placeholder = "e.g., fix indexer health checks"
	ti.Focus()
	ti.CharLimit = 100
	ti.Width = 60

	return WorkLabelModel{
		textInput: ti,
	}
}

func (m WorkLabelModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m WorkLabelModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.result.Quit = true
			m.done = true
			return m, tea.Quit
		case "enter":
			value := strings.TrimSpace(m.textInput.Value())
			if value == "" {
				m.hint = "Enter a short label so the new codespace is easier to recognize later."
				return m, nil
			}
			m.result.Label = value
			m.done = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m WorkLabelModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString("What work are you planning to do in this codespace?\n\n")
	b.WriteString(m.textInput.View())
	b.WriteString("\n")
	if m.hint != "" {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(m.hint))
		b.WriteString("\n")
	}
	return b.String()
}

// Result returns the work label result after the program exits.
func (m WorkLabelModel) Result() WorkLabelResult {
	return m.result
}

// --- Spinner Model ---

// SpinnerResult holds the outcome of a spinner task.
type SpinnerResult struct {
	Err  error
	Quit bool
}

type taskDoneMsg struct {
	err error
}

// SpinnerModel runs a background task with a spinner.
type SpinnerModel struct {
	spinner spinner.Model
	message string
	result  SpinnerResult
	done    bool
	task    func() error
}

// NewSpinnerModel creates a spinner that runs the given task in the background.
func NewSpinnerModel(message string, task func() error) SpinnerModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	return SpinnerModel{
		spinner: s,
		message: message,
		task:    task,
	}
}

func (m SpinnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		return taskDoneMsg{err: m.task()}
	})
}

func (m SpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case taskDoneMsg:
		m.result.Err = msg.err
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.result.Quit = true
			m.done = true
			return m, tea.Quit
		}
	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m SpinnerModel) View() string {
	if m.done {
		return ""
	}
	return fmt.Sprintf("%s %s", m.spinner.View(), m.message)
}

// Result returns the spinner result.
func (m SpinnerModel) Result() SpinnerResult {
	return m.result
}

// RunWithSpinner runs a task with a spinner, printing a success/failure line when done.
func RunWithSpinner(message string, task func() error) error {
	model := NewSpinnerModel(message, task)
	p := tea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	result := finalModel.(SpinnerModel).Result()
	if result.Quit {
		os.Exit(0)
	}
	if result.Err != nil {
		StatusErr("✗", message)
		return result.Err
	}
	return nil
}

// RunWithSpinnerResult runs a task that returns a value, with a spinner.
func RunWithSpinnerResult[T any](message string, task func() (T, error)) (T, error) {
	var result T
	err := RunWithSpinner(message, func() error {
		var e error
		result, e = task()
		return e
	})
	return result, err
}
