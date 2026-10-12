// Package agents is the bring-your-own-agent registry: the commands that
// may run in the agent pane. Builtin entries plus user-defined agents
// from ~/.config/jaed/agents.conf ("name = command [args...]").
package agents

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Agent is a runnable workspace agent.
type Agent struct {
	Name    string
	Command string
	Args    []string
}

var builtin = map[string]Agent{
	"pi":       {Name: "pi", Command: "pi"},
	"opencode": {Name: "opencode", Command: "opencode"},
}

// custom holds user-defined agents loaded from the config file.
var custom = map[string]Agent{}

// Shell is the terminal pane's agent: the user's shell.
var Shell = Agent{Name: "shell", Command: shell()}

func shell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// Get returns an agent by name (case-insensitive). The special name
// "shell" resolves to the user's shell.
func Get(name string) (Agent, error) {
	if name == "shell" {
		return Shell, nil
	}
	key := normalize(name)
	if a, ok := builtin[key]; ok {
		return a, nil
	}
	if a, ok := custom[key]; ok {
		return a, nil
	}
	return Agent{}, fmt.Errorf("unknown agent %q (have: %s)", name, Names())
}

// List returns every registered agent — shell, builtin, and user-defined
// — sorted by name. The picker shows these.
func List() []Agent {
	names := []string{"shell"}
	for name := range builtin {
		names = append(names, name)
	}
	for name := range custom {
		names = append(names, name)
	}
	sort.Strings(names)
	list := make([]Agent, 0, len(names))
	for _, name := range names {
		a, err := Get(name)
		if err == nil {
			list = append(list, a)
		}
	}
	return list
}

// Names lists registered agent names, sorted.
func Names() []string {
	names := []string{"shell"}
	for name := range builtin {
		names = append(names, name)
	}
	for name := range custom {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LoadUser merges agents from a config file. Missing file is not an
// error. Lines: "# comment", "name = command [args...]".
func LoadUser(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, cmd, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected %q", path, i+1, "name = command [args...]")
		}
		name = strings.ToLower(strings.TrimSpace(name))
		fields := strings.Fields(cmd)
		if name == "" || len(fields) == 0 || strings.Contains(name, "/") {
			return fmt.Errorf("%s:%d: bad agent line %q", path, i+1, line)
		}
		if name == "shell" || name == "system" {
			return fmt.Errorf("%s:%d: %q is a reserved name", path, i+1, name)
		}
		custom[name] = Agent{Name: name, Command: fields[0], Args: fields[1:]}
	}
	return nil
}

func normalize(name string) string {
	lower := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower[i] = c
	}
	return string(lower)
}
