# xdg-open Forwarder Plan

This plan describes an opt-in `xdg-open`-style forwarder for remote workspaces
launched by Cosmonaut. The goal is that a command run inside a Coder workspace
or GitHub Codespace can open URLs and supported resources on the local macOS
machine that is running the Cosmonaut applet.

## Goals

- Make remote `xdg-open https://example.com` open on the local Mac.
- Keep the local open endpoint private to the active workspace session.
- Reuse Cosmonaut's existing workspace launch, SSH setup, daemon, and provider
  abstractions.
- Start with a focused Coder-compatible MVP, then generalize to GitHub
  Codespaces.

## Non-Goals

- Do not expose a network listener beyond localhost.
- Do not open arbitrary shell commands from the remote workspace.
- Do not require users to hand-edit remote shell startup files for the MVP.
- Do not make this feature mandatory for all launches.

## Architecture

1. Cosmonaut starts a local HTTP open server from the daemon.
2. When a workspace is launched, Cosmonaut starts a reverse SSH tunnel from the
   remote workspace back to that local server.
3. Cosmonaut installs or updates a remote `xdg-open`-compatible shim.
4. The remote shim sends open requests to the tunneled localhost endpoint.
5. The local daemon validates the request and invokes macOS `/usr/bin/open`.

The effective request path is:

```text
remote xdg-open
  -> http://127.0.0.1:<remote-port>/open
  -> ssh -R tunnel
  -> Cosmonaut daemon on 127.0.0.1:<local-port>
  -> /usr/bin/open <target>
```

## Configuration

Add an optional target-level config block:

```jsonc
{
  "targets": {
    "demo": {
      "workspaceProvider": "coder",
      "workspacePath": "/workspaces/demo",
      "openForward": {
        "enabled": true,
        "remotePort": 17890,
        "installShim": true
      }
    }
  }
}
```

Suggested fields:

- `enabled`: opt into the feature for this target.
- `remotePort`: remote loopback port that the shim calls.
- `installShim`: whether Cosmonaut should install `~/.local/bin/xdg-open`.
- `allowedSchemes`: optional list of URI schemes. Default to `http`, `https`,
  and `mailto`.

Implementation files likely touched:

- `internal/config/config.go`
- `dist/cosmonaut.config.example.json`
- `docs/config.md`
- `modules/home-manager.nix`

## Local Open Server

Create a small package, probably `internal/openforward`, responsible for:

- Starting a localhost-only HTTP server.
- Registering per-workspace sessions and tokens.
- Validating incoming requests.
- Calling the platform opener.

Endpoint:

```http
POST /open
Content-Type: application/json
```

Body:

```json
{
  "target": "https://example.com",
  "token": "session-token"
}
```

Validation rules:

- Token must match an active workspace session.
- Target must be non-empty.
- Target scheme must be allowed.
- Requests with multiple targets are rejected.
- The server binds only to `127.0.0.1`.

macOS opener:

```sh
/usr/bin/open <target>
```

Linux can be left as a later platform-specific implementation.

## Reverse SSH Tunnel

Add a daemon-managed tunnel component mirroring the shape of
`internal/daemon/port_forward.go`.

For each active workspace:

```sh
ssh -N -R 127.0.0.1:<remotePort>:127.0.0.1:<localPort> <sshAlias>
```

Responsibilities:

- Keep one tunnel per workspace.
- Reuse the provider-neutral SSH alias returned by `PrepareSSH`.
- Restart or surface errors if the tunnel exits unexpectedly.
- Stop the tunnel when the workspace session is no longer active.

Likely new component:

- `internal/daemon/open_forward.go`
- `internal/daemon/open_forward_test.go`

## Remote Shim

Install a small executable at `~/.local/bin/xdg-open` when configured.

Initial shell-based shim:

```sh
#!/bin/sh
set -eu

target="${1:-}"
if [ -z "$target" ]; then
  echo "xdg-open: missing target" >&2
  exit 2
fi

exec curl -fsS \
  -H "Content-Type: application/json" \
  -d "{\"target\":\"$target\",\"token\":\"$COSMONAUT_OPEN_TOKEN\"}" \
  "$COSMONAUT_OPEN_URL"
```

The production version should avoid fragile shell JSON escaping. Good options:

- Install a tiny static `cosmonaut-open` helper.
- Render a shell shim that uses `python3 -c` or another structured encoder when
  available.
- Keep `xdg-open` as a thin wrapper around that helper.

Remote environment values:

```sh
COSMONAUT_OPEN_URL=http://127.0.0.1:17890/open
COSMONAUT_OPEN_TOKEN=<generated-token>
```

Cosmonaut can also write:

```text
~/.config/cosmonaut/open-forward.env
```

## Launch Flow Integration

Hook into both launch paths after SSH is prepared and before the editor opens:

- CLI path in `main.go`
- applet path in `internal/daemon/gui_flow.go`

Sequence:

1. Resolve or create workspace.
2. Start workspace.
3. Ensure SSH is reachable.
4. Prepare SSH config and get `sshAlias`.
5. If open forwarding is enabled:
   - start or reuse local open server
   - create session token
   - start reverse SSH tunnel
   - install or refresh remote shim
6. Configure editor connection.
7. Launch editor.
8. Track editor and tunnel lifecycle.

## Provider Notes

Coder is the best MVP target because Cosmonaut already has Coder workspace
support and SSH aliases from `coder config-ssh`.

GitHub Codespaces can use the same tunnel and shim approach after SSH aliasing is
prepared. Codespaces may need extra care around devcontainer lifecycle and
whether `~/.local/bin` wins over the system `xdg-open` in PATH.

## Security Defaults

- Feature is disabled by default.
- Local server listens only on `127.0.0.1`.
- Remote tunnel binds only on remote `127.0.0.1`.
- Token is random per workspace session.
- Token is never logged.
- Allowed schemes default to `http`, `https`, and `mailto`.
- `file:` should require explicit opt-in.
- No shell command execution is accepted from the remote side.

## Tests

Unit tests:

- Config parsing and defaults.
- Allowed and rejected URI schemes.
- Token validation.
- Tunnel command construction.
- Shim rendering.
- macOS opener command construction with a fake runner.

Integration smoke test:

1. Launch a Coder workspace with `openForward.enabled`.
2. Confirm the reverse tunnel starts.
3. Run remote `xdg-open https://example.com`.
4. In test mode, assert the local daemon receives the target and would call
   `/usr/bin/open https://example.com`.

## MVP Milestones

1. Add config shape and docs.
2. Implement local open server with fakeable opener.
3. Implement daemon-managed reverse tunnel.
4. Install remote shim over SSH.
5. Wire Coder launch flow.
6. Add tests for validation, command construction, and shim install.
7. Extend to GitHub Codespaces once the Coder flow is stable.
