package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	agents2 "jaed/internal/agents"
	"jaed/internal/pane"
	"jaed/internal/settings"
	"jaed/internal/theme"
)

// testSpecs spawns three fast marker panes instead of nvim/zsh/pi.
func testSpecs() []pane.Spec {
	marker := func(text string) pane.Spec {
		return pane.Spec{
			Command:    "/bin/sh",
			Args:       []string{"-c", "printf " + text + "; sleep 60"},
			Background: "#2b3339", // opaque panes, like production
		}
	}
	return []pane.Spec{
		marker("MAIN-MARK"),
		marker("TERM-MARK"),
		marker("AGENT-MARK"),
	}
}

func newTestApp(t *testing.T) App {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	// a real directory: spawn paths must exist
	cwd := filepath.Join(t.TempDir(), "chess")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	specs := testSpecs()
	panes := make([]pane.Pane, len(specs))
	for i, spec := range specs {
		p, err := pane.Spawn(ctx, spec, 40, 10)
		if err != nil {
			t.Fatal(err)
		}
		panes[i] = p
		t.Cleanup(p.Close)
	}
	app := New(panes, specs, cwd, theme.Everforest, settings.Default())
	model, cmd := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a := model.(App)
	if cmd != nil {
		cmd()
	}
	return a
}

// waitView polls the pane emulators until all markers render, then returns.
func (a *App) waitView(t *testing.T, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var view string
	for time.Now().Before(deadline) {
		view = a.View().Content
		found := true
		for _, w := range want {
			if !strings.Contains(stripANSI(view), w) {
				found = false
			}
		}
		if found {
			return view
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("markers never rendered; want %v; view was:\n%s", want, stripANSI(view))
	return ""
}

func stripANSI(s string) string {
	repl := func(r rune) rune { return ' ' }
	_ = repl
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && !((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
				j++
			}
			i = j + 1
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func TestWorkspaceGrid(t *testing.T) {
	a := newTestApp(t)

	// All three panes render their markers inside the grid, and the
	// status bar knows the workspace and the focused pane.
	_ = a.waitView(t, "MAIN-MARK", "TERM-MARK", "AGENT-MARK",
		"chess", "everforest", "● sh")
	if !a.panes[PaneMain].Focused() {
		t.Error("main pane should start focused")
	}

	// ctrl+j moves focus to the terminal pane, and the status dot moves.
	model, _ := a.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	a = model.(App)
	if !a.panes[PaneTerm].Focused() || a.panes[PaneMain].Focused() {
		t.Error("ctrl+j should move focus from main to terminal")
	}
	if !strings.Contains(stripANSI(a.statusBar()), "● sh") {
		t.Error("focused marker should follow focus")
	}

	// ctrl+l reaches the agent column from the terminal pane.
	model, _ = a.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	a = model.(App)
	if !a.panes[PaneAgent].Focused() {
		t.Error("ctrl+l should move focus to the agent pane")
	}

	// ctrl+q releases to chrome mode; HJKL resizes the grid; ctrl+q quits.
	model, _ = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	if a.focus != -1 {
		t.Error("ctrl+q should release to chrome mode")
	}
	model, cmd := a.Update(tea.KeyPressMsg{Code: 'J', Mod: tea.ModShift})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if a.layout.TermFrac <= DefaultLayout().TermFrac {
		t.Errorf("J in chrome mode should grow the terminal pane, have %v", a.layout.TermFrac)
	}
	_, termRect, _ := a.layout.Split(120, 39)
	if termRect.H <= 12 {
		t.Errorf("terminal rect should have grown, have %+v", termRect)
	}

	// L widens the agent column.
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'L', Mod: tea.ModShift})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if a.layout.AgentFrac <= DefaultLayout().AgentFrac {
		t.Errorf("L in chrome mode should widen the agent column, have %v", a.layout.AgentFrac)
	}
	_, _, agentRect := a.layout.Split(120, 39)
	if agentRect.W <= 42 {
		t.Errorf("agent rect should have widened, have %+v", agentRect)
	}

	// ctrl+q in chrome mode is now a no-op: quitting is its own key.
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	if cmd != nil {
		t.Fatal("ctrl+q in chrome mode should be a no-op")
	}
	// Q in chrome mode quits.
	_, cmd = a.Update(tea.KeyPressMsg{Code: 'Q'})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("Q in chrome mode should quit")
	}
	_ = a
}

func TestRespawnAndDeath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A main pane that exits instantly exercises the respawn loop.
	crashing := pane.Spec{Command: "/bin/sh", Args: []string{"-c", "printf CRASH; true"}}
	specs := testSpecs()
	specs[PaneMain] = crashing

	panes := make([]pane.Pane, len(specs))
	for i, spec := range specs {
		p, err := pane.Spawn(ctx, spec, 40, 10)
		if err != nil {
			t.Fatal(err)
		}
		panes[i] = p
		t.Cleanup(p.Close)
	}
	cwd := filepath.Join(t.TempDir(), "chess")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	app := New(panes, specs, cwd, theme.Everforest, settings.Default())
	model, _ := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a := model.(App)

	// Feed the first exit: the pane respawns (fresh ID), not dead.
	firstID := panes[PaneMain].ID()
	msg := panes[PaneMain].ExitCmd()()
	if exit, ok := msg.(pane.ExitMsg); ok {
		model, _ := a.Update(exit)
		a = model.(App)
	} else {
		t.Fatalf("ExitCmd returned %T, want pane.ExitMsg", msg)
	}
	if a.dead[PaneMain] {
		t.Error("first exit should respawn, not mark dead")
	}
	if a.panes[PaneMain].ID() == firstID {
		t.Error("respawned pane should have a fresh ID")
	}

	// The respawned instance also exits instantly: within the window,
	// so the slot goes dead instead of looping forever.
	msg = a.panes[PaneMain].ExitCmd()()
	exit, ok := msg.(pane.ExitMsg)
	if !ok {
		t.Fatalf("ExitCmd returned %T, want pane.ExitMsg", msg)
	}
	model, _ = a.Update(exit)
	a = model.(App)
	if !a.dead[PaneMain] {
		t.Error("instant re-exit should mark the pane dead")
	}
	if !strings.Contains(a.View().Content, "exited") {
		t.Error("dead pane should render its exit state")
	}
	if !strings.Contains(a.statusBar(), "✕") {
		t.Error("status bar should mark the dead pane")
	}
}

func TestHidePanes(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "MAIN-MARK", "TERM-MARK", "AGENT-MARK")

	// release to chrome mode, then 't' hides the terminal pane
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, cmd := a.Update(tea.KeyPressMsg{Code: 't'})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if !a.hidden[PaneTerm] {
		t.Fatal("t in chrome mode should hide the terminal pane")
	}
	view := stripANSI(a.View().Content)
	if strings.Contains(view, "TERM-MARK") {
		t.Error("hidden terminal should not render")
	}
	for _, want := range []string{"MAIN-MARK", "AGENT-MARK", "◇ sh"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q after hiding terminal", want)
		}
	}
	if !a.panes[PaneTerm].Alive() {
		t.Error("hidden pane's process should keep running")
	}

	// 't' again restores it
	model, cmd = a.Update(tea.KeyPressMsg{Code: 't'})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if a.hidden[PaneTerm] || !strings.Contains(stripANSI(a.View().Content), "TERM-MARK") {
		t.Error("second t should restore the terminal pane")
	}

	// 'a' hides the agent; with both hidden only main remains
	model, _ = a.Update(tea.KeyPressMsg{Code: 'a'})
	a = model.(App)
	model, cmd = a.Update(tea.KeyPressMsg{Code: 't'})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	view = stripANSI(a.View().Content)
	if strings.Contains(view, "TERM-MARK") || strings.Contains(view, "AGENT-MARK") {
		t.Error("both panes should be hidden now")
	}
	if !strings.Contains(view, "MAIN-MARK") {
		t.Error("main should still render fullscreen")
	}
	main := a.rects[PaneMain]
	if main.W != 120 || main.H != a.bodyHeight() {
		t.Errorf("main should be fullscreen, have %+v (body %d)", main, a.bodyHeight())
	}

	// restore everything
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'a'})
	a = model.(App)
	model, cmd = a.Update(tea.KeyPressMsg{Code: 't'})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	a.waitView(t, "MAIN-MARK", "TERM-MARK", "AGENT-MARK")

	// hiding the focused pane releases focus to chrome mode
	a.setFocus(PaneTerm)
	a.togglePane(PaneTerm)
	if a.focus != -1 {
		t.Error("hiding the focused pane should release focus")
	}
}

func TestPicker(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "MAIN-MARK", "TERM-MARK", "AGENT-MARK")

	// release to chrome, open the picker with 's'
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 's'})
	a = model.(App)
	if !a.pickerOpen {
		t.Fatal("s in chrome mode should open the agent picker")
	}
	view := stripANSI(a.View().Content)
	for _, want := range []string{"agent", "opencode", "pi", "shell", "j/k select"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker view missing %q", want)
		}
	}
	// entries are sorted; opencode is selected first
	if !strings.Contains(view, "▸ opencode") {
		t.Error("first entry should be selected")
	}

	// j moves the selection to pi
	model, _ = a.Update(tea.KeyPressMsg{Code: 'j'})
	a = model.(App)
	if !strings.Contains(stripANSI(a.View().Content), "▸ pi") {
		t.Error("j should move the selection to pi")
	}

	// esc closes without swapping
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = model.(App)
	if a.pickerOpen {
		t.Error("esc should close the picker")
	}
	view = stripANSI(a.View().Content)
	if strings.Contains(view, "j/k select") {
		t.Error("picker should be gone from the view")
	}

	// same-command selection is a no-op: pick the current agent
	a.pickerOpen = true
	a.pick = picker{entries: []agents2.Agent{{Name: "same", Command: a.specs[PaneAgent].Command}}, selected: 0}
	oldID := a.panes[PaneAgent].ID()
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = model.(App)
	if a.pickerOpen {
		t.Error("enter should close the picker")
	}
	if a.panes[PaneAgent].ID() != oldID {
		t.Error("same-agent selection must not swap the pane")
	}
}

func TestSwapAgent(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "AGENT-MARK")

	oldID := a.panes[PaneAgent].ID()
	cmd := a.swapAgent(agents2.Agent{
		Name:    "marker",
		Command: "/bin/sh",
		Args:    []string{"-c", "printf SWAP-MARK; sleep 60"},
	})
	if cmd != nil {
		cmd()
	}
	if a.panes[PaneAgent].ID() == oldID {
		t.Error("swapAgent should replace the agent pane")
	}
	if a.specs[PaneAgent].Command != "/bin/sh" {
		t.Errorf("agent spec not updated: %+v", a.specs[PaneAgent])
	}
	if !a.panes[PaneAgent].Focused() {
		t.Error("swapAgent should focus the agent pane")
	}
	if a.hidden[PaneAgent] || a.dead[PaneAgent] {
		t.Error("swapAgent should unhide and revive the agent pane")
	}

	// the swapped pane renders its marker
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(a.panes[PaneAgent].View(), "SWAP-MARK") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("swapped agent never rendered its marker")
}

func TestQuitKeys(t *testing.T) {
	a := newTestApp(t)

	// ctrl+shift+q quits from a focused pane, from anywhere.
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl | tea.ModShift})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("ctrl+shift+q should quit from a focused pane")
	}

	// Q in kitty form (code q + shift) also quits in chrome mode.
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}) // release
	a = model.(App)
	_, cmd = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModShift})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("shift+q should quit in chrome mode")
	}

	// ctrl+q alone never quits anymore: release, then a no-op.
	model, _ = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	if a.focus != -1 {
		t.Fatal("ctrl+q should release to chrome mode")
	}
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("ctrl+q in chrome mode should be a no-op")
	}
	a = model.(App)
	if a.focus != -1 {
		t.Fatal("ctrl+q no-op must not refocus")
	}

	// the quit chord also works while the picker is open.
	model, _ = a.Update(tea.KeyPressMsg{Code: 's'})
	a = model.(App)
	if !a.pickerOpen {
		t.Fatal("s should open the picker")
	}
	_, cmd = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl | tea.ModShift})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("ctrl+shift+q should quit through the picker")
	}

	// plain q in the picker closes it instead of quitting
	a.pickerOpen = true
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("ctrl+q should close the picker without quitting")
	}
	a = model.(App)
	if a.pickerOpen {
		t.Fatal("ctrl+q should have closed the picker")
	}
}

// gitRepo builds a temp git repository with an unstaged change and an
// untracked file, for git-view tests.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "chess")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("new note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGitView(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	repo := gitRepo(t)
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

	// chrome mode, then 'g' opens the fullscreen git view
	model, _ = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'g'})
	a = model.(App)
	if !a.gitMode {
		t.Fatal("g in chrome mode should open the git view")
	}
	view := stripANSI(a.View().Content)
	for _, want := range []string{
		"git · main", "unstaged", "untracked", "main.go", "notes.md", "+changed", "-base",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("git view missing %q", want)
		}
	}

	// j selects the next file; the diff column follows
	model, _ = a.Update(tea.KeyPressMsg{Code: 'j'})
	a = model.(App)
	view = stripANSI(a.View().Content)
	if !strings.Contains(view, "notes.md") || !strings.Contains(view, "+new note") {
		t.Errorf("j should select notes.md and show its diff, view:\n%s", view)
	}

	// r refreshes repository state
	if err := os.WriteFile(filepath.Join(repo, "extra.txt"), []byte("extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, _ = a.Update(tea.KeyPressMsg{Code: 'r'})
	a = model.(App)
	view = stripANSI(a.View().Content)
	if !strings.Contains(view, "extra.txt") {
		t.Error("r should reload the file list")
	}

	// esc closes the view; the panes never stopped running
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = model.(App)
	if a.gitMode {
		t.Error("esc should close the git view")
	}
	if !a.panes[PaneAgent].Alive() {
		t.Error("panes should keep running behind the git view")
	}

	// ctrl+q in git mode closes the view instead of quitting
	model, _ = a.Update(tea.KeyPressMsg{Code: 'g'})
	a = model.(App)
	model, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Error("ctrl+q in git mode should close the view, not quit")
	}
	a = model.(App)
	if a.gitMode {
		t.Error("ctrl+q should have closed the git view")
	}

	// Q still quits from the git view
	model, _ = a.Update(tea.KeyPressMsg{Code: 'g'})
	a = model.(App)
	_, cmd = a.Update(tea.KeyPressMsg{Code: 'Q'})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("Q should quit from the git view")
	}
}

// TestViewBeforeReady guards the pre-layout render: View must not touch
// pane rectangles before the first WindowSizeMsg populates them.
func TestViewBeforeReady(t *testing.T) {
	a := newTestApp(t)
	a.ready = false
	a.rects = nil
	if got := a.View().Content; !strings.Contains(stripANSI(got), "starting") {
		t.Fatalf("pre-ready view should say starting, got %q", stripANSI(got))
	}
}

// TestMouseModeAlwaysOn: jaed always takes mouse reporting so the host
// terminal never converts the wheel into arrow keys. A wheel over a
// non-tracking pane then scrolls that pane's own history.
func TestMouseModeAlwaysOn(t *testing.T) {
	a := newTestApp(t)
	if a.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("jaed should always request mouse events")
	}
	// A wheel over a non-tracking pane routes to its own history: a
	// scroll tick comes back (and never guest input).
	_, cmd := a.Update(tea.MouseWheelMsg{
		X: a.rects[PaneMain].X + 1, Y: a.rects[PaneMain].Y + 1, Button: tea.MouseWheelUp,
	})
	if cmd == nil {
		t.Fatal("wheel over a non-tracking pane should schedule a scrollback scroll")
	}
}

// TestMouseRouting: a wheel over a guest that enabled mouse tracking
// arrives as an SGR mouse event (so opencode scrolls its own view instead
// of walking prompt history).
func TestMouseRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	cwd := filepath.Join(t.TempDir(), "chess")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "in")

	specs := testSpecs()
	for i := range specs {
		specs[i].Dir = cwd
	}
	// The agent enables mouse tracking, then records raw stdin to a log
	// (raw mode, so bytes flush without a newline).
	specs[PaneAgent] = pane.Spec{
		Command: "/bin/sh",
		Args: []string{"-c",
			`printf '\x1b[?1000h\x1b[?1006h'; stty raw -echo; cat > "` + log + `"`},
		Dir: cwd,
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
	app := New(panes, specs, cwd, theme.Everforest, settings.Default())
	model, cmd := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a := model.(App)
	if cmd != nil {
		cmd()
	}

	// Wait for the agent's mouse modes to reach the watcher.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !a.panes[PaneAgent].MouseTracking() {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.panes[PaneAgent].MouseTracking() {
		t.Fatal("agent pane never reported mouse tracking")
	}
	if a.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse mode should be enabled when a visible guest wants the mouse")
	}

	// A wheel over the agent box is forwarded as SGR, pane-local coords.
	r := a.rects[PaneAgent]
	model, _ = a.Update(tea.MouseWheelMsg{X: r.X + 2, Y: r.Y + 3, Button: tea.MouseWheelUp})
	a = model.(App)

	want := []byte("\x1b[<64;2;3M")
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(log); err == nil && bytes.Contains(data, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(log)
	t.Fatalf("agent never received the SGR wheel event %q; log was %q", want, data)
}

func TestGitViewNotARepo(t *testing.T) {
	a := newTestApp(t) // temp dir, not a git repository
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'g'})
	a = model.(App)
	if !a.gitMode {
		t.Fatal("git view should still open")
	}
	if !strings.Contains(stripANSI(a.View().Content), "not a git repository") {
		t.Error("non-repository should explain itself")
	}
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = model.(App)
	if a.gitMode {
		t.Error("esc should close even outside a repository")
	}
}
