package main

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/tui"
)

// tuiCmd implements `cosmonaut tui` — a persistent terminal applet that
// mirrors the Fyne GUI's surfaces (workspace list, per-workspace detail,
// settings) inside the terminal. Useful for headless workflows (SSH
// sessions, tmux, Linux without a display) where the tray GUI can't run.
func tuiCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:          "tui",
		Short:        "Open the persistent terminal applet",
		Long:         `Open the persistent terminal applet: workspace list, per-workspace detail (including the SSH option toggles), and settings, all keyboard-driven.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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
