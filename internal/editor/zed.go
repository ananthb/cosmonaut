package editor

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/linuskendall/cosmonaut/internal/fileutil"
)

// ZedEditor implements Editor for the Zed text editor.
type ZedEditor struct{}

func (z *ZedEditor) Name() string { return "zed" }

func (z *ZedEditor) FindBinary() (string, error) {
	for _, name := range []string{"zed", "zeditor"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("zed editor not found on PATH (tried \"zed\" and \"zeditor\")")
}

func (z *ZedEditor) ConfigureConnection(sshAlias, workspacePath, nickname string, uploadBinary *bool) error {
	path := resolveSettingsPath()
	if path == "" {
		return fmt.Errorf("cannot resolve home directory for Zed settings")
	}
	conn := buildConnection(sshAlias, workspacePath, nickname, uploadBinary)
	return upsertConnectionInFile(path, conn)
}

func (z *ZedEditor) LaunchRemote(sshAlias, workspacePath string) error {
	bin, err := z.FindBinary()
	if err != nil {
		return err
	}
	remoteURL := fmt.Sprintf("ssh://%s/%s", sshAlias, strings.TrimLeft(workspacePath, "/"))
	return exec.Command(bin, remoteURL).Run()
}

// --- Zed settings.json manipulation (moved from internal/zed/) ---

type project struct {
	Paths []string `json:"paths"`
}

type sshConnection struct {
	Host                string    `json:"host"`
	Nickname            string    `json:"nickname,omitempty"`
	Projects            []project `json:"projects,omitempty"`
	UploadBinaryOverSSH *bool     `json:"upload_binary_over_ssh,omitempty"`
	Port                int       `json:"port,omitempty"`
	Username            string    `json:"username,omitempty"`
}

func resolveSettingsPath() string {
	// Zed reads ~/.config/zed/settings.json on every platform, macOS
	// included (its paths::config_dir is not platform-switched). An
	// earlier version wrote ~/.zed/settings.json on darwin — a file Zed
	// never reads — which silently disabled nickname/upload_binary
	// configuration for every macOS user.
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "zed", "settings.json")
}

func buildConnection(host, workspacePath, nickname string, uploadBinary *bool) sshConnection {
	conn := sshConnection{
		Host:     host,
		Nickname: nickname,
		Projects: []project{{Paths: []string{workspacePath}}},
	}
	if uploadBinary != nil {
		conn.UploadBinaryOverSSH = uploadBinary
	}
	return conn
}

func connectionIdentity(conn map[string]any) string {
	host, _ := conn["host"].(string)
	username, _ := conn["username"].(string)
	port := 22
	if p, ok := conn["port"].(float64); ok {
		port = int(p)
	}
	return fmt.Sprintf("%s@%s:%d", username, host, port)
}

func upsertConnection(settings map[string]any, conn sshConnection) map[string]any {
	result := maps.Clone(settings)
	if result == nil {
		result = map[string]any{}
	}

	connJSON, err := json.Marshal(conn)
	if err != nil {
		return result
	}
	var connMap map[string]any
	if err := json.Unmarshal(connJSON, &connMap); err != nil {
		return result
	}

	newIdentity := connectionIdentity(connMap)

	var existing []any
	if raw, ok := result["ssh_connections"]; ok {
		if arr, ok := raw.([]any); ok {
			existing = append(existing, arr...)
		}
	}

	found := -1
	for i, item := range existing {
		if m, ok := item.(map[string]any); ok {
			if connectionIdentity(m) == newIdentity {
				found = i
				break
			}
		}
	}

	if found >= 0 {
		merged := maps.Clone(existing[found].(map[string]any))
		// Preserve projects the user added to this connection inside Zed:
		// union our project into theirs instead of overwriting the list.
		connMap["projects"] = mergeProjects(merged["projects"], connMap["projects"])
		maps.Copy(merged, connMap)
		existing[found] = merged
	} else {
		existing = append(existing, connMap)
	}

	result["ssh_connections"] = existing
	return result
}

// mergeProjects unions two ssh_connection project lists, deduplicating by
// their path sets so re-launching the same workspace doesn't stack
// duplicate entries while user-added projects survive.
func mergeProjects(oldRaw, newRaw any) []any {
	var out []any
	seen := map[string]bool{}
	keyOf := func(item any) string {
		m, ok := item.(map[string]any)
		if !ok {
			return fmt.Sprintf("%v", item)
		}
		paths, _ := m["paths"].([]any)
		parts := make([]string, 0, len(paths))
		for _, p := range paths {
			parts = append(parts, fmt.Sprintf("%v", p))
		}
		return strings.Join(parts, "\x00")
	}
	appendAll := func(raw any) {
		arr, ok := raw.([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			k := keyOf(item)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, item)
		}
	}
	appendAll(oldRaw)
	appendAll(newRaw)
	return out
}

// upsertConnectionInFile updates the ssh_connections list inside Zed's
// settings.json. The file is user-owned JSONC (Zed's own default template
// contains comments), so this is deliberately conservative:
//
//   - parsing uses a real JWCC parser (tailscale/hujson), never regexes,
//     so comment-like or comma-brace sequences inside string values can't
//     corrupt the parse, and malformed input fails loudly without a write;
//   - the edit is a JSON Patch on the parsed document — only the
//     ssh_connections member changes; every other byte, comments
//     included, is preserved;
//   - the result is re-parsed before writing and the write is atomic, so
//     a bug here fails loudly rather than destroying the user's settings.
func upsertConnectionInFile(settingsPath string, conn sshConnection) error {
	data, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		data = []byte("{}\n")
	}

	// hujson keeps references into (and Standardize blanks) the buffer it
	// is given, so val gets its own copy — it must stay pristine for the
	// format-preserving Patch below.
	val, err := hujson.Parse(append([]byte(nil), data...))
	if err != nil {
		return fmt.Errorf("parsing %s: %w", settingsPath, err)
	}
	std, err := hujson.Standardize(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", settingsPath, err)
	}
	current := make(map[string]any)
	if err := json.Unmarshal(std, &current); err != nil {
		return fmt.Errorf("parsing %s: %w", settingsPath, err)
	}

	updated := upsertConnection(current, conn)
	connJSON, err := json.Marshal(updated["ssh_connections"])
	if err != nil {
		return err
	}

	op := "add"
	if _, ok := current["ssh_connections"]; ok {
		op = "replace"
	}
	patch := fmt.Sprintf(`[{"op": %q, "path": "/ssh_connections", "value": %s}]`, op, connJSON)
	if err := val.Patch([]byte(patch)); err != nil {
		return fmt.Errorf("updating %s: %w", settingsPath, err)
	}
	newDoc := val.Pack()

	// Round-trip safety net: never write a document we can't parse back.
	// Standardize blanks its input in place, so give it a copy of the
	// bytes we're about to write.
	if _, err := hujson.Standardize(append([]byte(nil), newDoc...)); err != nil {
		return fmt.Errorf("refusing to write %s: edited settings would not parse: %w", settingsPath, err)
	}

	dir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(settingsPath, newDoc, 0o644)
}
