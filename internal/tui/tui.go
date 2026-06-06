// Package tui provides terminal UI components built on Bubbletea.
//
// The package owns two surfaces:
//
//   - The persistent applet (AppletModel et al. in applet*.go) — a full-screen
//     Bubbletea program that mirrors the Fyne GUI's workspace list, detail,
//     settings, and create views.
//   - A small set of shared utilities used by both the applet and the
//     scripted CLI flow: colored status lines, a generic spinner helper, and
//     the shared lipgloss palette.
package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// escTimeout is the window between two `esc` presses that the applet
// treats as a quit chord; a single esc that doesn't get a follow-up
// inside this window clears the current filter or closes a modal.
const escTimeout = 300 * time.Millisecond

// escTimeoutMsg fires when the double-esc window expires. Sub-models in
// the applet match on the matching seq to ignore stale timers.
type escTimeoutMsg struct {
	seq int
}

// Shared lipgloss styles. Re-exported informally by being referenced from
// every applet sub-view, so the whole UI shares one palette.
var (
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// Status prints a colored status line to stderr. Used by the CLI launch
// path to surface a one-line "ok / waiting / done" without owning a full
// Bubbletea program.
func Status(icon, msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", successStyle.Render(icon), msg)
}

// StatusErr prints a colored error status line to stderr.
func StatusErr(icon, msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", errorStyle.Render(icon), msg)
}

// SpinnerResult holds the outcome of a spinner task.
type SpinnerResult struct {
	Err  error
	Quit bool
}

// taskDoneMsg is the internal Bubbletea message that signals the
// background task wrapped by SpinnerModel has finished.
type taskDoneMsg struct {
	err error
}

// SpinnerModel runs a single background task with a spinner. Used by the
// CLI launch path for "Listing workspaces", "Preparing SSH config", etc.
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

// Init kicks off both the spinner ticker and the background task.
func (m SpinnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		return taskDoneMsg{err: m.task()}
	})
}

// Update advances the spinner and exits when the background task signals done.
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

// View renders the spinner + message line.
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

// RunWithSpinner runs a task with a spinner, exiting the process with
// status 0 if the user interrupts with ctrl+c.
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
