
## 2026-03-23 - GitHub Codespaces Zed Launcher Tool

### Goal
- Add a local developer tool that can:
- Start an existing predefined GitHub Codespace or create a new one.
- Update Zed configuration so the Codespace is available as a remote target.
- Launch Zed connected to that Codespace.

### Assumptions
- The tool will be intended for the local developer machine, not the deployed app.
- GitHub CLI (`gh`) and Zed CLI (`zed`) are the preferred integrations.
- The user is already authenticated with GitHub CLI and has Zed installed locally.
- Codespace selection can be driven by a small local config file or predefined defaults committed in the repo.

### Proposed Implementation
1. Build a standalone tool in this directory
2. Define a small configuration format for named Codespace targets:
- repository
- branch or ref
- machine/region/devcontainer options when creating a new Codespace
- workspace path or display name used for Zed integration
3. Implement the script flow:
- read configured target or CLI arguments
- detect matching existing Codespaces with `gh codespace list`
- if a matching Codespace exists, start it when needed
- otherwise create a new Codespace with `gh codespace create`
- fetch connection details needed by Zed
- update Zed remote/collaboration config idempotently
- invoke `zed` to open the remote workspace
4. Add guardrails and clear errors for:
- missing `gh` or `zed`
- missing GitHub auth
- ambiguous Codespace matches
- Zed config format mismatch
5. Document usage in `README.md` or a dedicated tooling doc.

### Validation Plan
- Verify the script resolves an existing Codespace path without creating duplicates.
- Verify the script can create a new Codespace from configuration.
- Verify repeated runs are idempotent with respect to Zed config updates.
- Smoke-test that the final command launches Zed with the expected remote target.

### Deliverables
- Tooling script and any supporting config/types.
- Usage documentation.
- One dedicated commit for this feature, per repository instructions.
- Add parser tests with saved HTML fixtures for each adapter.

## 2026-05-07 - Add Coder CLI Support as an Alternative Workspace Provider

### Goal
- Add support for Coder CLI as an alternative to GitHub Codespaces.
- Make provider selection a global setting for the initial release.
- Preserve a clean path to support GitHub and Coder in parallel later.

### Current State
- Cosmonaut is currently GitHub Codespaces-specific in its CLI flow, daemon, config schema, SSH config handling, docs, and Home Manager module.
- The main product model today is repo-centric: pick a repository, then reuse or create a Codespace.
- Coder uses a different model: workspaces, templates, agents, and apps, with SSH integration managed by the `coder` CLI.

### Research Notes
- Verified locally that `coder` is installed: `Coder v2.33.0`.
- Verified that the local CLI is authenticated against a live Coder deployment.
- Verified that the authenticated deployment currently exposes at least one workspace and a `nomad-devcontainer` template.
- Observed a client/server version mismatch warning: local CLI `v2.33.0`, server `v2.32.0`.
- Relevant Coder CLI surfaces for this work:
- `coder whoami` for auth and identity
- `coder list -o json` for workspace discovery
- `coder show` for detailed workspace state
- `coder templates list -o json` for template discovery
- `coder create` for workspace creation
- `coder start` for starting a stopped workspace
- `coder config-ssh` for SSH config generation

### MVP Boundary
- Support one globally selected provider at a time: `github` or `coder`.
- Do not implement dual-provider operation in the same picker or daemon in the first pass.
- Keep the design extensible so targets can eventually opt into different providers and the daemon can poll both.

### Key Design Decision
- Introduce a provider abstraction before adding Coder-specific behavior.
- Do not try to fit Coder into `internal/codespace` directly.
- Use provider-neutral naming such as "workspace" in shared layers, while keeping GitHub-specific behavior in a GitHub adapter.

### Proposed Implementation
1. Introduce a new provider package, likely `internal/provider`, with a shared domain model and interface for provider operations.
2. Define a provider-neutral workspace model that can represent the fields Cosmonaut actually needs:
- stable identifier
- display name
- state
- source repository when known
- branch/ref when known
- workspace path
- provider-specific metadata
3. Define the initial provider interface around current product needs:
- ensure auth
- list all workspaces
- list workspaces for a target
- resolve an explicit workspace by name
- create a workspace
- start a workspace when needed
- wait until it is reachable
- prepare SSH configuration
- delete a workspace
- describe a workspace for TUI and logs
4. Wrap the existing GitHub Codespaces logic in a GitHub provider adapter without changing user-facing behavior.
5. Implement a Coder provider adapter that uses:
- `coder whoami`
- `coder list -o json`
- `coder show`
- `coder templates list -o json`
- `coder create`
- `coder start`
- `coder config-ssh`
6. Refactor the main CLI flow in `main.go` to choose a provider first, then route all list/create/start/SSH operations through the provider interface.
7. Keep the current named-target flow, but make selection behavior provider-aware:
- GitHub keeps the current repo -> codespace flow
- Coder uses a workspace/template-oriented flow
8. For the first Coder picker, prefer workspace selection over repository selection.
9. If no matching Coder workspace exists for a target, create one from a configured template instead of inferring GitHub Codespace semantics.
10. Refactor SSH config handling so it no longer assumes everything is a codespace.
11. For GitHub, preserve the existing per-codespace SSH config behavior.
12. For Coder, prefer delegating SSH config generation to `coder config-ssh` instead of manually synthesizing host stanzas.
13. Store or reference Coder SSH config in a Cosmonaut-managed include path so it can coexist with GitHub-generated entries.
14. Make the daemon provider-aware:
- poll the active provider selected in config
- display provider-specific auth state in Settings
- reword generic UI copy from "codespaces" to "workspaces" where appropriate
15. Keep dual-provider polling, merged workspace lists, and mixed-provider tray behavior out of MVP.

### Configuration Plan
- Add a top-level global provider setting, for example:
- `workspaceProvider: "github" | "coder"`
- Default to `github` for backward compatibility.
- Add a provider configuration section, for example:
- `providers.github`
- `providers.coder`
- Keep common target fields at the shared target level where they are provider-neutral:
- `workspacePath`
- `zedNickname`
- `uploadBinaryOverSsh`
- `preWarm`
- Add provider-specific target configuration under nested keys rather than overloading existing GitHub fields.
- Example direction for Coder target settings:
- `template`
- `workspaceName`
- `parameters`
- `stopAfter`
- `organization` if needed

### Suggested Config Shape
```json
{
  "workspaceProvider": "coder",
  "editor": "zed",
  "providers": {
    "coder": {
      "organization": "coder"
    }
  },
  "targets": {
    "work": {
      "workspacePath": "/workspaces/my-repo",
      "coder": {
        "template": "nomad-devcontainer",
        "workspaceName": "my-repo",
        "parameters": {
          "repo": "my-org/my-repo"
        },
        "stopAfter": "8h"
      }
    }
  }
}
```

### File Areas Expected to Change
- `main.go`
- `internal/config/config.go`
- `internal/codespace/*` or a replacement GitHub provider package
- `internal/sshconfig/sshconfig.go`
- `internal/tui/tui.go`
- `internal/daemon/*`
- `internal/doctor/doctor.go`
- `modules/home-manager.nix`
- `README.md`
- `docs/config.md`
- additional API/docs pages as needed

### Migration and Compatibility Notes
- Preserve existing GitHub config behavior by default.
- Existing users should not need to set a provider unless they want Coder.
- Avoid renaming user-facing GitHub target fields until the provider abstraction is in place and compatibility shims exist.
- The first refactor should minimize behavioral drift for GitHub users.

### Risks and Open Questions
- The current repo-centric UX maps naturally to GitHub Codespaces but not to Coder.
- Coder workspace creation may depend heavily on template parameters, so one generic create flow may not be enough.
- `coder config-ssh` owns SSH host generation, so Cosmonaut should avoid fighting it or duplicating its logic.
- The daemon, doctor checks, and UI strings currently assume GitHub auth and Codespaces-specific error modes.
- If Coder templates differ significantly across deployments, target config may need to support arbitrary parameter maps.

### Rollout Order
1. Introduce provider abstraction and move GitHub behavior behind it.
2. Add the global provider config switch.
3. Implement the Coder adapter and basic target launch flow.
4. Update SSH config integration for provider neutrality.
5. Make the daemon and settings UI provider-aware.
6. Update docs and Home Manager support.
7. Add tests for config parsing, provider selection, and Coder workspace matching.

### Validation Plan
- Verify GitHub remains unchanged when `workspaceProvider` is unset or set to `github`.
- Verify `workspaceProvider: "coder"` uses Coder auth, listing, create, start, and SSH preparation paths.
- Verify repeated runs are idempotent for both GitHub and Coder SSH config handling.
- Verify the daemon can poll the configured provider and render sensible status.
- Verify a configured Coder target can create or reuse a workspace from a template and open it in the configured editor.
- Verify config parsing and defaults remain backward compatible.

### Deliverables
- Provider abstraction and GitHub adapter refactor.
- Initial Coder provider adapter.
- Global provider setting in CLI config and Home Manager module.
- Provider-aware SSH config handling.
- Updated docs and examples for GitHub and Coder usage.
- Tests covering provider selection and the initial Coder workflow.
