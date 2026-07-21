// Package daemon: Cosmonaut Fyne theme.
//
// Thin wrapper around the default Fyne theme. Overrides only the
// Cosmonaut lime brand accent (primary / focus / selection) and a few
// type-scale tweaks. Backgrounds, foregrounds, and dark/light variant
// switching delegate to theme.DefaultTheme(), so the app follows the
// OS appearance just like every other Fyne app.
package daemon

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type cosmoTheme struct{}

func (cosmoTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary, theme.ColorNameSuccess:
		return statusOK
	case theme.ColorNameFocus:
		return color.NRGBA{0xa3, 0xe6, 0x35, 0x40}
	case theme.ColorNameSelection:
		return color.NRGBA{0xa3, 0xe6, 0x35, 0x33}
	}
	return theme.DefaultTheme().Color(name, variant)
}

func (cosmoTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (cosmoTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (cosmoTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameHeadingText:
		return 18
	case theme.SizeNameSubHeadingText:
		return 15
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 8
	}
	return theme.DefaultTheme().Size(name)
}

func newCosmoTheme() fyne.Theme { return cosmoTheme{} }
