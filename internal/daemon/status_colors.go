package daemon

import "image/color"

// Semantic status colors. These intentionally do NOT vary by theme
// variant: "running is green, error is red" should read the same way
// in light mode and dark mode. For chrome colors (text, borders,
// backgrounds) use fyne.io/fyne/v2/theme accessors instead so they
// follow the OS appearance.
var (
	statusOK    = color.NRGBA{0xa3, 0xe6, 0x35, 0xff} // lime
	statusWarn  = color.NRGBA{0xf9, 0x73, 0x16, 0xff} // orange
	statusError = color.NRGBA{0xef, 0x44, 0x44, 0xff} // red
)
