# Usage

```
cosmonaut [target] [flags]
```

Run `cosmonaut --help` for flag and config field documentation.

## Interactive mode (no target)

When no target is given, the tool enters a TUI:

1. **Repository picker**: shows repos from your codespaces, config targets, and all your GitHub repositories. Type to filter, or type `owner/repo` to use any repository.
2. **Codespace selector**: pick an existing codespace or create a new one. Press `d` to delete a codespace, `esc` to choose a different repo.

If a `defaultTarget` is set in the config, the tool jumps straight to the codespace selector for that repo. Press `esc` to go back to the full repo picker.

## Auto-select

In non-interactive mode (e.g. when invoked from scripts or the applet), if a repository has exactly one codespace the selector is skipped. When the codespace is already `Available` and an SSH config already exists under `~/.ssh/cosmonaut/`, the tool also skips the SSH wait and config fetch, launching the editor immediately.

## Named target

```bash
cosmonaut work
```

Uses the target definition from the config file directly.

## Direct repository

```bash
cosmonaut owner/repo
```

When the argument contains a `/`, it is treated as a repository name rather than a config target. This builds a default target for that repo (workspace path `/workspaces/<repo>`) and skips the repo picker.

## Flags

| Flag | Description |
|---|---|
| `--config <path>` | Config file path (default `cosmonaut.config.json`) |
| `--codespace <name>` | Launch a specific codespace by name, skipping the selector |
| `--editor <name>` | Editor to launch: `zed` (default) or `neovim` |

The editor can also be set per-config via the top-level `editor` field.

## Scripted lookup (`cosmonaut resolve`)

For scripts that need a workspace's SSH alias without launching an editor,
use the `resolve` subcommand. It performs the same selection as `cosmonaut
shell` (positional target or `--codespace <name>`), writes the OpenSSH
config under `~/.ssh/cosmonaut/`, and prints a JSON document to stdout:

```bash
$ cosmonaut resolve work
{
  "target": "owner/repo",
  "workspace": "cs-abcdef",
  "provider": "github",
  "sshAlias": "cs.cs-abcdef.github.dev",
  "workspacePath": "/workspaces/repo",
  "remoteUrl": "ssh://cs.cs-abcdef.github.dev//workspaces/repo"
}
```

All status output goes to stderr, so the stdout JSON is safe to pipe into
`jq` or another consumer. This subcommand replaces the previous `--no-open`
/ `--dry-run` flags on the root command.

## What it updates

| Path | Description |
|---|---|
| `~/.ssh/config` | Ensures `Include ~/.ssh/cosmonaut/*.conf` exists |
| `~/.ssh/cosmonaut/<codespace>.conf` | OpenSSH config from `gh codespace ssh --config` |
| Zed's `settings.json` | Upserts one `ssh_connections` entry per host alias (Zed editor only) |

Launching with `--editor neovim` opens an SSH session in a terminal emulator and runs `nvim`; it does not write to Zed's settings.

## Forwarded ports

The applet can list URLs for ports already forwarded by GitHub Codespaces:

```bash
gh codespace ports --json browseUrl,label,sourcePort,visibility -c <codespace>
```

From the tray or codespace detail window, each port can be opened in the browser, copied to the clipboard, or forwarded to localhost with:

```bash
gh codespace ports forward <remote-port>:<local-port> -c <codespace>
```

The one-click localhost action uses the same port on both sides, for example `1455:1455`. If that local port is already bound by another process or another codespace forward, Cosmonaut reports the conflict.

## Shell completions

Completions are installed automatically by the nix package. To set them up manually:

=== "bash"

    ```bash
    cosmonaut completion bash > /etc/bash_completion.d/cosmonaut
    ```

=== "zsh"

    ```bash
    cosmonaut completion zsh > "${fpath[1]}/_cosmonaut"
    ```

=== "fish"

    ```bash
    cosmonaut completion fish > ~/.config/fish/completions/cosmonaut.fish
    ```
