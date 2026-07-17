// Doctor-driven warning banners shown at the top of the main window.
package daemon

import (
	"image/color"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/linuskendall/cosmonaut/internal/doctor"
	"github.com/linuskendall/cosmonaut/internal/terminal"
)

// refreshBanner re-renders the top banner. The banner is sourced from
// the doctor.Catalog so adding a check there automatically gives it a
// banner. Each banner can be dismissed; dismissed checks remain visible
// in the Settings page Health section.
func (uw *unifiedWindow) refreshBanner() {
	uw.banner.Objects = nil
	for _, c := range doctor.Catalog(uw.daemon.ListErr) {
		issue := c.Status()
		if issue == nil || uw.daemon.IsDismissed(c.ID) {
			continue
		}
		uw.banner.Objects = append(uw.banner.Objects, uw.buildIssueBanner(c, issue))
	}
	uw.banner.Refresh()
}

// buildIssueBanner renders a prominent, dismissable banner for one
// failing check. A tinted background and bold severity badge make the
// banner hard to miss, so users notice cosmonaut needs their attention.
func (uw *unifiedWindow) buildIssueBanner(c doctor.Check, issue *doctor.Issue) fyne.CanvasObject {
	accent := statusWarn
	badgeText := "WARNING"
	if issue.Severity == doctor.SeverityError {
		accent = statusError
		badgeText = "ERROR"
	}

	bg := canvas.NewRectangle(color.NRGBA{accent.R, accent.G, accent.B, 0x22})
	bg.StrokeColor = color.NRGBA{accent.R, accent.G, accent.B, 0x77}
	bg.StrokeWidth = 1
	bg.CornerRadius = 6

	badge := canvas.NewText(badgeText, accent)
	badge.TextSize = 10
	badge.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}

	title := canvas.NewText(c.Title, theme.Color(theme.ColorNameForeground))
	title.TextSize = 13
	title.TextStyle = fyne.TextStyle{Bold: true}

	summary := widget.NewLabel(issue.Summary)
	summary.Wrapping = fyne.TextWrapWord

	titleRow := container.NewHBox(badge, title)
	leftStack := container.NewVBox(titleRow, summary)

	dismissBtn := widget.NewButton("Dismiss", func() {
		uw.daemon.DismissCheck(c.ID)
		uw.refreshBanner()
	})
	dismissBtn.Importance = widget.LowImportance

	actions := container.NewHBox(layout.NewSpacer())
	if fix := uw.fixButton(c); fix != nil {
		actions.Add(fix)
	}
	actions.Add(dismissBtn)

	body := container.NewBorder(nil, actions, nil, nil, leftStack)
	stack := container.NewStack(bg, container.NewPadded(body))
	return container.NewPadded(stack)
}

// fixButton returns the appropriate "fix this" button for a check, or
// nil if no fix is available.
func (uw *unifiedWindow) fixButton(c doctor.Check) *widget.Button {
	switch {
	case c.HasInProcessFix():
		return primaryButton("Fix", func() {
			go func() {
				if err := c.Fix(); err != nil {
					log.Printf("doctor: fix %s: %v", c.ID, err)
				}
				fyne.Do(func() { uw.refreshBanner() })
			}()
		})
	case c.HasTerminalFix():
		return primaryButton("Fix in terminal", func() {
			cmd := c.FixCommand() + `; echo; echo "Press enter to close"; read _`
			go terminal.OpenCommandInTerminal(cmd)
		})
	}
	return nil
}
