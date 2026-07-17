package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseJSONCAcceptsCommentsAndTrailingCommas(t *testing.T) {
	source := `
	{
	  // comment
	  "name": "demo",
	  "nested": {
	    "enabled": true,
	  },
	}`

	clean, err := ParseJSONC(source)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(clean, &got); err != nil {
		t.Fatal(err)
	}

	if got["name"] != "demo" {
		t.Errorf("name = %v, want demo", got["name"])
	}
	nested := got["nested"].(map[string]any)
	if nested["enabled"] != true {
		t.Errorf("nested.enabled = %v, want true", nested["enabled"])
	}
}

func TestLoadConfig(t *testing.T) {
	content := `{
		"defaultTarget": "demo",
		"targets": {
			"demo": {
				"repository": "acme/demo",
				"workspacePath": "/workspaces"
			}
		}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultTarget != "demo" {
		t.Errorf("defaultTarget = %q, want demo", cfg.DefaultTarget)
	}
	if cfg.WorkspaceProvider != "github" {
		t.Errorf("workspaceProvider = %q, want github default", cfg.WorkspaceProvider)
	}
	if _, ok := cfg.Targets["demo"]; !ok {
		t.Error("missing target 'demo'")
	}
}

func TestWorkspaceSSHDefaultsAndOverrides(t *testing.T) {
	var cfg Config

	// Defaults: ControlMaster on, Tmux off, no map allocated.
	if !cfg.WorkspaceSSHControlMaster("github", "cs-a") {
		t.Error("default ControlMaster should be true")
	}
	if cfg.WorkspaceSSHTmux("github", "cs-a") {
		t.Error("default Tmux should be false")
	}

	// Explicit override on one workspace doesn't leak to others.
	off := false
	cfg.SetWorkspaceSSHControlMaster("github", "cs-a", &off)
	if cfg.WorkspaceSSHControlMaster("github", "cs-a") {
		t.Error("explicit false should win over default")
	}
	if !cfg.WorkspaceSSHControlMaster("github", "cs-b") {
		t.Error("cs-b should still see the default")
	}

	on := true
	cfg.SetWorkspaceSSHTmux("coder", "ws-1", &on)
	if !cfg.WorkspaceSSHTmux("coder", "ws-1") {
		t.Error("explicit true should win over default")
	}
	if cfg.WorkspaceSSHTmux("github", "cs-a") {
		t.Error("tmux for coder:ws-1 must not affect github:cs-a")
	}

	// Clearing the last setting on a workspace drops the entry, so the
	// settings map doesn't accumulate dead keys over time.
	cfg.SetWorkspaceSSHControlMaster("github", "cs-a", nil)
	if _, ok := cfg.WorkspaceSSH["github:cs-a"]; ok {
		t.Error("entry should be removed once both fields are nil")
	}
}

func TestLoadCoderConfig(t *testing.T) {
	content := `{
		"workspaceProvider": "coder",
		"providers": {
			"coder": {
				"organization": "coder"
			}
		},
		"targets": {
			"work": {
				"workspacePath": "/workspaces/demo",
				"coder": {
					"template": "nomad-devcontainer",
					"workspaceName": "demo",
					"parameters": {
						"repo": "acme/demo"
					},
					"stopAfter": "8h",
					"portForwards": [
						{"label": "app", "localPort": 8080, "remotePort": 3000, "protocol": "tcp"}
					]
				}
			}
		}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.EffectiveWorkspaceProvider(); got != "coder" {
		t.Fatalf("workspace provider = %q, want coder", got)
	}
	target := cfg.Targets["work"]
	if target.Coder == nil || target.Coder.Template != "nomad-devcontainer" {
		t.Fatalf("coder target not parsed: %+v", target.Coder)
	}
	if target.Coder.Parameters["repo"] != "acme/demo" {
		t.Fatalf("coder parameters = %+v", target.Coder.Parameters)
	}
	if len(target.Coder.PortForwards) != 1 || target.Coder.PortForwards[0].RemotePort != 3000 {
		t.Fatalf("coder port forwards = %+v", target.Coder.PortForwards)
	}
}

func TestTargetCloneIsDeep(t *testing.T) {
	up := true
	orig := Target{
		Repository:          "acme/demo",
		UploadBinaryOverSSH: &up,
		Coder: &CoderTargetConfig{
			WorkspaceName: "ws",
			Parameters:    map[string]string{"repo": "acme/demo"},
			PortForwards:  []PortForward{{RemotePort: 3000, LocalPort: 3000}},
		},
	}
	cp := orig.Clone()
	cp.Coder.WorkspaceName = "changed"
	cp.Coder.Parameters["repo"] = "changed"
	cp.Coder.PortForwards[0].RemotePort = 9999
	*cp.UploadBinaryOverSSH = false

	if orig.Coder.WorkspaceName != "ws" {
		t.Fatal("Clone shares Coder pointer")
	}
	if orig.Coder.Parameters["repo"] != "acme/demo" {
		t.Fatal("Clone shares Parameters map")
	}
	if orig.Coder.PortForwards[0].RemotePort != 3000 {
		t.Fatal("Clone shares PortForwards slice")
	}
	if !*orig.UploadBinaryOverSSH {
		t.Fatal("Clone shares UploadBinaryOverSSH pointer")
	}
}

func TestUpdateTargetReportsExistence(t *testing.T) {
	cfg := &Config{}
	cfg.UpdateTarget("new", func(tg *Target, exists bool) {
		if exists {
			t.Error("target should not exist yet")
		}
		tg.Repository = "acme/demo"
	})
	cfg.UpdateTarget("new", func(tg *Target, exists bool) {
		if !exists {
			t.Error("target should exist now")
		}
		if tg.Repository != "acme/demo" {
			t.Errorf("repository = %q", tg.Repository)
		}
	})
}

// TestConfigConcurrentAccess exercises the accessor surface from many
// goroutines at once; it exists to fail under `go test -race` if any
// accessor touches shared state without the mutex.
func TestConfigConcurrentAccess(t *testing.T) {
	cfg := &Config{Targets: map[string]Target{
		"work": {Repository: "acme/demo", Coder: &CoderTargetConfig{WorkspaceName: "ws"}},
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			cfg.UpdateTarget("work", func(tg *Target, _ bool) {
				tg.Coder.PortForwards = append(tg.Coder.PortForwards, PortForward{RemotePort: i})
			})
			cfg.SetDaemonInhibitSleep("sleep")
			cfg.SetEditor("zed")
		}
	}()
	for i := 0; i < 500; i++ {
		for range cfg.TargetsSnapshot() {
		}
		_, _ = cfg.Target("work")
		_ = cfg.GetDefaultTarget()
		_ = cfg.GetEditor()
		_ = cfg.CoderOrganization()
		_ = cfg.EnsureDaemon()
		_ = cfg.EffectiveWorkspaceProvider()
	}
	<-done
}
