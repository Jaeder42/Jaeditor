// jaed is an AI workspace TUI: an editor, a terminal, and a bring-your-own
// agent side panel, composed as embedded terminal panes in one grid.
//
// Usage: jaed [dir]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"jaed/internal/agents"
	"jaed/internal/pane"
	"jaed/internal/theme"
	"jaed/internal/workspace"
)

func main() {
	themeName := flag.String("theme", "everforest",
		"theme: "+strings.Join(theme.Names(), ", "))
	agentName := flag.String("a", "pi",
		"agent for the side panel: "+strings.Join(agents.Names(), ", "))
	editorName := flag.String("editor", "nvim", "command for the main pane")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [dir]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		fatal(err)
	}
	if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
		fatalf("jaed: not a directory: %s", dir)
	}

	th, ok := theme.Get(*themeName)
	if !ok {
		fatalf("jaed: unknown theme %q (have: %s)", *themeName, strings.Join(theme.Names(), ", "))
	}
	if home, err := os.UserHomeDir(); err == nil {
		if err := agents.LoadUser(filepath.Join(home, ".config", "jaed", "agents.conf")); err != nil {
			fatalf("jaed: %v", err)
		}
	}
	agent, err := agents.Get(*agentName)
	if err != nil {
		fatalf("jaed: %v", err)
	}
	for _, cmd := range []string{*editorName, agent.Command} {
		if _, err := exec.LookPath(cmd); err != nil {
			fatalf("jaed: %v", err)
		}
	}

	specs := []pane.Spec{
		// "nvim ." opens the workspace folder's file listing; "." resolves
		// against Dir, the folder jaed was launched in.
		{Command: *editorName, Args: []string{"."}, Dir: absDir, Background: th.Bg}, // main
		{Command: agents.Shell.Command, Dir: absDir, Background: th.Bg},             // terminal
		{Command: agent.Command, Args: agent.Args, Dir: absDir, Background: th.Bg},  // agent
	}
	panes := make([]pane.Pane, len(specs))
	for i, spec := range specs {
		p, err := pane.Spawn(context.Background(), spec, 80, 24)
		if err != nil {
			fatal(err)
		}
		panes[i] = p
	}

	app := workspace.New(panes, specs, absDir, th)
	if _, err := tea.NewProgram(app).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "jaed:", err)
		os.Exit(1)
	}
	for i := range panes {
		panes[i].Close() // teardown even on unusual exits
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "jaed:", err)
	os.Exit(1)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
