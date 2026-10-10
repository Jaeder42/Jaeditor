// Package workspace composes panes into the app: layout, focus, chrome.
//
// The grid:
//
//	┌──────────────┬──────────┐
//	│  main        │  agent   │
//	│  (nvim)      │  (pi)    │
//	├──────────────┤          │
//	│  terminal    │          │
//	└──────────────┴──────────┘
//
// Keys: ctrl+hjkl switches pane focus from anywhere. ctrl+q releases the
// focused pane to chrome mode; in chrome mode hjkl focuses, HJKL resizes
// the grid, and ctrl+q quits. Everything else reaches the focused pane.
package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"aw/internal/agents"
	"aw/internal/pane"
	"aw/internal/theme"
)

const (
	borderChrome  = 2 // one border cell per side, both axes
	statusRows    = 1
	respawnWindow = 3 * time.Second // a respawned pane that dies faster is marked dead
)

// App is the workspace tea.Model.
type App struct {
	panes      []pane.Pane
	specs      []pane.Spec // original spawn specs, for respawn
	rects      []Rect
	dead       []bool
	hidden     []bool      // pane toggled away from the layout (process lives on)
	spawnedAt  []time.Time // respawn time of each pane's current instance
	layout     Layout
	focus      int // pane index, or -1 for chrome mode
	lastFocus  int
	cwd        string // absolute workspace path
	theme      theme.Theme
	pickerOpen bool
	pick       picker
	gitMode    bool
	git        gitView
	width      int
	height     int
	ready      bool
}

// New builds the workspace around already-spawned panes, with the main
// pane focused. specs must match panes one-to-one; cwd is the absolute
// workspace path.
func New(panes []pane.Pane, specs []pane.Spec, cwd string, th theme.Theme) App {
	app := App{
		panes:     append([]pane.Pane(nil), panes...), // own our arrays: respawn
		specs:     append([]pane.Spec(nil), specs...), // must not alias the caller's
		dead:      make([]bool, len(panes)),
		hidden:    make([]bool, len(panes)),
		spawnedAt: make([]time.Time, len(panes)),
		layout:    DefaultLayout(),
		focus:     PaneMain,
		lastFocus: PaneMain,
		cwd:       cwd,
		theme:     th,
	}
	if len(app.panes) != PaneCount || len(app.specs) != PaneCount {
		panic("workspace.New: expected one pane and spec per slot")
	}
	app.panes[PaneMain].Focus()
	return app
}

func (a App) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2*len(a.panes))
	for i := range a.panes {
		cmds = append(cmds, a.panes[i].Init(), a.panes[i].ExitCmd())
	}
	return tea.Batch(cmds...)
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.ready = true
		return a, a.applyLayout()

	case pane.ExitMsg:
		for i := range a.panes {
			if a.panes[i].ID() == msg.PaneID {
				return a, a.respawn(i)
			}
		}
		return a, nil

	case tea.KeyPressMsg:
		if isQuitChord(msg) {
			a.closeAll()
			return a, tea.Quit
		}
		if a.gitMode {
			return a.gitKey(msg)
		}
		if a.pickerOpen {
			return a.pickerKey(msg)
		}
		if cmd, handled := a.key(msg); handled {
			return a, cmd
		}
	}

	// Everything else (FrameMsg, key passthrough, mouse) reaches every
	// pane; spanreed filters by ID and focus.
	return a, a.forward(msg)
}

// key handles the workspace's own keys. handled means the key never
// reaches any pane.
func (a *App) key(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	key := msg.String()

	// ctrl+hjkl switches focus from anywhere — one keystroke, tmux-style.
	if dir, ok := strings.CutPrefix(key, "ctrl+"); ok {
		switch dir {
		case "h", "j", "k", "l":
			a.moveTo(dir)
			return nil, true
		}
	}

	if key == "ctrl+q" {
		// Release only. In chrome mode it is a no-op: quitting is a
		// separate chord (ctrl+shift+q, or Q in chrome mode).
		if a.focus >= 0 {
			a.setFocus(-1)
		}
		return nil, true
	}

	// Chrome mode owns navigation and resize; when a pane is focused
	// those plain keys belong to the guest. Keys are matched on Code and
	// Mod: String() renders shift+j as "shift+j", not "J", and the kitty
	// protocol reports shift via Mod with the unshifted Code.
	if a.focus < 0 {
		switch msg.Code {
		case 'h', 'j', 'k', 'l':
			switch msg.Mod {
			case 0:
				a.moveTo(string(msg.Code))
				return nil, true
			case tea.ModShift:
				a.layout, _ = a.layout.Resize(string(msg.Code))
				return a.applyLayout(), true
			}
		case 'H', 'J', 'K', 'L':
			if msg.Mod == 0 || msg.Mod == tea.ModShift {
				a.layout, _ = a.layout.Resize(strings.ToLower(string(msg.Code)))
				return a.applyLayout(), true
			}
		case 't':
			if msg.Mod == 0 {
				return a.togglePane(PaneTerm), true
			}
		case 'a':
			if msg.Mod == 0 {
				return a.togglePane(PaneAgent), true
			}
		case 's':
			if msg.Mod == 0 {
				a.pickerOpen = true
				a.pick = newPicker(a.specs[PaneAgent].Command)
				return nil, true
			}
		case 'g':
			if msg.Mod == 0 {
				a.openGitView()
				return nil, true
			}
		case 'Q':
			if msg.Mod&tea.ModCtrl == 0 {
				a.closeAll()
				return tea.Quit, true
			}
		case 'q':
			if msg.Mod == tea.ModShift { // kitty reports shift+q as code q
				a.closeAll()
				return tea.Quit, true
			}
		default:
			switch key {
			case "left", "down", "up", "right":
				a.moveTo(direction(key))
				return nil, true
			}
		}
	}
	return nil, false
}

func direction(key string) string {
	switch key {
	case "left":
		return "h"
	case "down":
		return "j"
	case "up":
		return "k"
	case "right":
		return "l"
	}
	return key
}

// moveTo shifts focus in the given direction. From chrome mode it moves
// relative to the last focused pane.
func (a *App) moveTo(dir string) {
	from := a.focus
	if from < 0 {
		from = a.lastFocus
	}
	if target := Neighbor(from, dir); target >= 0 && !a.hidden[target] {
		a.setFocus(target)
	} else if a.focus < 0 {
		if a.hidden[from] {
			from = PaneMain // main is always visible
		}
		a.setFocus(from)
	}
}

// togglePane hides or shows a pane. Hidden panes keep running; showing
// again re-lays out and resizes them. Hiding the focused pane releases
// focus to chrome mode.
func (a *App) togglePane(i int) tea.Cmd {
	a.hidden[i] = !a.hidden[i]
	if a.hidden[i] && a.focus == i {
		a.setFocus(-1)
	}
	return a.applyLayout()
}

func (a *App) setFocus(i int) {
	if a.focus >= 0 {
		a.lastFocus = a.focus
	}
	for j := range a.panes {
		if j != i {
			a.panes[j].Blur()
		}
	}
	if i >= 0 {
		a.panes[i].Focus()
	}
	a.focus = i
}

// applyLayout re-splits the grid and resizes every live pane.
func (a *App) applyLayout() tea.Cmd {
	main, term, agent := a.layout.SplitVisible(
		a.width, a.height-statusRows,
		!a.hidden[PaneTerm], !a.hidden[PaneAgent],
	)
	a.rects = []Rect{main, term, agent}

	cmds := make([]tea.Cmd, 0, len(a.panes))
	for i := range a.panes {
		if a.dead[i] || a.hidden[i] {
			continue
		}
		boxW := max(2, a.rects[i].W-borderChrome)
		boxH := max(2, a.rects[i].H-borderChrome)
		var cmd tea.Cmd
		a.panes[i], cmd = a.panes[i].SetSize(boxW, boxH)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// respawn restarts a pane's process. A pane that dies within
// respawnWindow of its own spawn is crashing, not exiting: the slot is
// marked dead instead of looping.
func (a *App) respawn(i int) tea.Cmd {
	if a.dead[i] {
		return nil
	}
	a.panes[i].Close()
	if t := a.spawnedAt[i]; !t.IsZero() && time.Since(t) < respawnWindow {
		a.dead[i] = true
		if a.focus == i {
			a.setFocus(-1)
		}
		return nil
	}
	a.spawnedAt[i] = time.Now()
	rect := a.rects[i]
	p, err := pane.Spawn(context.Background(), a.specs[i],
		max(2, rect.W-borderChrome), max(2, rect.H-borderChrome))
	if err != nil {
		a.dead[i] = true
		return nil
	}
	if a.focus == i {
		p.Focus()
	}
	a.panes[i] = p
	return tea.Batch(p.Init(), p.ExitCmd())
}

// openGitView enters the fullscreen git mode and loads repository state.
func (a *App) openGitView() {
	a.setFocus(-1)
	a.gitMode = true
	a.git.reload(a.cwd)
}

// gitKey handles keys while the git view owns the screen.
func (a *App) gitKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case 'g', tea.KeyEscape:
		a.gitMode = false
	case 'j', tea.KeyDown:
		if msg.Mod == 0 && a.git.move(1) {
			a.git.loadDiff(a.cwd)
		}
	case 'k', tea.KeyUp:
		if msg.Mod == 0 && a.git.move(-1) {
			a.git.loadDiff(a.cwd)
		}
	case 'r':
		if msg.Mod == 0 {
			a.git.reload(a.cwd)
		}
	case 'd':
		if msg.Mod == tea.ModCtrl {
			a.git.scrollBy(diffPageSize(a.height), max(3, a.height-statusRows-4))
		}
	case 'u':
		if msg.Mod == tea.ModCtrl {
			a.git.scrollBy(-diffPageSize(a.height), max(3, a.height-statusRows-4))
		}
	case tea.KeyPgDown:
		a.git.scrollBy(diffPageSize(a.height), max(3, a.height-statusRows-4))
	case tea.KeyPgUp:
		a.git.scrollBy(-diffPageSize(a.height), max(3, a.height-statusRows-4))
	case 'Q':
		if msg.Mod&tea.ModCtrl == 0 {
			a.closeAll()
			return *a, tea.Quit
		}
	default:
		if msg.String() == "ctrl+q" {
			a.gitMode = false
		}
	}
	return *a, nil
}

// diffPageSize is half the diff viewport, for ctrl+d/u and page keys.
func diffPageSize(height int) int {
	return max(3, (height-statusRows-4)/2)
}

// pickerKey handles keys while the agent picker is open. The popup owns
// the keyboard: j/k move, enter swaps, esc (or ctrl+q) closes.
func (a *App) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case 'j', tea.KeyDown:
		if msg.Mod == 0 {
			a.pick.move(1)
		}
	case 'k', tea.KeyUp:
		if msg.Mod == 0 {
			a.pick.move(-1)
		}
	case tea.KeyEnter:
		return *a, a.applyPicker()
	case tea.KeyEscape:
		a.pickerOpen = false
	case 'Q':
		if msg.Mod&tea.ModCtrl == 0 {
			a.closeAll()
			return *a, tea.Quit
		}
	case 'q':
		switch msg.Mod {
		case tea.ModShift: // kitty shift+q
			a.closeAll()
			return *a, tea.Quit
		case tea.ModCtrl: // ctrl+q closes the picker, never quits
			a.pickerOpen = false
		}
	default:
		if msg.String() == "ctrl+q" {
			a.pickerOpen = false
		}
	}
	return *a, nil
}

// applyPicker swaps the agent pane to the highlighted agent.
func (a *App) applyPicker() tea.Cmd {
	a.pickerOpen = false
	ag := a.pick.entries[a.pick.selected]
	if ag.Command == a.specs[PaneAgent].Command {
		return nil // same agent: just close
	}
	return a.swapAgent(ag)
}

// swapAgent replaces the agent pane's process with another agent. The
// pane is unhidden and focused; the crash guard restarts for the new
// process.
func (a *App) swapAgent(ag agents.Agent) tea.Cmd {
	a.panes[PaneAgent].Close()
	a.dead[PaneAgent] = false
	a.hidden[PaneAgent] = false
	a.spawnedAt[PaneAgent] = time.Now()
	a.specs[PaneAgent] = pane.Spec{
		Command:    ag.Command,
		Args:       ag.Args,
		Dir:        a.cwd,
		Background: a.theme.Bg,
	}

	rect := a.rects[PaneAgent]
	cols, rows := 80, 24
	if rect.W > borderChrome && rect.H > borderChrome {
		cols, rows = rect.W-borderChrome, rect.H-borderChrome
	}
	p, err := pane.Spawn(context.Background(), a.specs[PaneAgent], cols, rows)
	if err != nil {
		a.dead[PaneAgent] = true
		return nil
	}
	a.panes[PaneAgent] = p
	a.setFocus(PaneAgent)
	return tea.Batch(p.Init(), p.ExitCmd(), a.applyLayout())
}

func (a *App) forward(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(a.panes))
	for i := range a.panes {
		if a.dead[i] {
			continue
		}
		var cmd tea.Cmd
		a.panes[i], cmd = a.panes[i].Update(msg)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (a *App) closeAll() {
	for i := range a.panes {
		a.panes[i].Close()
	}
}

func (a App) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = true
	if !a.ready {
		view.SetContent("aw: starting…")
		return view
	}

	var content string
	if a.gitMode {
		content = a.renderGit() + "\n" + a.statusBar()
	} else {
		rows := make([]string, a.height-statusRows)
		for i := range a.panes {
			a.blitPane(&rows, i)
		}
		for y, row := range rows {
			if pad := a.width - ansi.StringWidth(row); pad > 0 {
				rows[y] = row + strings.Repeat(" ", pad)
			}
		}
		content = strings.Join(rows, "\n") + "\n" + a.statusBar()
	}
	if a.pickerOpen {
		content = overlayCenter(content, a.pick.render(a.theme), a.width, a.height)
	}
	view.SetContent(lipgloss.NewStyle().Background(lipgloss.Color(a.theme.Bg)).Render(content))

	if a.focus >= 0 {
		if x, y, ok := a.panes[a.focus].Cursor(); ok {
			r := a.rects[a.focus]
			view.Cursor = tea.NewCursor(r.X+x+1, r.Y+y+1)
		}
	}
	return view
}

// blitPane writes a pane's bordered box into the rows grid at its rect.
func (a App) blitPane(rows *[]string, i int) {
	rect := a.rects[i]
	if rect.W < 1 || rect.H < 1 {
		return
	}

	var box string
	if a.dead[i] {
		box = lipgloss.Place(rect.W, rect.H, lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().Foreground(lipgloss.Color(a.theme.Dim)).Render("exited"),
			lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().
				Background(lipgloss.Color(a.theme.Bg)).
				Foreground(lipgloss.Color(a.theme.Bg))),
		)
	} else {
		borderColor := a.theme.Border
		if a.focus == i {
			borderColor = a.theme.BorderFocused
		}
		box = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(borderColor)).
			Render(a.panes[i].View())
	}

	lines := strings.Split(box, "\n")
	for y := 0; y < rect.H && rect.Y+y < len(*rows); y++ {
		line := ""
		if y < len(lines) {
			line = lines[y]
		}
		if width := ansi.StringWidth(line); width > rect.W {
			line = ansi.Truncate(line, rect.W, "")
		} else if width < rect.W {
			line += strings.Repeat(" ", rect.W-width)
		}
		target := (*rows)[rect.Y+y]
		pad := rect.X - ansi.StringWidth(target)
		if pad > 0 {
			target += strings.Repeat(" ", pad)
		}
		(*rows)[rect.Y+y] = target + line
	}
}

// statusBar is one line: aw, the workspace, the theme, pane states, and
// the key hint.
func (a App) statusBar() string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	fg := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))

	left := fg.Render(" aw ") +
		dim.Render("· "+filepath.Base(a.cwd)+" · "+t.Name)
	if a.gitMode {
		left += dim.Render(" · git")
	}

	var panes strings.Builder
	for i, p := range a.panes {
		switch {
		case a.dead[i]:
			panes.WriteString(lipgloss.NewStyle().
				Background(surface).Foreground(lipgloss.Color(t.Error)).Render("✕"))
			panes.WriteString(" " + dim.Render(paneLabel(p)))
		case a.hidden[i]:
			panes.WriteString(lipgloss.NewStyle().
				Background(surface).Foreground(lipgloss.Color(t.Subtext)).Render("◇"))
			panes.WriteString(" " + dim.Render(paneLabel(p)))
		case a.focus == i:
			panes.WriteString(lipgloss.NewStyle().
				Background(surface).Foreground(lipgloss.Color(t.Success)).Render("●"))
			panes.WriteString(" " + fg.Render(paneLabel(p)))
		default:
			panes.WriteString(dim.Render("○ " + paneLabel(p)))
		}
		if i < len(a.panes)-1 {
			panes.WriteString("  ")
		}
	}

	var hint string
	switch {
	case a.gitMode:
		hint = dim.Render("j/k file · ctrl+d/u scroll · r refresh · g/esc close · Q quit")
	case a.focus >= 0:
		hint = dim.Render("ctrl+hjkl focus · ctrl+q chrome · ctrl+shift+q quit")
	default:
		hint = dim.Render("hjkl focus · t/a hide · s swap · g git · HJKL resize · Q quit")
	}

	visible := ansi.StringWidth(left) + ansi.StringWidth(panes.String()) + ansi.StringWidth(hint)
	gap := a.width - visible - 6
	if gap < 1 {
		gap = 1
	}
	content := left + "  " + panes.String() + strings.Repeat(" ", gap) + hint + " "
	return lipgloss.NewStyle().Background(surface).Width(a.width).Render(content)
}

// paneLabel is a pane's status-bar name: the guest's OSC title when set
// (nvim names the file it edits), else the command's base name.
func paneLabel(p pane.Pane) string {
	if title := p.GuestTitle(); title != "" {
		return shortLabel(title)
	}
	return p.Title()
}

// shortLabel trims a label to max visible cells.
func shortLabel(s string) string {
	const max = 18
	if ansi.StringWidth(s) <= max {
		return s
	}
	return ansi.Truncate(s, max-1, "…")
}

// isQuitChord reports the quit chord: ctrl+shift+q, honored from anywhere.
// With the kitty protocol the shift is explicit; a legacy terminal that
// cannot report it sends plain ctrl+q, which only releases to chrome
// mode — Q remains the always-works fallback.
func isQuitChord(msg tea.KeyPressMsg) bool {
	const wanted = tea.ModCtrl | tea.ModShift
	if msg.Mod&wanted != wanted || msg.Mod&^wanted != 0 {
		return false
	}
	return msg.Code == 'q' || msg.Code == 'Q'
}
