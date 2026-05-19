// Package config loads the cosmonaut JSONC configuration file
// and defines the Target struct that describes a named codespace target
// (repository, branch, machine type, Zed display settings, etc.).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Config struct {
	DefaultTarget     string            `json:"defaultTarget,omitempty"`
	WorkspaceProvider string            `json:"workspaceProvider,omitempty"` // "github" (default) or "coder"
	Editor            string            `json:"editor,omitempty"`            // "zed" (default) or "neovim"
	Providers         ProviderConfigs   `json:"providers,omitempty"`
	Targets           map[string]Target `json:"targets"`
	Daemon            *DaemonConfig     `json:"daemon,omitempty"`
}

type ProviderConfigs struct {
	GitHub GitHubProviderConfig `json:"github,omitempty"`
	Coder  CoderProviderConfig  `json:"coder,omitempty"`
}

type GitHubProviderConfig struct{}

type CoderProviderConfig struct {
	Organization string `json:"organization,omitempty"`
}

// DaemonConfig holds settings for the background daemon (tray, hotkey, poller).
type DaemonConfig struct {
	Hotkey       string `json:"hotkey,omitempty"`       // e.g. "Cmd+Shift+S" (macOS) or "Ctrl+Shift+S" (Linux)
	HotkeyAction string `json:"hotkeyAction,omitempty"` // "picker" (default), "previous", or "default"
	Terminal     string `json:"terminal,omitempty"`     // terminal app to launch picker in; "auto" to detect
	PollInterval string `json:"pollInterval,omitempty"` // how often to poll codespace state (e.g. "5m")
	InhibitSleep string `json:"inhibitSleep,omitempty"` // "off" (default), "sleep", or "sleep+shutdown"
}

type Target struct {
	Repository          string             `json:"repository,omitempty"`
	Branch              string             `json:"branch,omitempty"`
	DisplayName         string             `json:"displayName,omitempty"`
	CodespaceName       string             `json:"codespaceName,omitempty"`
	WorkspacePath       string             `json:"workspacePath"`
	Machine             string             `json:"machine,omitempty"`
	Location            string             `json:"location,omitempty"`
	DevcontainerPath    string             `json:"devcontainerPath,omitempty"`
	IdleTimeout         string             `json:"idleTimeout,omitempty"`
	RetentionPeriod     string             `json:"retentionPeriod,omitempty"`
	UploadBinaryOverSSH *bool              `json:"uploadBinaryOverSsh,omitempty"`
	ZedNickname         string             `json:"zedNickname,omitempty"`
	AutoStop            string             `json:"autoStop,omitempty"` // auto-stop after idle duration (e.g. "30m")
	PreWarm             string             `json:"preWarm,omitempty"`  // time-of-day to pre-warm codespace (e.g. "08:00")
	Coder               *CoderTargetConfig `json:"coder,omitempty"`
}

type CoderTargetConfig struct {
	Template      string            `json:"template,omitempty"`
	WorkspaceName string            `json:"workspaceName,omitempty"`
	Parameters    map[string]string `json:"parameters,omitempty"`
	StopAfter     string            `json:"stopAfter,omitempty"`
	Organization  string            `json:"organization,omitempty"`
	PortForwards  []PortForward     `json:"portForwards,omitempty"`
}

type PortForward struct {
	Label      string `json:"label,omitempty"`
	LocalPort  int    `json:"localPort,omitempty"`
	RemotePort int    `json:"remotePort,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

var (
	blockCommentRe  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCommentRe   = regexp.MustCompile(`(?m)^\s*//.*$`)
	trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)
)

// ParseJSONC strips comments and trailing commas, then returns clean JSON bytes.
func ParseJSONC(source string) ([]byte, error) {
	s := blockCommentRe.ReplaceAllString(source, "")
	s = lineCommentRe.ReplaceAllString(s, "")
	s = trailingCommaRe.ReplaceAllString(s, "$1")
	return []byte(s), nil
}

// LoadConfig reads a JSONC config file and returns the parsed Config.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	clean, err := ParseJSONC(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(clean, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if cfg.WorkspaceProvider == "" {
		cfg.WorkspaceProvider = "github"
	}

	return &cfg, nil
}

// SaveConfig writes the config to the given path as formatted JSON
// with 4-space indentation for easy hand-editing.
func SaveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func (c *Config) EffectiveWorkspaceProvider() string {
	if c == nil || c.WorkspaceProvider == "" {
		return "github"
	}
	return c.WorkspaceProvider
}

func (t Target) ExplicitWorkspaceName(provider string) string {
	if provider == "coder" {
		if t.Coder != nil && t.Coder.WorkspaceName != "" {
			return t.Coder.WorkspaceName
		}
		return ""
	}
	if t.CodespaceName != "" {
		return t.CodespaceName
	}
	return ""
}

// FieldDoc describes a single config target field for generated documentation.
type FieldDoc struct {
	JSON     string // JSON key name
	Type     string // human-readable type
	Required bool
	Desc     string
}

// TargetFieldDocs is the authoritative documentation for every Target field.
var TargetFieldDocs = []FieldDoc{
	{"repository", "string", false, "GitHub repository in owner/repo form; optional for Coder targets"},
	{"branch", "string", false, "Preferred branch when creating or matching a codespace"},
	{"displayName", "string", false, "Exact display name to disambiguate codespace matches"},
	{"codespaceName", "string", false, "Exact codespace name for strict reuse"},
	{"workspacePath", "string", true, "Remote folder Zed should open (e.g. /workspaces/repo)"},
	{"machine", "string", false, "Machine type forwarded to gh codespace create"},
	{"location", "string", false, "Location forwarded to gh codespace create"},
	{"devcontainerPath", "string", false, "Dev container config path forwarded to gh codespace create"},
	{"idleTimeout", "string", false, "Idle timeout forwarded to gh codespace create (e.g. 30m)"},
	{"retentionPeriod", "string", false, "Retention period forwarded to gh codespace create (e.g. 720h)"},
	{"uploadBinaryOverSsh", "bool", false, "Set Zed's upload_binary_over_ssh for this host"},
	{"zedNickname", "string", false, "Friendly name shown in Zed's remote project list"},
	{"autoStop", "string", false, "Auto-stop codespace after idle duration (e.g. 30m)"},
	{"preWarm", "string", false, "Time-of-day to pre-warm codespace (e.g. 08:00)"},
	{"coder", "object", false, "Coder-specific target settings: template, workspaceName, parameters, stopAfter, organization, portForwards"},
}

// DaemonFieldDocs is the authoritative documentation for DaemonConfig fields.
var DaemonFieldDocs = []FieldDoc{
	{"hotkey", "string", false, "Global hotkey (e.g. Cmd+Shift+S)"},
	{"hotkeyAction", "string", false, "Hotkey behavior: picker (default), previous, or default"},
	{"terminal", "string", false, "Terminal app for picker; auto to detect"},
	{"pollInterval", "string", false, "Codespace poll interval (e.g. 5m)"},
	{"inhibitSleep", "string", false, "Hold sleep/shutdown inhibitor while a codespace session is active: off (default), sleep, or sleep+shutdown"},
}

// TargetFieldsHelp returns a formatted help string for all target fields.
func TargetFieldsHelp() string {
	var b strings.Builder
	for _, f := range TargetFieldDocs {
		req := ""
		if f.Required {
			req = " (required)"
		}
		fmt.Fprintf(&b, "  %-22s %s%s\n", f.JSON, f.Desc, req)
	}
	return b.String()
}

// DaemonFieldsHelp returns a formatted help string for daemon config fields.
func DaemonFieldsHelp() string {
	var b strings.Builder
	for _, f := range DaemonFieldDocs {
		fmt.Fprintf(&b, "  %-22s %s\n", f.JSON, f.Desc)
	}
	return b.String()
}

// ParseJSONCAny parses JSONC into an arbitrary value (used for Zed settings).
func ParseJSONCAny(source string) (any, error) {
	clean, err := ParseJSONC(source)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(clean, &v); err != nil {
		return nil, err
	}
	return v, nil
}
