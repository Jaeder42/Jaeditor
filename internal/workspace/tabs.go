package workspace

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/pane"
)

// Terminal tabs. The terminal slot (PaneTerm) is a stack of tabbed shells:
// one tab is active and mirrored into the App's slot arrays, so the rest of
// the workspace renders it, routes input to it, and resizes it like any
// other pane. The other tabs live here and keep running in the background.
//
// Invariant: a.panes[PaneTerm] (and its spec, dead, spawnedAt) is the tab at
// position a.termPos in the full tab order; a.termTabs holds the remaining
// tabs in display order, with the active tab removed. termTabCount() is
// always at least one.
type termTab struct {
	pane      pane.Pane
	spec      pane.Spec
	dead      bool
	spawnedAt time.Time
}

// termTabCount is the number of terminal tabs (always at least one).
func (a App) termTabCount() int { return len(a.termTabs) + 1 }

// activeTab reads the mirrored active tab out of the slot arrays.
func (a App) activeTab() termTab {
	return termTab{
		pane:      a.panes[PaneTerm],
		spec:      a.specs[PaneTerm],
		dead:      a.dead[PaneTerm],
		spawnedAt: a.spawnedAt[PaneTerm],
	}
}

// termOrder returns every terminal tab in display order: the tabs before the
// active one, the active tab, then the tabs after it.
func (a App) termOrder() []termTab {
	out := make([]termTab, 0, a.termTabCount())
	out = append(out, a.termTabs[:a.termPos]...)
	out = append(out, a.activeTab())
	out = append(out, a.termTabs[a.termPos:]...)
	return out
}

// setTermOrder installs a new full tab order with tabs[activePos] active,
// writing that tab back into the slot arrays. Focus is the caller's job.
func (a *App) setTermOrder(tabs []termTab, activePos int) {
	a.paneSel.clear()
	act := tabs[activePos]
	rest := make([]termTab, 0, len(tabs)-1)
	rest = append(rest, tabs[:activePos]...)
	rest = append(rest, tabs[activePos+1:]...)
	a.termTabs = rest
	a.termPos = activePos
	a.panes[PaneTerm] = act.pane
	a.specs[PaneTerm] = act.spec
	a.dead[PaneTerm] = act.dead
	a.spawnedAt[PaneTerm] = act.spawnedAt
}

// paneBox returns a slot's content size in cells, with a fallback used
// before the first layout.
func (a App) paneBox(slot int) (int, int) {
	if slot < len(a.rects) {
		if r := a.rects[slot]; r.W > borderChrome && r.H > borderChrome {
			return r.W - borderChrome, r.H - borderChrome
		}
	}
	return 80, 24
}

// spawnTab starts one terminal tab, returning the tab and its frame/exit
// commands.
func (a *App) spawnTab(spec pane.Spec, cols, rows int) (termTab, tea.Cmd, error) {
	p, err := pane.Spawn(context.Background(), spec, cols, rows)
	if err != nil {
		return termTab{}, nil, err
	}
	t := termTab{pane: p, spec: spec, spawnedAt: time.Now()}
	return t, tea.Batch(p.Init(), p.ExitCmd()), nil
}

// newTermTab spawns a shell tab just after the active one and focuses it,
// unhiding the terminal pane if needed.
func (a *App) newTermTab() tea.Cmd {
	spec := a.specs[PaneTerm]
	cols, rows := a.paneBox(PaneTerm)
	t, cmd, err := a.spawnTab(spec, cols, rows)
	if err != nil {
		return nil
	}
	// Blur before the copy so the tab parked in termTabs is not left focused.
	if a.focus == PaneTerm {
		a.panes[PaneTerm].Blur()
	}
	order := a.termOrder()
	pos := a.termPos + 1
	order = append(order, termTab{})
	copy(order[pos+1:], order[pos:])
	order[pos] = t
	a.hidden[PaneTerm] = false
	a.setTermOrder(order, pos)
	a.setFocus(PaneTerm)
	return tea.Batch(cmd, a.applyLayout())
}

// switchTermTab makes the tab delta positions away active (wrapping).
func (a *App) switchTermTab(delta int) tea.Cmd {
	n := a.termTabCount()
	if n < 2 {
		return nil
	}
	pos := (a.termPos + delta + n) % n
	if pos == a.termPos {
		return nil
	}
	focused := a.focus == PaneTerm
	if focused {
		a.panes[PaneTerm].Blur()
	}
	order := a.termOrder()
	a.hidden[PaneTerm] = false
	a.setTermOrder(order, pos)
	if focused {
		a.panes[PaneTerm].Focus()
	}
	return a.applyLayout()
}

// closeTermTab closes the active tab and activates its neighbor. Closing the
// last tab hides the terminal pane, replacing the process with a fresh shell
// so it is ready when the pane is shown again ('t').
func (a *App) closeTermTab() tea.Cmd {
	order := a.termOrder()
	if len(order) == 1 {
		return a.replaceTermTab()
	}
	focused := a.focus == PaneTerm
	if focused {
		a.panes[PaneTerm].Blur()
	}
	order[a.termPos].pane.Close()
	pos := a.termPos
	order = append(order[:pos], order[pos+1:]...)
	if pos >= len(order) {
		pos = len(order) - 1
	}
	a.setTermOrder(order, pos)
	if focused {
		a.panes[PaneTerm].Focus()
	}
	return a.applyLayout()
}

// replaceTermTab is closeTermTab for the last tab: hide the pane and leave a
// fresh shell behind it.
func (a *App) replaceTermTab() tea.Cmd {
	spec := a.specs[PaneTerm]
	cols, rows := a.paneBox(PaneTerm)
	a.panes[PaneTerm].Close()
	a.hidden[PaneTerm] = true
	if a.focus == PaneTerm {
		a.setFocus(-1)
	}
	t, cmd, err := a.spawnTab(spec, cols, rows)
	if err != nil {
		a.dead[PaneTerm] = true
		return a.applyLayout()
	}
	a.panes[PaneTerm] = t.pane
	a.specs[PaneTerm] = t.spec
	a.dead[PaneTerm] = false
	a.spawnedAt[PaneTerm] = t.spawnedAt
	return tea.Batch(cmd, a.applyLayout())
}

// respawnTermTab restarts an inactive tab whose process exited, mirroring
// App.respawn's crash guard.
func (a *App) respawnTermTab(i int) tea.Cmd {
	t := &a.termTabs[i]
	if t.dead {
		return nil
	}
	t.pane.Close()
	if !t.spawnedAt.IsZero() && time.Since(t.spawnedAt) < respawnWindow {
		t.dead = true
		return nil
	}
	cols, rows := a.paneBox(PaneTerm)
	p, err := pane.Spawn(context.Background(), t.spec, cols, rows)
	if err != nil {
		t.dead = true
		return nil
	}
	t.pane = p
	t.spawnedAt = time.Now()
	return tea.Batch(p.Init(), p.ExitCmd())
}

// borderColor is a slot's border color: focused panes get the accent.
func (a App) borderColor(slot int) string {
	if a.focus == slot {
		return a.theme.BorderFocused
	}
	return a.theme.Border
}

// tabBar renders the terminal pane's tab strip as its top border line: one
// row of exactly rect.W cells, shown only when there is more than one tab.
func (a App) tabBar(rect Rect) string {
	t := a.theme
	border := lipgloss.NewStyle().Foreground(lipgloss.Color(a.borderColor(PaneTerm)))
	inner := max(0, rect.W-2)

	tabs := a.termOrder()
	var strip strings.Builder
	for i, tab := range tabs {
		label := " " + tabLabel(tab) + " "
		switch {
		case i == a.termPos:
			strip.WriteString(lipgloss.NewStyle().
				Background(lipgloss.Color(t.Surface)).
				Foreground(lipgloss.Color(t.Fg)).Bold(true).Render(label))
		case tab.dead:
			strip.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(t.Error)).Render(label))
		default:
			strip.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(t.Dim)).Render(label))
		}
		if i < len(tabs)-1 {
			strip.WriteString(border.Render("│"))
		}
	}

	content := strip.String()
	if w := ansi.StringWidth(content); w < inner {
		content += border.Render(strings.Repeat("─", inner-w))
	} else if w > inner {
		content = ansi.Truncate(content, inner, "…")
	}
	return border.Render("╭") + content + border.Render("╮")
}

// tabLabel is a tab's display name: the guest's OSC title when set, else the
// command's base name; dead tabs are marked.
func tabLabel(t termTab) string {
	if t.dead {
		return "✕ " + t.pane.Title()
	}
	if title := t.pane.GuestTitle(); title != "" {
		return shortLabel(title)
	}
	return t.pane.Title()
}
