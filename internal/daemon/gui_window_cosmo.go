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
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"image/color"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/doctor"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

const (
	cosmoWinW float32 = 560
	cosmoWinH float32 = 400
)

// newCosmoWindow replaces newUnifiedWindow. Swap the call site in
// gui_flow.go's showGUI() to use this constructor.
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
	win.SetOnClosed(func() {
		if d.activeUnifiedWindow() == uw {
			d.setActiveUnifiedWindow(nil)
		}
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

	sidebar := uw.buildCosmoSidebar()
	uw.showCosmoWelcome()

	split := container.NewHSplit(sidebar, uw.content)
	split.Offset = 0.32
	win.SetContent(container.NewBorder(uw.banner, nil, nil, nil, split))
	return uw
}

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
	accent := cOrange
	badgeText := "WARNING"
	if issue.Severity == doctor.SeverityError {
		accent = cRed
		badgeText = "ERROR"
	}

	bg := canvas.NewRectangle(color.NRGBA{accent.R, accent.G, accent.B, 0x22})
	bg.StrokeColor = color.NRGBA{accent.R, accent.G, accent.B, 0x77}
	bg.StrokeWidth = 1
	bg.CornerRadius = 6

	badge := canvas.NewText(badgeText, accent)
	badge.TextSize = 10
	badge.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}

	title := canvas.NewText(c.Title, cText)
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
			go openCommandInTerminal(cmd)
		})
	}
	return nil
}

// buildCosmoSidebar constructs the left pane with title row, search,
// workspace tree, and account footer. Separator canvases give crisp 1px lines
// that respect the theme's border color.
func (uw *unifiedWindow) buildCosmoSidebar() fyne.CanvasObject {
	// Title row: mark + name + "+" action
	mark := canvas.NewImageFromResource(markIconResource())
	mark.SetMinSize(fyne.NewSize(22, 22))
	mark.FillMode = canvas.ImageFillContain

	title := canvas.NewText("Cosmonaut", cText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.TextSize = 13

	newBtn := widget.NewButtonWithIcon("", widget.NewIcon(nil).Resource, func() {
		uw.showCreateNewGeneric()
	})
	newBtn.Importance = widget.LowImportance

	titleRow := container.NewBorder(nil, nil, container.NewHBox(mark, title), container.NewHBox(newBtn))

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

// buildAccountFooter shows the signed-in GitHub handle with a small status dot.
func (uw *unifiedWindow) buildAccountFooter() fyne.CanvasObject {
	// Try to get the GitHub username.
	ghUser := "not authenticated"
	authed := false
	if out, err := uw.daemon.Runner.Run([]string{"auth", "status", "--hostname", "github.com"}); err == nil {
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

	dot := stateDot(func() string {
		if authed {
			return "Available"
		}
		return "Stopped"
	}())

	handle := canvas.NewText(ghUser, cText)
	handle.TextSize = 12
	handle.TextStyle = fyne.TextStyle{Bold: true}

	sub := canvas.NewText("github.com", cTextMute)
	sub.TextSize = 10
	sub.TextStyle = fyne.TextStyle{Monospace: true}

	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		go uw.daemon.showPreferences()
	})
	settingsBtn.Importance = widget.LowImportance

	info := container.NewVBox(handle, sub)
	return container.NewPadded(
		container.NewBorder(nil, nil, container.NewHBox(dot, info), settingsBtn),
	)
}

// thinDivider returns a 1px canvas line using the theme border color.
func thinDivider() fyne.CanvasObject {
	r := canvas.NewRectangle(cBorder)
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

// markIconResource returns the Cosmonaut app mark (used in the sidebar
// header). Points at the embedded SVG; reuses the same asset as the
// dock icon.
func markIconResource() fyne.Resource {
	return fyne.NewStaticResource("mark.svg", iconActiveSVG)
}

// ── CODESPACE DETAIL ────────────────────────────────────────────────────

func (uw *unifiedWindow) showCosmoCodespaceDetail(csName, repo string) {
	var cs *codespace.Codespace
	for _, c := range uw.daemon.Codespaces() {
		if c.Name == csName {
			cs = &c
			break
		}
	}
	if cs == nil {
		uw.showCosmoWelcome()
		return
	}

	target, resolvedName := guiTargetForRepo(uw.daemon.Cfg, repo)

	// ── HEADER: status + title + repo / branch links
	stateLbl := canvas.NewText(strings.ToUpper(cs.State), stateColor(cs.State))
	stateLbl.TextSize = 10
	stateLbl.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	statusRow := container.NewHBox(stateDot(cs.State), stateLbl)

	titleText := cs.DisplayName
	if titleText == "" {
		titleText = cs.Name
	}
	heroTitle := canvas.NewText(titleText, cText)
	heroTitle.TextSize = 16
	heroTitle.TextStyle = fyne.TextStyle{Bold: true}

	branchStr := ""
	if cs.GitStatus != nil {
		branchStr = cs.GitStatus.Ref
		if branchStr == "" {
			branchStr = cs.GitStatus.Branch
		}
	}

	repoLink := widget.NewHyperlink("⌂ "+repo, githubURL(repo))
	branchLink := widget.NewHyperlink("⎇ "+branchStr, githubURL(repo, "tree", branchStr))

	repoRow := container.NewHBox(repoLink, branchLink)

	// ── ACTIONS
	selectedEditor := uw.daemon.getEditor().Name()
	editorSel := widget.NewSelect([]string{"zed", "neovim"}, func(val string) {
		selectedEditor = val
	})
	editorSel.Selected = selectedEditor

	openBtn := primaryButton("Open", func() {
		origEditor := uw.daemon.Cfg.Editor
		uw.daemon.Cfg.Editor = selectedEditor
		workspace := provider.Workspace{
			Provider:    provider.NameGitHub,
			Name:        cs.Name,
			DisplayName: cs.DisplayName,
			Repository:  repo,
			State:       cs.State,
			MachineName: cs.MachineName,
			CreatedAt:   cs.CreatedAt,
			LastUsedAt:  cs.LastUsedAt,
		}
		if cs.GitStatus != nil {
			workspace.Branch = cs.GitStatus.Ref
			if workspace.Branch == "" {
				workspace.Branch = cs.GitStatus.Branch
			}
		}
		uw.daemon.runLaunchFlow(uw.win, target, resolvedName, &workspace)
		uw.daemon.Cfg.Editor = origEditor
	})
	sshBtn := widget.NewButton("SSH", func() {
		go func() {
			sshAlias := fmt.Sprintf("cs.%s.github.dev", cs.Name)
			openSSHInTerminal(sshAlias, target.WorkspacePath)
			if uw.daemon.sessions != nil {
				uw.daemon.sessions.TrackSession(sshAlias)
			}
		}()
	})

	deleteBtn := destructiveButton("Delete", func() {
		uw.daemon.confirmAndDeleteWorkspace(uw.win, provider.NameGitHub, cs.Name, func() {
			uw.tree.Refresh()
			uw.showCosmoWelcome()
		})
	})
	if !uw.daemon.canDeleteWorkspace(provider.NameGitHub) {
		deleteBtn.Disable()
	}

	actions := container.NewHBox(openBtn, editorSel, sshBtn, layout.NewSpacer(), deleteBtn)

	// ── INFO: codespace details + SSH connection
	csNameVal := widget.NewLabel(cs.Name)
	csNameVal.TextStyle = fyne.TextStyle{Monospace: true}
	csNameVal.Truncation = fyne.TextTruncateEllipsis

	machineVal := widget.NewLabel(cs.MachineName)
	machineVal.Truncation = fyne.TextTruncateEllipsis

	createdVal := widget.NewLabel(formatTimeAgo(cs.CreatedAt))
	lastUsedVal := widget.NewLabel(formatTimeAgo(cs.LastUsedAt))

	sshHostVal := widget.NewLabel(fmt.Sprintf("cs.%s.github.dev", cs.Name))
	sshHostVal.TextStyle = fyne.TextStyle{Monospace: true}
	sshHostVal.Truncation = fyne.TextTruncateEllipsis

	pathVal := widget.NewLabel(target.WorkspacePath)
	pathVal.TextStyle = fyne.TextStyle{Monospace: true}
	pathVal.Truncation = fyne.TextTruncateEllipsis

	info := widget.NewForm(
		widget.NewFormItem("Codespace", csNameVal),
		widget.NewFormItem("Machine", machineVal),
		widget.NewFormItem("Created", createdVal),
		widget.NewFormItem("Last used", lastUsedVal),
		widget.NewFormItem("SSH host", sshHostVal),
		widget.NewFormItem("Path", pathVal),
	)

	ports := uw.buildCodespacePortsSection(cs.Name, repo)

	body := container.NewVBox(
		statusRow,
		heroTitle,
		repoRow,
		widget.NewSeparator(),
		actions,
		widget.NewSeparator(),
		info,
		widget.NewSeparator(),
		ports,
	)
	uw.setContent(container.NewPadded(body))
}

func (uw *unifiedWindow) buildCodespacePortsSection(csName, repo string) fyne.CanvasObject {
	title := caption("PORTS")
	refreshBtn := widget.NewButton("Refresh", func() {
		uw.daemon.refreshPortsAsync(csName, func() {
			uw.showCosmoCodespaceDetail(csName, repo)
		})
	})
	forwardBtn := widget.NewButton("Forward port...", func() {
		uw.showAdHocPortForwardDialog(provider.NameGitHub, csName, func() {
			uw.showCosmoCodespaceDetail(csName, repo)
		})
	})
	header := container.NewHBox(title, layout.NewSpacer(), forwardBtn, refreshBtn)

	entry := uw.daemon.ensurePortsWithCallback(csName, func() {
		uw.showCosmoCodespaceDetail(csName, repo)
	})

	var rows []fyne.CanvasObject
	rows = append(rows, header)
	switch {
	case entry.Loading:
		rows = append(rows, widget.NewLabel("Loading forwarded ports..."))
	case entry.Err != nil:
		rows = append(rows, widget.NewLabel("Ports unavailable. Refresh to try again."))
	case len(entry.Ports) == 0:
		rows = append(rows, widget.NewLabel("No forwarded ports."))
	default:
		for _, port := range entry.Ports {
			rows = append(rows, uw.portRow(csName, repo, port))
		}
	}
	return container.NewVBox(rows...)
}

func (uw *unifiedWindow) portRow(csName, repo string, port codespace.Port) fyne.CanvasObject {
	title := widget.NewLabel(codespace.PortLabel(port))
	title.TextStyle = fyne.TextStyle{Bold: true}
	urlLabel := widget.NewLabel(port.BrowseURL)
	if port.BrowseURL == "" {
		urlLabel.SetText("No browse URL")
	}
	urlLabel.Truncation = fyne.TextTruncateEllipsis
	urlLabel.TextStyle = fyne.TextStyle{Monospace: true}

	openBtn := widget.NewButton("Open", func() {
		uw.daemon.openURL(port.BrowseURL)
	})
	copyBtn := widget.NewButton("Copy", func() {
		uw.daemon.copyText(port.BrowseURL)
	})
	if port.BrowseURL == "" {
		openBtn.Disable()
		copyBtn.Disable()
	}

	remotePort := port.SourcePort
	localPort := port.SourcePort
	var forwardBtn *widget.Button
	if uw.daemon.forwards != nil && uw.daemon.forwards.IsActive(provider.NameGitHub, csName, remotePort, localPort) {
		forwardBtn = widget.NewButton(fmt.Sprintf("Stop localhost %d", localPort), func() {
			uw.daemon.stopLocalPortForward(csName, remotePort, localPort)
			uw.showCosmoCodespaceDetail(csName, repo)
		})
	} else {
		forwardBtn = widget.NewButton(fmt.Sprintf("Forward localhost %d", localPort), func() {
			go func() {
				if err := uw.daemon.startLocalPortForward(csName, remotePort, localPort); err != nil {
					uw.daemon.notify(err.Error())
				}
				fyne.Do(func() { uw.showCosmoCodespaceDetail(csName, repo) })
			}()
		})
	}

	left := container.NewVBox(title, urlLabel)
	actions := container.NewHBox(openBtn, copyBtn, forwardBtn)
	return surfaceCard(container.NewBorder(nil, nil, nil, actions, left))
}

func stateColor(state string) color.Color {
	switch state {
	case "Available", "Started", "ready", "running", "connected":
		return cLime
	case "Starting", "starting", "pending":
		return cOrange
	case "Error":
		return cRed
	}
	return cTextMute
}

// ── WELCOME ─────────────────────────────────────────────────────────────

func (uw *unifiedWindow) showCosmoWelcome() {
	mark := canvas.NewImageFromResource(markIconResource())
	mark.SetMinSize(fyne.NewSize(56, 56))
	mark.FillMode = canvas.ImageFillContain

	h := canvas.NewText("Welcome to Cosmonaut", cText)
	h.TextSize = 16
	h.TextStyle = fyne.TextStyle{Bold: true}
	h.Alignment = fyne.TextAlignCenter

	sub := canvas.NewText("Select a GitHub repo or Coder workspace to get started.", cTextMute)
	sub.TextSize = 12
	sub.Alignment = fyne.TextAlignCenter

	uw.setContent(container.NewCenter(container.NewVBox(
		container.NewCenter(mark),
		h, sub,
	)))
}

// ── REPO SUMMARY ───────────────────────────────────────────────────────

func (uw *unifiedWindow) showCosmoRepoSummary(repo string) {
	all := filterWorkspacesByProvider(uw.daemon.Workspaces(), provider.NameGitHub)
	repoCS := provider.FilterByRepo(all, repo)

	title := canvas.NewText(repo, cText)
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	countText := fmt.Sprintf("%d workspace(s)", len(repoCS))
	info := canvas.NewText(countText, cTextDim)
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

// ── CREATE ──────────────────────────────────────────────────────────────

func (uw *unifiedWindow) showCreateNewGeneric() {
	title := canvas.NewText("Create a new workspace", cText)
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	copy := widget.NewLabel("Choose a provider. GitHub creation is repo-based; Coder creation uses configured Coder targets.")
	copy.Wrapping = fyne.TextWrapWord

	githubBtn := primaryButton("GitHub Codespace", func() {
		uw.showCosmoWelcome()
	})
	coderBtn := primaryButton("Coder Workspace", func() {
		uw.showCosmoCreateNewCoder()
	})

	hint := widget.NewLabel("For GitHub, select a repository in the left pane and use its create action.")
	hint.Wrapping = fyne.TextWrapWord

	body := container.NewPadded(container.NewVBox(
		title,
		widget.NewSeparator(),
		copy,
		container.NewHBox(githubBtn, coderBtn),
		hint,
	))
	uw.setContent(container.NewCenter(body))
}

func (uw *unifiedWindow) showCosmoCreateNew(repo string) {
	target, resolvedName := guiTargetForRepo(uw.daemon.Cfg, repo)

	title := canvas.NewText("Create a new codespace", cText)
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	hint := canvas.NewText("A short label makes it easier to find later.", cTextMute)
	hint.TextSize = 12

	repoLbl := widget.NewLabel(repo)

	// Branch selector: starts with config branch or "main", fetches real branches async.
	defaultBranch := target.Branch
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	branchSel := widget.NewSelect([]string{defaultBranch}, func(string) {})
	branchSel.Selected = defaultBranch

	// Fetch branches in background.
	if repo != "" {
		go func() {
			branches := fetchBranches(uw.daemon.Runner, repo)
			if len(branches) > 0 {
				fyne.Do(func() {
					branchSel.Options = branches
					// Keep current selection if it's in the list.
					found := false
					for _, b := range branches {
						if b == branchSel.Selected {
							found = true
							break
						}
					}
					if !found {
						branchSel.Selected = branches[0]
					}
					branchSel.Refresh()
				})
			}
		}()
	}

	labelEntry := widget.NewEntry()
	labelEntry.PlaceHolder = "e.g. fix indexer health checks"

	form := widget.NewForm(
		widget.NewFormItem("Repository", repoLbl),
		widget.NewFormItem("Branch", branchSel),
		widget.NewFormItem("Label", labelEntry),
	)

	createBtn := primaryButton("Create and open", func() {
		text := strings.TrimSpace(labelEntry.Text)
		createTarget := target
		createTarget.DisplayName = text
		createTarget.Branch = branchSel.Selected
		uw.daemon.runCreateAndLaunch(uw.win, createTarget, resolvedName)
	})
	cancelBtn := widget.NewButton("Cancel", func() { uw.showCosmoWelcome() })

	actions := container.NewHBox(layout.NewSpacer(), cancelBtn, createBtn)

	body := container.NewPadded(container.NewVBox(
		title, hint,
		widget.NewSeparator(),
		form,
		actions,
	))
	uw.setContent(container.NewScroll(body))
}

func (uw *unifiedWindow) showCoderWorkspaceDetail(ws provider.Workspace) {
	target, resolvedName := guiTargetForCoderWorkspace(uw.daemon.Cfg, ws)

	stateLbl := canvas.NewText(strings.ToUpper(ws.State), stateColor(ws.State))
	stateLbl.TextSize = 10
	stateLbl.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	statusRow := container.NewHBox(stateDot(ws.State), stateLbl)

	title := ws.DisplayName
	if title == "" {
		title = ws.Name
	}
	heroTitle := canvas.NewText(title, cText)
	heroTitle.TextSize = 16
	heroTitle.TextStyle = fyne.TextStyle{Bold: true}

	subtitle := canvas.NewText("coder", cTextMute)
	subtitle.TextSize = 11
	subtitle.TextStyle = fyne.TextStyle{Monospace: true}

	selectedEditor := uw.daemon.getEditor().Name()
	editorSel := widget.NewSelect([]string{"zed", "neovim"}, func(val string) {
		selectedEditor = val
	})
	editorSel.Selected = selectedEditor

	openBtn := primaryButton("Open", func() {
		origEditor := uw.daemon.Cfg.Editor
		uw.daemon.Cfg.Editor = selectedEditor
		workspace := ws
		uw.daemon.runLaunchFlow(uw.win, target, resolvedName, &workspace)
		uw.daemon.Cfg.Editor = origEditor
	})
	var refreshBtn *widget.Button
	refreshBtn = widget.NewButton("Refresh", func() {
		refreshBtn.Disable()
		uw.daemon.refreshCoderWorkspacesAsync(func() {
			uw.loadRepos()
			uw.applyFilter()
			uw.tree.Refresh()
			for _, latest := range uw.daemon.Workspaces() {
				if latest.Provider == provider.NameCoder && latest.Name == ws.Name {
					uw.showCoderWorkspaceDetail(latest)
					return
				}
			}
			uw.showCoderSummary()
		})
	})

	deleteBtn := destructiveButton("Delete", func() {
		uw.daemon.confirmAndDeleteWorkspace(uw.win, provider.NameCoder, ws.Name, func() {
			uw.tree.Refresh()
			uw.showCoderSummary()
		})
	})
	if !uw.daemon.canDeleteWorkspace(provider.NameCoder) {
		deleteBtn.Disable()
	}

	nameVal := widget.NewLabel(ws.Name)
	nameVal.TextStyle = fyne.TextStyle{Monospace: true}
	stateVal := widget.NewLabel(ws.State)
	templateVal := widget.NewLabel(ws.Template)
	lastUsedVal := widget.NewLabel(formatTimeAgo(ws.LastUsedAt))
	sshHostVal := widget.NewLabel(fmt.Sprintf("%s.coder", ws.Name))
	sshHostVal.TextStyle = fyne.TextStyle{Monospace: true}
	pathVal := widget.NewLabel(guessWorkspacePath(target, &ws))
	pathVal.TextStyle = fyne.TextStyle{Monospace: true}

	info := widget.NewForm(
		widget.NewFormItem("Workspace", nameVal),
		widget.NewFormItem("State", stateVal),
		widget.NewFormItem("Template", templateVal),
		widget.NewFormItem("Last used", lastUsedVal),
		widget.NewFormItem("SSH host", sshHostVal),
		widget.NewFormItem("Path", pathVal),
	)
	portTargetName := coderPortTargetName(uw.daemon.Cfg, ws, resolvedName)
	portTarget := target
	if uw.daemon.Cfg != nil {
		if configured, ok := uw.daemon.Cfg.Targets[portTargetName]; ok {
			portTarget = applyWorkspaceDefaults(configured, ws)
		}
	}
	ports := uw.buildCoderPortsSection(ws, portTarget, portTargetName)

	body := container.NewVBox(
		statusRow,
		heroTitle,
		subtitle,
		widget.NewSeparator(),
		container.NewHBox(openBtn, editorSel, layout.NewSpacer(), refreshBtn, deleteBtn),
		widget.NewSeparator(),
		info,
		widget.NewSeparator(),
		ports,
	)
	uw.setContent(container.NewPadded(body))
}

func (uw *unifiedWindow) buildCoderPortsSection(ws provider.Workspace, target config.Target, targetName string) fyne.CanvasObject {
	title := caption("CONFIGURED PORT FORWARDS")
	adHocBtn := widget.NewButton("Forward port...", func() {
		uw.showAdHocPortForwardDialog(provider.NameCoder, ws.Name, func() {
			uw.showCoderWorkspaceDetail(ws)
		})
	})
	addBtn := primaryButton("Add port forward", func() {
		uw.showCoderPortDialog(ws, target, targetName, -1, nil)
	})

	rows := []fyne.CanvasObject{
		container.NewHBox(title, layout.NewSpacer(), adHocBtn, addBtn),
	}

	if target.Coder == nil || len(target.Coder.PortForwards) == 0 {
		rows = append(rows, widget.NewLabel("No configured Coder port forwards."))
		return container.NewVBox(rows...)
	}
	for i, pf := range target.Coder.PortForwards {
		rows = append(rows, uw.coderPortRow(ws, targetName, i, pf))
	}
	return container.NewVBox(rows...)
}

func (uw *unifiedWindow) coderPortRow(ws provider.Workspace, targetName string, index int, pf config.PortForward) fyne.CanvasObject {
	protocol := normalizePortForwardProtocol(pf.Protocol)
	remotePort := pf.RemotePort
	localPort := pf.LocalPort
	if localPort == 0 {
		localPort = remotePort
	}
	label := pf.Label
	if label == "" {
		label = fmt.Sprintf("%s %d:%d", strings.ToUpper(protocol), localPort, remotePort)
	}
	title := widget.NewLabel(label)
	title.TextStyle = fyne.TextStyle{Bold: true}
	detail := widget.NewLabel(fmt.Sprintf("localhost:%d -> %s:%d", localPort, ws.Name, remotePort))
	detail.TextStyle = fyne.TextStyle{Monospace: true}

	var forwardBtn *widget.Button
	if uw.daemon.forwards != nil && uw.daemon.forwards.IsActiveProtocol(provider.NameCoder, ws.Name, protocol, remotePort, localPort) {
		forwardBtn = widget.NewButton(fmt.Sprintf("Stop localhost %d", localPort), func() {
			uw.daemon.stopWorkspacePortForward(provider.NameCoder, ws.Name, protocol, remotePort, localPort)
			uw.showCoderWorkspaceDetail(ws)
		})
	} else {
		forwardBtn = widget.NewButton(fmt.Sprintf("Forward localhost %d:%d", remotePort, localPort), func() {
			go func() {
				if err := uw.daemon.startWorkspacePortForward(provider.NameCoder, ws.Name, protocol, remotePort, localPort); err != nil {
					uw.daemon.notify(err.Error())
				}
				fyne.Do(func() { uw.showCoderWorkspaceDetail(ws) })
			}()
		})
	}

	editBtn := widget.NewButton("Edit", func() {
		uw.showCoderPortDialog(ws, config.Target{}, targetName, index, &pf)
	})
	removeBtn := widget.NewButton("Remove", func() {
		if err := uw.removeCoderPortForward(targetName, index); err != nil {
			dialog.ShowError(err, uw.win)
			return
		}
		uw.showCoderWorkspaceDetail(ws)
	})
	left := container.NewVBox(title, detail)
	actions := container.NewHBox(forwardBtn, editBtn, removeBtn)
	return surfaceCard(container.NewBorder(nil, nil, nil, actions, left))
}

func (uw *unifiedWindow) showCoderPortDialog(ws provider.Workspace, target config.Target, targetName string, index int, existing *config.PortForward) {
	labelEntry := widget.NewEntry()
	labelEntry.PlaceHolder = "app"
	localEntry := widget.NewEntry()
	localEntry.PlaceHolder = "8080"
	remoteEntry := widget.NewEntry()
	remoteEntry.PlaceHolder = "3000"
	protocolSelect := widget.NewSelect([]string{"tcp", "udp"}, nil)
	protocolSelect.Selected = "tcp"
	if existing != nil {
		labelEntry.SetText(existing.Label)
		if existing.LocalPort > 0 {
			localEntry.SetText(strconv.Itoa(existing.LocalPort))
		}
		if existing.RemotePort > 0 {
			remoteEntry.SetText(strconv.Itoa(existing.RemotePort))
		}
		protocolSelect.Selected = normalizePortForwardProtocol(existing.Protocol)
	}

	items := []*widget.FormItem{
		widget.NewFormItem("Label", labelEntry),
		widget.NewFormItem("Local port", localEntry),
		widget.NewFormItem("Remote port", remoteEntry),
		widget.NewFormItem("Protocol", protocolSelect),
	}
	title := "Add Coder port forward"
	if existing != nil {
		title = "Edit Coder port forward"
	}
	dialog.ShowForm(title, "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		remotePort, err := strconv.Atoi(strings.TrimSpace(remoteEntry.Text))
		if err != nil || remotePort <= 0 || remotePort > 65535 {
			dialog.ShowError(fmt.Errorf("remote port must be between 1 and 65535"), uw.win)
			return
		}
		localPort := remotePort
		if strings.TrimSpace(localEntry.Text) != "" {
			localPort, err = strconv.Atoi(strings.TrimSpace(localEntry.Text))
			if err != nil || localPort <= 0 || localPort > 65535 {
				dialog.ShowError(fmt.Errorf("local port must be between 1 and 65535"), uw.win)
				return
			}
		}
		protocol := normalizePortForwardProtocol(protocolSelect.Selected)
		if protocol != "tcp" && protocol != "udp" {
			protocol = "tcp"
		}

		pf := config.PortForward{
			Label:      strings.TrimSpace(labelEntry.Text),
			LocalPort:  localPort,
			RemotePort: remotePort,
			Protocol:   protocol,
		}
		var saveErr error
		if existing == nil {
			saveErr = uw.addCoderPortForward(targetName, ws, target, pf)
		} else {
			saveErr = uw.updateCoderPortForward(targetName, index, pf)
		}
		if saveErr != nil {
			dialog.ShowError(saveErr, uw.win)
			return
		}
		uw.showCoderWorkspaceDetail(ws)
	}, uw.win)
}

// showAdHocPortForwardDialog prompts for remote/local port (+ protocol for
// providers that support UDP) and starts a one-off workspace port forward.
// The forward is not saved to config — it lives for the daemon's lifetime.
func (uw *unifiedWindow) showAdHocPortForwardDialog(providerName, workspaceName string, onStarted func()) {
	remoteEntry := widget.NewEntry()
	remoteEntry.PlaceHolder = "3000"
	localEntry := widget.NewEntry()
	localEntry.PlaceHolder = "same as remote"

	items := []*widget.FormItem{
		widget.NewFormItem("Remote port", remoteEntry),
		widget.NewFormItem("Local port", localEntry),
	}
	var protocolSelect *widget.Select
	if providerName == provider.NameCoder {
		protocolSelect = widget.NewSelect([]string{"tcp", "udp"}, nil)
		protocolSelect.Selected = "tcp"
		items = append(items, widget.NewFormItem("Protocol", protocolSelect))
	}

	title := fmt.Sprintf("Forward port — %s", workspaceName)
	dialog.ShowForm(title, "Forward", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		remotePort, err := strconv.Atoi(strings.TrimSpace(remoteEntry.Text))
		if err != nil || remotePort <= 0 || remotePort > 65535 {
			dialog.ShowError(fmt.Errorf("remote port must be between 1 and 65535"), uw.win)
			return
		}
		localPort := remotePort
		if strings.TrimSpace(localEntry.Text) != "" {
			localPort, err = strconv.Atoi(strings.TrimSpace(localEntry.Text))
			if err != nil || localPort <= 0 || localPort > 65535 {
				dialog.ShowError(fmt.Errorf("local port must be between 1 and 65535"), uw.win)
				return
			}
		}
		protocol := "tcp"
		if protocolSelect != nil {
			protocol = normalizePortForwardProtocol(protocolSelect.Selected)
		}
		go func() {
			if err := uw.daemon.startWorkspacePortForward(providerName, workspaceName, protocol, remotePort, localPort); err != nil {
				fyne.Do(func() { dialog.ShowError(err, uw.win) })
				return
			}
			if onStarted != nil {
				fyne.Do(onStarted)
			}
		}()
	}, uw.win)
}

func (uw *unifiedWindow) addCoderPortForward(targetName string, ws provider.Workspace, target config.Target, pf config.PortForward) error {
	if uw.daemon.Cfg == nil {
		return fmt.Errorf("no config is loaded, so port forwards cannot be saved")
	}
	if uw.daemon.Cfg.Targets == nil {
		uw.daemon.Cfg.Targets = map[string]config.Target{}
	}
	targetName = strings.TrimSpace(targetName)
	if targetName == "" {
		targetName = ws.Name
	}
	current, ok := uw.daemon.Cfg.Targets[targetName]
	if !ok {
		current = target
	}
	current = applyWorkspaceDefaults(current, ws)
	if current.Coder == nil {
		current.Coder = &config.CoderTargetConfig{}
	}
	current.Coder.WorkspaceName = ws.Name
	current.Coder.PortForwards = append(current.Coder.PortForwards, pf)
	uw.daemon.Cfg.Targets[targetName] = current
	uw.daemon.persistConfig()
	return nil
}

func (uw *unifiedWindow) updateCoderPortForward(targetName string, index int, pf config.PortForward) error {
	if uw.daemon.Cfg == nil || uw.daemon.Cfg.Targets == nil {
		return fmt.Errorf("no config is loaded, so port forwards cannot be saved")
	}
	target, ok := uw.daemon.Cfg.Targets[targetName]
	if !ok || target.Coder == nil {
		return fmt.Errorf("coder target %q was not found in config", targetName)
	}
	if index < 0 || index >= len(target.Coder.PortForwards) {
		return fmt.Errorf("port forward no longer exists")
	}
	target.Coder.PortForwards[index] = pf
	uw.daemon.Cfg.Targets[targetName] = target
	uw.daemon.persistConfig()
	return nil
}

func (uw *unifiedWindow) removeCoderPortForward(targetName string, index int) error {
	if uw.daemon.Cfg == nil || uw.daemon.Cfg.Targets == nil {
		return fmt.Errorf("no config is loaded, so port forwards cannot be saved")
	}
	target, ok := uw.daemon.Cfg.Targets[targetName]
	if !ok || target.Coder == nil {
		return fmt.Errorf("coder target %q was not found in config", targetName)
	}
	if index < 0 || index >= len(target.Coder.PortForwards) {
		return fmt.Errorf("port forward no longer exists")
	}
	target.Coder.PortForwards = append(target.Coder.PortForwards[:index], target.Coder.PortForwards[index+1:]...)
	uw.daemon.Cfg.Targets[targetName] = target
	uw.daemon.persistConfig()
	return nil
}

func coderPortTargetName(cfg *config.Config, ws provider.Workspace, fallback string) string {
	if cfg != nil {
		for name, target := range cfg.Targets {
			if target.Coder != nil && target.Coder.WorkspaceName == ws.Name {
				return name
			}
		}
	}
	if strings.TrimSpace(fallback) != "" && fallback == ws.Name {
		return fallback
	}
	return ws.Name
}

func (uw *unifiedWindow) showCosmoCreateNewCoder() {
	title := canvas.NewText("Create a new Coder workspace", cText)
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	targetNames := configuredCoderTargets(uw.daemon.Cfg)
	if len(targetNames) == 0 {
		uw.showCosmoCreateNewCoderFromTemplates(title)
		return
	}

	targetSel := widget.NewSelect(targetNames, func(string) {})
	targetSel.SetSelected(targetNames[0])
	baseTarget := uw.daemon.Cfg.Targets[targetNames[0]]

	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "e.g. my-repo-review"
	if baseTarget.Coder != nil && baseTarget.Coder.WorkspaceName != "" {
		nameEntry.SetText(baseTarget.Coder.WorkspaceName)
	}

	pathEntry := widget.NewEntry()
	pathEntry.PlaceHolder = "/workspaces/my-repo"
	pathEntry.SetText(guessWorkspacePath(baseTarget, nil))

	targetSel.OnChanged = func(name string) {
		t := uw.daemon.Cfg.Targets[name]
		if t.Coder != nil && t.Coder.WorkspaceName != "" {
			nameEntry.SetText(t.Coder.WorkspaceName)
		}
		pathEntry.SetText(guessWorkspacePath(t, nil))
	}

	form := widget.NewForm(
		widget.NewFormItem("Target", targetSel),
		widget.NewFormItem("Workspace name", nameEntry),
		widget.NewFormItem("Workspace path", pathEntry),
	)

	hint := widget.NewLabel("")
	hint.Wrapping = fyne.TextWrapWord

	createBtn := primaryButton("Create and open", func() {
		targetName := targetSel.Selected
		target := uw.daemon.Cfg.Targets[targetName]
		if target.Coder == nil {
			hint.SetText("Selected target is missing coder settings.")
			return
		}
		name := coderWorkspaceNameFromInput(nameEntry.Text)
		if name == "" {
			hint.SetText("Enter a workspace name.")
			return
		}
		target.WorkspacePath = strings.TrimSpace(pathEntry.Text)
		if target.WorkspacePath == "" {
			target.WorkspacePath = "/workspaces/" + name
		}
		target.Coder.WorkspaceName = name
		uw.daemon.runCreateAndLaunch(uw.win, target, targetName)
	})
	cancelBtn := widget.NewButton("Cancel", func() { uw.showCosmoWelcome() })

	actions := container.NewHBox(layout.NewSpacer(), cancelBtn, createBtn)
	body := container.NewPadded(container.NewVBox(
		title,
		widget.NewSeparator(),
		form,
		hint,
		actions,
	))
	uw.setContent(container.NewScroll(body))
}

func (uw *unifiedWindow) showCosmoCreateNewCoderFromTemplates(title *canvas.Text) {
	manager := provider.NewCoderManager(uw.daemon.Cfg)
	templates, err := manager.ListTemplates()
	if err != nil {
		msg := widget.NewLabel(fmt.Sprintf("Could not load Coder templates automatically: %v", err))
		msg.Wrapping = fyne.TextWrapWord
		uw.setContent(container.NewPadded(container.NewVBox(title, widget.NewSeparator(), msg)))
		return
	}
	if len(templates) == 0 {
		msg := widget.NewLabel("No Coder templates were found.")
		msg.Wrapping = fyne.TextWrapWord
		uw.setContent(container.NewPadded(container.NewVBox(title, widget.NewSeparator(), msg)))
		return
	}

	templateNames := make([]string, 0, len(templates))
	templateByName := make(map[string]provider.CoderTemplate, len(templates))
	for _, tpl := range templates {
		templateNames = append(templateNames, tpl.Name)
		templateByName[tpl.Name] = tpl
	}

	templateSel := widget.NewSelect(templateNames, func(string) {})
	templateSel.SetSelected(templateNames[0])

	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "e.g. my-repo-review"

	pathEntry := widget.NewEntry()
	pathEntry.PlaceHolder = "/workspaces/my-workspace"

	form := widget.NewForm(
		widget.NewFormItem("Template", templateSel),
		widget.NewFormItem("Workspace name", nameEntry),
		widget.NewFormItem("Workspace path", pathEntry),
	)

	hint := widget.NewLabel("Using live Coder templates because no Coder target is configured.")
	hint.Wrapping = fyne.TextWrapWord

	createBtn := primaryButton("Create and open", func() {
		name := coderWorkspaceNameFromInput(nameEntry.Text)
		if name == "" {
			hint.SetText("Enter a workspace name.")
			return
		}
		target := config.Target{
			WorkspacePath: strings.TrimSpace(pathEntry.Text),
			Coder: &config.CoderTargetConfig{
				Template:      templateSel.Selected,
				WorkspaceName: name,
				Organization:  templateByName[templateSel.Selected].Organization,
			},
		}
		if target.WorkspacePath == "" {
			target.WorkspacePath = "/workspaces/" + name
		}
		uw.daemon.runCreateAndLaunch(uw.win, target, name)
	})
	cancelBtn := widget.NewButton("Cancel", func() { uw.showCosmoWelcome() })

	body := container.NewPadded(container.NewVBox(
		title,
		widget.NewSeparator(),
		form,
		hint,
		container.NewHBox(layout.NewSpacer(), cancelBtn, createBtn),
	))
	uw.setContent(container.NewScroll(body))
}

// fetchBranches returns branch names for a repo, default branch first.
func fetchBranches(runner codespace.GHRunner, repo string) []string {
	// Get default branch.
	defOut, err := runner.Run([]string{
		"api", fmt.Sprintf("repos/%s", repo),
		"--jq", ".default_branch",
	})
	defaultBranch := strings.TrimSpace(defOut)
	if err != nil || defaultBranch == "" {
		defaultBranch = "main"
	}

	// Get all branches.
	out, err := runner.Run([]string{
		"api", fmt.Sprintf("repos/%s/branches", repo),
		"--paginate", "--jq", ".[].name",
	})
	if err != nil {
		return []string{defaultBranch}
	}

	var branches []string
	branches = append(branches, defaultBranch) // default first
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		b := strings.TrimSpace(line)
		if b != "" && b != defaultBranch {
			branches = append(branches, b)
		}
	}
	return branches
}

// githubURL builds a GitHub URL from path segments. No parsing needed since
// we construct the URL struct directly.
// formatTimeAgo turns an ISO 8601 timestamp into a relative time string.
func formatTimeAgo(iso string) string {
	if iso == "" {
		return ":"
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

// openSSHInTerminal opens an SSH session to a codespace in the default terminal.
func openSSHInTerminal(sshAlias, workspacePath string) {
	sshCmd := fmt.Sprintf("ssh -t %s 'cd %s && exec $SHELL -l'", sshAlias, workspacePath)
	openCommandInTerminal(sshCmd)
}

// openCommandInTerminal launches the platform's default terminal emulator
// running the given shell command. Used for any flow that needs a real TTY
// (gh device-flow auth, SSH).
func openCommandInTerminal(shellCmd string) {
	if runtime.GOOS == "darwin" {
		script := fmt.Sprintf(`tell application "Terminal"
activate
do script "%s"
end tell`, shellCmd)
		exec.Command("osascript", "-e", script).Run()
		return
	}
	for _, term := range []string{"ghostty", "alacritty", "kitty", "gnome-terminal", "xterm"} {
		if _, err := exec.LookPath(term); err == nil {
			exec.Command(term, "-e", "sh", "-c", shellCmd).Run()
			return
		}
	}
}
