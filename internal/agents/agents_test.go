package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.conf")
	conf := "# a custom agent\nclaude = /usr/local/bin/claude --model opus\n\n# another\ncrush = crush\n"
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadUser(path); err != nil {
		t.Fatal(err)
	}

	a, err := Get("CLAUDE") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if a.Command != "/usr/local/bin/claude" {
		t.Errorf("command = %q", a.Command)
	}
	if strings.Join(a.Args, " ") != "--model opus" {
		t.Errorf("args = %v", a.Args)
	}

	names := strings.Join(Names(), ",")
	for _, want := range []string{"claude", "crush", "opencode", "pi", "shell"} {
		if !strings.Contains(names, want) {
			t.Errorf("names %q missing %q", names, want)
		}
	}

	list := List()
	if len(list) < 5 {
		t.Errorf("List() = %v, want at least 5 entries", list)
	}
}

func TestLoadUserMissingFileOK(t *testing.T) {
	if err := LoadUser(filepath.Join(t.TempDir(), "nope.conf")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUserBadLines(t *testing.T) {
	for name, conf := range map[string]string{
		"missing equals": "claude claude",
		"reserved name":  "shell = something",
		"empty command":  "x =",
	} {
		path := filepath.Join(t.TempDir(), "agents.conf")
		if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := LoadUser(path); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
