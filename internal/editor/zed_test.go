package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

func writeSettings(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func parseSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	std, err := hujson.Standardize(data)
	if err != nil {
		t.Fatalf("settings are not valid JSONC after edit: %v\n%s", err, data)
	}
	var v map[string]any
	if err := json.Unmarshal(std, &v); err != nil {
		t.Fatalf("settings do not parse after edit: %v\n%s", err, data)
	}
	return v
}

func TestUpsertConnectionInFilePreservesComments(t *testing.T) {
	// Zed's default settings template is commented JSONC — an edit must
	// not destroy those comments (the old implementation re-marshalled
	// the whole file and lost everything).
	path := writeSettings(t, `// Zed settings
//
// For information on how to configure Zed, see the Zed
// documentation: https://zed.dev/docs/configuring-zed
{
  "theme": "One Dark", // user's theme choice
  "vim_mode": true
}
`)
	err := upsertConnectionInFile(path, buildConnection("cs.demo.github.dev", "/workspaces/demo", "demo", nil))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"// Zed settings", "https://zed.dev/docs/configuring-zed", "// user's theme choice", `"vim_mode": true`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("comment/content destroyed by edit: missing %q\n%s", want, data)
		}
	}
	v := parseSettings(t, path)
	conns := v["ssh_connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("ssh_connections = %v", conns)
	}
	if h := conns[0].(map[string]any)["host"]; h != "cs.demo.github.dev" {
		t.Errorf("host = %v", h)
	}
}

func TestUpsertConnectionInFileStringContentsSafe(t *testing.T) {
	// Regression: values containing comment markers or ", }" sequences
	// were corrupted by the old regex stripper.
	path := writeSettings(t, `{
  "terminal": {"shell": {"program": "sh", "args": ["-c", "echo /* keep me */ , }"]}},
  "file_types": {"Dockerfile": ["Dockerfile*"]}
}
`)
	if err := upsertConnectionInFile(path, buildConnection("h", "/w", "", nil)); err != nil {
		t.Fatal(err)
	}
	v := parseSettings(t, path)
	args := v["terminal"].(map[string]any)["shell"].(map[string]any)["args"].([]any)
	if args[1] != "echo /* keep me */ , }" {
		t.Errorf("string value corrupted: %q", args[1])
	}
}

func TestUpsertConnectionUpdatesExistingAndPreservesProjects(t *testing.T) {
	path := writeSettings(t, `{
  "ssh_connections": [
    {
      "host": "cs.demo.github.dev",
      "projects": [{"paths": ["/workspaces/demo"]}, {"paths": ["/workspaces/other"]}]
    }
  ]
}
`)
	up := true
	err := upsertConnectionInFile(path, buildConnection("cs.demo.github.dev", "/workspaces/demo", "nick", &up))
	if err != nil {
		t.Fatal(err)
	}
	v := parseSettings(t, path)
	conns := v["ssh_connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(conns))
	}
	conn := conns[0].(map[string]any)
	if conn["nickname"] != "nick" {
		t.Errorf("nickname = %v", conn["nickname"])
	}
	if conn["upload_binary_over_ssh"] != true {
		t.Errorf("upload_binary_over_ssh = %v", conn["upload_binary_over_ssh"])
	}
	projects := conn["projects"].([]any)
	if len(projects) != 2 {
		t.Fatalf("user-added project lost: projects = %v", projects)
	}
}

func TestUpsertConnectionInFileCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zed", "settings.json")
	if err := upsertConnectionInFile(path, buildConnection("h", "/w", "", nil)); err != nil {
		t.Fatal(err)
	}
	v := parseSettings(t, path)
	if _, ok := v["ssh_connections"]; !ok {
		t.Error("ssh_connections missing in freshly created settings")
	}
}

func TestUpsertConnectionInFileRefusesUnparseable(t *testing.T) {
	garbage := "{ this is not json at all ]]"
	path := writeSettings(t, garbage)
	err := upsertConnectionInFile(path, buildConnection("h", "/w", "", nil))
	if err == nil {
		t.Fatal("expected an error for unparseable settings")
	}
	data, _ := os.ReadFile(path)
	if string(data) != garbage {
		t.Error("file was modified despite parse failure")
	}
}
