package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jaed/internal/settings"
)

func TestSettingsPane(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	a := newTestApp(t)
	a.cfg = settings.Default()

	// release to chrome, open settings with 'S'
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'S'})
	a = model.(App)
	if !a.settingsOpen {
		t.Fatal("S in chrome mode should open the settings popup")
	}
	view := stripANSI(a.View().Content)
	for _, want := range []string{"settings", "theme", "agent", "everforest", "catppuccin", "pi", "opencode"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings popup missing %q", want)
		}
	}
	if !strings.Contains(view, "▸") {
		t.Error("a row should be selected")
	}

	// Themes sort alphabetically: catppuccin is the first option and is
	// already selected when the popup opens. Enter applies it live and
	// persists it.
	if !strings.Contains(stripANSI(a.View().Content), "▸ catppuccin") {
		t.Fatal("catppuccin should be the initially selected theme")
	}
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = model.(App)
	if a.theme.Name != "catppuccin" {
		t.Fatalf("enter should switch the theme live, have %q", a.theme.Name)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "jaed", "settings.json"))
	if err != nil {
		t.Fatal("settings were not persisted:", err)
	}
	if !strings.Contains(string(data), `"catppuccin"`) {
		t.Fatalf("persisted settings = %s, want catppuccin theme", data)
	}

	// the applied theme keeps marking itself as current once selection
	// moves off it
	model, _ = a.Update(tea.KeyPressMsg{Code: 'j'})
	a = model.(App)
	if !strings.Contains(stripANSI(a.View().Content), "● catppuccin") {
		t.Error("applied theme should be marked current")
	}

	// rows: [2..5]=themes, [7..9]=agents (opencode, pi, shell); six
	// j presses from everforest land on shell
	for range 6 {
		model, _ = a.Update(tea.KeyPressMsg{Code: 'j'})
		a = model.(App)
	}
	if !strings.Contains(stripANSI(a.View().Content), "▸ shell") {
		t.Fatal("six j presses should select the shell agent")
	}
	oldID := a.panes[PaneAgent].ID()
	model, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if a.panes[PaneAgent].ID() == oldID {
		t.Fatal("enter on an agent should swap the agent pane")
	}
	if a.specs[PaneAgent].Command == "" {
		t.Fatal("agent spec missing after swap")
	}
	data, _ = os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "jaed", "settings.json"))
	if !strings.Contains(string(data), `"shell"`) {
		t.Fatalf("persisted settings = %s, want shell agent", data)
	}

	// esc closes; a fresh app picks up the persisted defaults
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = model.(App)
	if a.settingsOpen {
		t.Fatal("esc should close the settings popup")
	}
	loaded := settings.Load()
	if loaded.Theme != "catppuccin" || loaded.Agent != "shell" {
		t.Fatalf("reloaded settings = %+v, want catppuccin/shell", loaded)
	}
}
