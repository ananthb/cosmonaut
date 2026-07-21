// Create-new flows: GitHub codespace form (repo/branch/label) and the
// Coder workspace forms (target-based and template-based).
package daemon

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

// ── CREATE ──────────────────────────────────────────────────────────────

func (uw *unifiedWindow) showCreateNewGeneric() {
	uw.currentView = uw.showCreateNewGeneric
	uw.currentViewID = "create-generic"
	title := canvas.NewText("Create a new workspace", theme.Color(theme.ColorNameForeground))
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
	uw.currentView = func() { uw.showCosmoCreateNew(repo) }
	uw.currentViewID = "create:" + repo
	target, resolvedName := guiTargetForRepo(uw.daemon.Cfg, repo)

	title := canvas.NewText("Create a new codespace", theme.Color(theme.ColorNameForeground))
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	hint := canvas.NewText("A short label makes it easier to find later.", theme.Color(theme.ColorNamePlaceHolder))
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

func (uw *unifiedWindow) showCosmoCreateNewCoder() {
	uw.currentView = uw.showCosmoCreateNewCoder
	uw.currentViewID = "create-coder"
	title := canvas.NewText("Create a new Coder workspace", theme.Color(theme.ColorNameForeground))
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}

	targetNames := configuredCoderTargets(uw.daemon.Cfg)
	if len(targetNames) == 0 {
		uw.showCosmoCreateNewCoderFromTemplates(title)
		return
	}

	targetSel := widget.NewSelect(targetNames, func(string) {})
	targetSel.SetSelected(targetNames[0])
	baseTarget, _ := uw.daemon.Cfg.Target(targetNames[0])

	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "e.g. my-repo-review"
	if baseTarget.Coder != nil && baseTarget.Coder.WorkspaceName != "" {
		nameEntry.SetText(baseTarget.Coder.WorkspaceName)
	}

	pathEntry := widget.NewEntry()
	pathEntry.PlaceHolder = "/workspaces/my-repo"
	pathEntry.SetText(provider.GuessWorkspacePath(baseTarget, nil))

	targetSel.OnChanged = func(name string) {
		t, _ := uw.daemon.Cfg.Target(name)
		if t.Coder != nil && t.Coder.WorkspaceName != "" {
			nameEntry.SetText(t.Coder.WorkspaceName)
		}
		pathEntry.SetText(provider.GuessWorkspacePath(t, nil))
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
		// Target() returns a deep copy, so the WorkspacePath/WorkspaceName
		// staging below never mutates (or persists into) the live config.
		target, _ := uw.daemon.Cfg.Target(targetName)
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
	// ListTemplates execs the coder CLI; this runs from tree OnSelected on
	// the Fyne main thread, so show a loading screen and fetch async.
	loading := widget.NewLabel("Loading Coder templates…")
	loading.Wrapping = fyne.TextWrapWord
	uw.setContent(container.NewPadded(container.NewVBox(title, widget.NewSeparator(), loading)))

	manager := provider.NewCoderManager(uw.daemon.Cfg)
	go func() {
		templates, err := manager.ListTemplates()
		fyne.Do(func() { uw.renderCosmoCoderTemplateForm(title, templates, err) })
	}()
}

func (uw *unifiedWindow) renderCosmoCoderTemplateForm(title *canvas.Text, templates []provider.CoderTemplate, err error) {
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
