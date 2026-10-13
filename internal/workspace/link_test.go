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
	app := New(panes, specs, cwd, theme.Everforest)
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
	app := New(panes, specs, cwd, theme.Everforest)

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
