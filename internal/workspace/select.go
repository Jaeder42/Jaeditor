package workspace

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Mouse text selection for the overlay views (git and tools). The host
// terminal never sees the drag — jaed takes mouse reporting — so selection is
// tracked here in the view's own cell coordinates: row 0 is the view's first
// row (below the resource bar), column 0 its left edge.

// selection is a drag selection over an overlay view's cells. It is confined
// to a single pane (the list or the diff/code view) so a drag never picks up
// the neighbouring pane or the box borders.
type selection struct {
	active bool
	region selectRegion
	anchor cell
	cursor cell
}

// cell is one visible position in an overlay view.
type cell struct{ x, y int }

// selectRegion is the inclusive cell rectangle a selection is confined to, in
// overlay content coordinates.
type selectRegion struct{ x0, y0, x1, y1 int }

// clamp pulls p inside the region.
func (r selectRegion) clamp(p cell) cell {
	return cell{x: clamp(p.x, r.x0, r.x1), y: clamp(p.y, r.y0, r.y1)}
}

// start begins a fresh selection anchored at p, confined to region.
func (s *selection) start(p cell, region selectRegion) {
	s.active = true
	s.region = region
	s.anchor = region.clamp(p)
	s.cursor = s.anchor
}

// extend moves the free end of the selection to p, kept inside the region.
func (s *selection) extend(p cell) {
	if s.active {
		s.cursor = s.region.clamp(p)
	}
}

// clear drops the selection.
func (s *selection) clear() { s.active = false }

// bounds returns the selection's ends in reading order, top-left to
// bottom-right, so start never follows end.
func (s selection) bounds() (start, end cell) {
	start, end = s.anchor, s.cursor
	if end.y < start.y || (end.y == start.y && end.x < start.x) {
		start, end = end, start
	}
	return start, end
}

// empty reports whether the selection covers no cells (a bare click).
func (s selection) empty() bool { return !s.active || s.anchor == s.cursor }

// rowRange is the column span to take from row y: the anchor's column on the
// first row, the cursor's on the last, and the whole pane in between.
func (s selection) rowRange(start, end cell, y int) (int, int) {
	c0, c1 := s.region.x0, s.region.x1+1
	if y == start.y {
		c0 = start.x
	}
	if y == end.y {
		c1 = end.x + 1
	}
	return c0, c1
}

// rows returns every row covered by the selection, clamped to the region.
func (s selection) rows() []int {
	if s.empty() {
		return nil
	}
	start, end := s.bounds()
	var out []int
	for y := start.y; y <= end.y; y++ {
		if y >= s.region.y0 && y <= s.region.y1 {
			out = append(out, y)
		}
	}
	return out
}

// text returns the selected rows of an overlay, clipped to the selection's
// pane and columns and stripped of styling and trailing padding.
func (s selection) text(lines []string) string {
	start, end := s.bounds()
	var out []string
	for _, y := range s.rows() {
		if y < 0 || y >= len(lines) {
			continue
		}
		c0, c1 := s.rowRange(start, end, y)
		w := ansi.StringWidth(lines[y])
		c0, c1 = clamp(c0, 0, w), clamp(c1, 0, w)
		if c1 <= c0 {
			out = append(out, "")
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(lines[y], c0, c1)), " \t"))
	}
	return strings.Join(out, "\n")
}

// highlightSelection paints style over the selected cells of a rendered
// overlay, leaving the rest of each row's styling intact.
func highlightSelection(content string, s selection, style lipgloss.Style) string {
	if s.empty() {
		return content
	}
	start, end := s.bounds()
	lines := strings.Split(content, "\n")
	for _, y := range s.rows() {
		if y < 0 || y >= len(lines) {
			continue
		}
		w := ansi.StringWidth(lines[y])
		c0, c1 := s.rowRange(start, end, y)
		c0, c1 = clamp(c0, 0, w), clamp(c1, 0, w)
		if c1 <= c0 {
			continue
		}
		// Cut keeps the escape codes around the range, so the unselected
		// remainder keeps its colors after the selection's reset.
		before := ansi.Cut(lines[y], 0, c0)
		mid := ansi.Strip(ansi.Cut(lines[y], c0, c1))
		after := ansi.TruncateLeft(lines[y], c1, "")
		lines[y] = before + style.Render(mid) + after
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard writes text to the system clipboard using the platform's
// tool, reporting whether one was available. OSC 52 is deliberately avoided:
// many terminals gate it behind an opt-in.
func copyToClipboard(text string) bool {
	if text == "" {
		return false
	}
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip"}}
	default:
		candidates = [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		}
	}
	for _, argv := range candidates {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

// selectionStyle is the highlight used over selected text: the same accent
// the views use for a selected row.
func (a App) selectionStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(a.theme.Accent)).
		Foreground(lipgloss.Color(a.theme.Bg))
}

// overlayContent is the active overlay's rendered rows, exactly as View draws
// them above the status bar.
func (a App) overlayContent() string {
	switch {
	case a.gitMode:
		return a.renderGit()
	case a.toolsMode:
		return a.renderTools()
	default:
		return ""
	}
}

// withSelection overlays the active text selection onto an overlay's rows.
func (a App) withSelection(content string) string {
	if !a.sel.active {
		return content
	}
	return highlightSelection(content, a.sel, a.selectionStyle())
}

// setOverlayNotice stores a transient message in the active overlay's footer.
func (a *App) setOverlayNotice(msg string, isErr bool) {
	switch {
	case a.gitMode:
		a.git.setNotice(msg, isErr)
	case a.toolsMode:
		a.tools.setNotice(msg, isErr)
	}
}

// selectRegion returns the inner cell bounds of the overlay pane containing p:
// the left list or the right diff/code view, excluding the box borders. The
// selection is confined to whichever pane the drag starts in.
func (a App) selectRegion(p cell) selectRegion {
	contentH := a.bodyHeight()
	y0, y1 := 1, max(1, contentH-2)
	switch {
	case a.gitMode:
		if p.x < a.gitListWidth() {
			return selectRegion{x0: 1, y0: y0, x1: max(1, a.gitListWidth()-2), y1: y1}
		}
	case a.toolsMode:
		if p.x < a.toolsListWidth() {
			return selectRegion{x0: 1, y0: y0, x1: max(1, a.toolsListWidth()-2), y1: y1}
		}
	}
	// The right pane: its content sits between the list and the right border.
	return selectRegion{x0: a.rightPaneX0() + 1, y0: y0, x1: max(0, a.width-2), y1: y1}
}

// rightPaneX0 is the column where the overlay's right box begins (its left
// border), matching renderGit/renderTools.
func (a App) rightPaneX0() int {
	switch {
	case a.toolsMode:
		return a.toolsListWidth()
	default:
		return a.gitListWidth()
	}
}

// paneSelectRegion is grid pane i's content rectangle in pane-local
// coordinates: row/column zero is the first cell inside the box border.
func (a App) paneSelectRegion(i int) selectRegion {
	cols, rows := a.panes[i].Size()
	return selectRegion{x0: 0, y0: 0, x1: max(0, cols-1), y1: max(0, rows-1)}
}

// paneCell maps a grid cell (already shifted by the resource bar) onto a
// pane-local content cell.
func (a App) paneCell(i, x, y int) cell {
	x, y = a.localCoords(i, x, y)
	return cell{x: x, y: y}
}

// paneMouse drives text selection inside a grid pane whose guest has not
// enabled mouse tracking (shells and other plain programs). Tracking guests
// keep the mouse for themselves. A press anchors, motion extends, and the
// release is handled by mouse once the selection is active.
func (a *App) paneMouse(i int, msg tea.MouseMsg, x, y int) {
	if _, ok := msg.(tea.MouseClickMsg); ok {
		a.paneSel.clear()
	}
	m := msg.Mouse()
	p := a.paneCell(i, x, y)
	switch msg.(type) {
	case tea.MouseClickMsg:
		if m.Button == tea.MouseLeft {
			a.paneSel.start(p, a.paneSelectRegion(i))
			a.paneSelPane = i
		}
	case tea.MouseMotionMsg:
		if m.Button == tea.MouseLeft {
			a.paneSel.extend(p)
		}
	}
}

// copyPaneSelection copies the highlighted cells of the active grid pane to
// the system clipboard, using the pane's own rendered view (so the copied
// text matches what is on screen) and confirming it in the status bar.
func (a *App) copyPaneSelection() {
	i := a.paneSelPane
	text := ""
	if i >= 0 && i < len(a.panes) && !a.dead[i] {
		text = a.paneSel.text(strings.Split(a.panes[i].View(), "\n"))
	}
	a.paneSel.clear()
	if text == "" {
		return
	}
	if !clipboardWrite(text) {
		a.copyNotice, a.copyNoticeErr = "clipboard unavailable", true
		return
	}
	a.copyNotice, a.copyNoticeErr = copiedLabel(text), false
}

// copiedLabel is the confirmation shown after a copy: "copied" for one
// line, "copied N lines" for several.
func copiedLabel(text string) string {
	if n := strings.Count(text, "\n") + 1; n > 1 {
		return fmt.Sprintf("copied %d lines", n)
	}
	return "copied"
}

// selectMouse drives the overlay text selection: press to anchor, drag to
// extend, release to copy the highlighted text to the clipboard.
func (a *App) selectMouse(msg tea.MouseMsg) {
	m := msg.Mouse()
	p := cell{x: m.X, y: m.Y - a.topRows()}
	switch msg.(type) {
	case tea.MouseClickMsg:
		if m.Button == tea.MouseLeft {
			a.sel.start(p, a.selectRegion(p))
		} else {
			a.sel.clear()
		}
	case tea.MouseMotionMsg:
		if m.Button == tea.MouseLeft {
			a.sel.extend(p)
		}
	case tea.MouseReleaseMsg:
		if !a.sel.active {
			return
		}
		a.sel.extend(p)
		a.copySelection()
	}
}

// clipboardWrite writes to the system clipboard; a variable so tests can
// intercept it instead of touching the real clipboard.
var clipboardWrite = copyToClipboard

// copySelection copies the highlighted overlay text to the system clipboard
// and reports the result in the overlay's footer.
func (a *App) copySelection() {
	text := a.sel.text(strings.Split(a.overlayContent(), "\n"))
	a.sel.clear()
	if text == "" {
		return
	}
	if !clipboardWrite(text) {
		a.setOverlayNotice("clipboard unavailable", true)
		return
	}
	a.setOverlayNotice(copiedLabel(text), false)
}
