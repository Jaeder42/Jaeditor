package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"jaed/internal/pane"
	"jaed/internal/settings"
	"jaed/internal/theme"
)

func TestPushEditorContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	cwd := filepath.Join(t.TempDir(), "chess")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	specs := testSpecs()
	for i := range specs {
		specs[i].Dir = cwd
	}
	// the agent pane runs cat, so typed input echoes into its view
	specs[PaneAgent] = pane.Spec{Command: "cat", Dir: cwd, Background: "#2b3339"}

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
	model, _ := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a := model.(App)
	a.bufferQuery = func() (string, error) { return "/tmp/chess/main.go", nil }

	// release to chrome, then 'c'
	model, _ = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, cmd := a.Update(tea.KeyPressMsg{Code: 'c'})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if !a.panes[PaneAgent].Focused() || a.focus != PaneAgent {
		t.Fatal("c should focus the agent pane")
	}

	// the @-mention landed in the agent's input
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(a.panes[PaneAgent].View(), "@/tmp/chess/main.go") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(a.panes[PaneAgent].View(), "@/tmp/chess/main.go") {
		t.Fatal("agent pane never received the @-mention")
	}

	// a hidden agent pane is unhidden by the push
	a.hidden[PaneAgent] = true
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'c'})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if a.hidden[PaneAgent] {
		t.Fatal("c should unhide the agent pane")
	}

	// no file open: focus still moves, nothing typed
	a.setFocus(-1)
	a.bufferQuery = func() (string, error) { return "", nil }
	model, _ = a.Update(tea.KeyPressMsg{Code: 'c'})
	a = model.(App)
	if a.focus != PaneAgent {
		t.Fatal("c should focus the agent even without a file")
	}

	// a dead agent pane: nothing happens
	a.dead[PaneAgent] = true
	a.setFocus(PaneMain)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'c'})
	a = model.(App)
	if a.focus == PaneAgent {
		t.Fatal("c should not focus a dead agent pane")
	}
}

// TestEditorBufferTitleIntegration runs a real nvim in a real pane —
// with jaed's title args — and reads the buffer path back out of the
// captured OSC title. --clean keeps the test independent of the user's
// config.
func TestEditorBufferTitleIntegration(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("nvim not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spec := pane.Spec{
		Command: "nvim",
		Args:    append([]string{"--clean", path}, NvimTitleArgs()...),
	}
	p, err := pane.Spawn(ctx, spec, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// expand("%:p") resolves symlinks (/var -> /private/var on macOS),
	// so compare against the resolved path.
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := p.GuestTitle(); got == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("title never reported the buffer; last title %q", p.GuestTitle())
}

func TestActiveFilePath(t *testing.T) {
	// deterministic
	if activeFilePath("/a/b") != activeFilePath("/a/b") {
		t.Fatal("activeFilePath must be deterministic")
	}
	// symlink resolution: /tmp -> /private/tmp on macOS must not fork
	// the path (pi's process.cwd() sees the resolved form)
	if activeFilePath("/tmp") != activeFilePath("/private/tmp") {
		t.Fatal("symlinks must not split the live-link path")
	}
	// the extension hashes with sha256 hex, first 16 chars
	resolved, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(resolved))
	want := filepath.Join(os.TempDir(), fmt.Sprintf("jaed-active-%x", sum[:8]))
	if activeFilePath("/tmp") != want {
		t.Fatalf("path = %q, want %q", activeFilePath("/tmp"), want)
	}
}

func TestSyncActiveFileLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	cwd := filepath.Join(t.TempDir(), "chess")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// the "editor" is an interactive shell: it announces buffers via
	// OSC titles, exactly like jaed's nvim does
	specs := testSpecs()
	specs[PaneMain] = pane.Spec{Command: "/bin/sh", Dir: cwd, Background: "#2b3339"}
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

	waitTitle := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if panes[PaneMain].GuestTitle() == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("title never became %q; last %q", want, panes[PaneMain].GuestTitle())
	}

	// open a "buffer": the link file must carry its path after an Update
	_ = panes[PaneMain].Write([]byte(`printf '\x1b]2;/tmp/hello.go\x07'` + "\r"))
	waitTitle("/tmp/hello.go")
	model, _ := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a := model.(App)

	data, err := os.ReadFile(activeFilePath(cwd))
	if err != nil {
		t.Fatalf("live-link file missing: %v", err)
	}
	if string(data) != "/tmp/hello.go" {
		t.Fatalf("live-link file = %q, want the buffer path", data)
	}

	// a non-path title clears the link, live
	_ = panes[PaneMain].Write([]byte(`printf '\x1b]2;dashboard\x07'` + "\r"))
	waitTitle("dashboard")
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if data, _ = os.ReadFile(activeFilePath(cwd)); len(data) != 0 {
		t.Fatalf("link should be empty for a non-path title, got %q", data)
	}
}

func TestThemeFilePath(t *testing.T) {
	// deterministic
	if themeFilePath("/a/b") != themeFilePath("/a/b") {
		t.Fatal("themeFilePath must be deterministic")
	}
	// symlink resolution must not fork the path (nvim gets the resolved
	// form baked into its args)
	if themeFilePath("/tmp") != themeFilePath("/private/tmp") {
		t.Fatal("symlinks must not split the theme-file path")
	}
	resolved, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(resolved))
	want := filepath.Join(os.TempDir(), fmt.Sprintf("jaed-theme-%x", sum[:8]))
	if themeFilePath("/tmp") != want {
		t.Fatalf("path = %q, want %q", themeFilePath("/tmp"), want)
	}
	// and it must not collide with the active-file link
	if themeFilePath("/tmp") == activeFilePath("/tmp") {
		t.Fatal("theme and active files must be distinct")
	}
}

func TestThemeForColorscheme(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" means no mapping
	}{
		{"everforest", "everforest"},
		{"Everforest", "everforest"}, // case-insensitive
		{" gruvbox\n", "gruvbox"},    // writefile adds a newline
		{"gruvbox-material", "gruvbox"},
		{"catppuccin-mocha", "catppuccin"},
		{"catppuccin_latte", "catppuccin"},
		{"tokyonight", "tokyonight"},
		{"TokyoNight-Storm", "tokyonight"},
		{"vim", ""},
		{"habamax", ""},
		{"", ""},
		{"gruvboxish", ""}, // a bare prefix must not false-positive
	}
	for _, tc := range cases {
		th, ok := themeForColorscheme(tc.in)
		if tc.want == "" {
			if ok {
				t.Errorf("%q should not map, got %q", tc.in, th.Name)
			}
			continue
		}
		if !ok || th.Name != tc.want {
			t.Errorf("%q should map to %q, got %q (ok=%v)", tc.in, tc.want, th.Name, ok)
		}
	}
}

func TestNvimThemeArgs(t *testing.T) {
	args := NvimThemeArgs("/tmp")
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, themeFilePath("/tmp")) {
		t.Fatalf("args should embed the theme file path, got %v", args)
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "+") {
			t.Errorf("args must be +commands, got %q", arg)
		}
	}
}

// TestAutoThemeFollowsNvim: in auto mode the workspace follows the
// editor's colorscheme reports — mapped schemes apply, unmapped ones
// leave the palette alone, and a workspace with a chosen theme ignores
// the reports entirely.
func TestAutoThemeFollowsNvim(t *testing.T) {
	a := newTestApp(t)
	a.cfg = settings.Settings{Theme: "auto", Agent: "pi", Stats: true}
	if a.themeFile != themeFilePath(a.cwd) {
		t.Fatalf("theme file = %q, want %q", a.themeFile, themeFilePath(a.cwd))
	}

	report := func(scheme string) {
		t.Helper()
		time.Sleep(2 * time.Millisecond) // distinct mtimes between writes
		if err := os.WriteFile(a.themeFile, []byte(scheme), 0o644); err != nil {
			t.Fatal(err)
		}
		model, cmd := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		a = model.(App)
		if cmd != nil {
			cmd()
		}
	}

	// a mapped scheme applies on the next Update, and the status bar
	// shows the auto label
	report("gruvbox-material\n")
	if a.theme.Name != "gruvbox" {
		t.Fatalf("gruvbox-material should map to gruvbox, have %q", a.theme.Name)
	}
	if sb := stripANSI(a.statusBar()); !strings.Contains(sb, "auto:gruvbox") {
		t.Errorf("status bar should show the auto label, have %q", sb)
	}

	// variants map too, and later switches keep following
	report("TokyoNight_Storm")
	if a.theme.Name != "tokyonight" {
		t.Fatalf("tokyonight_storm should map to tokyonight, have %q", a.theme.Name)
	}
	report("catppuccin")
	if a.theme.Name != "catppuccin" {
		t.Fatalf("auto should follow a later switch too, have %q", a.theme.Name)
	}

	// an unmapped scheme leaves the palette alone
	report("habamax")
	if a.theme.Name != "catppuccin" {
		t.Fatalf("an unmapped scheme should leave the theme alone, have %q", a.theme.Name)
	}

	// a workspace with a chosen theme ignores the reports entirely
	b := newTestApp(t)
	if err := os.WriteFile(b.themeFile, []byte("catppuccin"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, cmd := b.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	b = model.(App)
	if cmd != nil {
		cmd()
	}
	if b.theme.Name != "everforest" {
		t.Fatalf("a chosen theme must ignore the editor report, have %q", b.theme.Name)
	}
}

// TestNvimThemeArgsIntegration runs a real nvim — with jaed's theme
// args — and checks that a colorscheme switch lands in the theme file.
// --clean keeps the test independent of the user's config.
func TestNvimThemeArgsIntegration(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("nvim not installed")
	}
	cwd := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spec := pane.Spec{
		Command: "nvim",
		Args:    append([]string{"--clean"}, NvimThemeArgs(cwd)...),
		Dir:     cwd,
	}
	p, err := pane.Spawn(ctx, spec, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// :colorscheme blue fires the ColorScheme autocmd; the report file
	// must carry the new name
	_ = p.Write([]byte(":colorscheme blue\r"))
	want := "blue"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(themeFilePath(cwd)); err == nil && strings.TrimSpace(string(data)) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, _ := os.ReadFile(themeFilePath(cwd))
	t.Fatalf("theme file never reported %q; last content %q", want, data)
}

func TestAgentStateFilePath(t *testing.T) {
	// deterministic
	if agentStateFilePath("/a/b") != agentStateFilePath("/a/b") {
		t.Fatal("agentStateFilePath must be deterministic")
	}
	// symlink resolution must not fork the path (the extension hashes
	// process.cwd(), the resolved form)
	if agentStateFilePath("/tmp") != agentStateFilePath("/private/tmp") {
		t.Fatal("symlinks must not split the state-file path")
	}
	// and it must be distinct from the other live-link files
	if agentStateFilePath("/tmp") == activeFilePath("/tmp") ||
		agentStateFilePath("/tmp") == themeFilePath("/tmp") {
		t.Fatal("the state file must not collide with the other links")
	}
}

// TestAgentStateChannel: the agent publishes its session state to the
// state file; the status bar shows the model and a running marker, a
// malformed report never clobbers a good one, and a dead agent pane
// renders no state at all.
func TestAgentStateChannel(t *testing.T) {
	a := newTestApp(t)
	if a.agentStateFile != agentStateFilePath(a.cwd) {
		t.Fatalf("state file = %q, want %q", a.agentStateFile, agentStateFilePath(a.cwd))
	}

	report := func(content string) {
		t.Helper()
		time.Sleep(2 * time.Millisecond) // distinct mtimes between writes
		if err := os.WriteFile(a.agentStateFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		model, cmd := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		a = model.(App)
		if cmd != nil {
			cmd()
		}
	}

	// an idle report shows the model next to the agent label
	report(`{"status":"idle","model":"opus"}`)
	sb := stripANSI(a.statusBar())
	if !strings.Contains(sb, "opus") {
		t.Fatalf("status bar should show the agent model, have %q", sb)
	}
	if strings.Contains(sb, "running") {
		t.Fatalf("an idle agent must not show the running marker, have %q", sb)
	}

	// a running report adds the marker
	report(`{"status":"running","model":"opus"}`)
	if sb = stripANSI(a.statusBar()); !strings.Contains(sb, "running") {
		t.Fatalf("a working agent should show the running marker, have %q", sb)
	}

	// an activity replaces the generic running marker with the tool name
	report(`{"status":"running","model":"opus","activity":"read"}`)
	sb = stripANSI(a.statusBar())
	if !strings.Contains(sb, "read") || strings.Contains(sb, "running") {
		t.Fatalf("an activity should replace the running marker, have %q", sb)
	}

	// a retry shows its own marker
	report(`{"status":"retry","model":"opus"}`)
	if sb = stripANSI(a.statusBar()); !strings.Contains(sb, "retrying") {
		t.Fatalf("a retrying agent should show the retry marker, have %q", sb)
	}

	// context usage renders as a percentage
	report(`{"status":"running","model":"opus","context":42}`)
	if sb = stripANSI(a.statusBar()); !strings.Contains(sb, "42%") {
		t.Fatalf("context usage should render as a percentage, have %q", sb)
	}

	// settling clears it again
	report(`{"status":"idle","model":"opus"}`)
	if sb = stripANSI(a.statusBar()); strings.Contains(sb, "running") {
		t.Fatalf("a settled agent must not show the running marker, have %q", sb)
	}

	// a malformed report never clobbers the last good one
	report(`{"status":"running",`)
	if a.agentState.Model != "opus" || a.agentState.Status != "idle" {
		t.Fatalf("a malformed report clobbered the state: %+v", a.agentState)
	}

	// a dead agent pane renders no state
	a.dead[PaneAgent] = true
	if sb = stripANSI(a.statusBar()); strings.Contains(sb, "opus") {
		t.Fatalf("a dead agent pane should show no state, have %q", sb)
	}
}
