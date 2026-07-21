package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/config"
)

// launchCmd implements `cosmonaut launch [target]` — a one-shot, no-picker
// editor launch. With no [target] it uses cfg.defaultTarget; with a target
// it behaves the same as `cosmonaut <target>`. The difference from the bare
// command is that it never falls back to the TUI applet — if no target can
// be resolved, it errors instead of opening the picker.
func launchCmd(configPath *string) *cobra.Command {
	var (
		codespaceName string
		editorFlag    string
		controlMaster bool
	)
	cmd := &cobra.Command{
		Use:               "launch [target]",
		Short:             "Launch a workspace in your editor (no picker)",
		Long:              `Launch the editor for [target], or for config.defaultTarget if no target is given. Errors instead of opening the TUI picker.`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		SilenceErrors:     true,
		ValidArgsFunction: completeTargets(configPath),
		RunE: func(cmd *cobra.Command, args []string) error {
			var targetName string
			if len(args) > 0 {
				targetName = args[0]
			}
			if targetName == "" {
				absPath, err := filepath.Abs(*configPath)
				if err != nil {
					return err
				}
				cfg, _ := config.LoadConfig(absPath)
				if cfg == nil || cfg.DefaultTarget == "" {
					return fmt.Errorf("no target was provided and config.defaultTarget is not set")
				}
				targetName = cfg.DefaultTarget
			}
			var cmOverride *bool
			if cmd.Flags().Changed("control-master") {
				v := controlMaster
				cmOverride = &v
			}
			// noPicker: launch's documented contract is to error instead
			// of opening the TUI picker, including on ambiguous or zero
			// workspace matches (run() used to open the applet anyway
			// whenever stdin was a TTY).
			return run(*configPath, targetName, codespaceName, editorFlag, cmOverride, true)
		},
	}
	cmd.Flags().StringVar(&codespaceName, "codespace", "", "launch this codespace, skipping selection")
	cmd.Flags().StringVar(&editorFlag, "editor", "", "editor binary to launch (empty = cfg.Editor)")
	cmd.Flags().BoolVar(&controlMaster, "control-master", true, "use SSH ControlMaster multiplexing for instant reconnects")
	return cmd
}
