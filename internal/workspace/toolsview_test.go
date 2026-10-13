package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jaed/internal/tools"
)

// addTool writes a tool source into a workspace's .jaed/tools.
func addTool(t *testing.T, cwd, name, src string) {
	t.Helper()
	dir := tools.ToolsDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestToolsView(t *testing.T) {
	a := newTestApp(t)
	addTool(t, a.cwd, "list-todos.js",
		"/**\n * @description Count TODO comments in the workspace.\n */\n"+
			"export default async function run(input, jaed) {\n"+
			"  return \"TODO-MARK\";\n}\n")

	// chrome mode, then T opens the fullscreen tools view
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'T'})
	a = model.(App)
	if !a.toolsMode {
		t.Fatal("T in chrome mode should open the tools view")
	}
	if a.hidden[PaneTerm] {
		t.Error("T must not also hide the terminal pane")
	}
	view := stripANSI(a.View().Content)
	for _, want := range []string{
		"tools", "list-todos", "Count TODO comments", "export default", "TODO-MARK",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("tools view missing %q, view:\n%s", want, view)
		}
	}

	// the panes keep running behind it
	if !a.panes[PaneAgent].Alive() {
		t.Error("panes should keep running behind the tools view")
	}

	// T closes the view
	model, _ = a.Update(tea.KeyPressMsg{Code: 'T'})
	a = model.(App)
	if a.toolsMode {
		t.Error("T should close the tools view")
	}

	// the kitty shift+t form opens it too, and esc closes.
	model, _ = a.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModShift})
	a = model.(App)
	if !a.toolsMode {
		t.Fatal("shift+t should open the tools view")
	}
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = model.(App)
	if a.toolsMode {
		t.Error("esc should close the tools view")
	}

	// plain t still hides the terminal
	model, _ = a.Update(tea.KeyPressMsg{Code: 't'})
	a = model.(App)
	if !a.hidden[PaneTerm] {
		t.Error("plain t should still hide the terminal pane")
	}
}

func TestToolsViewEmpty(t *testing.T) {
	a := newTestApp(t) // no .jaed folder at all
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'T'})
	a = model.(App)
	if !a.toolsMode {
		t.Fatal("tools view should open even with no tools")
	}
	if !strings.Contains(stripANSI(a.View().Content), "no tools yet") {
		t.Error("empty tools view should say so")
	}
}
