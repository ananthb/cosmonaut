package provider

import (
	"encoding/json"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

type GitHubManager struct {
	Runner codespace.GHRunner
}

func NewGitHubManager(runner codespace.GHRunner) *GitHubManager {
	return &GitHubManager{Runner: runner}
}

func (m *GitHubManager) Name() string { return NameGitHub }

func (m *GitHubManager) EnsurePrereqs() error { return codespace.RequireCommand("gh") }

func (m *GitHubManager) EnsureAuth() error { return codespace.EnsureGHAuth(m.Runner) }

func (m *GitHubManager) ListAllWorkspaces() ([]Workspace, error) {
	items, err := codespace.ListAllCodespaces(m.Runner)
	if err != nil {
		return nil, err
	}
	return githubWorkspaces(items), nil
}

func (m *GitHubManager) ListRepositories() ([]string, error) {
	return codespace.ListAllRepos(m.Runner)
}

func (m *GitHubManager) ListWorkspacesForTarget(target config.Target) ([]Workspace, error) {
	items, err := codespace.ListCodespaces(m.Runner, target.Repository)
	if err != nil {
		return nil, err
	}
	return githubWorkspaces(items), nil
}

func (m *GitHubManager) ResolveWorkspace(name string) (*Workspace, error) {
	out, err := m.Runner.Run([]string{
		"codespace", "view",
		"--codespace", name,
		"--json", "name,displayName,repository,state,gitStatus,machineName,createdAt,lastUsedAt",
	})
	if err != nil {
		return nil, err
	}
	var cs codespace.Codespace
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		return nil, err
	}
	ws := githubWorkspace(cs)
	return &ws, nil
}

func (m *GitHubManager) CreateWorkspace(target config.Target, interactive bool) (*Workspace, error) {
	var (
		cs  *codespace.Codespace
		err error
	)
	if interactive {
		cs, err = codespace.CreateCodespaceInteractive(m.Runner, target)
	} else {
		cs, err = codespace.CreateCodespace(m.Runner, target)
	}
	if err != nil {
		return nil, err
	}
	ws := githubWorkspace(*cs)
	return &ws, nil
}

func (m *GitHubManager) StartWorkspace(workspace *Workspace) (*Workspace, error) {
	return workspace, nil
}

func (m *GitHubManager) DeleteWorkspace(name string) error {
	return codespace.DeleteCodespace(m.Runner, name)
}

func (m *GitHubManager) EnsureReachable(workspace *Workspace) error {
	return codespace.EnsureReachable(m.Runner, workspace.Name)
}

func (m *GitHubManager) PrepareSSH(paths sshconfig.SSHPaths, workspace *Workspace) (string, error) {
	sshCfg, err := codespace.GetSSHConfig(m.Runner, workspace.Name)
	if err != nil {
		return "", err
	}
	alias, err := sshconfig.ParsePrimaryHostAlias(sshCfg)
	if err != nil {
		return "", err
	}
	if err := sshconfig.EnsureWorkspaceConfig(paths, workspace.Provider, workspace.Name, sshCfg); err != nil {
		return "", err
	}
	return alias, nil
}

func githubWorkspaces(items []codespace.Codespace) []Workspace {
	result := make([]Workspace, 0, len(items))
	for _, item := range items {
		result = append(result, githubWorkspace(item))
	}
	return result
}

func githubWorkspace(item codespace.Codespace) Workspace {
	ws := Workspace{
		Provider:    NameGitHub,
		Name:        item.Name,
		DisplayName: item.DisplayName,
		Repository:  string(item.Repository),
		State:       item.State,
		MachineName: item.MachineName,
		CreatedAt:   item.CreatedAt,
		LastUsedAt:  item.LastUsedAt,
	}
	if item.GitStatus != nil {
		ws.Branch = item.GitStatus.Ref
		if ws.Branch == "" {
			ws.Branch = item.GitStatus.Branch
		}
	}
	return ws
}
