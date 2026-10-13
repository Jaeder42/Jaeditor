package pane

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// TestSpawnStreamsOutput proves the core path: a process runs on a raw
// PTY, its output reaches the emulator, and the pane renders it.
func TestSpawnStreamsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := Spawn(ctx, Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 0.2; printf hello-pane"},
	}, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	deadline := time.Now().Add(5 * time.Second)
	var view string
	for time.Now().Before(deadline) {
		view = p.View()
		if strings.Contains(view, "hello-pane") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("output never reached the view; view was:\n%s", view)
}

func TestPaneTitle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p, err := Spawn(ctx, Spec{Command: "/bin/sh"}, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Title() != "sh" {
		t.Fatalf("title = %q, want %q", p.Title(), "sh")
	}
	if !p.Alive() {
		t.Fatal("freshly spawned pane should be alive")
	}
}

// TestSpawnDir proves the child runs in the spec's directory — panes
// must open in the workspace folder, not wherever jaed was built from.
func TestSpawnDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := Spawn(ctx, Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", `printf %s "$PWD"`},
		Dir:     "/tmp",
	}, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	deadline := time.Now().Add(5 * time.Second)
	var view string
	for time.Now().Before(deadline) {
		view = p.View()
		if strings.Contains(view, "/tmp") {
			return // /tmp resolves to /private/tmp on macOS; both contain /tmp
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child did not run in /tmp; view was:\n%s", view)
}

func TestPasteBracketedWhenEnabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	log := filepath.Join(t.TempDir(), "pastelog")
	p, err := Spawn(ctx, Spec{
		// a guest that enables DECSET 2004, then cats stdin to a log
		Command: "/bin/sh",
		Args:    []string{"-c", `printf '\x1b[?2004h'; cat >"` + log + `"`},
	}, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// wait for the mode watcher to see the enable
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !p.BracketedPaste() {
		time.Sleep(20 * time.Millisecond)
	}
	if !p.BracketedPaste() {
		t.Fatal("watcher never saw DECSET 2004")
	}

	// trailing newline flushes the cooked-mode line discipline; the
	// close marker stays buffered until the guest reads again, so the
	// assertion targets the open marker and content
	if err := p.Paste("hello\nworld\n"); err != nil {
		t.Fatal(err)
	}
	assertLogContains(t, log, "\x1b[200~hello\nworld\n")
}

func TestPasteRawWhenNotEnabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	log := filepath.Join(t.TempDir(), "pastelog")
	p, err := Spawn(ctx, Spec{
		// a guest that never enables 2004
		Command: "/bin/sh",
		Args:    []string{"-c", `cat >"` + log + `"`},
	}, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.BracketedPaste() {
		t.Fatal("no DECSET 2004 was sent; pane must not report bracketed")
	}

	if err := p.Paste("plain text\n"); err != nil {
		t.Fatal(err)
	}
	assertLogContains(t, log, "plain text\n")
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("\x1b[200~")) {
		t.Fatal("raw paste must not carry bracketed markers")
	}
}

// TestScanModes: DECSET/DECRST sequences drive the guest mode set.
func TestScanModes(t *testing.T) {
	modes := &guestModes{}
	if rest := scanModes([]byte("noise\x1b[?2004h\x1b[?1000;1006h"), modes); len(rest) != 0 {
		t.Fatalf("complete sequences should be consumed, got %q", rest)
	}
	if !modes.BracketedPaste() || !modes.MouseTracking() || !modes.SGRMouse() {
		t.Fatalf("modes not all set: paste=%v mouse=%v sgr=%v",
			modes.BracketedPaste(), modes.MouseTracking(), modes.SGRMouse())
	}

	// DECRST 1000 clears tracking but must not clear the SGR encoding.
	scanModes([]byte("\x1b[?1000l"), modes)
	if modes.MouseTracking() {
		t.Fatal("DECRST 1000 should clear mouse tracking")
	}
	if !modes.SGRMouse() {
		t.Fatal("DECRST 1000 must not clear the SGR encoding (1006)")
	}

	// Bracketed paste can be reset independently.
	scanModes([]byte("\x1b[?2004l"), modes)
	if modes.BracketedPaste() {
		t.Fatal("DECRST 2004 should clear bracketed paste")
	}
}

// TestScanModesSplitChunks: a sequence split across PTY reads is held
// whole and applied once its terminator arrives.
func TestScanModesSplitChunks(t *testing.T) {
	modes := &guestModes{}
	carry := scanModes([]byte("\x1b[?10"), modes)
	if len(carry) == 0 {
		t.Fatal("a partial sequence should be held for the next chunk")
	}
	if modes.MouseTracking() {
		t.Fatal("an incomplete sequence must not apply")
	}
	scanModes(append(carry, []byte("02h")...), modes)
	if !modes.MouseTracking() {
		t.Fatal("split DECSET 1002 should apply once completed")
	}
}

// TestEncodeMouse pins the wire encoding for both protocols.
func TestEncodeMouse(t *testing.T) {
	up := encodeMouse(true, ansi.MouseWheelUp, false, false, false, false, false, 2, 1)
	if got, want := string(up), "\x1b[<64;3;2M"; got != want {
		t.Fatalf("SGR wheel up = %q, want %q", got, want)
	}
	down := encodeMouse(true, ansi.MouseWheelDown, false, false, false, false, false, 0, 0)
	if got, want := string(down), "\x1b[<65;1;1M"; got != want {
		t.Fatalf("SGR wheel down = %q, want %q", got, want)
	}
	release := encodeMouse(true, ansi.MouseLeft, false, true, false, false, false, 0, 0)
	if got, want := string(release), "\x1b[<0;1;1m"; got != want {
		t.Fatalf("SGR release = %q, want %q", got, want)
	}

	// Legacy X10: ESC [ M then 32+code, 32+x, 32+y.
	legacy := encodeMouse(false, ansi.MouseWheelUp, false, false, false, false, false, 0, 0)
	if len(legacy) != 6 || legacy[0] != 0x1b || legacy[1] != '[' || legacy[2] != 'M' {
		t.Fatalf("legacy encoding malformed: %q", legacy)
	}
	if legacy[3] != 32+64 || legacy[4] != 32+1 || legacy[5] != 32+1 {
		t.Fatalf("legacy body wrong: %v", legacy[3:])
	}
	if encodeMouse(false, ansi.MouseWheelUp, false, false, false, false, false, 300, 0) != nil {
		t.Fatal("legacy encoding cannot represent x > 223")
	}
}

func assertLogContains(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte(want)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("log %q never contained %q; log was %q", path, want, data)
}
