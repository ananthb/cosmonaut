package daemon

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// SVGs (not PNGs) are embedded for the tray because theme.NewThemedResource
// recolors SVG fills/strokes via regex but is a no-op on bitmap formats.
// The source SVGs are authored in black; the cosmo theme's white foreground
// is what actually makes them visible against GNOME's dark top bar.

//go:embed assets/icon.svg
var iconIdleSVG []byte

//go:embed assets/icon_active.svg
var iconActiveSVG []byte

//go:embed assets/icon_starting.svg
var iconStartingSVG []byte

// trayIconIdle returns the helmet icon (no active codespaces).
func trayIconIdle() fyne.Resource {
	return theme.NewThemedResource(fyne.NewStaticResource("icon.svg", iconIdleSVG))
}

// trayIconActive returns the helmet icon with filled dot (codespaces running).
func trayIconActive() fyne.Resource {
	return theme.NewThemedResource(fyne.NewStaticResource("icon_active.svg", iconActiveSVG))
}

// trayIconStarting returns the helmet icon with half dot (codespaces starting).
func trayIconStarting() fyne.Resource {
	return theme.NewThemedResource(fyne.NewStaticResource("icon_starting.svg", iconStartingSVG))
}

// appIcon returns the app icon for dock/taskbar. Reuses the active-state
// SVG (helmet + filled dot) so the dock badge matches the tray look.
func appIcon() fyne.Resource {
	return fyne.NewStaticResource("icon.svg", iconActiveSVG)
}
