package main

import (
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/daemon"
	"github.com/linuskendall/cosmonaut/internal/migrate"
)

// appletConfigPath returns the default config path for the applet
// using XDG base directories (works correctly on macOS and Linux).
func appletConfigPath() string {
	return filepath.Join(xdg.ConfigHome, "cosmonaut", "config.json")
}

func appletCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "applet",
		Short: "Run the menu bar applet (tray, hotkey, lifecycle)",
		Long:  `Run the cosmonaut applet: tray icon, global hotkey, and workspace polling.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppletStart(configPath)
		},
	}
}

func runAppletStart(configPath *string) error {
	// Migrate old codespace-zed paths to cosmonaut.
	migrate.Run()

	// If the user didn't explicitly set --config, prefer the XDG config path
	// over the CWD-relative default (which makes no sense for a background applet).
	path := *configPath
	if path == defaultConfigPath {
		xdg := appletConfigPath()
		if _, err := os.Stat(xdg); err == nil {
			path = xdg
		}
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	cfg, _ := config.LoadConfig(absPath)
	if cfg == nil {
		cfg = &config.Config{Targets: map[string]config.Target{}}
	}

	d := daemon.New(cfg, absPath)
	return d.Run()
}
