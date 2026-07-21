//go:build !nodaemon

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/daemon"
	"github.com/linuskendall/cosmonaut/internal/migrate"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
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
			return runAppletStart(configPath, cmd.Flags().Changed("config"))
		},
	}
}

func runAppletStart(configPath *string, configFlagSet bool) error {
	// Migrate old codespace-zed paths to cosmonaut.
	migrate.Run()

	// If the user didn't explicitly set --config, prefer the XDG config
	// path over the CWD-relative default (which makes no sense for a
	// background applet). "Explicitly" means the flag was actually passed
	// — comparing values meant an explicit `--config cosmonaut.config.json`
	// was silently overridden by the XDG file.
	path := *configPath
	if !configFlagSet && path == defaultConfigPath {
		xdgPath := appletConfigPath()
		if _, err := os.Stat(xdgPath); err == nil {
			path = xdgPath
		}
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	// Malformed config: refuse to start rather than silently running the
	// applet with empty defaults (no targets, no hotkey). A missing file
	// is fine — first-run applets have no config yet.
	cfg, err := config.LoadConfig(absPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil {
		cfg = &config.Config{Targets: map[string]config.Target{}}
	}

	// migrate.Run() above ran refreshSSHExtras with nil options (defaults
	// only) so the v2→v3 marker rewrite happens unconditionally on the
	// CLI path. Now that config is loaded, do a second sweep that
	// re-applies each workspace's persisted ControlMaster preference so
	// users who had it enabled don't lose it on first launch after
	// upgrade.
	optsFor := func(filename string) sshconfig.ManagedExtrasOptions {
		provider, name := sshconfig.ProviderAndNameFromFilename(filename)
		return sshconfig.ManagedExtrasOptions{
			ControlMaster: cfg.WorkspaceSSHControlMaster(provider, name),
		}
	}
	paths := sshconfig.ResolvePaths()
	if _, err := sshconfig.RefreshAllManagedExtras(paths.IncludeDir, optsFor); err != nil {
		log.Printf("post-config ssh refresh: %v", err)
	}

	d := daemon.New(cfg, absPath)
	return d.Run()
}
