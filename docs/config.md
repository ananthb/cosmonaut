# Configuration

cosmonaut uses a JSONC config file (comments and trailing commas are supported).

The default path is `cosmonaut.config.json` in the current directory for the CLI, or `~/.config/cosmonaut/config.json` (XDG) for the applet.

## Example

```json
{
  // Default target to use when no target name is given.
  "defaultTarget": "work",

  // Global workspace provider: "github" (default) or "coder".
  "workspaceProvider": "coder",

  // Editor to launch: "zed" (default) or "neovim".
  "editor": "zed",

  "providers": {
    "coder": {
      "organization": "coder"
    }
  },

  // Applet settings (menu bar icon, hotkey, codespace lifecycle).
  "daemon": {
    "hotkey": "Cmd+Shift+S",
    "hotkeyAction": "picker"
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
        "stopAfter": "8h",
        "portForwards": [
          {
            "label": "app",
            "localPort": 8080,
            "remotePort": 3000,
            "protocol": "tcp"
          }
        ]
      }
    }
  }
}
```

## Top-level fields

| Field | Type | Description |
|---|---|---|
| `defaultTarget` | string | Target name to use when no target is passed on the CLI |
| `workspaceProvider` | string | `github` (default) or `coder` |
| `editor` | string | `zed` (default) or `neovim`; overridden by `--editor` |
| `providers` | object | Provider-specific defaults such as `providers.coder.organization` |
| `targets` | object | Map of target name to target definition |
| `daemon` | object | Applet settings (see [Daemon fields](#daemon-fields)) |

## Target fields

| Field | Type | Required | Description |
|---|---|---|---|
| `repository` | string | | GitHub repository in `owner/repo` form; optional for Coder targets |
| `branch` | string | | Preferred branch when creating or matching a codespace |
| `displayName` | string | | Exact display name to disambiguate codespace matches |
| `codespaceName` | string | | Exact codespace name for strict reuse |
| `workspacePath` | string | yes | Remote folder Zed should open (e.g. `/workspaces/repo`) |
| `machine` | string | | Machine type forwarded to `gh codespace create` |
| `location` | string | | Location forwarded to `gh codespace create` |
| `devcontainerPath` | string | | Dev container config path |
| `idleTimeout` | string | | Idle timeout (e.g. `30m`) |
| `retentionPeriod` | string | | Retention period (e.g. `720h`) |
| `uploadBinaryOverSsh` | bool | | Zed's `upload_binary_over_ssh` setting |
| `zedNickname` | string | | Friendly name in Zed's remote project list |
| `autoStop` | string | | Reserved for a future auto-stop feature; currently parsed but not acted on |
| `preWarm` | string | | Time-of-day to pre-warm codespace (applet only, e.g. `08:00`) |
| `coder` | object | | Coder-specific target settings: `template`, `workspaceName`, `parameters`, `stopAfter`, `organization`, `portForwards` |

### Coder port forwards

Configured Coder port forwards appear in the applet workspace detail screen and tray menu. They run `coder port-forward <workspace> --tcp <localPort>:<remotePort>` by default. Set `protocol` to `udp` to use `--udp`.

| Field | Type | Required | Description |
|---|---|---|---|
| `label` | string | | Friendly label shown in the applet |
| `localPort` | number | | Localhost port to bind; defaults to `remotePort` when omitted |
| `remotePort` | number | yes | Port inside the Coder workspace |
| `protocol` | string | | `tcp` (default) or `udp` |

## Daemon fields

These go in the top-level `"daemon"` object and configure the menu bar applet.

| Field | Type | Description |
|---|---|---|
| `hotkey` | string | Global hotkey (default `Cmd+Shift+S` on macOS, `Ctrl+Shift+S` elsewhere) |
| `hotkeyAction` | string | `picker` (default), `previous`, or `default` |
| `terminal` | string | Reserved for selecting the terminal emulator used to launch Neovim. Parsed but not yet wired up; Neovim currently auto-detects (Terminal.app on macOS, first of ghostty/alacritty/kitty/gnome-terminal/xterm on Linux). |
| `inhibitSleep` | string | Hold a sleep inhibitor while a launched SSH session is alive: `off` (default), `sleep`, or `sleep+shutdown`. On macOS, `sleep+shutdown` degrades to `sleep` (no user-space shutdown inhibitor). |
