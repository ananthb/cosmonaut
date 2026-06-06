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
	"sync"
)

type Config struct {
	// mu guards every field below. Reads take RLock, writes take Lock.
	// The GUI/TUI mutate Config from goroutines other than the one that
	// reads it during a launch or tray rebuild, so all access has to be
	// serialized.
	//
	// Embedding sync.RWMutex on Config means Config must always be used
	// via a pointer — copying it would copy the mutex (which `go vet`
	// flags as a copylock violation).
	mu sync.RWMutex

	DefaultTarget     string            `json:"defaultTarget,omitempty"`
	WorkspaceProvider string            `json:"workspaceProvider,omitempty"` // "github" (default) or "coder"
	Editor            string            `json:"editor,omitempty"`            // any binary on PATH; "" / "zed" / "zeditor" use the built-in Zed integration
	Providers         ProviderConfigs   `json:"providers,omitempty"`
	Targets           map[string]Target `json:"targets"`
	Daemon            *DaemonConfig     `json:"daemon,omitempty"`

	// WorkspaceSSH holds per-workspace SSH options keyed by "<provider>:<name>"
	// (e.g. "github:cs-abc" or "coder:my-ws"). Unset workspaces fall back to
	// the global defaults: ControlMaster on, Tmux off.
	WorkspaceSSH map[string]WorkspaceSSHSettings `json:"workspaceSsh,omitempty"`
}

// WorkspaceSSHSettings stores per-workspace SSH knobs. Each field is a pointer
// so "unset" can be distinguished from an explicit on/off.
type WorkspaceSSHSettings struct {
	// ControlMaster enables OpenSSH connection multiplexing
	// (ControlMaster auto + ControlPersist) in the managed extras block,
	// so additional sessions to the same workspace reuse the existing TCP
	// connection. Default: true.
	ControlMaster *bool `json:"controlMaster,omitempty"`
	// Tmux wraps `cosmonaut shell` (and the GUI's SSH button) in
	// `tmux new -A -s cosmonaut` on the remote so the shell session
	// survives SSH drops. Default: false.
	Tmux *bool `json:"tmux,omitempty"`
}

// WorkspaceSSHKey returns the canonical map key used by Config.WorkspaceSSH
// for a workspace. Stable across renames since both provider and the
// provider-issued name are immutable.
func WorkspaceSSHKey(provider, name string) string {
	return provider + ":" + name
}

// WorkspaceSSHControlMaster returns the resolved ControlMaster setting for a
// workspace, with the default (true) applied when no explicit value is set.
func (c *Config) WorkspaceSSHControlMaster(provider, name string) bool {
	if c == nil {
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if s, ok := c.WorkspaceSSH[WorkspaceSSHKey(provider, name)]; ok && s.ControlMaster != nil {
		return *s.ControlMaster
	}
	return true
}

// WorkspaceSSHTmux returns the resolved Tmux setting for a workspace, with
// the default (false) applied when no explicit value is set.
func (c *Config) WorkspaceSSHTmux(provider, name string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if s, ok := c.WorkspaceSSH[WorkspaceSSHKey(provider, name)]; ok && s.Tmux != nil {
		return *s.Tmux
	}
	return false
}

// SetWorkspaceSSHControlMaster persists an explicit ControlMaster setting for
// a workspace. Passing nil clears it (so the default applies).
func (c *Config) SetWorkspaceSSHControlMaster(provider, name string, val *bool) {
	c.setWorkspaceSSH(provider, name, func(s *WorkspaceSSHSettings) { s.ControlMaster = val })
}

// SetWorkspaceSSHTmux persists an explicit Tmux setting for a workspace.
// Passing nil clears it (so the default applies).
func (c *Config) SetWorkspaceSSHTmux(provider, name string, val *bool) {
	c.setWorkspaceSSH(provider, name, func(s *WorkspaceSSHSettings) { s.Tmux = val })
}

func (c *Config) setWorkspaceSSH(provider, name string, mut func(*WorkspaceSSHSettings)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := WorkspaceSSHKey(provider, name)
	if c.WorkspaceSSH == nil {
		c.WorkspaceSSH = map[string]WorkspaceSSHSettings{}
	}
	s := c.WorkspaceSSH[key]
	mut(&s)
	if s.ControlMaster == nil && s.Tmux == nil {
		delete(c.WorkspaceSSH, key)
		if len(c.WorkspaceSSH) == 0 {
			c.WorkspaceSSH = nil
		}
		return
	}
	c.WorkspaceSSH[key] = s
}

// GetEditor returns the configured editor name.
func (c *Config) GetEditor() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Editor
}

// SetEditor persists the editor name. Callers should follow up with
// SaveConfig to flush to disk.
func (c *Config) SetEditor(editor string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Editor = editor
}

// WithEditor swaps in a temporary editor for the duration of fn (typically a
// launch flow whose editor override should not leak to the persistent config)
// and restores the prior value when fn returns. Holds the write lock for the
// whole window so no concurrent reader observes a half-swapped state.
func (c *Config) WithEditor(editor string, fn func()) {
	if c == nil {
		fn()
		return
	}
	c.mu.Lock()
	prev := c.Editor
	c.Editor = editor
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.Editor = prev
		c.mu.Unlock()
	}()
	fn()
}

// EnsureDaemon initialises Config.Daemon to a zero-value DaemonConfig if it's
// nil, and returns a snapshot of the current daemon settings under the read
// lock. The returned struct is a copy: mutating it does not affect the live
// config — use the SetDaemon* helpers for that.
func (c *Config) EnsureDaemon() DaemonConfig {
	if c == nil {
		return DaemonConfig{}
	}
	c.mu.Lock()
	if c.Daemon == nil {
		c.Daemon = &DaemonConfig{}
	}
	snap := *c.Daemon
	c.mu.Unlock()
	return snap
}

// SetDaemonHotkeyAction persists the daemon HotkeyAction field. Daemon is
// auto-created if nil.
func (c *Config) SetDaemonHotkeyAction(action string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Daemon == nil {
		c.Daemon = &DaemonConfig{}
	}
	c.Daemon.HotkeyAction = action
}

// SetDaemonInhibitSleep persists the daemon InhibitSleep field. Daemon is
// auto-created if nil.
func (c *Config) SetDaemonInhibitSleep(mode string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Daemon == nil {
		c.Daemon = &DaemonConfig{}
	}
	c.Daemon.InhibitSleep = mode
}

// Target returns a copy of the named target, or the zero value with ok=false
// when no such target is configured.
func (c *Config) Target(name string) (Target, bool) {
	if c == nil {
		return Target{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.Targets[name]
	return t, ok
}

// SetTarget writes a target into the Targets map. The Targets map is
// auto-created if nil.
func (c *Config) SetTarget(name string, t Target) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Targets == nil {
		c.Targets = map[string]Target{}
	}
	c.Targets[name] = t
}

// UpdateTarget performs a read-modify-write on the named target atomically.
// The callback receives a pointer to the live target value; whatever it
// stores is written back. If the target doesn't exist, a zero-value Target
// is passed and stored on return. The Targets map is auto-created if nil.
func (c *Config) UpdateTarget(name string, fn func(*Target)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Targets == nil {
		c.Targets = map[string]Target{}
	}
	t := c.Targets[name]
	fn(&t)
	c.Targets[name] = t
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
//
// LoadConfig returns a freshly-allocated *Config that no other goroutine has
// a reference to yet, so it doesn't need to hold the mutex while populating
// it.
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
//
// Takes the write lock for the duration of the marshal so a concurrent
// writer can't mutate the struct mid-serialization.
func SaveConfig(path string, cfg *Config) error {
	if cfg != nil {
		cfg.mu.Lock()
		defer cfg.mu.Unlock()
	}
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func (c *Config) EffectiveWorkspaceProvider() string {
	if c == nil {
		return "github"
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.effectiveWorkspaceProviderLocked()
}

// effectiveWorkspaceProviderLocked is the lock-free body of
// EffectiveWorkspaceProvider, callable by other methods that already hold
// c.mu (read or write).
func (c *Config) effectiveWorkspaceProviderLocked() string {
	if c == nil || c.WorkspaceProvider == "" {
		return "github"
	}
	return c.WorkspaceProvider
}

// IsCoderConfigured reports whether Coder is in use anywhere in the
// config: as the effective provider, or via any target that declares a
// `coder` block. Used to keep the Coder menu visible whenever the user
// has wired up Coder, even if the API call failed on the last poll.
func (c *Config) IsCoderConfigured() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.effectiveWorkspaceProviderLocked() == "coder" {
		return true
	}
	for _, t := range c.Targets {
		if t.Coder != nil {
			return true
		}
	}
	return false
}

// IsGitHubConfigured reports whether GitHub Codespaces is in use:
// either as the effective provider (the default), or via any target
// that doesn't declare a `coder` block.
//
// Note the asymmetry with IsCoderConfigured: GitHub is the default
// provider, so the absence of an explicit `coder` block on a target
// makes that target an implicit GitHub target. A nil Config (no file
// loaded yet) is therefore treated as "GitHub configured", which lets
// the Codespaces tray submenu appear during early startup. Coder, by
// contrast, only counts when targets opt in explicitly via t.Coder.
func (c *Config) IsGitHubConfigured() bool {
	if c == nil {
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.effectiveWorkspaceProviderLocked() == "github" {
		return true
	}
	for _, t := range c.Targets {
		if t.Coder == nil {
			return true
		}
	}
	return false
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
