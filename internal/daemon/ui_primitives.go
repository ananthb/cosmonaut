// Cosmonaut native UI primitives.
//
// Small building blocks reused across the unified window, codespace
// detail, create flow, and settings: keeps widget wiring consistent.
package daemon

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"time"

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

// stateDotColor normalizes across the two state vocabularies in play:
// GitHub codespaces report TitleCase ("Available", "Starting"), Coder
// agents report lowercase ("ready", "running", "starting"). Before the
// normalization a running Coder workspace got a grey "disabled" dot next
// to a green RUNNING label.
func stateDotColor(state string) color.Color {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "available", "ready", "running", "connected", "started":
		return statusOK
	case "starting", "pending", "created", "creating", "initializing":
		return statusWarn
	case "stopped", "shutdown":
		return theme.Color(theme.ColorNameDisabled)
	case "error", "failed":
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

func stateColor(state string) color.Color {
	switch state {
	case "Available", "Started", "ready", "running", "connected":
		return statusOK
	case "Starting", "starting", "pending":
		return statusWarn
	case "Error":
		return statusError
	}
	return theme.Color(theme.ColorNamePlaceHolder)
}

// formatTimeAgo turns an ISO 8601 timestamp into a relative time string.
func formatTimeAgo(iso string) string {
	if iso == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d min ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func githubURL(pathSegments ...string) *url.URL {
	u := url.URL{
		Scheme: "https",
		Host:   "github.com",
		Path:   strings.Join(pathSegments, "/"),
	}
	return &u
}
