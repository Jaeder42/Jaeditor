package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/pane"
	"jaed/internal/settings"
	"jaed/internal/theme"
)

func TestSelectionText(t *testing.T) {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	lines := []string{
		accent.Render("abcdef"),
		lipgloss.NewStyle().Render("ghijkl"),
	}
	wide := selectRegion{x0: 0, y0: 0, x1: 99, y1: 99}
	// From column 2 of row 0 to column 3 of row 1 (inclusive).
	sel := selection{active: true, region: wide, anchor: cell{x: 2, y: 0}, cursor: cell{x: 3, y: 1}}
	if got, want := sel.text(lines), "cdef\nghij"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}

	// Dragging backwards selects the same range.
	sel = selection{active: true, region: wide, anchor: cell{x: 3, y: 1}, cursor: cell{x: 2, y: 0}}
	if got, want := sel.text(lines), "cdef\nghij"; got != want {
		t.Errorf("backwards text = %q, want %q", got, want)
	}

	// A bare click selects nothing.
	sel = selection{active: true, region: wide, anchor: cell{x: 2, y: 0}, cursor: cell{x: 2, y: 0}}
	if !sel.empty() || sel.text(lines) != "" {
		t.Error("a click should select no text")
	}
}

// TestSelectionConfinedToRegion: a multi-row selection takes column c0 on the
// first row, c1 on the last, and the pane's full width in between — never the
// neighbouring pane or the borders.
func TestSelectionConfinedToRegion(t *testing.T) {
	lines := []string{"LIST│DIFF-A   ", "LIST│DIFF-B   ", "LIST│DIFF-C   "}
	// A "pane" starting at column 5 through column 10.
	region := selectRegion{x0: 5, y0: 0, x1: 10, y1: 2}
	sel := selection{active: true, region: region, anchor: cell{x: 6, y: 0}, cursor: cell{x: 7, y: 2}}
	got := sel.text(lines)
	if strings.Contains(got, "LIST") || strings.Contains(got, "│") {
		t.Errorf("selection leaked outside the pane: %q", got)
	}
	if want := "IFF-A\nDIFF-B\nDIF"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestSelectRegion(t *testing.T) {
	a := App{width: 120, height: 40, gitMode: true}
	a.cfg.Stats = true
	// Pressing in the left list confines to the list's inner columns.
	left := a.selectRegion(cell{x: 5, y: 5})
	if left.x0 != 1 || left.x1 != a.gitListWidth()-2 {
		t.Errorf("list region columns = %d..%d", left.x0, left.x1)
	}
	if left.y0 != 1 || left.y1 != a.bodyHeight()-2 {
		t.Errorf("list region rows = %d..%d", left.y0, left.y1)
	}
	// Pressing in the diff confines to the right pane, clear of the list.
	right := a.selectRegion(cell{x: a.gitListWidth() + 3, y: 5})
	if right.x0 != a.gitListWidth()+1 {
		t.Errorf("diff region x0 = %d, want %d", right.x0, a.gitListWidth()+1)
	}
	if right.x1 != a.width-2 {
		t.Errorf("diff region x1 = %d, want %d", right.x1, a.width-2)
	}
}

func TestHighlightSelection(t *testing.T) {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	content := accent.Render("abcdef") + "\n" + lipgloss.NewStyle().Render("ghijkl")
	wide := selectRegion{x0: 0, y0: 0, x1: 99, y1: 99}
	sel := selection{active: true, region: wide, anchor: cell{x: 2, y: 0}, cursor: cell{x: 3, y: 1}}

	got := highlightSelection(content, sel, lipgloss.NewStyle().Background(lipgloss.Color("#00ff00")))
	if ansi.Strip(got) != ansi.Strip(content) {
		t.Errorf("highlight changed the text: %q", ansi.Strip(got))
	}
	if got == content {
		t.Error("highlight should have added styling")
	}
	// The unselected prefix keeps its own color code.
	if !strings.Contains(got, accent.Render("ab")) {
		t.Errorf("the unselected prefix lost its styling: %q", got)
	}
}

func TestOverlayViewHeight(t *testing.T) {
	a := newTestApp(t) // Stats on: the resource bar must be accounted for
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	for _, key := range []rune{'g', 'T'} {
		model, _ = a.Update(tea.KeyPressMsg{Code: key})
		a = model.(App)
		if got := strings.Count(a.View().Content, "\n") + 1; got != a.height {
			t.Errorf("overlay %c rendered %d rows, want %d", key, got, a.height)
		}
		model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		a = model.(App)
	}
}

// TestOverlayFillsWidth guards the diff/code box against shrinking to its
// content: every row of the view must span the full terminal width.
func TestOverlayFillsWidth(t *testing.T) {
	a := newTestApp(t) // no repository: the diff is short and used to shrink
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	for _, key := range []rune{'g', 'T'} {
		model, _ = a.Update(tea.KeyPressMsg{Code: key})
		a = model.(App)
		for i, line := range strings.Split(a.View().Content, "\n") {
			if w := ansi.StringWidth(line); w != a.width {
				t.Errorf("overlay %c row %d width = %d, want %d", key, i, w, a.width)
			}
		}
		model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		a = model.(App)
	}
}

func TestGitSelectionCopies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	repo := twoHunkRepo(t)
	specs := testSpecs()
	for i := range specs {
		specs[i].Dir = repo
	}
	panes := make([]pane.Pane, len(specs))
	for i, spec := range specs {
		p, err := pane.Spawn(ctx, spec, 40, 10)
		if err != nil {
			t.Fatal(err)
		}
		panes[i] = p
		t.Cleanup(p.Close)
	}
	app := New(panes, specs, repo, theme.Everforest, settings.Default())
	model, _ := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a := model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'g'})
	a = model.(App)

	var copied string
	old := clipboardWrite
	clipboardWrite = func(s string) bool { copied = s; return true }
	t.Cleanup(func() { clipboardWrite = old })

	top := a.topRows()
	// Press in the diff, drag down a couple of rows, release to copy.
	model, _ = a.Update(tea.MouseClickMsg{X: 40, Y: top + 5, Button: tea.MouseLeft})
	a = model.(App)
	if !a.sel.active {
		t.Fatal("a press should start a selection")
	}
	model, _ = a.Update(tea.MouseMotionMsg{X: 46, Y: top + 7, Button: tea.MouseLeft})
	a = model.(App)
	model, _ = a.Update(tea.MouseReleaseMsg{X: 46, Y: top + 7, Button: tea.MouseLeft})
	a = model.(App)

	if a.sel.active {
		t.Error("release should end the selection")
	}
	if copied == "" {
		t.Fatal("release should copy the selection")
	}
	if !strings.Contains(copied, "f.txt") {
		t.Errorf("copied text should be diff content, got %q", copied)
	}
	// The drag must not pick up the file list or the box borders.
	if strings.Contains(copied, "unstaged") || strings.Contains(copied, "git ·") {
		t.Errorf("copied text leaked from the file list: %q", copied)
	}
	if strings.ContainsAny(copied, "╭╮╰╯│") {
		t.Errorf("copied text leaked box borders: %q", copied)
	}
	if !strings.Contains(stripANSI(a.View().Content), "copied") {
		t.Errorf("the footer should confirm the copy, view:\n%s", stripANSI(a.View().Content))
	}

	// A bare click (no drag) copies nothing.
	copied = ""
	model, _ = a.Update(tea.MouseClickMsg{X: 40, Y: top + 5, Button: tea.MouseLeft})
	a = model.(App)
	model, _ = a.Update(tea.MouseReleaseMsg{X: 40, Y: top + 5, Button: tea.MouseLeft})
	a = model.(App)
	if copied != "" {
		t.Errorf("a click should not copy, got %q", copied)
	}
}
