package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/linuskendall/cosmonaut/internal/config"
)

func TestNewAppletDataDefaults(t *testing.T) {
	cfg := &config.Config{Targets: map[string]config.Target{}}
	d := NewAppletData(cfg, "/tmp/cosmonaut.config.json")
	if d.Config() != cfg {
		t.Error("Config() should return the same pointer that was passed in")
	}
	if d.ConfigPath() != "/tmp/cosmonaut.config.json" {
		t.Errorf("ConfigPath() = %q, want /tmp/cosmonaut.config.json", d.ConfigPath())
	}
	if d.PortForwards() == nil {
		t.Error("PortForwards() should be initialised")
	}
	if got := d.Codespaces(); got == nil {
		t.Error("Codespaces() should return a non-nil (empty) slice")
	}
	if got := d.Workspaces(); got == nil {
		t.Error("Workspaces() should return a non-nil (empty) slice")
	}
}

func TestNewAppletDataAcceptsNilConfig(t *testing.T) {
	d := NewAppletData(nil, "")
	if d.Config() == nil {
		t.Error("Config() should not be nil after NewAppletData(nil, ...)")
	}
	if err := d.PersistConfig(); err != nil {
		t.Errorf("PersistConfig() with empty path should be a no-op, got %v", err)
	}
}

func TestAppletModelInitialView(t *testing.T) {
	d := NewAppletData(&config.Config{}, "")
	m := NewAppletModel(d)
	if m.view != viewList {
		t.Errorf("initial view = %v, want viewList", m.view)
	}
}

func TestListModelRebuildEmpty(t *testing.T) {
	d := NewAppletData(&config.Config{}, "")
	m := newListModel(d)
	// With no workspaces, rebuild should produce a single "no workspaces"
	// hint row.
	if len(m.rows) != 1 {
		t.Fatalf("expected 1 hint row when no workspaces present, got %d", len(m.rows))
	}
	if m.rows[0].kind != rowEmptyHint {
		t.Errorf("expected the row to be a rowEmptyHint, got %v", m.rows[0].kind)
	}
}

// TestAppletViewCreatePreservesInputAcrossTabs is the regression test for
// PR #19 review finding #8, updated for the tab-routing fix: inside Create,
// tab moves field focus (it must NOT leave the view); esc parks the form
// back to the list keeping its input; tab from the list re-enters the
// still-active form with the input intact.
func TestAppletViewCreatePreservesInputAcrossTabs(t *testing.T) {
	d := NewAppletData(&config.Config{Targets: map[string]config.Target{}}, "")
	m := NewAppletModel(d)
	m.width, m.height = 120, 40

	// Enter Create the way the list view does — via a switchViewMsg with
	// reset, simulating the user pressing "n".
	updated, _ := m.Update(switchViewMsg{view: viewCreate, reset: true})
	m = updated.(AppletModel)
	if m.view != viewCreate {
		t.Fatalf("after switchViewMsg{viewCreate}: view = %v, want viewCreate", m.view)
	}
	if !m.create.active {
		t.Fatal("create model should be active after deliberate switchToFresh(viewCreate, ...)")
	}

	// Type "foo/bar" into the repo field by dispatching key messages —
	// this exercises the same routeInput path the real TUI uses.
	for _, r := range "foo/bar" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(AppletModel)
	}
	if got := m.create.repoInput.Value(); got != "foo/bar" {
		t.Fatalf("repoInput.Value() = %q, want %q", got, "foo/bar")
	}

	// renderHeader should now include the Create tab — the regression
	// from PATCH 15 was that viewCreate was never added to the header.
	if header := m.renderHeader(); !strings.Contains(header, "Create") {
		t.Errorf("renderHeader() = %q, want it to contain %q", header, "Create")
	}

	// Tab inside Create must advance field focus, not switch views.
	focusBefore := m.create.focus
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(AppletModel)
	if m.view != viewCreate {
		t.Fatalf("tab inside Create switched view to %v; it must cycle field focus instead", m.view)
	}
	if m.create.focus == focusBefore {
		t.Error("tab inside Create did not advance field focus")
	}

	// esc parks the form back to the list without clearing it. The esc
	// handler returns a switchTo command; run it and dispatch its message
	// the way the bubbletea runtime would.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(AppletModel)
	if cmd == nil {
		t.Fatal("esc in Create returned no command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(AppletModel)
	if m.view != viewList {
		t.Fatalf("after esc: view = %v, want viewList", m.view)
	}
	if !m.create.active {
		t.Fatal("esc must park the create form, not clear it")
	}

	// Tab forward from the list re-enters the active create form
	// (rotation list → create → settings, detail hidden).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(AppletModel)
	if m.view != viewCreate {
		t.Fatalf("after tab from list: view = %v, want viewCreate", m.view)
	}

	// The typed input must still be there — this is the bug under fix.
	if got := m.create.repoInput.Value(); got != "foo/bar" {
		t.Errorf("repoInput.Value() after round trip = %q, want %q", got, "foo/bar")
	}
}

// TestAppletTabCyclesSettingsSections locks in the tab-routing fix for
// Settings: before it, the top-level model consumed tab so m.section could
// never change and most of the settings surface was unreachable.
func TestAppletTabCyclesSettingsSections(t *testing.T) {
	d := NewAppletData(&config.Config{Targets: map[string]config.Target{}}, "")
	m := NewAppletModel(d)
	m.width, m.height = 120, 40

	updated, _ := m.Update(switchViewMsg{view: viewSettings})
	m = updated.(AppletModel)
	if m.view != viewSettings {
		t.Fatalf("view = %v, want viewSettings", m.view)
	}

	sectionBefore := m.settings.section
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(AppletModel)
	if m.view != viewSettings {
		t.Fatalf("tab inside Settings switched view to %v; it must cycle sections instead", m.view)
	}
	if m.settings.section == sectionBefore {
		t.Error("tab inside Settings did not advance the section")
	}
}

// TestListFilterAcceptsVimKeys locks in the filter fix: once a filter is
// being typed, j/k/g/G are text, not navigation — "ajkg" must not become
// "a" with three cursor jumps. (Like the s/n/r/d command keys, vim-style
// navigation still applies while the filter is empty, so the first typed
// character must be a non-command one.)
func TestListFilterAcceptsVimKeys(t *testing.T) {
	d := NewAppletData(&config.Config{Targets: map[string]config.Target{}}, "")
	m := newListModel(d)
	for _, r := range "ajkgG" {
		m, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, d)
	}
	if m.filter != "ajkgG" {
		t.Errorf("filter = %q, want %q", m.filter, "ajkgG")
	}
}

// TestAppletRenderHeaderHidesCreateWhenInactive confirms the inverse of the
// regression test: the Create tab should not appear in the header until a
// form is actually in progress.
func TestAppletRenderHeaderHidesCreateWhenInactive(t *testing.T) {
	d := NewAppletData(&config.Config{Targets: map[string]config.Target{}}, "")
	m := NewAppletModel(d)
	m.width, m.height = 120, 40

	if strings.Contains(m.renderHeader(), "Create") {
		t.Error("renderHeader() should not include the Create tab when no form is active")
	}
}

// TestCreateDoneRoutedWhileOnAnotherView locks in typed message routing:
// a createDoneMsg arriving while the user tabbed away must still reach the
// create model — before the fix it was dropped and the form was stuck in
// submitting=true forever (which also swallows all key input).
func TestCreateDoneRoutedWhileOnAnotherView(t *testing.T) {
	d := NewAppletData(&config.Config{Targets: map[string]config.Target{}}, "")
	m := NewAppletModel(d)
	m.width, m.height = 120, 40

	updated, _ := m.Update(switchViewMsg{view: viewCreate, reset: true})
	m = updated.(AppletModel)
	m.create.submitting = true

	// Simulate the user parking the form and returning to the list.
	updated, _ = m.Update(switchViewMsg{view: viewList})
	m = updated.(AppletModel)

	// The async create fails while the list is focused.
	updated, _ = m.Update(createDoneMsg{err: fmt.Errorf("boom")})
	m = updated.(AppletModel)
	if m.create.submitting {
		t.Fatal("createDoneMsg was not delivered to the create model while unfocused")
	}
}
