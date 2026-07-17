// Cosmonaut native UI primitives.
//
// Small building blocks reused across the unified window, codespace
// detail, create flow, and settings: keeps widget wiring consistent.
package daemon

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ── stateDot ────────────────────────────────────────────────────────────
// A small filled circle indicating codespace state. Matches the dots
// used in tray menu and sidebar.
func stateDot(state string) *canvas.Circle {
	dot := canvas.NewCircle(stateDotColor(state))
	dot.StrokeWidth = 0
	dot.Resize(fyne.NewSize(8, 8))
	return dot
}

// updateStateDot recolors an existing dot in place (e.g. when an async
// probe finishes). Must be called on the Fyne main thread.
func updateStateDot(dot *canvas.Circle, state string) {
	dot.FillColor = stateDotColor(state)
	dot.Refresh()
}

func stateDotColor(state string) color.Color {
	switch state {
	case "Available":
		return statusOK
	case "Starting":
		return statusWarn
	case "Stopped":
		return theme.Color(theme.ColorNameDisabled)
	case "Error":
		return statusError
	default:
		return theme.Color(theme.ColorNameDisabled)
	}
}

// ── caption ─────────────────────────────────────────────────────────────
// Small uppercase monospace section headers (e.g. "SSH CONNECTION").
func caption(text string) *canvas.Text {
	t := canvas.NewText(text, theme.Color(theme.ColorNamePlaceHolder))
	t.TextSize = 10
	t.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	return t
}

// ── surfaceCard ─────────────────────────────────────────────────────────
// Wraps content in a subtle surface with a 1px border: replaces Fyne's
// default grey "card" style with something closer to the design.
func surfaceCard(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameOverlayBackground))
	bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	bg.StrokeWidth = 1
	bg.CornerRadius = 6
	return container.NewStack(bg, container.NewPadded(content))
}

// ── primaryButton / destructiveButton ───────────────────────────────────
// Fyne's built-in button importance flags map our accent colors in.
func primaryButton(label string, onTap func()) *widget.Button {
	b := widget.NewButton(label, onTap)
	b.Importance = widget.HighImportance // uses theme.ColorNamePrimary (lime)
	return b
}

func destructiveButton(label string, onTap func()) *widget.Button {
	b := widget.NewButton(label, onTap)
	b.Importance = widget.DangerImportance
	return b
}
