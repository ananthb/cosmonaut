# Menu Bar Applet

The applet runs as a background process providing quick access to your codespaces from the system tray.

```bash
cosmonaut applet
```

## Features

- **System tray icon**: Z-in-cloud icon; hollow when idle, filled when tracking active codespaces
- **Global hotkey**: configurable shortcut (default `Cmd+Shift+S` on macOS, `Ctrl+Shift+S` on Linux)
- **Tray menu**: default target, recent repos, full picker
- **Port actions**: open/copy Codespaces forwarded-port URLs and start localhost forwards
- **Codespace polling**: monitors running codespaces, sends desktop notifications on state changes
- **Pre-warm**: creates or starts codespaces on a daily schedule before work hours
- **Sleep inhibitor**: optionally holds a sleep/shutdown inhibitor while a launched SSH session is alive (see the `inhibitSleep` daemon option)

The `autoStop` target field is reserved for a future idle-based auto-stop feature but is not yet acted on; codespace idle timeouts today come from `idleTimeout` passed to `gh codespace create`.

## Hotkey

The hotkey launches the configured `defaultTarget` via the same Launch flow as the tray menu. If no `defaultTarget` is set, it opens the interactive picker instead.

## Tray menu structure

The tray menu lists the default target first, then recent repositories from history. Hovering a repository reveals a submenu of its codespaces (up to five, sorted with Available and Starting first).

Each codespace submenu includes a port list loaded from `gh codespace ports`. For each forwarded port you can open the GitHub browse URL, copy it, or start a localhost forward using `gh codespace ports forward <remote>:<local> -c <codespace>`. Cosmonaut uses the same remote and local port for the one-click action, for example `1455:1455`.

If the local port is already in use, including by another codespace forward, Cosmonaut shows a notification instead of starting a duplicate forward.

## Quick reconnect

Selecting a codespace from the submenu runs the launch flow in a progress window. If the codespace is already Available and an SSH config already exists under `~/.ssh/cosmonaut/`, the applet skips the SSH wait and config fetch and hands straight off to the editor.

## Service management

The applet is designed to be managed by your OS service manager.

### macOS (launchd)

The home-manager module automatically creates a launchd agent when `daemon.enable` is set (defaults to `true`).

### Linux (systemd)

The home-manager module creates a systemd user service, or use the included service file manually:

```bash
cp cosmonaut.service ~/.config/systemd/user/
systemctl --user enable --now cosmonaut
```

## Config

See the `daemon` section in [Configuration](config.md#daemon-fields).
