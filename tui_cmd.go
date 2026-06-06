package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/tui"
)

// tuiCmd implements `cosmonaut tui` — a persistent terminal applet that
// mirrors the Fyne GUI's surfaces (workspace list, per-workspace detail,
// settings) inside the terminal. Useful for headless workflows (SSH
// sessions, tmux, Linux without a display) where the tray GUI can't run.
func tuiCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the persistent terminal applet",
		Long: `Open the persistent terminal applet: a keyboard-driven mirror of the
Fyne GUI's workspace list, per-workspace detail (including the SSH option
toggles), and settings, running inside your terminal.

Unlike ` + "`cosmonaut applet`" + `, the TUI does not own the tray icon or
global hotkey — it's a foreground process you exit with q. Unlike
` + "`cosmonaut shell`" + `, it doesn't open an SSH session; it's the
interactive picker/manager you use to start, stop, and open workspaces.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The applet uses Bubbletea's alt-screen mode, which writes
			// terminal control sequences to stdout and reads keys from
			// stdin. Both ends must be a TTY or the user gets garbage
			// (alt-screen escape codes in piped output) and an unusable
			// session. Fail loudly instead of guessing.
			if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
				return fmt.Errorf("cosmonaut tui requires a terminal on both stdin and stdout")
			}
			absPath, err := filepath.Abs(*configPath)
			if err != nil {
				return err
			}
			cfg, _ := config.LoadConfig(absPath)
			if cfg == nil {
				cfg = &config.Config{Targets: map[string]config.Target{}}
			}
			data := tui.NewAppletData(cfg, absPath)
			return tui.RunApplet(data)
		},
	}
}
