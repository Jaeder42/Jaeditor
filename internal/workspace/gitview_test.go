package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"jaed/internal/git"
)

// diffViewForTest builds a gitView over a synthetic two-hunk diff so the
// scroll/hunk coupling can be tested without a repository.
func diffViewForTest() *gitView {
	return &gitView{
		display: []diffRow{
			{text: "diff --git a/x b/x", hunk: -1},
			{text: "@@ -1,2 +1,2 @@", hunk: 0, header: true},
			{text: "-a", hunk: 0},
			{text: "+A", hunk: 0},
			{text: "@@ -10,2 +10,2 @@", hunk: 1, header: true},
			{text: "-z", hunk: 1},
			{text: "+Z", hunk: 1},
		},
		hunks: make([]git.HunkSpan, 2),
		hunk:  0,
	}
}

// TestGitScrollSyncsHunk: scrolling the diff makes the hunk at the top of the
// viewport active, so the actions follow the scroll.
func TestGitScrollSyncsHunk(t *testing.T) {
	g := diffViewForTest()

	// Scrolled to the preamble and into the first hunk: still hunk 1.
	g.scrollBy(3, 3)
	if g.scroll != 3 || g.hunk != 0 {
		t.Fatalf("scroll=%d hunk=%d, want 3/0", g.scroll, g.hunk)
	}
	// One more line puts the second hunk's header at the top.
	g.scrollBy(1, 3)
	if g.scroll != 4 || g.hunk != 1 {
		t.Fatalf("scroll=%d hunk=%d, want 4/1", g.scroll, g.hunk)
	}
	// Scrolling back up hands the first hunk the selection again.
	g.scrollBy(-2, 3)
	if g.hunk != 0 {
		t.Fatalf("hunk=%d after scrolling up, want 0", g.hunk)
	}
	// Past the last header the last hunk stays active.
	g.scrollBy(10, 3)
	if g.scroll != 4 || g.hunk != 1 {
		t.Fatalf("scroll=%d hunk=%d, want clamped 4/1", g.scroll, g.hunk)
	}
}

func TestGitScrollNoHunks(t *testing.T) {
	g := &gitView{display: []diffRow{{text: "Binary files differ", hunk: -1}}}
	g.scrollBy(5, 3)
	if g.hunk != -1 {
		t.Errorf("a file with no hunks should keep hunk = -1, got %d", g.hunk)
	}
	if g.scroll != 0 {
		t.Errorf("scrolling a single line should clamp to 0, got %d", g.scroll)
	}
}

// TestGitWheelDirection: a wheel-down scrolls the diff down (later lines) and
// a wheel-up scrolls it back, matching the pane wheel convention.
func TestGitWheelDirection(t *testing.T) {
	a := newTestApp(t)
	a.gitMode = true
	a.git.display = make([]diffRow, 100)
	a.git.hunks = []git.HunkSpan{{}}
	a.git.hunk = 0

	model, _ := a.Update(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown})
	a = model.(App)
	if a.git.scroll <= 0 {
		t.Errorf("wheel down should scroll down, got scroll=%d", a.git.scroll)
	}
	model, _ = a.Update(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelUp})
	a = model.(App)
	if a.git.scroll != 0 {
		t.Errorf("wheel up should scroll back up, got scroll=%d", a.git.scroll)
	}
}
