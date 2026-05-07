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
	os.WriteFile(path, []byte(content), 0644)

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
					"stopAfter": "8h"
				}
			}
		}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(content), 0644)

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
}
