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
// Keys: ctrl+hjkl switches pane focus from anywhere, and a mouse click
// on a pane focuses it. ctrl+q releases the focused pane to chrome mode;
// in chrome mode hjkl focuses, HJKL resizes the grid, and ctrl+q quits.
// Everything else reaches the focused pane.
//
// The terminal pane is tabbed: ctrl+t opens a shell tab, ctrl+pgup/pgdn
// (or ctrl+shift+[/]) switches, and ctrl+w closes — while the terminal is
// focused, or from chrome mode, which has no guest. See tabs.go.
package workspace

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/agents"
	"jaed/internal/pane"
	"jaed/internal/settings"
	"jaed/internal/stats"
	"jaed/internal/theme"
)

const (
	borderChrome  = 2 // one border cell per side, both axes
	statusRows    = 1
	respawnWindow = 3 * time.Second // a respawned pane that dies faster is marked dead
)

// App is the workspace tea.Model.
type App struct {
	panes        []pane.Pane
	specs        []pane.Spec // original spawn specs, for respawn
	rects        []Rect
	dead         []bool
	hidden       []bool      // pane toggled away from the layout (process lives on)
	spawnedAt    []time.Time // respawn time of each pane's current instance
	termTabs     []termTab   // inactive terminal tabs, in display order (see tabs.go)
	termPos      int         // position of the active terminal tab in the full order
	layout       Layout
	focus        int // pane index, or -1 for chrome mode
	lastFocus    int
	cwd          string // absolute workspace path
	theme        theme.Theme
	pickerOpen   bool
	pick         picker
	gitMode      bool
	git          gitView
	toolsMode    bool
	tools        toolsView
	settingsOpen bool
	settingsSel  int
	cfg          settings.Settings
	// Resource bar state (see statsview.go). The collector is shared by
	// pointer across App copies; usage is the latest sample.
	statsCollector *stats.Collector
	usage          stats.Usage
	statsOK        bool
	activeFile     string // live-link path (see link.go)
	activeSeen     string // last title published to it
	// bufferQuery asks the editor pane for the active file; overridable
	// in tests.
	bufferQuery func() (string, error)
	width       int
	height      int
	ready       bool
}

// New builds the workspace around already-spawned panes, with the main
// pane focused. specs must match panes one-to-one; cwd is the absolute
// workspace path; cfg is the persisted user settings.
func New(panes []pane.Pane, specs []pane.Spec, cwd string, th theme.Theme, cfg settings.Settings) App {
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
	app.bufferQuery = app.editorBufferTitle
	app.activeFile = activeFilePath(cwd)
	app.cfg = cfg
	app.statsCollector = stats.New()
	app.panes[PaneMain].Focus()
	return app
}

func (a App) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2*(len(a.panes)+len(a.termTabs))+1)
	for i := range a.panes {
		cmds = append(cmds, a.panes[i].Init(), a.panes[i].ExitCmd())
	}
	for i := range a.termTabs {
		p := a.termTabs[i].pane
		cmds = append(cmds, p.Init(), p.ExitCmd())
	}
	if a.cfg.Stats {
		cmds = append(cmds, a.sampleStats())
	}
	return tea.Batch(cmds...)
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a.syncActiveFile()

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.ready = true
		return a, a.applyLayout()

	case tea.PasteMsg:
		// pastes go to the focused pane; chrome, git, and picker modes
		// have no pane to receive them
		if a.focus >= 0 && !a.dead[a.focus] {
			_ = a.panes[a.focus].Paste(msg.Content)
		}
		return a, nil

	case pane.ExitMsg:
		for i := range a.panes {
			if a.panes[i].ID() == msg.PaneID {
				return a, a.respawn(i)
			}
		}
		for i := range a.termTabs {
			if a.termTabs[i].pane.ID() == msg.PaneID {
				return a, a.respawnTermTab(i)
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
		if a.toolsMode {
			return a.toolsKey(msg)
		}
		if a.settingsOpen {
			return a.settingsKey(msg)
		}
		if a.pickerOpen {
			return a.pickerKey(msg)
		}
		if cmd, handled := a.key(msg); handled {
			return a, cmd
		}

	case tea.MouseWheelMsg:
		return a, a.mouseWheel(msg)

	case tea.MouseMsg:
		return a, a.mouse(msg)

	case statsSampleMsg:
		a.usage, a.statsOK = msg.usage, msg.ok
		if a.cfg.Stats {
			return a, statsTick()
		}
		return a, nil

	case statsTickMsg:
		if a.cfg.Stats {
			return a, a.sampleStats()
		}
		return a, nil
	}

	// Everything else (FrameMsg, key passthrough) reaches every pane;
	// spanreed filters by ID and focus.
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

	// Tab chords — open, switch, close — are consumed only while the
	// terminal is focused (or from chrome mode, which has no guest), so
	// they never shadow other panes' chords: the editor owns ctrl+t (nvim
	// jumps tags with it), ctrl+w, and ctrl+pgup/pgdn. ctrl+w and
	// ctrl+shift+w both close; ctrl+shift+[ and ] switch alongside
	// pgup/pgdn.
	if (a.focus == PaneTerm || a.focus < 0) && msg.Mod&tea.ModCtrl != 0 {
		switch msg.Code {
		case 't':
			return a.newTermTab(), true
		case 'w':
			return a.closeTermTab(), true
		case tea.KeyPgUp:
			return a.switchTermTab(-1), true
		case tea.KeyPgDown:
			return a.switchTermTab(1), true
		case '[', ']':
			// Require shift: a bare ctrl+[ is ESC on legacy terminals.
			if msg.Mod&tea.ModShift == 0 {
				break
			}
			delta := -1
			if msg.Code == ']' {
				delta = 1
			}
			return a.switchTermTab(delta), true
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
			switch msg.Mod {
			case 0:
				return a.togglePane(PaneTerm), true
			case tea.ModShift:
				a.openToolsView()
				return nil, true
			}
		case 'T':
			// legacy parsers report the shifted code directly
			if msg.Mod&tea.ModCtrl == 0 {
				a.openToolsView()
				return nil, true
			}
		case 'a':
			if msg.Mod == 0 {
				return a.togglePane(PaneAgent), true
			}
		case 's', 'S':
			// real input reports shift+s as code 's' with the shift
			// modifier (legacy and kitty agree); code 'S' covers any
			// parser that reports the shifted code directly.
			switch {
			case msg.Code == 'S' && msg.Mod&tea.ModCtrl == 0,
				msg.Mod == tea.ModShift:
				a.settingsOpen = true
				a.settingsSel = 0
				a.settingsMove(1) // land on the first selectable row
				return nil, true
			case msg.Mod == 0:
				a.pickerOpen = true
				a.pick = newPicker(a.specs[PaneAgent].Command)
				return nil, true
			}
		case 'g':
			if msg.Mod == 0 {
				a.openGitView()
				return nil, true
			}
		case 'c':
			if msg.Mod == 0 {
				return a.pushEditorContext(), true
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

// pushEditorContext links the editor pane with the agent: it asks the
// editor for the active file, types an @-mention of it into the agent's
// input, and focuses the agent so the next keystrokes finish the prompt.
// The agent (pi, opencode, …) attaches the file as context on submit.
func (a *App) pushEditorContext() tea.Cmd {
	if a.dead[PaneAgent] {
		return nil
	}
	var cmd tea.Cmd
	if a.hidden[PaneAgent] {
		a.hidden[PaneAgent] = false
		cmd = a.applyLayout()
	}
	a.setFocus(PaneAgent)
	if a.dead[PaneMain] {
		return cmd // no editor to ask
	}
	path, err := a.bufferQuery()
	if err != nil || path == "" {
		return cmd // no file open, or the editor is not listening
	}
	_ = a.panes[PaneAgent].Write([]byte("@" + path + " "))
	return cmd
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
		a.width, a.bodyHeight(),
		!a.hidden[PaneTerm], !a.hidden[PaneAgent],
	)
	a.rects = []Rect{main, term, agent}

	cmds := make([]tea.Cmd, 0, len(a.panes)+len(a.termTabs))
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
	// Inactive terminal tabs share the terminal box, so switching to one is
	// instant and its process always sees the right size.
	if !a.hidden[PaneTerm] {
		boxW := max(2, a.rects[PaneTerm].W-borderChrome)
		boxH := max(2, a.rects[PaneTerm].H-borderChrome)
		for i := range a.termTabs {
			if a.termTabs[i].dead {
				continue
			}
			var cmd tea.Cmd
			a.termTabs[i].pane, cmd = a.termTabs[i].pane.SetSize(boxW, boxH)
			cmds = append(cmds, cmd)
		}
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
			a.git.scrollBy(diffPageSize(a.bodyHeight()), max(3, a.bodyHeight()-4))
		}
	case 'u':
		if msg.Mod == tea.ModCtrl {
			a.git.scrollBy(-diffPageSize(a.height), max(3, a.height-statusRows-4))
		}
	case tea.KeyPgDown:
		a.git.scrollBy(diffPageSize(a.bodyHeight()), max(3, a.bodyHeight()-4))
	case tea.KeyPgUp:
		a.git.scrollBy(-diffPageSize(a.height), max(3, a.height-statusRows-4))
	case 'Q', 'q':
		// real input reports shift+q as code 'q' with the shift
		// modifier; quit on either form, and close on ctrl+q.
		switch {
		case msg.Code == 'Q' && msg.Mod&tea.ModCtrl == 0,
			msg.Mod == tea.ModShift:
			a.closeAll()
			return *a, tea.Quit
		case msg.String() == "ctrl+q":
			a.gitMode = false
		}
	default:
		if msg.String() == "ctrl+q" {
			a.gitMode = false
		}
	}
	return *a, nil
}

// diffPageSize is half the diff viewport, for ctrl+d/u and page keys.
// diffPageSize is half the diff viewport for ctrl+d/u and the page keys,
// computed from the body height (status and resource bars already removed).
func diffPageSize(bodyHeight int) int {
	return max(3, (bodyHeight-4)/2)
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
	cmds := make([]tea.Cmd, 0, len(a.panes)+len(a.termTabs))
	for i := range a.panes {
		if a.dead[i] {
			continue
		}
		var cmd tea.Cmd
		a.panes[i], cmd = a.panes[i].Update(msg)
		cmds = append(cmds, cmd)
	}
	for i := range a.termTabs {
		if a.termTabs[i].dead {
			continue
		}
		var cmd tea.Cmd
		a.termTabs[i].pane, cmd = a.termTabs[i].pane.Update(msg)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// mouseWheel routes a wheel event to the pane under the cursor: to the
// guest as a mouse event when it enabled tracking (so full-screen TUIs
// like opencode scroll their own view), otherwise to the pane's scrollback.
func (a *App) mouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	if a.gitMode || a.toolsMode || a.settingsOpen || a.pickerOpen {
		return nil
	}
	m := msg.Mouse()
	// Mouse events arrive in zero-based screen rows, but the grid starts
	// below the resource bar (topRows): shift into grid rows before the
	// hit test, or every event lands one line low.
	m.Y -= a.topRows()
	i := a.paneAt(m.X, m.Y)
	if i < 0 {
		return nil
	}
	if a.panes[i].MouseTracking() {
		x, y := a.localCoords(i, m.X, m.Y)
		_ = a.panes[i].Mouse(msg, x, y)
		return nil
	}
	delta := 3
	switch m.Button {
	case tea.MouseWheelUp:
	case tea.MouseWheelDown:
		delta = -3
	default:
		return nil
	}
	return a.panes[i].QueueScroll(delta)
}

// mouse focuses the pane under the cursor on a click — tmux-style, so
// the grid can be driven without chords — and relays clicks, releases,
// and motion to that pane when its guest enabled mouse tracking; other
// panes let the host terminal handle selection.
func (a *App) mouse(msg tea.MouseMsg) tea.Cmd {
	if a.gitMode || a.toolsMode || a.settingsOpen || a.pickerOpen {
		return nil
	}
	m := msg.Mouse()
	// Screen row → grid row: the resource bar sits above the grid, so
	// raw screen coordinates would hit one line below the cursor.
	m.Y -= a.topRows()
	i := a.paneAt(m.X, m.Y)
	if i < 0 {
		return nil
	}
	// A click — any button press — moves focus; motion and releases do
	// not, so dragging across panes never steals focus mid-selection.
	if _, ok := msg.(tea.MouseClickMsg); ok && a.focus != i {
		a.setFocus(i)
	}
	if !a.panes[i].MouseTracking() {
		return nil
	}
	x, y := a.localCoords(i, m.X, m.Y)
	_ = a.panes[i].Mouse(msg, x, y)
	return nil
}

// paneAt returns the visible pane containing grid cell (x, y), or -1.
// Coordinates are content rows — zero is the first grid row, below the
// resource bar; the mouse handlers shift screen rows on the way in.
func (a *App) paneAt(x, y int) int {
	for i, r := range a.rects {
		if a.hidden[i] || a.dead[i] || r.W < 1 || r.H < 1 {
			continue
		}
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return i
		}
	}
	return -1
}

// localCoords maps a grid cell to pane-local, zero-based guest
// coordinates (the pane's border is one cell thick).
func (a *App) localCoords(i, x, y int) (int, int) {
	r := a.rects[i]
	cols, rows := a.panes[i].Size()
	return clamp(x-r.X-1, 0, max(0, cols-1)), clamp(y-r.Y-1, 0, max(0, rows-1))
}

func (a *App) closeAll() {
	for i := range a.panes {
		a.panes[i].Close()
	}
	for i := range a.termTabs {
		a.termTabs[i].pane.Close()
	}
}

func (a App) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = true
	if !a.ready {
		view.SetContent("jaed: starting…")
		return view
	}
	// Always take mouse reporting. Otherwise the host terminal's
	// alternate-scroll mode turns every wheel event into Up/Down arrow
	// keys before jaed sees it — which walks prompt history and cursor
	// position instead of scrolling. With reporting on, jaed routes the
	// wheel to the pane under the cursor itself (see mouseWheel).
	view.MouseMode = tea.MouseModeCellMotion

	var content string
	if a.gitMode {
		content = a.renderGit() + "\n" + a.statusBar()
	} else if a.toolsMode {
		content = a.renderTools() + "\n" + a.statusBar()
	} else {
		rows := make([]string, a.bodyHeight())
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
	// The resource bar sits above everything, including the git and tools
	// views, so the reading is always in the same place.
	if a.cfg.Stats {
		content = a.statsBar() + "\n" + content
	}
	if a.settingsOpen {
		content = overlayCenter(content, a.renderSettings(), a.width, a.height)
	} else if a.pickerOpen {
		content = overlayCenter(content, a.pick.render(a.theme), a.width, a.height)
	}
	view.SetContent(lipgloss.NewStyle().Background(lipgloss.Color(a.theme.Bg)).Render(content))

	if a.focus >= 0 {
		if x, y, ok := a.panes[a.focus].Cursor(); ok {
			r := a.rects[a.focus]
			view.Cursor = tea.NewCursor(r.X+x+1, r.Y+y+1+a.topRows())
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
		box = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(a.borderColor(i))).
			Render(a.panes[i].View())
	}

	lines := strings.Split(box, "\n")
	// The terminal pane's top border doubles as its tab strip.
	if i == PaneTerm && a.termTabCount() > 1 && rect.W >= 6 && len(lines) > 0 {
		lines[0] = a.tabBar(rect)
	}
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

// statusBar is one line: jaed, the workspace, the theme, pane states, and
// the key hint.
func (a App) statusBar() string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	fg := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))

	left := fg.Render(" jaed ") +
		dim.Render("· "+filepath.Base(a.cwd)+" · "+t.Name)
	if a.gitMode {
		left += dim.Render(" · git")
	}
	if a.toolsMode {
		left += dim.Render(" · tools")
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
		// The terminal slot shows which tab is active.
		if i == PaneTerm && a.termTabCount() > 1 {
			tab := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))
			panes.WriteString(tab.Render(" " + strconv.Itoa(a.termPos+1) + "/" + strconv.Itoa(a.termTabCount())))
		}
		if i < len(a.panes)-1 {
			panes.WriteString("  ")
		}
	}

	var hint string
	switch {
	case a.gitMode:
		hint = dim.Render("j/k file · ctrl+d/u scroll · r refresh · g/esc close · Q quit")
	case a.toolsMode:
		hint = dim.Render("j/k tool · ctrl+d/u scroll · r rescan · T/esc close · Q quit")
	case a.focus == PaneTerm:
		hint = dim.Render("ctrl+t tab · ctrl+pgup/pgdn switch · ctrl+w close · ctrl+q chrome")
	case a.focus >= 0:
		hint = dim.Render("ctrl+hjkl focus · ctrl+q chrome")
	default:
		hint = dim.Render("hjkl focus · t/a hide · s swap · g git · T tools · S settings · Q quit")
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
