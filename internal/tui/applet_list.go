package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// listRow is one selectable row in the list view. Either a repo header (when
// kind=rowRepo), a workspace under a repo (kind=rowWorkspace), or a Coder
// workspace at top level (kind=rowCoder). repoHeaders aren't selectable —
// they're rendered but skipped by the cursor.
type listRow struct {
	kind      listRowKind
	repo      string
	workspace *provider.Workspace
}

type listRowKind int

const (
	rowRepoHeader listRowKind = iota
	rowWorkspace
	rowCoderWorkspace
	rowEmptyHint
)

// listModel renders the repo/workspace tree with filter-as-you-type and
// cursor navigation. Mirrors the GUI's sidebar tree but as a flat scrolling
// list since terminal real-estate is single-pane.
type listModel struct {
	rows         []listRow
	visible      []int // indices into rows after filter applied
	cursor       int   // index within visible
	filter       string
	escPending   bool
	escSeq       int
	confirmDelete *provider.Workspace
}

func newListModel(d *AppletData) listModel {
	m := listModel{}
	m.rebuild(d)
	return m
}

func (m listModel) Init() tea.Cmd { return nil }

func (m listModel) update(msg tea.Msg, d *AppletData) (listModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case reloadMsg:
		m.rebuild(d)
		return m, nil
	case escTimeoutMsg:
		if msg.seq == m.escSeq {
			m.escPending = false
		}
		return m, nil
	case tea.KeyMsg:
		// Confirm-delete inline prompt grabs all keys until resolved.
		if m.confirmDelete != nil {
			return m.handleConfirmDelete(msg, d)
		}
		return m.handleKey(msg, d)
	}
	return m, nil
}

func (m listModel) handleKey(msg tea.KeyMsg, d *AppletData) (listModel, tea.Cmd) {
	key := msg.String()
	switch key {
	case "q":
		if m.filter == "" {
			return m, tea.Quit
		}
		m.filter += key
		m.applyFilter()
	case "esc":
		if m.escPending {
			return m, tea.Quit
		}
		if m.filter != "" {
			m.filter = ""
			m.applyFilter()
			return m, nil
		}
		m.escPending = true
		m.escSeq++
		seq := m.escSeq
		return m, tea.Tick(escTimeout, func(time.Time) tea.Msg { return escTimeoutMsg{seq: seq} })
	case "up", "k":
		m.cursorUp()
	case "down", "j":
		m.cursorDown()
	case "home", "g":
		m.cursor = m.firstSelectableIndex()
	case "end", "G":
		m.cursor = m.lastSelectableIndex()
	case "enter":
		if ws := m.workspaceAtCursor(); ws != nil {
			return m, switchTo(viewDetail, ws)
		}
	case "s":
		if m.filter == "" {
			return m, switchTo(viewSettings, nil)
		}
		m.filter += key
		m.applyFilter()
	case "n":
		if m.filter == "" {
			return m, switchTo(viewCreate, nil)
		}
		m.filter += key
		m.applyFilter()
	case "r":
		if m.filter == "" {
			return m, func() tea.Msg { return pollDoneMsg{result: d.Poll()} }
		}
		m.filter += key
		m.applyFilter()
	case "d", "x":
		if m.filter == "" {
			if ws := m.workspaceAtCursor(); ws != nil {
				wsCopy := *ws
				m.confirmDelete = &wsCopy
			}
		} else {
			m.filter += key
			m.applyFilter()
		}
	case "backspace", "ctrl+h":
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
			m.applyFilter()
		}
	default:
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.filter += key
			m.applyFilter()
		}
	}
	return m, nil
}

func (m listModel) handleConfirmDelete(msg tea.KeyMsg, d *AppletData) (listModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		ws := *m.confirmDelete
		m.confirmDelete = nil
		return m, func() tea.Msg {
			if err := d.DeleteWorkspace(ws.Provider, ws.Name); err != nil {
				return flashMsg{text: fmt.Sprintf("delete %s: %v", ws.Name, err), err: true}
			}
			go func() { d.Poll() }()
			return flashMsg{text: fmt.Sprintf("Deleted %s", ws.Name)}
		}
	case "n", "N", "esc", "q", "ctrl+c":
		m.confirmDelete = nil
	}
	return m, nil
}

func (m *listModel) cursorUp() {
	if len(m.visible) == 0 {
		return
	}
	for i := 1; i < len(m.visible)+1; i++ {
		next := (m.cursor - i + len(m.visible)) % len(m.visible)
		if m.isSelectable(m.rows[m.visible[next]]) {
			m.cursor = next
			return
		}
	}
}

func (m *listModel) cursorDown() {
	if len(m.visible) == 0 {
		return
	}
	for i := 1; i < len(m.visible)+1; i++ {
		next := (m.cursor + i) % len(m.visible)
		if m.isSelectable(m.rows[m.visible[next]]) {
			m.cursor = next
			return
		}
	}
}

func (m listModel) isSelectable(r listRow) bool {
	return r.kind == rowWorkspace || r.kind == rowCoderWorkspace
}

func (m listModel) firstSelectableIndex() int {
	for i, idx := range m.visible {
		if m.isSelectable(m.rows[idx]) {
			return i
		}
	}
	return 0
}

func (m listModel) lastSelectableIndex() int {
	last := 0
	for i, idx := range m.visible {
		if m.isSelectable(m.rows[idx]) {
			last = i
		}
	}
	return last
}

func (m listModel) workspaceAtCursor() *provider.Workspace {
	if len(m.visible) == 0 || m.cursor >= len(m.visible) {
		return nil
	}
	r := m.rows[m.visible[m.cursor]]
	return r.workspace
}

// rebuild constructs the row list from current data. Groups GitHub
// codespaces by repository (matching the GUI tree), then puts Coder
// workspaces as their own top-level rows.
func (m *listModel) rebuild(d *AppletData) {
	all := d.Workspaces()

	// Bucket GitHub workspaces by repo so we can render header + children.
	repoBuckets := map[string][]provider.Workspace{}
	var coderWS []provider.Workspace
	for _, w := range all {
		if w.Provider == provider.NameCoder {
			coderWS = append(coderWS, w)
			continue
		}
		repoBuckets[w.Repository] = append(repoBuckets[w.Repository], w)
	}
	repos := make([]string, 0, len(repoBuckets))
	for r := range repoBuckets {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	rows := []listRow{}
	for _, repo := range repos {
		rows = append(rows, listRow{kind: rowRepoHeader, repo: repo})
		bucket := repoBuckets[repo]
		sort.SliceStable(bucket, func(i, j int) bool { return bucket[i].Name < bucket[j].Name })
		for i := range bucket {
			ws := bucket[i]
			rows = append(rows, listRow{kind: rowWorkspace, repo: repo, workspace: &ws})
		}
	}
	if len(coderWS) > 0 {
		rows = append(rows, listRow{kind: rowRepoHeader, repo: "Coder workspaces"})
		sort.SliceStable(coderWS, func(i, j int) bool { return coderWS[i].Name < coderWS[j].Name })
		for i := range coderWS {
			ws := coderWS[i]
			rows = append(rows, listRow{kind: rowCoderWorkspace, workspace: &ws})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, listRow{kind: rowEmptyHint, repo: "No workspaces found."})
	}
	m.rows = rows
	m.applyFilter()
}

func (m *listModel) applyFilter() {
	m.visible = m.visible[:0]
	lower := strings.ToLower(m.filter)
	for i, r := range m.rows {
		if m.filter != "" {
			label := r.repo
			if r.workspace != nil {
				label = r.workspace.Name
				if r.workspace.DisplayName != "" {
					label += " " + r.workspace.DisplayName
				}
			}
			if !strings.Contains(strings.ToLower(label), lower) {
				continue
			}
		}
		m.visible = append(m.visible, i)
	}
	if m.cursor >= len(m.visible) {
		m.cursor = 0
	}
	// Move off any unselectable row.
	if len(m.visible) > 0 && !m.isSelectable(m.rows[m.visible[m.cursor]]) {
		m.cursorDown()
	}
}

func (m listModel) view(d *AppletData, width, height int) string {
	var b strings.Builder

	if m.filter != "" {
		fmt.Fprintf(&b, "Filter: %s\n", selectedStyle.Render(m.filter))
	} else {
		b.WriteString(captionStyle.Render("WORKSPACES") + "\n")
	}

	if len(m.visible) == 0 {
		b.WriteString(dimStyle.Render("  no matches"))
		b.WriteString("\n")
	}

	for i, idx := range m.visible {
		r := m.rows[idx]
		switch r.kind {
		case rowRepoHeader:
			b.WriteString("\n")
			b.WriteString(captionStyle.Render(r.repo))
			b.WriteString("\n")
		case rowEmptyHint:
			b.WriteString(dimStyle.Render("  " + r.repo))
			b.WriteString("\n")
		case rowWorkspace, rowCoderWorkspace:
			cursor := "  "
			if i == m.cursor {
				cursor = cursorStyle.Render("> ")
			}
			ws := r.workspace
			icon := stateColor(ws.State).Render(stateIconChar(ws.State))
			label := ws.DisplayName
			if label == "" {
				label = ws.Name
			}
			meta := dimStyle.Render(strings.ToLower(ws.State))
			if ws.Branch != "" {
				meta += dimStyle.Render(fmt.Sprintf("  ⎇ %s", ws.Branch))
			}
			line := fmt.Sprintf("%s%s  %s   %s", cursor, icon, label, meta)
			if i == m.cursor {
				b.WriteString(selectedStyle.Render(line))
			} else {
				b.WriteString(line)
			}
			b.WriteString("\n")
		}
	}

	if listErr := d.ListErr(); listErr != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("provider error: %v", listErr)))
		b.WriteString("\n")
	}

	if m.confirmDelete != nil {
		b.WriteString("\n")
		prompt := fmt.Sprintf("Delete %s? (y/N) ", m.confirmDelete.Name)
		b.WriteString(errorStyle.Render(prompt))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	hint := "enter open  n new  d delete  r refresh  s settings  type to filter  q quit"
	b.WriteString(dimStyle.Render(hint))

	return clampHeight(b.String(), height, width)
}

// clampHeight truncates s to fit into height lines, padding shorter strings
// up so the footer doesn't jitter across renders.
func clampHeight(s string, height, _ int) string {
	if height <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

