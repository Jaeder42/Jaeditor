package workspace

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/git"
	"jaed/internal/theme"
)

// gitView is the fullscreen git mode: changed files on the left, the
// selected file's diff on the right. The diff is split into navigable hunks
// that can be staged, unstaged, or reverted inline, like an editor's inline
// diff. The panes keep running behind it.
type gitView struct {
	branch   string
	entries  []git.Entry
	err      string // why the view is unavailable, if it is
	selected int
	diff     string
	lines    []string // diff split for scrolling
	scroll   int

	preamble int            // offset where the diff's first hunk begins
	hunks    []git.HunkSpan // the diff's hunks, for per-hunk actions
	hunk     int            // selected hunk, -1 when the file has none
	display  []diffRow      // the diff flattened into display rows

	notice    string // transient result of the last action
	noticeErr bool
}

// reload re-reads repository state and refreshes the selected diff.
func (g *gitView) reload(dir string) {
	g.notice, g.noticeErr = "", false
	st, err := git.ReadStatus(dir)
	if err != nil {
		g.err = strings.TrimSuffix(err.Error(), "\n")
		g.branch, g.entries = "", nil
		g.selected, g.scroll = 0, 0
		g.diff, g.lines = "", nil
		g.preamble, g.hunks, g.display, g.hunk = 0, nil, nil, -1
		return
	}
	g.err = ""
	g.branch = st.Branch
	g.entries = st.Entries
	if g.selected >= len(g.entries) {
		g.selected, g.scroll = 0, 0
	}
	g.loadDiff(dir)
}

// loadDiff fetches the selected entry's diff, splits it into hunks, and
// resets scrolling onto the first hunk.
func (g *gitView) loadDiff(dir string) {
	g.scroll = 0
	g.hunk = -1
	g.hunks = nil
	g.preamble = 0
	g.display = nil
	if len(g.entries) == 0 {
		g.diff, g.lines = "", nil
		return
	}
	g.diff = git.Diff(dir, g.entries[g.selected])
	g.preamble, g.hunks = git.SplitHunks(g.diff)
	if len(g.hunks) > 0 {
		g.hunk = 0
	}
	g.lines = strings.Split(g.diff, "\n")
	// A diff ends in a newline, leaving an empty final element that would
	// otherwise draw a lone gutter bar.
	if n := len(g.lines); n > 0 && g.lines[n-1] == "" {
		g.lines = g.lines[:n-1]
	}
	g.display = buildDiffRows(g.lines)
}

// selectPath moves the selection to path, preferring the given section but
// falling back to any section so an action that moves a file between staged
// and unstaged keeps the user on it.
func (g *gitView) selectPath(path string, section git.Section) {
	next := -1
	for i, e := range g.entries {
		if e.Path == path && e.Section == section {
			next = i
			break
		}
	}
	if next < 0 {
		for i, e := range g.entries {
			if e.Path == path {
				next = i
				break
			}
		}
	}
	if next >= 0 {
		g.selected = next
	} else if g.selected >= len(g.entries) {
		g.selected = 0
	}
	g.scroll, g.hunk = 0, -1
}

// move shifts the file selection, reporting whether it changed.
func (g *gitView) move(delta int) bool {
	if len(g.entries) == 0 {
		return false
	}
	next := g.selected + delta
	if next < 0 || next >= len(g.entries) {
		return false
	}
	g.selected = next
	g.notice = ""
	return true
}

// moveHunk shifts the selected hunk, reporting whether it changed.
func (g *gitView) moveHunk(delta int) bool {
	if len(g.hunks) == 0 {
		return false
	}
	next := g.hunk + delta
	if next < 0 || next >= len(g.hunks) {
		return false
	}
	g.hunk = next
	g.notice = ""
	return true
}

// hunkRow is the display row that opens hunk k, for scrolling it into view.
func (g *gitView) hunkRow(k int) int {
	for i, row := range g.display {
		if row.header && row.hunk == k {
			return i
		}
	}
	return 0
}

// scrollBy shifts the diff view, clamped to the diff's height, and makes the
// hunk now at the top of the viewport the active one — so the actions follow
// the scroll.
func (g *gitView) scrollBy(delta, viewHeight int) {
	if viewHeight < 1 {
		viewHeight = 1
	}
	g.scroll = clamp(g.scroll+delta, 0, max(0, len(g.display)-viewHeight))
	g.syncHunk()
	g.notice = ""
}

// syncHunk selects the hunk occupying the top of the viewport: the last hunk
// whose header has scrolled to or above the top, or the first hunk while the
// preamble is still in view.
func (g *gitView) syncHunk() {
	if len(g.hunks) == 0 {
		g.hunk = -1
		return
	}
	k := 0
	for i, row := range g.display {
		if row.header && i <= g.scroll {
			k = row.hunk
		}
	}
	g.hunk = k
}

// panelBody is the number of content rows visible in an overlay's right-hand
// view: the body height minus the two box borders and the title line.
func (a App) panelBody() int {
	return max(1, a.bodyHeight()-3)
}

// setNotice records the outcome of an action for the footer line.
func (g *gitView) setNotice(msg string, isErr bool) {
	g.notice, g.noticeErr = " "+msg+" ", isErr
}

// gitRow is one rendered row of the file list.
type gitRow struct {
	kind    git.Section // header rows use their section kind
	header  bool
	entry   git.Entry
	listIdx int // index into entries, -1 for headers
}

// rows flattens entries into sectioned rows: a header per section, then
// its files.
func (g gitView) rows() []gitRow {
	var rows []gitRow
	for _, section := range []git.Section{git.Staged, git.Unstaged, git.Untracked} {
		found := false
		for i, e := range g.entries {
			if e.Section != section {
				continue
			}
			if !found {
				rows = append(rows, gitRow{kind: section, header: true, listIdx: -1})
				found = true
			}
			rows = append(rows, gitRow{kind: section, entry: e, listIdx: i})
		}
	}
	return rows
}

// diffRow is one line of the diff view, tagged with the hunk it belongs to
// (-1 for the file preamble) and whether it opens a hunk.
type diffRow struct {
	text   string
	hunk   int
	header bool
}

// buildDiffRows tags each diff line with its hunk. A hunk's own lines are
// prefixed with ' ', '+', or '-', so "@@" at the start of a line is
// unambiguously a hunk header.
func buildDiffRows(lines []string) []diffRow {
	rows := make([]diffRow, 0, len(lines))
	hunk := -1
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			hunk++
			rows = append(rows, diffRow{text: line, hunk: hunk, header: true})
			continue
		}
		rows = append(rows, diffRow{text: line, hunk: hunk})
	}
	return rows
}

// gitListWidth is the file list's column width in the git view, shared with
// the selection's pane geometry so they agree.
func (a App) gitListWidth() int {
	return clamp(a.width/3, 24, a.width-24)
}

// renderGit composes the fullscreen git view: file list left, diff right.
func (a App) renderGit() string {
	t := a.theme
	contentW, contentH := a.width, a.bodyHeight()
	listW := a.gitListWidth()

	// Left: the file list, bordered, with the branch as its header.
	listBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent)).
		Render(a.gitListContent(listW-2, contentH-2))
	diffBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent)).
		Render(a.gitDiffContent(listW+1, 0, contentW-listW-2, contentH-2))

	rows := make([]string, contentH)
	listLines := strings.Split(listBox, "\n")
	diffLines := strings.Split(diffBox, "\n")
	for y := range rows {
		left, right := "", ""
		if y < len(listLines) {
			left = listLines[y]
		}
		if y < len(diffLines) {
			right = diffLines[y]
		}
		rows[y] = padRow(left, listW) + right
	}
	return strings.Join(rows, "\n")
}

// gitListContent renders the list box's inner content.
func (a App) gitListContent(w, h int) string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	base := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))
	header := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Accent))
	selected := lipgloss.NewStyle().Background(lipgloss.Color(t.Accent)).Foreground(lipgloss.Color(t.Bg))

	lines := make([]string, 0, h)
	if a.git.err != "" {
		lines = append(lines, header.Render(" git "), "", dim.Render(" "+a.git.err))
	} else {
		lines = append(lines, header.Render(" git · "+a.git.branch+" "))
	}

	rows := a.git.rows()
	if len(rows) == 0 {
		lines = append(lines, "", base.Render(" clean — nothing to commit ")+dimStyle(t).Render(" ✓"))
	}

	// Keep the selected row on screen.
	visible := h - len(lines) - 1
	top := 0
	for i, row := range rows {
		if row.listIdx == a.git.selected && i >= visible && i > 0 {
			top = i - visible + 1
		}
	}
	for _, row := range rows[top:] {
		if len(lines) >= h {
			break
		}
		if row.header {
			lines = append(lines, dim.Render("── "+row.kind.String()+" "))
			continue
		}
		label := " " + row.entry.Code + "  " + row.entry.Path
		if ansi.StringWidth(label) > w {
			label = ansi.Truncate(label, w-1, "…")
		}
		if row.listIdx == a.git.selected {
			lines = append(lines, selected.Width(w).Render(label))
		} else {
			lines = append(lines, base.Render(label))
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, line := range lines[:h] {
		lines[i] = padToWidth(line, w, base)
	}
	return strings.Join(lines[:h], "\n")
}

// gitDiffContent renders the diff box's inner content as an inline diff: the
// file preamble, then each hunk, with the selected hunk bar carrying its
// stage/unstage/revert actions.
func (a App) gitDiffContent(_, _, w, h int) string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))
	accent := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Accent))
	selected := lipgloss.NewStyle().Background(lipgloss.Color(t.Accent)).Foreground(lipgloss.Color(t.Bg))
	errStyle := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Error))

	title := " diff "
	hasEntry := a.git.err == "" && len(a.git.entries) > 0
	if hasEntry {
		title = " " + a.git.entries[a.git.selected].Path
		if n := len(a.git.hunks); n > 0 {
			title += fmt.Sprintf("  ·  hunk %d/%d", a.git.hunk+1, n)
		}
		title += " "
		if ansi.StringWidth(title) > w {
			title = ansi.Truncate(title, w-1, "…")
		}
	}
	lines := []string{accent.Render(title)}

	// Reserve the last row for a notice, or the whole box for the body.
	body := h - 1
	if a.git.notice != "" {
		body = max(0, h-2)
	}
	if hasEntry && len(a.git.display) == 0 {
		lines = append(lines, dim.Render(" no textual changes "))
	} else {
		top := clamp(a.git.scroll, 0, max(0, len(a.git.display)-1))
		end := min(top+body, len(a.git.display))
		for _, row := range a.git.display[top:end] {
			lines = append(lines, a.renderDiffRow(row, w, dim, accent, selected))
		}
	}
	if a.git.notice != "" {
		style := dim
		if a.git.noticeErr {
			style = errStyle
		}
		lines = lines[:min(len(lines), h-1)]
		lines = append(lines, style.Render(a.git.notice))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	// Fill every row to the pane's width so the box never shrinks to its
	// content — the diff is the wide view and should span the screen.
	fill := lipgloss.NewStyle().Background(surface)
	for i, line := range lines[:h] {
		lines[i] = padToWidth(line, w, fill)
	}
	return strings.Join(lines[:h], "\n")
}

// renderDiffRow draws one diff line, marking the selected hunk and, on its
// header, the actions available for it.
func (a App) renderDiffRow(row diffRow, w int, dim, accent, selected lipgloss.Style) string {
	sel := row.hunk >= 0 && row.hunk == a.git.hunk

	if row.header {
		label := row.text
		if sel {
			if hints := hunkActions(a.git.entries[a.git.selected]); hints != "" {
				if gap := w - ansi.StringWidth(label) - ansi.StringWidth(hints); gap >= 1 {
					label += strings.Repeat(" ", gap) + hints
				} else {
					label = ansi.Truncate(label, max(0, w-1), "…")
				}
			}
		}
		if ansi.StringWidth(label) > w {
			label = ansi.Truncate(label, w, "")
		}
		if sel {
			return selected.Width(w).Render(label)
		}
		return accent.Render(label)
	}

	gutter := " "
	if sel {
		gutter = accent.Render("▌")
	}
	line := gutter + diffStyle(a.theme, row.text).Render(row.text)
	if ansi.StringWidth(line) > w {
		line = ansi.Truncate(line, w, "")
	}
	return line
}

// hunkActions names the per-hunk actions available for an entry, so the
// selected hunk's bar can show them.
func hunkActions(e git.Entry) string {
	switch e.Section {
	case git.Staged:
		return "u unstage "
	case git.Untracked:
		return "s stage "
	default:
		return "s stage · x revert "
	}
}

// gitEntry is the currently selected change, if any.
func (a *App) gitEntry() (git.Entry, bool) {
	if a.git.selected < 0 || a.git.selected >= len(a.git.entries) {
		return git.Entry{}, false
	}
	return a.git.entries[a.git.selected], true
}

// gitRefresh reloads after an action, keeping the selection on keep and the
// hunk index where a hunk was removed (so staging hunk k leaves hunk k
// selected).
func (a *App) gitRefresh(keep git.Entry, hunk int) {
	a.git.reload(a.cwd)
	a.git.selectPath(keep.Path, keep.Section)
	a.git.loadDiff(a.cwd)
	if len(a.git.hunks) > 0 {
		a.git.hunk = clamp(hunk, 0, len(a.git.hunks)-1)
		a.git.scroll = a.git.hunkRow(a.git.hunk)
	}
}

// gitMoveHunk shifts the selected hunk and scrolls its header into view.
func (a *App) gitMoveHunk(delta int) {
	if !a.git.moveHunk(delta) {
		return
	}
	a.git.scroll = a.git.hunkRow(a.git.hunk)
}

// gitStageHunk stages the selected hunk into the index (and an untracked
// file's single whole-file hunk by staging the file).
func (a *App) gitStageHunk() {
	e, ok := a.gitEntry()
	if !ok {
		return
	}
	if e.Section == git.Untracked {
		a.gitStageFile()
		return
	}
	if e.Section != git.Unstaged || a.git.hunk < 0 || a.git.hunk >= len(a.git.hunks) {
		return
	}
	k := a.git.hunk
	patch := a.git.hunks[k].Patch(a.git.diff, a.git.preamble)
	if err := git.ApplyPatch(a.cwd, patch, true, false); err != nil {
		a.git.setNotice(err.Error(), true)
		return
	}
	a.gitRefresh(e, k)
	a.git.setNotice(fmt.Sprintf("staged hunk %d", k+1), false)
}

// gitUnstageHunk takes the selected hunk back out of the index.
func (a *App) gitUnstageHunk() {
	e, ok := a.gitEntry()
	if !ok || e.Section != git.Staged || a.git.hunk < 0 || a.git.hunk >= len(a.git.hunks) {
		return
	}
	k := a.git.hunk
	patch := a.git.hunks[k].Patch(a.git.diff, a.git.preamble)
	if err := git.ApplyPatch(a.cwd, patch, true, true); err != nil {
		a.git.setNotice(err.Error(), true)
		return
	}
	a.gitRefresh(e, k)
	a.git.setNotice(fmt.Sprintf("unstaged hunk %d", k+1), false)
}

// gitRevertHunk discards the selected hunk from the worktree.
func (a *App) gitRevertHunk() {
	e, ok := a.gitEntry()
	if !ok || e.Section != git.Unstaged || a.git.hunk < 0 || a.git.hunk >= len(a.git.hunks) {
		return
	}
	k := a.git.hunk
	patch := a.git.hunks[k].Patch(a.git.diff, a.git.preamble)
	if err := git.ApplyPatch(a.cwd, patch, false, true); err != nil {
		a.git.setNotice(err.Error(), true)
		return
	}
	a.gitRefresh(e, k)
	a.git.setNotice(fmt.Sprintf("reverted hunk %d", k+1), false)
}

// gitStageFile stages the selected file.
func (a *App) gitStageFile() {
	e, ok := a.gitEntry()
	if !ok || e.Section == git.Staged {
		return
	}
	if err := git.StageFile(a.cwd, e.Path); err != nil {
		a.git.setNotice(err.Error(), true)
		return
	}
	a.gitRefresh(e, 0)
	a.git.setNotice("staged "+e.Path, false)
}

// gitUnstageFile takes the selected file back out of the index.
func (a *App) gitUnstageFile() {
	e, ok := a.gitEntry()
	if !ok || e.Section != git.Staged {
		return
	}
	if err := git.UnstageFile(a.cwd, e.Path); err != nil {
		a.git.setNotice(err.Error(), true)
		return
	}
	a.gitRefresh(e, 0)
	a.git.setNotice("unstaged "+e.Path, false)
}

// gitRevertFile discards the selected file's worktree changes.
func (a *App) gitRevertFile() {
	e, ok := a.gitEntry()
	if !ok || e.Section != git.Unstaged {
		return
	}
	if err := git.RevertFile(a.cwd, e.Path); err != nil {
		a.git.setNotice(err.Error(), true)
		return
	}
	a.gitRefresh(e, 0)
	a.git.setNotice("reverted "+e.Path, false)
}

// gitHint is the status bar's key hint for the git view, narrowed to the
// actions the selected change actually offers.
func (a App) gitHint() string {
	base := "j/k file · r reload · g/esc close · Q quit"
	e, ok := a.gitEntry()
	if !ok {
		return base
	}
	switch e.Section {
	case git.Staged:
		return "j/k file · [/] hunk · ctrl+d/u scroll · u unstage · U file · r · g close"
	case git.Untracked:
		return "j/k file · s stage · S stage file · ctrl+d/u scroll · r · g close"
	default:
		return "j/k file · [/] hunk · ctrl+d/u scroll · s stage · x revert · S/X file · r · g close"
	}
}

// diffStyle colors one line of unified diff output, on the pane's surface.
func diffStyle(t theme.Theme, line string) lipgloss.Style {
	fg := lipgloss.Color(t.Fg)
	switch {
	case strings.HasPrefix(line, "+"):
		fg = lipgloss.Color(t.Success)
	case strings.HasPrefix(line, "-"):
		fg = lipgloss.Color(t.Error)
	case strings.HasPrefix(line, "@@"):
		fg = lipgloss.Color(t.Accent)
	case strings.HasPrefix(line, "diff ") ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "--- ") ||
		strings.HasPrefix(line, "+++ "):
		fg = lipgloss.Color(t.Dim)
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(t.Surface)).Foreground(fg)
}

func dimStyle(t theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.Dim))
}

func padRow(row string, width int) string {
	if pad := width - ansi.StringWidth(row); pad > 0 {
		return row + strings.Repeat(" ", pad)
	}
	return ansi.Truncate(row, width, "")
}

// padToWidth right-pads a rendered line with fill so a box's interior is
// exactly w wide, truncating anything longer. Filling with the pane's
// background keeps its box from shrinking to its content.
func padToWidth(line string, w int, fill lipgloss.Style) string {
	switch pad := w - ansi.StringWidth(line); {
	case pad > 0:
		return line + fill.Render(strings.Repeat(" ", pad))
	case pad < 0:
		return ansi.Truncate(line, w, "")
	default:
		return line
	}
}
