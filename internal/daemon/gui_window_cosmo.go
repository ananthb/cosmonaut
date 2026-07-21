// Cosmonaut unified window: native Fyne implementation of the redesign.
//
// Layout:
//
//	┌────────────┬──────────────────────────────────────┐
//	│  Sidebar   │  Detail panel                        │
//	│  (logo,    │  (codespace detail / create / repo)  │
//	│   search,  │                                      │
//	│   tree,    │                                      │
//	│   account) │                                      │
//	└────────────┴──────────────────────────────────────┘
//
// The bulk of structure mirrors the existing gui_window.go; this file
// rewrites the visual wrapping (title bar, search chrome, captions,
// action buttons) to match the design system.
package daemon

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

const (
	cosmoWinW float32 = 560
	cosmoWinH float32 = 400
)

// newCosmoWindow constructs the main window: sidebar + content split,
// doctor banner, workspace/theme listeners, and the async repo fetch.
// The per-view builders live in gui_sidebar.go, gui_banner.go,
// gui_detail_github.go, gui_detail_coder.go, gui_ports.go, and
// gui_create.go.
func (d *Daemon) newCosmoWindow() *unifiedWindow {
	win := d.app.NewWindow("Cosmonaut")
	win.Resize(fyne.NewSize(cosmoWinW, cosmoWinH))
	win.CenterOnScreen()

	uw := &unifiedWindow{
		daemon:  d,
		win:     win,
		content: container.NewStack(),
		banner:  container.NewVBox(),
	}
	uw.loadRepos()
	uw.refreshBanner()
	d.setActiveUnifiedWindow(uw)

	// Subscribe to poll-driven workspace changes so the sidebar tree
	// refreshes on its own once new data lands. The listener guards on
	// uw.tree because it can fire before buildCosmoSidebar runs (the
	// initial poll may finish between setActiveUnifiedWindow and
	// SetContent), and unsubscribes on window close to avoid firing
	// into freed widgets after the user dismisses the picker.
	unsubscribe := d.AddWorkspaceListener(func() {
		uw.loadRepos()
		uw.applyFilter()
		if uw.tree != nil {
			uw.tree.Refresh()
		}
		uw.refreshBanner()
	})

	// Background fetch of all user repos.
	go func() {
		allUserRepos, err := provider.NewGitHubManager(d.Runner).ListRepositories()
		if err != nil {
			log.Printf("gui: fetch repos: %v", err)
			return
		}
		fyne.Do(func() {
			uw.allRepos = mergeRepos(uw.allRepos, allUserRepos)
			uw.applyFilter()
			uw.tree.Refresh()
		})
	}()

	rebuild := func() {
		sidebar := uw.buildCosmoSidebar()
		split := container.NewHSplit(sidebar, uw.content)
		split.Offset = 0.32
		win.SetContent(container.NewBorder(uw.banner, nil, nil, nil, split))
	}
	rebuild()
	uw.showCosmoWelcome()

	// Tree selection and branch expansion are reset on theme change.
	unsubscribeTheme := d.addThemeListener(func() {
		rebuild()
		uw.refreshBanner()
		if uw.currentView != nil {
			uw.currentView()
		}
	})

	win.SetOnClosed(func() {
		unsubscribe()
		unsubscribeTheme()
		if d.activeUnifiedWindow() == uw {
			d.setActiveUnifiedWindow(nil)
		}
	})
	return uw
}

// stillOn reports whether the user is still looking at the view
// identified by id. Async done-callbacks (port fetches, forward starts,
// refreshes) gate their re-render on it so a slow operation can't snap
// the window back to a view the user already left. Main thread only.
func (uw *unifiedWindow) stillOn(id string) bool {
	return uw.currentViewID == id
}

// ── WELCOME ─────────────────────────────────────────────────────────────

func (uw *unifiedWindow) showCosmoWelcome() {
	uw.currentView = uw.showCosmoWelcome
	uw.currentViewID = "welcome"
	mark := canvas.NewImageFromResource(markIconResource())
	mark.SetMinSize(fyne.NewSize(56, 56))
	mark.FillMode = canvas.ImageFillContain

	h := canvas.NewText("Welcome to Cosmonaut", theme.Color(theme.ColorNameForeground))
	h.TextSize = 16
	h.TextStyle = fyne.TextStyle{Bold: true}
	h.Alignment = fyne.TextAlignCenter

	sub := canvas.NewText("Select a GitHub repo or Coder workspace to get started.", theme.Color(theme.ColorNamePlaceHolder))
	sub.TextSize = 12
	sub.Alignment = fyne.TextAlignCenter

	uw.setContent(container.NewCenter(container.NewVBox(
		container.NewCenter(mark),
		h, sub,
	)))
}

// ── REPO SUMMARY ───────────────────────────────────────────────────────

func (uw *unifiedWindow) showCosmoRepoSummary(repo string) {
	uw.currentView = func() { uw.showCosmoRepoSummary(repo) }
	uw.currentViewID = "repo:" + repo
	all := filterWorkspacesByProvider(uw.daemon.Workspaces(), provider.NameGitHub)
	repoCS := provider.FilterByRepo(all, repo)

	title := canvas.NewText(repo, theme.Color(theme.ColorNameForeground))
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	countText := fmt.Sprintf("%d workspace(s)", len(repoCS))
	info := canvas.NewText(countText, theme.Color(theme.ColorNamePlaceHolder))
	info.TextSize = 13

	createBtn := primaryButton("Create new GitHub codespace", func() {
		uw.showCosmoCreateNew(repo)
	})

	uw.setContent(container.NewCenter(container.NewVBox(
		title, info,
		widget.NewSeparator(),
		createBtn,
	)))
}
