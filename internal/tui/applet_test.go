package tui

import (
	"testing"

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
