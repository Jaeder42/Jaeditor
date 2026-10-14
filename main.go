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
	"jaed/internal/settings"
	"jaed/internal/theme"
	"jaed/internal/tools"
	"jaed/internal/workspace"
)

func main() {
	themeName := flag.String("theme", "everforest",
		"theme: "+strings.Join(theme.Names(), ", ")+", or auto (follows nvim)")
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

	// Bootstrap the personal .jaed folder for agent-authored tools: it
	// holds runnable scripts the agent can write and run from the chat,
	// and is excluded from version control. Best-effort — jaed runs
	// without it, just without the tools view's contents.
	_ = tools.EnsureLayout(absDir)

	// Persisted settings fill in for flags the user did not pass.
	cfg := settings.Load()
	passed := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { passed[f.Name] = true })
	if !passed["theme"] {
		themeName = &cfg.Theme
	}
	if !passed["a"] {
		agentName = &cfg.Agent
	}

	// "auto" follows the editor's colorscheme once nvim reports one (see
	// internal/workspace/link.go); everforest is the standby until then.
	var th theme.Theme
	if strings.EqualFold(*themeName, "auto") {
		th = theme.Everforest
	} else {
		t, ok := theme.Get(*themeName)
		if !ok {
			fatalf("jaed: unknown theme %q (have: %s, or auto)", *themeName, strings.Join(theme.Names(), ", "))
		}
		th = t
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

	editorArgs := []string{"."}
	if filepath.Base(*editorName) == "nvim" {
		// Report the active buffer via the terminal title (the chrome-mode
		// "c" key pushes it to the agent pane) and the colorscheme via the
		// theme file (auto mode follows it) — see internal/workspace/link.go.
		editorArgs = append(editorArgs, workspace.NvimTitleArgs()...)
		editorArgs = append(editorArgs, workspace.NvimThemeArgs(absDir)...)
	}
	specs := []pane.Spec{
		// "nvim ." opens the workspace folder's file listing; "." resolves
		// against Dir, the folder jaed was launched in.
		{Command: *editorName, Args: editorArgs, Dir: absDir, Background: th.Bg},   // main
		{Command: agents.Shell.Command, Dir: absDir, Background: th.Bg},            // terminal
		{Command: agent.Command, Args: agent.Args, Dir: absDir, Background: th.Bg}, // agent
	}
	panes := make([]pane.Pane, len(specs))
	for i, spec := range specs {
		p, err := pane.Spawn(context.Background(), spec, 80, 24)
		if err != nil {
			fatal(err)
		}
		panes[i] = p
	}

	app := workspace.New(panes, specs, absDir, th, cfg)
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
