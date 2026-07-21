// The main window's left pane: logo row, search, workspace tree, and
// account footer.
package daemon

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// buildCosmoSidebar constructs the left pane with title row, search,
// workspace tree, and account footer. Separator canvases give crisp 1px lines
// that respect the theme's border color.
func (uw *unifiedWindow) buildCosmoSidebar() fyne.CanvasObject {
	// Title row: mark + name + "+" action
	mark := canvas.NewImageFromResource(markIconResource())
	mark.SetMinSize(fyne.NewSize(22, 22))
	mark.FillMode = canvas.ImageFillContain

	title := canvas.NewText("Cosmonaut", theme.Color(theme.ColorNameForeground))
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.TextSize = 13

	newBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		uw.showCreateNewGeneric()
	})
	newBtn.Importance = widget.LowImportance

	// Manual refresh: re-poll all providers and rebuild the tree once
	// fresh data lands. Auto-refresh fires on window focus, but a
	// visible button is the escape hatch when the user wants to prove
	// the list is current (e.g. after creating a workspace from the
	// Coder CLI in another window).
	var refreshBtn *widget.Button
	refreshBtn = widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		refreshBtn.Disable()
		uw.daemon.forcePollAsync(func() {
			uw.loadRepos()
			uw.applyFilter()
			uw.tree.Refresh()
			uw.refreshBanner()
			refreshBtn.Enable()
		})
	})
	refreshBtn.Importance = widget.LowImportance

	titleRow := container.NewBorder(nil, nil, container.NewHBox(mark, title), container.NewHBox(refreshBtn, newBtn))

	// Search
	filterEntry := widget.NewEntry()
	filterEntry.PlaceHolder = "Filter workspaces…"
	filterEntry.OnChanged = func(text string) {
		uw.filter = text
		uw.applyFilter()
		uw.tree.Refresh()
	}

	// Tree: reuse structure from gui_window.go but override selection callbacks.
	uw.tree = uw.buildTree()
	uw.tree.OnSelected = func(id widget.TreeNodeID) {
		if isRepoNode(id) {
			repo := repoFromNode(id)
			if len(provider.FilterByRepo(filterWorkspacesByProvider(uw.daemon.Workspaces(), provider.NameGitHub), repo)) > 0 {
				uw.tree.OpenBranch(id)
			}
			uw.showCosmoRepoSummary(repo)
		} else if isWorkspaceNode(id) {
			providerName, name := providerAndNameFromWorkspaceNode(id)
			if providerName == provider.NameGitHub {
				for _, ws := range uw.daemon.Workspaces() {
					if ws.Provider == providerName && ws.Name == name {
						uw.showCosmoCodespaceDetail(name, ws.Repository)
						return
					}
				}
			} else {
				for _, ws := range uw.daemon.Workspaces() {
					if ws.Provider == providerName && ws.Name == name {
						uw.showCoderWorkspaceDetail(ws)
						return
					}
				}
			}
		} else if isNewNode(id) {
			providerName, context := providerAndContextFromNewNode(id)
			if providerName == provider.NameCoder {
				uw.showCosmoCreateNewCoder()
			} else {
				uw.showCosmoCreateNew(context)
			}
		} else if isSectionNode(id) {
			if sectionFromNode(id) == provider.NameCoder {
				uw.showCoderSummary()
			} else {
				uw.showCosmoWelcome()
			}
		}
	}

	// Account footer
	account := uw.buildAccountFooter()

	top := container.NewVBox(
		container.NewPadded(titleRow),
		container.NewPadded(filterEntry),
		thinDivider(),
	)

	bottom := container.NewVBox(
		thinDivider(),
		account,
	)

	return container.NewBorder(top, bottom, nil, nil, uw.tree)
}

// buildAccountFooter shows the signed-in GitHub handle with a small status
// dot. The `gh auth status` probe is a network round-trip, and this builder
// runs on the Fyne main thread (window open, every theme change) — so it
// renders a placeholder immediately and fills the result in from a
// goroutine via fyne.Do.
func (uw *unifiedWindow) buildAccountFooter() fyne.CanvasObject {
	dot := stateDot("Starting")

	handle := canvas.NewText("checking…", theme.Color(theme.ColorNameForeground))
	handle.TextSize = 12
	handle.TextStyle = fyne.TextStyle{Bold: true}

	sub := canvas.NewText("github.com", theme.Color(theme.ColorNamePlaceHolder))
	sub.TextSize = 10
	sub.TextStyle = fyne.TextStyle{Monospace: true}

	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		go uw.daemon.showPreferences()
	})
	settingsBtn.Importance = widget.LowImportance

	info := container.NewVBox(handle, sub)
	footer := container.NewPadded(
		container.NewBorder(nil, nil, container.NewHBox(dot, info), settingsBtn),
	)

	runner := uw.daemon.Runner
	go func() {
		ghUser := "not authenticated"
		authed := false
		if out, err := runner.Run([]string{"auth", "status", "--hostname", "github.com"}); err == nil {
			// Parse "Logged in to github.com account <user>" from output.
			for _, line := range strings.Split(out, "\n") {
				if idx := strings.Index(line, "account "); idx >= 0 {
					parts := strings.Fields(line[idx:])
					if len(parts) >= 2 {
						ghUser = parts[1]
						authed = true
					}
					break
				}
			}
			if !authed {
				ghUser = "authenticated"
				authed = true
			}
		}
		fyne.Do(func() {
			handle.Text = ghUser
			handle.Refresh()
			state := "Stopped"
			if authed {
				state = "Available"
			}
			updateStateDot(dot, state)
			footer.Refresh()
		})
	}()

	return footer
}

// thinDivider returns a 1px canvas line using the theme border color.
func thinDivider() fyne.CanvasObject {
	r := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator))
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

// markIconResource returns the Cosmonaut app mark (used in the sidebar
// header). Points at the embedded SVG; reuses the same asset as the
// dock icon.
func markIconResource() fyne.Resource {
	return fyne.NewStaticResource("mark.svg", iconActiveSVG)
}
