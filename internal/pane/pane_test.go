package pane

import (
	"context"
	"strings"
	"testing"
	"time"
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
// must open in the workspace folder, not wherever aw was built from.
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
