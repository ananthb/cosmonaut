package editor

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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
	conn := buildConnection(sshAlias, workspacePath, nickname, uploadBinary)
	return upsertConnectionInFile(resolveSettingsPath(), conn)
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
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".zed", "settings.json")
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
		merged := existing[found].(map[string]any)
		maps.Copy(merged, connMap)
		existing[found] = merged
	} else {
		existing = append(existing, connMap)
	}

	result["ssh_connections"] = existing
	return result
}

func upsertConnectionInFile(settingsPath string, conn sshConnection) error {
	current := make(map[string]any)

	data, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &current); err != nil {
			clean := stripJSONComments(string(data))
			if err := json.Unmarshal([]byte(clean), &current); err != nil {
				return fmt.Errorf("parsing %s: %w", settingsPath, err)
			}
		}
	}

	updated := upsertConnection(current, conn)

	dir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	out, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, append(out, '\n'), 0o644)
}

var (
	blockCommentRe  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCommentRe   = regexp.MustCompile(`(?m)^\s*//.*$`)
	trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)
)

func stripJSONComments(s string) string {
	s = blockCommentRe.ReplaceAllString(s, "")
	s = lineCommentRe.ReplaceAllString(s, "")
	s = trailingCommaRe.ReplaceAllString(s, "$1")
	return s
}
