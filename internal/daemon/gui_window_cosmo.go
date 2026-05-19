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
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"image/color"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/doctor"
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
		allUserRepos, err := codespace.ListAllRepos(d.Runner)
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
// repo tree, and account footer. Separator canvases give crisp 1px lines
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

	var refreshBtn *widget.Button
	refreshBtn = widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		refreshBtn.Disable()
		go func() {
			uw.daemon.poll()
			allUserRepos, err := codespace.ListAllRepos(uw.daemon.Runner)
			if err != nil {
				log.Printf("gui: refresh repos: %v", err)
			}
			fyne.Do(func() {
				uw.loadRepos()
				if len(allUserRepos) > 0 {
					uw.allRepos = mergeRepos(uw.allRepos, allUserRepos)
				}
				uw.applyFilter()
				uw.tree.Refresh()
				uw.refreshBanner()
				refreshBtn.Enable()
			})
		}()
	})
	refreshBtn.Importance = widget.LowImportance

	titleRow := container.NewBorder(nil, nil, container.NewHBox(mark, title), container.NewHBox(refreshBtn, newBtn))

	// Search
	filterEntry := widget.NewEntry()
	filterEntry.PlaceHolder = "Filter repositories…"
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
			// Auto-expand repos that have codespaces.
			if len(codespace.FilterByRepo(uw.daemon.Codespaces(), repo)) > 0 {
				uw.tree.OpenBranch(id)
			}
			uw.showCosmoRepoSummary(repo)
		} else if isCsNode(id) {
			csName := csNameFromNode(id)
			repo := repoFromCsNode(id)
			uw.showCosmoCodespaceDetail(csName, repo)
		} else if isNewNode(id) {
			repo := repoFromNewNode(id)
			uw.showCosmoCreateNew(repo)
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
		uw.daemon.runLaunchFlow(uw.win, target, resolvedName, cs)
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
		go func() {
			_ = codespace.DeleteCodespace(uw.daemon.Runner, cs.Name)
			fyne.Do(func() {
				uw.tree.Refresh()
				uw.showCosmoWelcome()
			})
		}()
	})

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
	header := container.NewHBox(title, layout.NewSpacer(), refreshBtn)

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
	if uw.daemon.forwards != nil && uw.daemon.forwards.IsActive(csName, remotePort, localPort) {
		forwardBtn = widget.NewButton(fmt.Sprintf("Stop localhost %d", localPort), func() {
			uw.daemon.stopLocalPortForward(csName, remotePort, localPort)
			uw.showCosmoCodespaceDetail(csName, repo)
		})
	} else {
		forwardBtn = widget.NewButton(fmt.Sprintf("Forward localhost %d:%d", remotePort, localPort), func() {
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
	case "Available":
		return cLime
	case "Starting":
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

	sub := canvas.NewText("Select a repository or codespace to get started.", cTextMute)
	sub.TextSize = 12
	sub.Alignment = fyne.TextAlignCenter

	uw.setContent(container.NewCenter(container.NewVBox(
		container.NewCenter(mark),
		h, sub,
	)))
}

// ── REPO SUMMARY ───────────────────────────────────────────────────────

func (uw *unifiedWindow) showCosmoRepoSummary(repo string) {
	all := uw.daemon.Codespaces()
	repoCS := codespace.FilterByRepo(all, repo)

	title := canvas.NewText(repo, cText)
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	countText := fmt.Sprintf("%d codespace(s)", len(repoCS))
	info := canvas.NewText(countText, cTextDim)
	info.TextSize = 13

	createBtn := primaryButton("Create new codespace", func() {
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
	uw.showCosmoCreateNew("")
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
