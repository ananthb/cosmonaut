package tui

import (
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
// PR #19 review finding #8: viewCreate must be wired into the tab cycle and
// the create model must survive a round trip away and back without losing
// user input.
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

	// Tab forward to viewList (Create → Settings → List in the rotation).
	for i := 0; i < 3 && m.view != viewList; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(AppletModel)
	}
	if m.view != viewList {
		t.Fatalf("after cycling tabs: view = %v, want viewList", m.view)
	}

	// Tab back to viewCreate (List → Create in reverse, since detail is
	// hidden and create is active).
	for i := 0; i < 4 && m.view != viewCreate; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		m = updated.(AppletModel)
	}
	if m.view != viewCreate {
		t.Fatalf("after shift+tabbing back: view = %v, want viewCreate", m.view)
	}

	// The typed input must still be there — this is the bug under fix.
	if got := m.create.repoInput.Value(); got != "foo/bar" {
		t.Errorf("repoInput.Value() after round trip = %q, want %q", got, "foo/bar")
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
