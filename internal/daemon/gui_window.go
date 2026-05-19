package daemon

import (
	"fmt"
	"log"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/history"
)

const (
	guiWidth  float32 = 560
	guiHeight float32 = 400
)

// unifiedWindow is the main Cosmonaut window with sidebar + content.
type unifiedWindow struct {
	daemon  *Daemon
	win     fyne.Window
	content *fyne.Container // stack container for swapping content panels
	banner  *fyne.Container // top banner for transient warnings; empty when nothing to show
	tree    *widget.Tree

	// Data for the tree.
	allRepos    []string
	recentCount int
	filter      string
	filtered    []string // repos matching current filter
}

func (d *Daemon) newUnifiedWindow() *unifiedWindow {
	win := d.app.NewWindow("Cosmonaut")
	win.Resize(fyne.NewSize(guiWidth, guiHeight))
	win.CenterOnScreen()

	uw := &unifiedWindow{
		daemon:  d,
		win:     win,
		content: container.NewStack(),
	}

	// Build initial repo list.
	uw.loadRepos()

	// Fetch all user repos in background.
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

	// Build sidebar.
	filterEntry := widget.NewEntry()
	filterEntry.PlaceHolder = "Filter..."
	filterEntry.OnChanged = func(text string) {
		uw.filter = text
		uw.applyFilter()
		uw.tree.Refresh()
	}

	uw.tree = uw.buildTree()

	sidebar := container.NewBorder(
		container.NewPadded(filterEntry), // top
		nil,                              // bottom
		nil, nil,
		uw.tree, // center
	)

	// Initial content: welcome.
	uw.showWelcome()

	split := container.NewHSplit(sidebar, uw.content)
	split.Offset = 0.35

	win.SetContent(split)
	return uw
}

func (uw *unifiedWindow) loadRepos() {
	repos := codespace.UniqueRepos(uw.daemon.Codespaces())
	repos = mergeRepos(repos, configRepos(uw.daemon.Cfg))
	hist := history.Load()
	sorted := hist.SortRepos(repos)
	uw.recentCount = countRecentRepos(sorted, hist)
	uw.allRepos = sorted
	uw.applyFilter()
}

func (uw *unifiedWindow) applyFilter() {
	if uw.filter == "" {
		uw.filtered = uw.allRepos
		return
	}
	lower := strings.ToLower(uw.filter)
	uw.filtered = nil
	for _, repo := range uw.allRepos {
		if strings.Contains(strings.ToLower(repo), lower) {
			uw.filtered = append(uw.filtered, repo)
		}
	}
}

// setContent replaces the right panel content.
func (uw *unifiedWindow) setContent(obj fyne.CanvasObject) {
	uw.content.Objects = []fyne.CanvasObject{obj}
	uw.content.Refresh()
}

// --- Tree node ID scheme ---
// "repo:<owner/name>": branch node for a repo
// "cs:<codespace-name>:<owner/name>": leaf node for a codespace
// "new:<owner/name>": leaf node for "create new"

const (
	repoPrefix = "repo:"
	csPrefix   = "cs:"
	newPrefix  = "new:"
)

func repoNodeID(repo string) widget.TreeNodeID  { return repoPrefix + repo }
func csNodeID(cs, repo string) widget.TreeNodeID { return csPrefix + cs + ":" + repo }
func newNodeID(repo string) widget.TreeNodeID    { return newPrefix + repo }
func isRepoNode(id widget.TreeNodeID) bool       { return strings.HasPrefix(id, repoPrefix) }
func isCsNode(id widget.TreeNodeID) bool         { return strings.HasPrefix(id, csPrefix) }
func isNewNode(id widget.TreeNodeID) bool        { return strings.HasPrefix(id, newPrefix) }
func repoFromNode(id widget.TreeNodeID) string   { return strings.TrimPrefix(id, repoPrefix) }

func csNameFromNode(id widget.TreeNodeID) string {
	s := strings.TrimPrefix(id, csPrefix)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i]
	}
	return s
}

func repoFromCsNode(id widget.TreeNodeID) string {
	s := strings.TrimPrefix(id, csPrefix)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return ""
}

func repoFromNewNode(id widget.TreeNodeID) string { return strings.TrimPrefix(id, newPrefix) }

func (uw *unifiedWindow) buildTree() *widget.Tree {
	t := widget.NewTree(
		// childUIDs
		func(id widget.TreeNodeID) []widget.TreeNodeID {
			if id == "" {
				ids := make([]widget.TreeNodeID, len(uw.filtered))
				for i, repo := range uw.filtered {
					ids[i] = repoNodeID(repo)
				}
				return ids
			}
			if isRepoNode(id) {
				repo := repoFromNode(id)
				all := uw.daemon.Codespaces()
				repoCS := codespace.FilterByRepo(all, repo)
				ids := make([]widget.TreeNodeID, 0, len(repoCS)+1)
				for _, cs := range repoCS {
					ids = append(ids, csNodeID(cs.Name, repo))
				}
				ids = append(ids, newNodeID(repo))
				return ids
			}
			return nil
		},
		// isBranch
		func(id widget.TreeNodeID) bool {
			return id == "" || isRepoNode(id)
		},
		// create
		func(branch bool) fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		// update
		func(id widget.TreeNodeID, branch bool, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if isRepoNode(id) {
				repo := repoFromNode(id)
				count := len(codespace.FilterByRepo(uw.daemon.Codespaces(), repo))
				if count > 0 {
					label.SetText(fmt.Sprintf("%s (%d)", repo, count))
				} else {
					label.SetText(repo)
				}
			} else if isCsNode(id) {
				csName := csNameFromNode(id)
				for _, cs := range uw.daemon.Codespaces() {
					if cs.Name == csName {
						label.SetText(fmt.Sprintf("  %s %s", stateIcon(cs.State), csLabel(cs)))
						return
					}
				}
				label.SetText("  " + csName)
			} else if isNewNode(id) {
				label.SetText("  + Create new")
			}
		},
	)

	t.OnSelected = func(id widget.TreeNodeID) {
		if isRepoNode(id) {
			repo := repoFromNode(id)
			uw.showRepoSummary(repo)
		} else if isCsNode(id) {
			csName := csNameFromNode(id)
			repo := repoFromCsNode(id)
			uw.showCodespaceDetail(csName, repo)
		} else if isNewNode(id) {
			repo := repoFromNewNode(id)
			uw.showCreateNew(repo)
		}
	}

	return t
}

// --- Content panel builders ---

func (uw *unifiedWindow) showWelcome() {
	msg := widget.NewLabel("Select a repository or codespace to get started.")
	msg.Alignment = fyne.TextAlignCenter
	uw.setContent(container.NewCenter(msg))
}

func (uw *unifiedWindow) showRepoSummary(repo string) {
	all := uw.daemon.Codespaces()
	repoCS := codespace.FilterByRepo(all, repo)

	title := widget.NewLabel(repo)
	title.TextStyle = fyne.TextStyle{Bold: true}

	info := widget.NewLabel(fmt.Sprintf("%d codespace(s)", len(repoCS)))

	createBtn := widget.NewButton("Create new codespace", func() {
		uw.showCreateNew(repo)
	})

	uw.setContent(container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(container.NewVBox(title, info, createBtn)),
		layout.NewSpacer(),
	))
}

func (uw *unifiedWindow) showCodespaceDetail(csName, repo string) {
	var cs *codespace.Codespace
	for _, c := range uw.daemon.Codespaces() {
		if c.Name == csName {
			cs = &c
			break
		}
	}
	if cs == nil {
		uw.showWelcome()
		return
	}

	title := widget.NewLabel(csLabel(*cs))
	title.TextStyle = fyne.TextStyle{Bold: true}

	state := widget.NewLabel(fmt.Sprintf("State: %s %s", stateIcon(cs.State), cs.State))

	branch := ""
	if cs.GitStatus != nil {
		ref := cs.GitStatus.Ref
		if ref == "" {
			ref = cs.GitStatus.Branch
		}
		branch = ref
	}
	branchLabel := widget.NewLabel(fmt.Sprintf("Branch: %s", branch))

	target, resolvedName := guiTargetForRepo(uw.daemon.Cfg, repo)
	matches := codespace.FindMatching([]codespace.Codespace{*cs}, &target)
	matchLabel := widget.NewLabel("")
	if len(matches) > 0 {
		matchLabel.SetText("Matches config target")
	}

	openBtn := widget.NewButton("Open", func() {
		uw.daemon.runLaunchFlow(uw.win, target, resolvedName, cs)
	})

	deleteBtn := widget.NewButton("Delete", func() {
		go func() {
			_ = codespace.DeleteCodespace(uw.daemon.Runner, cs.Name)
			fyne.Do(func() {
				uw.tree.Refresh()
				uw.showWelcome()
			})
		}()
	})

	uw.setContent(container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(container.NewVBox(
			title, state, branchLabel, matchLabel,
			widget.NewSeparator(),
			container.NewHBox(openBtn, deleteBtn),
		)),
		layout.NewSpacer(),
	))
}

func (uw *unifiedWindow) showCreateNew(repo string) {
	target, resolvedName := guiTargetForRepo(uw.daemon.Cfg, repo)

	title := widget.NewLabel("Create a new codespace")
	title.TextStyle = fyne.TextStyle{Bold: true}

	subtitle := widget.NewLabel(repo)

	entry := widget.NewEntry()
	entry.PlaceHolder = "e.g., fix indexer health checks"

	hint := widget.NewLabel("")

	createBtn := widget.NewButton("Create", func() {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			hint.SetText("Enter a short label so the codespace is easier to recognize.")
			return
		}
		createTarget := target
		createTarget.DisplayName = text
		uw.daemon.runCreateAndLaunch(uw.win, createTarget, resolvedName)
	})

	cancelBtn := widget.NewButton("Cancel", func() {
		uw.showWelcome()
	})

	uw.setContent(container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(container.NewVBox(
			title, subtitle,
			widget.NewLabel("What work are you planning to do?"),
			entry, hint,
			container.NewHBox(cancelBtn, layout.NewSpacer(), createBtn),
		)),
		layout.NewSpacer(),
	))
}

// --- Helper functions ---

func mergeRepos(base, extra []string) []string {
	seen := make(map[string]bool, len(base))
	for _, r := range base {
		seen[r] = true
	}
	result := make([]string, len(base))
	copy(result, base)
	for _, r := range extra {
		if !seen[r] {
			seen[r] = true
			result = append(result, r)
		}
	}
	return result
}

func configRepos(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	seen := make(map[string]bool)
	var repos []string
	for _, t := range cfg.Targets {
		if t.Repository != "" && !seen[t.Repository] {
			seen[t.Repository] = true
			repos = append(repos, t.Repository)
		}
	}
	return repos
}

func guiTargetForRepo(cfg *config.Config, repo string) (config.Target, string) {
	if cfg != nil {
		for name, t := range cfg.Targets {
			if t.Repository == repo {
				return t, name
			}
		}
	}
	parts := strings.SplitN(repo, "/", 2)
	repoName := parts[len(parts)-1]
	return config.Target{
		Repository:    repo,
		WorkspacePath: "/workspaces/" + repoName,
	}, repo
}

func countRecentRepos(sorted []string, hist *history.History) int {
	n := 0
	for _, repo := range sorted {
		found := false
		for _, e := range hist.Entries {
			if e.Repository == repo {
				found = true
				break
			}
		}
		if !found {
			break
		}
		n++
	}
	return n
}

// createGUIWindow creates a standard GUI window (used by flow for progress).
func (d *Daemon) createGUIWindow(title string) fyne.Window {
	win := d.app.NewWindow(title)
	win.Resize(fyne.NewSize(guiWidth, guiHeight))
	win.CenterOnScreen()
	return win
}
