package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

// coderDeleteTimeout caps how long `coder delete --yes` can run. The
// workspace deletion is destructive but should resolve within a minute
// on a healthy backend; capping prevents a hung CLI from pinning the
// confirmAndDeleteWorkspace goroutine indefinitely.
const coderDeleteTimeout = 60 * time.Second

type CoderManager struct {
	Config *config.Config
}

type CoderTemplate struct {
	Name         string
	Organization string
}

func NewCoderManager(cfg *config.Config) *CoderManager {
	return &CoderManager{Config: cfg}
}

func (m *CoderManager) Name() string { return NameCoder }

func (m *CoderManager) EnsurePrereqs() error { return RequireCommand("coder") }

func (m *CoderManager) EnsureAuth() error {
	_, err := m.run("whoami", "-o", "json")
	if err != nil {
		return fmt.Errorf("coder CLI is not authenticated; run `coder login` first")
	}
	return nil
}

func (m *CoderManager) ListAllWorkspaces() ([]Workspace, error) {
	return m.ListAllWorkspacesCtx(context.Background())
}

// ListAllWorkspacesCtx is the context-aware variant of ListAllWorkspaces.
// The daemon poller uses this to cap how long a hung `coder list` can
// pin the in-flight poll slot.
func (m *CoderManager) ListAllWorkspacesCtx(ctx context.Context) ([]Workspace, error) {
	out, err := m.runCtx(ctx, "list", "-o", "json")
	if err != nil {
		return nil, err
	}
	var items []coderWorkspace
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return nil, fmt.Errorf("parsing coder workspace list: %w", err)
	}
	return coderWorkspaces(items), nil
}

func (m *CoderManager) ListTemplates() ([]CoderTemplate, error) {
	out, err := m.run("templates", "list", "-o", "json")
	if err != nil {
		return nil, err
	}
	// Coder CLI versions differ on the row shape: some wrap each row in a
	// {"Template": {...}} display object, others emit bare template
	// objects. Try the wrapped shape first, fall back to bare, and error
	// if a non-empty list produced zero templates (a shape we don't know
	// would otherwise render an empty picker with no diagnostic).
	type tpl struct {
		Name             string `json:"name"`
		OrganizationName string `json:"organization_name"`
	}
	var wrapped []struct {
		Template tpl `json:"Template"`
	}
	var rows []tpl
	if err := json.Unmarshal([]byte(out), &wrapped); err == nil {
		for _, item := range wrapped {
			rows = append(rows, item.Template)
		}
	}
	wrappedEmpty := true
	for _, r := range rows {
		if r.Name != "" {
			wrappedEmpty = false
			break
		}
	}
	if wrappedEmpty {
		var bare []tpl
		if err := json.Unmarshal([]byte(out), &bare); err == nil {
			rows = bare
		}
	}
	result := make([]CoderTemplate, 0, len(rows))
	for _, item := range rows {
		if item.Name == "" {
			continue
		}
		result = append(result, CoderTemplate{
			Name:         item.Name,
			Organization: item.OrganizationName,
		})
	}
	if len(result) == 0 && strings.TrimSpace(out) != "" && strings.TrimSpace(out) != "[]" && strings.TrimSpace(out) != "null" {
		return nil, fmt.Errorf("parsing coder template list: unrecognized JSON shape")
	}
	return result, nil
}

func (m *CoderManager) ListRepositories() ([]string, error) {
	return nil, nil
}

// ListWorkspacesForTarget returns the workspaces matching the target's
// constraints. A constrained target (workspaceName or repository set) that
// matches nothing returns an EMPTY slice — never the full list. The old
// return-everything fallback meant a target for repo "acme/api" with no
// matching workspace could hand the caller some unrelated workspace, which
// ChooseWorkspace would then auto-select and the flow would open, stop, or
// delete. Only a fully unconstrained target lists everything.
func (m *CoderManager) ListWorkspacesForTarget(target config.Target) ([]Workspace, error) {
	all, err := m.ListAllWorkspaces()
	if err != nil {
		return nil, err
	}
	return filterCoderWorkspacesForTarget(all, target), nil
}

func filterCoderWorkspacesForTarget(all []Workspace, target config.Target) []Workspace {
	if target.Coder != nil && target.Coder.WorkspaceName != "" {
		var filtered []Workspace
		for _, ws := range all {
			if ws.Name == target.Coder.WorkspaceName {
				filtered = append(filtered, ws)
			}
		}
		return filtered
	}
	if target.Repository != "" {
		// Exact matches only: the repository itself, or the workspace name
		// CreateWorkspace derives from it (pathBase). The previous
		// strings.Contains tier matched e.g. workspace "my-api-old" for
		// repo "api".
		var filtered []Workspace
		repoName := pathBase(target.Repository)
		for _, ws := range all {
			if ws.Repository == target.Repository || ws.Name == repoName {
				filtered = append(filtered, ws)
			}
		}
		return filtered
	}
	return all
}

func (m *CoderManager) ResolveWorkspace(name string) (*Workspace, error) {
	all, err := m.ListAllWorkspaces()
	if err != nil {
		return nil, err
	}
	for _, ws := range all {
		if ws.Name == name {
			return &ws, nil
		}
	}
	return nil, fmt.Errorf("workspace %q not found", name)
}

func (m *CoderManager) CreateWorkspace(target config.Target, interactive bool) (*Workspace, error) {
	if target.Coder == nil || target.Coder.Template == "" {
		return nil, fmt.Errorf("coder target requires coder.template")
	}
	name := coderWorkspaceName(target)
	if name == "" {
		return nil, fmt.Errorf("coder target requires coder.workspaceName or a repository-derived default")
	}
	if !coderWorkspaceNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid coder workspace name %q: must match %s", name, coderWorkspaceNameRe)
	}

	args := []string{"create"}
	if org := m.targetOrganization(target); org != "" {
		args = append(args, "--org", org)
	}
	args = append(args, "--template", target.Coder.Template)
	args = append(args, "--use-parameter-defaults")
	if target.Coder.StopAfter != "" {
		args = append(args, "--stop-after", target.Coder.StopAfter)
	}
	keys := sortedKeys(target.Coder.Parameters)
	for _, key := range keys {
		args = append(args, "--parameter", fmt.Sprintf("%s=%s", key, target.Coder.Parameters[key]))
	}
	args = append(args, "--yes", "--", name)

	if _, err := m.run(args...); err != nil {
		return nil, err
	}
	return m.ResolveWorkspace(name)
}

// coderWorkspaceNameRe mirrors Coder's own workspace-name rules (hostname
// label style). Validating here keeps names that start with '-' or contain
// separators from ever reaching the CLI as arguments or landing in SSH
// config filenames.
var coderWorkspaceNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,62}$`)

func (m *CoderManager) StartWorkspace(workspace *Workspace) (*Workspace, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is nil")
	}
	if isCoderReadyState(workspace.State) {
		return workspace, nil
	}
	if isCoderDeletingState(workspace.State) {
		return nil, fmt.Errorf("coder workspace %q is being deleted", workspace.Name)
	}
	if isCoderBusyState(workspace.State) {
		// A stop or cancel is still in flight; the CLI rejects `start` on a
		// busy workspace, so wait for the transition to settle first.
		settled, err := m.waitForWorkspaceState(workspace.Name, 60*time.Second, isCoderBusyState)
		if err != nil {
			return nil, err
		}
		workspace = settled
		if isCoderReadyState(workspace.State) {
			return workspace, nil
		}
	}
	if !isCoderTransitionalState(workspace.State) {
		if _, err := m.run("start", "--yes", "--", workspace.Name); err != nil {
			return nil, err
		}
	}
	return m.waitForWorkspaceState(workspace.Name, 60*time.Second, func(state string) bool {
		return isCoderReadyState(state) || isCoderTransitionalState(state)
	})
}

func (m *CoderManager) DeleteWorkspace(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("workspace name is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), coderDeleteTimeout)
	defer cancel()
	// Pass `--` before the workspace name so a name that happens to begin with
	// `--` is treated as a positional argument rather than parsed as a flag by
	// the coder CLI's cobra-style parser.
	if _, err := m.runCtx(ctx, "delete", "--yes", "--", name); err != nil {
		return err
	}
	return nil
}

func (m *CoderManager) EnsureReachable(workspace *Workspace) error {
	// waitForWorkspaceState only returns nil error once the workspace is
	// ready; failure states and timeout surface as errors.
	_, err := m.waitForWorkspaceState(workspace.Name, 90*time.Second, func(state string) bool {
		return isCoderReadyState(state) || isCoderTransitionalState(state)
	})
	return err
}

func (m *CoderManager) PrepareSSH(paths sshconfig.SSHPaths, workspace *Workspace, opts sshconfig.ManagedExtrasOptions) (string, error) {
	if err := sshconfig.EnsureMainConfigIncludesGenerated(paths.MainConfigPath); err != nil {
		return "", err
	}
	configPath := filepath.Join(paths.IncludeDir, "coder.conf")
	args := []string{"config-ssh", "--yes", "--ssh-config-file", configPath}
	// Coder defaults to writing os.Executable() into the ProxyCommand,
	// which on nix is a /nix/store/<hash>/bin/.coder-wrapped path that
	// gets GC'd when the package updates. Pin to the PATH-resolved entry
	// (a stable symlink under /etc/profiles, /run/current-system, or
	// ~/.nix-profile) so the SSH config survives store turnover.
	if coderPath, err := exec.LookPath("coder"); err == nil {
		args = append(args, "--coder-binary-path", coderPath)
	}
	if _, err := m.run(args...); err != nil {
		return "", err
	}
	// Append the cosmonaut-managed extras (keepalive + optional
	// ControlMaster) to whatever coder config-ssh wrote. The block lands
	// inside the last Host stanza in the file, which is the `Host *.coder`
	// pattern coder emits — i.e. it applies to the `<workspace>.coder`
	// alias we hand the editor.
	if _, err := sshconfig.RefreshManagedExtras(configPath, opts); err != nil {
		return "", err
	}
	return workspace.Name + ".coder", nil
}

func (m *CoderManager) run(args ...string) (string, error) {
	return m.runCtx(context.Background(), args...)
}

func (m *CoderManager) runCtx(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "coder", args...)
	// WaitDelay caps how long Wait blocks after the process is killed
	// while it drains the stderr/stdout pipes into our buffers. Without
	// this, exec.CommandContext's default Cancel callback can race the
	// runtime's I/O goroutines, returning before the buffers are
	// populated — breaking the "include stderr in timeout error"
	// guarantee.
	cmd.WaitDelay = 500 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// context.Cause instead of ctx.Err(): identical for real
		// WithTimeout contexts (their cause IS DeadlineExceeded), but it
		// also honors WithCancelCause(ctx); cancel(DeadlineExceeded),
		// which the tests use to fire the "deadline" at a deterministic
		// point instead of racing a wall-clock timer.
		if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			tail := strings.TrimSpace(stderr.String())
			if len(tail) > 200 {
				// Slice the last 200 bytes, then advance to a UTF-8 rune
				// boundary so we never emit a half-rune (which would render as
				// a replacement character) in the error message.
				tail = tail[len(tail)-200:]
				for i := 0; i < len(tail) && i < 4; i++ {
					if utf8.RuneStart(tail[i]) {
						tail = tail[i:]
						break
					}
				}
				tail = "..." + tail
			}
			if tail != "" {
				return "", fmt.Errorf("coder %s timed out: %s", strings.Join(args, " "), tail)
			}
			return "", fmt.Errorf("coder %s timed out", strings.Join(args, " "))
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("coder %s exited with code %d: %s", strings.Join(args, " "), cmd.ProcessState.ExitCode(), detail)
	}
	return stdout.String(), nil
}

type coderWorkspace struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	LastUsedAt   string `json:"last_used_at"`
	TemplateName string `json:"template_name"`
	OwnerName    string `json:"owner_name"`
	LatestBuild  struct {
		Status     string `json:"status"`
		Transition string `json:"transition"`
		Resources  []struct {
			Agents []struct {
				Status         string `json:"status"`
				LifecycleState string `json:"lifecycle_state"`
			} `json:"agents"`
		} `json:"resources"`
	} `json:"latest_build"`
}

func coderWorkspaces(items []coderWorkspace) []Workspace {
	result := make([]Workspace, 0, len(items))
	for _, item := range items {
		state := coderWorkspaceState(item)
		result = append(result, Workspace{
			Provider:    NameCoder,
			ID:          item.ID,
			Name:        item.Name,
			DisplayName: item.Name,
			State:       state,
			LastUsedAt:  item.LastUsedAt,
			Template:    item.TemplateName,
			Metadata: map[string]string{
				"owner": item.OwnerName,
			},
		})
	}
	return result
}

func coderWorkspaceState(item coderWorkspace) string {
	for _, resource := range item.LatestBuild.Resources {
		for _, agent := range resource.Agents {
			if agent.LifecycleState != "" {
				return agent.LifecycleState
			}
			if agent.Status != "" {
				return agent.Status
			}
		}
	}
	if item.LatestBuild.Status != "" {
		return item.LatestBuild.Status
	}
	return item.LatestBuild.Transition
}

func coderWorkspaceName(target config.Target) string {
	if target.Coder != nil && target.Coder.WorkspaceName != "" {
		return target.Coder.WorkspaceName
	}
	if target.Repository == "" {
		return ""
	}
	return pathBase(target.Repository)
}

func pathBase(s string) string {
	parts := strings.Split(s, "/")
	return parts[len(parts)-1]
}

func (m *CoderManager) targetOrganization(target config.Target) string {
	if target.Coder != nil && target.Coder.Organization != "" {
		return target.Coder.Organization
	}
	return m.Config.CoderOrganization()
}

func sortedKeys(maybe map[string]string) []string {
	keys := make([]string, 0, len(maybe))
	for key := range maybe {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// waitForWorkspaceState polls until the workspace is ready, returning an
// error when it lands in a state the allow callback rejects (e.g. failed)
// or when the timeout expires. It used to return nil in both of those
// cases, so StartWorkspace reported success for a workspace whose build
// had failed.
func (m *CoderManager) waitForWorkspaceState(name string, timeout time.Duration, allow func(string) bool) (*Workspace, error) {
	deadline := time.Now().Add(timeout)
	for {
		ws, err := m.ResolveWorkspace(name)
		if err != nil {
			return nil, err
		}
		if isCoderReadyState(ws.State) {
			return ws, nil
		}
		if !allow(ws.State) {
			return ws, fmt.Errorf("coder workspace %q entered state %q while waiting for it to become ready", name, ws.State)
		}
		if time.Now().After(deadline) {
			return ws, fmt.Errorf("timed out after %s waiting for coder workspace %q to become ready (last state: %s)", timeout, name, ws.State)
		}
		time.Sleep(2 * time.Second)
	}
}

func isCoderReadyState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "ready", "running", "connected", "started", "available":
		return true
	default:
		return false
	}
}

func isCoderTransitionalState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "created", "creating", "pending", "starting", "start", "initializing":
		return true
	default:
		return false
	}
}

// isCoderBusyState reports states where a stop/cancel transition is in
// flight — the workspace is neither startable nor ready until it settles.
func isCoderBusyState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "stopping", "canceling", "cancelling":
		return true
	default:
		return false
	}
}

// isCoderDeletingState reports a workspace that is going away; starting it
// is never right.
func isCoderDeletingState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "deleting", "deleted":
		return true
	default:
		return false
	}
}
