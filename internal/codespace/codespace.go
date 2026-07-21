// Package codespace wraps the GitHub CLI (gh) to list, create, delete,
// and SSH into GitHub Codespaces. All GitHub API interactions go through
// the GHRunner interface so they can be stubbed in tests.
package codespace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/linuskendall/cosmonaut/internal/config"
)

// RepoField handles the gh CLI returning repository as either a string or an object.
type RepoField string

func (r *RepoField) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*r = RepoField(s)
		return nil
	}

	var obj struct {
		FullName      string `json:"full_name"`
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("repository field is neither string nor object: %w", err)
	}
	name := obj.FullName
	if name == "" {
		name = obj.NameWithOwner
	}
	*r = RepoField(name)
	return nil
}

type GitStatus struct {
	Ref    string `json:"ref,omitempty"`
	Branch string `json:"branch,omitempty"`
}

type Codespace struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName,omitempty"`
	Repository  RepoField  `json:"repository"`
	State       string     `json:"state,omitempty"`
	GitStatus   *GitStatus `json:"gitStatus,omitempty"`
	MachineName string     `json:"machineName,omitempty"`
	CreatedAt   string     `json:"createdAt,omitempty"`
	LastUsedAt  string     `json:"lastUsedAt,omitempty"`
}

// GHRunner abstracts the GitHub CLI for testability.
type GHRunner interface {
	Run(args []string) (string, error)
	// RunCtx is like Run but cancels the underlying process when ctx is
	// done. Used by the daemon poller to cap long-running list calls so
	// a hung gh doesn't pin the in-flight poll slot.
	RunCtx(ctx context.Context, args []string) (string, error)
	RunMerged(args []string) (string, error)
	// RunInteractive runs a command with stdin connected and output teed to
	// both a capture buffer and the real terminal. Use this when the gh
	// subprocess may need to prompt the user (e.g. machine-type selection).
	RunInteractive(args []string) (string, error)
	// RunWithInput runs a command and feeds a string to its stdin. Used for
	// `gh api --input -` calls where we pipe a JSON body.
	RunWithInput(args []string, input string) (string, error)
}

// DefaultGHRunner executes real gh commands.
type DefaultGHRunner struct{}

func (d DefaultGHRunner) Run(args []string) (string, error) {
	return d.RunCtx(context.Background(), args)
}

func (d DefaultGHRunner) RunCtx(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("gh %s timed out", strings.Join(args, " "))
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("gh %s exited with code %d: %s", strings.Join(args, " "), cmd.ProcessState.ExitCode(), detail)
	}
	return stdout.String(), nil
}

func (d DefaultGHRunner) RunMerged(args []string) (string, error) {
	cmd := exec.Command("gh", args...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		return combined.String(), fmt.Errorf("gh %s exited with code %d: %s", strings.Join(args, " "), cmd.ProcessState.ExitCode(), combined.String())
	}
	return combined.String(), nil
}

func (d DefaultGHRunner) RunInteractive(args []string) (string, error) {
	cmd := exec.Command("gh", args...)
	var buf bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	cmd.Stderr = io.MultiWriter(&buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("gh %s exited with code %d: %s", strings.Join(args, " "), cmd.ProcessState.ExitCode(), buf.String())
	}
	return buf.String(), nil
}

func (d DefaultGHRunner) RunWithInput(args []string, input string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("gh %s exited with code %d: %s", strings.Join(args, " "), cmd.ProcessState.ExitCode(), detail)
	}
	return stdout.String(), nil
}

// RequireCommand checks that a command is on PATH.
func RequireCommand(name string) error {
	_, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%q not found on PATH", name)
	}
	return nil
}

// EnsureGHAuth verifies the user is authenticated with gh.
func EnsureGHAuth(runner GHRunner) error {
	_, err := runner.Run([]string{"auth", "status"})
	if err != nil {
		return fmt.Errorf("GitHub CLI is not authenticated. Run `gh auth login` first")
	}
	return nil
}

// ListCodespaces returns all codespaces for a given repository.
func ListCodespaces(runner GHRunner, repository string) ([]Codespace, error) {
	out, err := runner.Run([]string{
		"codespace", "list",
		"--repo", repository,
		"--json", "name,displayName,repository,state,gitStatus,machineName,createdAt,lastUsedAt",
	})
	if err != nil {
		return nil, err
	}

	var codespaces []Codespace
	if err := json.Unmarshal([]byte(out), &codespaces); err != nil {
		return nil, fmt.Errorf("parsing codespace list: %w", err)
	}
	return codespaces, nil
}

// ListAllCodespaces returns all codespaces across all repos.
func ListAllCodespaces(runner GHRunner) ([]Codespace, error) {
	return ListAllCodespacesCtx(context.Background(), runner)
}

// ListAllCodespacesCtx is the context-aware variant of ListAllCodespaces.
// The daemon poller uses this to cap how long a hung `gh codespace list`
// can pin the in-flight poll slot.
func ListAllCodespacesCtx(ctx context.Context, runner GHRunner) ([]Codespace, error) {
	out, err := runner.RunCtx(ctx, []string{
		"codespace", "list",
		"--json", "name,displayName,repository,state,gitStatus,machineName,createdAt,lastUsedAt",
	})
	if err != nil {
		return nil, err
	}

	var codespaces []Codespace
	if err := json.Unmarshal([]byte(out), &codespaces); err != nil {
		return nil, fmt.Errorf("parsing codespace list: %w", err)
	}
	return codespaces, nil
}

// UniqueRepos extracts sorted unique repository names from codespaces.
func UniqueRepos(codespaces []Codespace) []string {
	seen := make(map[string]bool)
	var repos []string
	for _, cs := range codespaces {
		repo := string(cs.Repository)
		if repo != "" && !seen[repo] {
			seen[repo] = true
			repos = append(repos, repo)
		}
	}
	return repos
}

// ListAllRepos returns all repositories the user has access to.
func ListAllRepos(runner GHRunner) ([]string, error) {
	out, err := runner.Run([]string{
		"repo", "list",
		"--json", "nameWithOwner",
		"--limit", "1000",
	})
	if err != nil {
		return nil, err
	}

	var repos []struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		return nil, fmt.Errorf("parsing repo list: %w", err)
	}

	result := make([]string, len(repos))
	for i, r := range repos {
		result[i] = r.NameWithOwner
	}
	return result, nil
}

// FilterByRepo returns codespaces belonging to a given repository.
func FilterByRepo(codespaces []Codespace, repo string) []Codespace {
	var result []Codespace
	for _, cs := range codespaces {
		if string(cs.Repository) == repo {
			result = append(result, cs)
		}
	}
	return result
}

// codespaceNameRe matches a real GitHub codespace name: a multi-word
// petname plus a trailing alphanumeric hash (e.g.
// "expert-spoon-vwqr5wq4x73xjpj"). The old pattern
// ([A-Za-z0-9-]+-[A-Za-z0-9]{6,}) matched ordinary hyphenated words gh
// echoes in its prompts — a branch like "fix-timeout-handling" matched
// before the actual name, and the follow-up `codespace view` targeted the
// wrong string.
var codespaceNameRe = regexp.MustCompile(`\b[a-z0-9]+(?:-[a-z0-9]+)+-[a-z0-9]{10,}\b`)

// CreateCodespace creates a new codespace by POSTing directly to the GitHub REST
// API (`POST /repos/{owner}/{repo}/codespaces`). This bypasses the interactive
// prompts that `gh codespace create` emits when billing is paid by a third
// party or when a machine type isn't specified: prompts that fail without a
// terminal (e.g. from the tray/daemon). Use CreateCodespaceInteractive when
// running from a real TTY and prompts are desired.
func CreateCodespace(runner GHRunner, target config.Target) (*Codespace, error) {
	owner, repoName, err := splitRepo(target.Repository)
	if err != nil {
		return nil, err
	}
	body, err := buildCreateAPIBody(target)
	if err != nil {
		return nil, err
	}

	apiOut, err := runner.RunWithInput([]string{
		"api", "-X", "POST",
		fmt.Sprintf("/repos/%s/%s/codespaces", owner, repoName),
		"--input", "-",
	}, string(body))
	if err != nil {
		return nil, err
	}

	var created struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(apiOut), &created); err != nil {
		return nil, fmt.Errorf("parsing codespace create response: %w\n%s", err, strings.TrimSpace(apiOut))
	}
	if created.Name == "" {
		return nil, fmt.Errorf("codespace create response missing name:\n%s", strings.TrimSpace(apiOut))
	}

	out, err := runner.Run([]string{
		"codespace", "view",
		"--codespace", created.Name,
		"--json", "name,displayName,repository,state,gitStatus,machineName,createdAt,lastUsedAt",
	})
	if err != nil {
		return nil, err
	}

	var cs Codespace
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		return nil, fmt.Errorf("parsing codespace view: %w", err)
	}
	return &cs, nil
}

func splitRepo(repo string) (owner, name string, err error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repository %q (expected owner/name)", repo)
	}
	return parts[0], parts[1], nil
}

// buildCreateAPIBody maps a config.Target to the JSON body accepted by
// POST /repos/{owner}/{repo}/codespaces.
func buildCreateAPIBody(target config.Target) ([]byte, error) {
	body := map[string]any{}
	if target.Branch != "" {
		body["ref"] = target.Branch
	}
	if target.DisplayName != "" {
		body["display_name"] = target.DisplayName
	}
	if target.Machine != "" {
		body["machine"] = target.Machine
	}
	if target.Location != "" {
		body["location"] = target.Location
	}
	if target.DevcontainerPath != "" {
		body["devcontainer_path"] = target.DevcontainerPath
	}
	if target.IdleTimeout != "" {
		mins, err := durationToMinutes(target.IdleTimeout)
		if err != nil {
			return nil, fmt.Errorf("idleTimeout %q: %w", target.IdleTimeout, err)
		}
		body["idle_timeout_minutes"] = mins
	}
	if target.RetentionPeriod != "" {
		mins, err := durationToMinutes(target.RetentionPeriod)
		if err != nil {
			return nil, fmt.Errorf("retentionPeriod %q: %w", target.RetentionPeriod, err)
		}
		body["retention_period_minutes"] = mins
	}
	return json.Marshal(body)
}

func durationToMinutes(s string) (int, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	mins := int(d / time.Minute)
	if mins <= 0 {
		return 0, fmt.Errorf("must be at least 1 minute")
	}
	return mins, nil
}

// CreateCodespaceInteractive creates a codespace with terminal access so gh can
// prompt the user if needed (e.g. machine type selection). Output is shown on
// the terminal in real time.
func CreateCodespaceInteractive(runner GHRunner, target config.Target) (*Codespace, error) {
	args := BuildCreateArgs(target)
	combined, err := runner.RunInteractive(args[1:]) // strip "gh" prefix
	if err != nil && combined == "" {
		return nil, err
	}

	// Take the LAST match: gh prints the created codespace name at the end
	// of its output, after prompts that may echo repo/branch strings.
	matches := codespaceNameRe.FindAllString(combined, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("codespace created but name could not be determined from gh output:\n%s", strings.TrimSpace(combined))
	}
	match := matches[len(matches)-1]

	out, err := runner.Run([]string{
		"codespace", "view",
		"--codespace", match,
		"--json", "name,displayName,repository,state,gitStatus,machineName,createdAt,lastUsedAt",
	})
	if err != nil {
		return nil, err
	}

	var cs Codespace
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		return nil, fmt.Errorf("parsing codespace view: %w", err)
	}
	return &cs, nil
}

// DeleteCodespace deletes a codespace by name.
func DeleteCodespace(runner GHRunner, name string) error {
	_, err := runner.Run([]string{"codespace", "delete", "--codespace", name, "--force"})
	return err
}

// EnsureReachable verifies the codespace SSH server is accessible.
// It retries with exponential backoff since the codespace may be
// starting up or the SSH tunnel may be slow to establish.
func EnsureReachable(runner GHRunner, codespaceName string) error {
	var lastErr error
	delays := []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	for i, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		_, err := runner.Run([]string{"codespace", "ssh", "--codespace", codespaceName, "--", "true"})
		if err == nil {
			return nil
		}
		lastErr = err
		if i < len(delays)-1 {
			// Log retry for debugging.
			fmt.Fprintf(os.Stderr, "  SSH attempt %d failed, retrying...\n", i+1)
		}
	}
	return fmt.Errorf("could not SSH into codespace %q after %d attempts: %w", codespaceName, len(delays), lastErr)
}

// GetSSHConfig retrieves the OpenSSH config for a codespace.
// Retries once on failure since the SSH tunnel may not be ready immediately
// after EnsureReachable succeeds.
func GetSSHConfig(runner GHRunner, codespaceName string) (string, error) {
	out, err := runner.Run([]string{"codespace", "ssh", "--codespace", codespaceName, "--config"})
	if err == nil {
		return out, nil
	}
	time.Sleep(2 * time.Second)
	return runner.Run([]string{"codespace", "ssh", "--codespace", codespaceName, "--config"})
}
