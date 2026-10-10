package workspace

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"aw/internal/git"
	"aw/internal/theme"
)

// gitView is the fullscreen git mode: changed files on the left, the
// selected file's diff on the right. The panes keep running behind it.
type gitView struct {
	branch   string
	entries  []git.Entry
	err      string // why the view is unavailable, if it is
	selected int
	diff     string
	lines    []string // diff split for scrolling
	scroll   int
}

// reload re-reads repository state and refreshes the selected diff.
func (g *gitView) reload(dir string) {
	st, err := git.ReadStatus(dir)
	if err != nil {
		g.err = strings.TrimSuffix(err.Error(), "\n")
		g.branch, g.entries = "", nil
		g.selected, g.scroll = 0, 0
		g.diff, g.lines = "", nil
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

// loadDiff fetches the selected entry's diff and resets scrolling.
func (g *gitView) loadDiff(dir string) {
	g.scroll = 0
	if len(g.entries) == 0 {
		g.diff, g.lines = "", nil
		return
	}
	g.diff = git.Diff(dir, g.entries[g.selected])
	g.lines = strings.Split(g.diff, "\n")
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
	return true
}

// scrollBy shifts the diff view, clamped to the diff's height.
func (g *gitView) scrollBy(delta, viewHeight int) {
	if viewHeight < 1 || len(g.lines) <= viewHeight {
		g.scroll = 0
		return
	}
	g.scroll = clamp(g.scroll+delta, 0, len(g.lines)-viewHeight)
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

// renderGit composes the fullscreen git view: file list left, diff right.
func (a App) renderGit() string {
	t := a.theme
	contentW, contentH := a.width, a.height-statusRows
	listW := clamp(contentW/3, 24, contentW-24)

	// Left: the file list, bordered, with the branch as its header.
	listBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent)).
		Render(a.gitListContent(listW-2, contentH-2))
	diffBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent)).
		Render(a.gitDiffContent(listW+1, 0, contentW-listW-1, contentH-2))

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
	return strings.Join(lines[:h], "\n")
}

// gitDiffContent renders the diff box's inner content.
func (a App) gitDiffContent(x, _, w, h int) string {
	t := a.theme
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Dim))

	title := " diff "
	if a.git.err == "" && len(a.git.entries) > 0 {
		title = " " + a.git.entries[a.git.selected].Path + " "
		if ansi.StringWidth(title) > w {
			title = ansi.Truncate(title, w-1, "…")
		}
	}
	lines := []string{dim.Render(title)}

	if a.git.err == "" && a.git.diff == "" && len(a.git.entries) > 0 {
		lines = append(lines, dimStyle(t).Render(" no textual changes "))
	} else {
		end := min(a.git.scroll+max(0, h-1), len(a.git.lines))
		for _, line := range a.git.lines[a.git.scroll:end] {
			if ansi.StringWidth(line) > w {
				line = ansi.Truncate(line, w, "")
			}
			lines = append(lines, diffStyle(t, line).Render(line))
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// diffStyle colors one line of unified diff output.
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
	return lipgloss.NewStyle().Foreground(fg)
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
