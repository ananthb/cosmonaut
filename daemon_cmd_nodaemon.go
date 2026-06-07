//go:build nodaemon

// Build with -tags=nodaemon to skip importing internal/daemon, which
// pulls in golang.design/x/hotkey. The hotkey package's linux/x11 init
// opens an X display at process start and panics when none exists —
// which is always the case in sealed sandboxes like the nix builder.
// Keeping the import out of the root package is what makes `go test
// -tags=nodaemon ./...` succeed there.

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

func appletCmd(*string) *cobra.Command {
	return &cobra.Command{
		Use:    "applet",
		Hidden: true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("applet stubbed out: rebuild without -tags=nodaemon")
		},
	}
}
