package workspace

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/pane"
)

// marker builds a shell spec that prints text and lingers, like testSpecs.
func markerSpec(text string) pane.Spec {
	return pane.Spec{
		Command:    "/bin/sh",
		Args:       []string{"-c", "printf " + text + "; sleep 60"},
		Background: "#2b3339",
	}
}

func TestTerminalTabs(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "MAIN-MARK", "TERM-MARK", "AGENT-MARK")

	// focus the terminal pane
	model, _ := a.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	a = model.(App)
	if a.focus != PaneTerm {
		t.Fatalf("ctrl+j should focus the terminal, focus=%d", a.focus)
	}
	if n := a.termTabCount(); n != 1 {
		t.Fatalf("a fresh workspace should have one terminal tab, have %d", n)
	}

	// new tabs run the terminal's spec; give them a distinct marker
	a.specs[PaneTerm] = markerSpec("TAB2-MARK")
	tab1 := a.panes[PaneTerm].ID()

	// ctrl+t opens and focuses a second tab
	model, cmd := a.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if n := a.termTabCount(); n != 2 {
		t.Fatalf("ctrl+t should add a tab, have %d", n)
	}
	if !a.panes[PaneTerm].Focused() {
		t.Error("the new tab should be focused")
	}
	if a.panes[PaneTerm].ID() == tab1 {
		t.Error("the new tab should be a fresh process")
	}
	a.waitView(t, "TAB2-MARK")
	if strings.Contains(stripANSI(a.View().Content), "TERM-MARK") {
		t.Error("the inactive tab must not render")
	}
	if !a.panes[PaneTerm].Alive() {
		t.Error("the new tab's process should be alive")
	}

	// the status bar reports the active tab, and the tab strip is drawn
	if sb := stripANSI(a.statusBar()); !strings.Contains(sb, "2/2") {
		t.Errorf("status bar should show tab 2/2, have %q", sb)
	}

	// ctrl+pgup switches back to the first tab; input follows the active tab
	model, cmd = a.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	a.waitView(t, "TERM-MARK")
	if strings.Contains(stripANSI(a.View().Content), "TAB2-MARK") {
		t.Error("switching should stop rendering the other tab")
	}

	// ctrl+shift+] returns to the second tab
	model, cmd = a.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl | tea.ModShift})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	a.waitView(t, "TAB2-MARK")

	// ctrl+w closes it and falls back to the surviving tab
	model, cmd = a.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl | tea.ModShift})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if n := a.termTabCount(); n != 1 {
		t.Fatalf("ctrl+w should close a tab, have %d", n)
	}
	a.waitView(t, "TERM-MARK")
}

func TestCloseLastTermTabHidesPane(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "TERM-MARK")
	a.setFocus(PaneTerm)

	model, cmd := a.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if !a.hidden[PaneTerm] {
		t.Error("closing the last tab should hide the terminal pane")
	}
	if a.focus != -1 {
		t.Errorf("focus should release to chrome mode, focus=%d", a.focus)
	}
	if n := a.termTabCount(); n != 1 {
		t.Errorf("a fresh shell should replace the closed last tab, have %d", n)
	}
	if !a.panes[PaneTerm].Alive() {
		t.Error("the replacement shell should be running")
	}

	// 't' from chrome mode shows the pane again
	model, cmd = a.Update(tea.KeyPressMsg{Code: 't'})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if a.hidden[PaneTerm] {
		t.Error("t should show the terminal pane again")
	}
}

func TestTabChordsIgnoreOtherPanes(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "MAIN-MARK")

	// ctrl+t opens a tab from anywhere, including the editor: jaed consumes
	// it before the guest can.
	if a.focus != PaneMain {
		t.Fatalf("expected the editor focused, focus=%d", a.focus)
	}
	a.specs[PaneTerm] = markerSpec("TAB2-MARK")
	model, cmd := a.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if n := a.termTabCount(); n != 2 {
		t.Fatalf("ctrl+t from the editor should open a tab, have %d", n)
	}
	if a.focus != PaneTerm {
		t.Errorf("the new tab should take focus, focus=%d", a.focus)
	}

	// But close and switch stay scoped to the terminal, so the editor keeps
	// its own ctrl+w (and ctrl+pgup/pgdn). Move focus back to the editor.
	a.setFocus(PaneMain)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	a = model.(App)
	if n := a.termTabCount(); n != 2 {
		t.Errorf("ctrl+w outside the terminal should not close a tab, have %d", n)
	}
}

func TestTabBarRenders(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "TERM-MARK")
	a.setFocus(PaneTerm)
	a.specs[PaneTerm] = markerSpec("TAB2-MARK")

	model, cmd := a.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if n := a.termTabCount(); n != 2 {
		t.Fatalf("expected two tabs, have %d", n)
	}

	rect := a.rects[PaneTerm]
	bar := a.tabBar(rect)
	if got := ansi.StringWidth(bar); got != rect.W {
		t.Errorf("tab bar width = %d, want %d", got, rect.W)
	}
	if !strings.Contains(stripANSI(bar), "sh") {
		t.Errorf("tab bar should name the tabs, have %q", stripANSI(bar))
	}
}

func TestInactiveTabRespawn(t *testing.T) {
	a := newTestApp(t)
	a.waitView(t, "TERM-MARK")

	// A second tab parked behind the first.
	a.specs[PaneTerm] = markerSpec("TAB2-MARK")
	a.setFocus(PaneTerm)
	model, cmd := a.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	model, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl})
	a = model.(App)
	if len(a.termTabs) != 1 {
		t.Fatalf("expected one parked tab, have %d", len(a.termTabs))
	}

	// An established inactive tab that exits respawns with a fresh ID.
	tab := &a.termTabs[0]
	tab.spawnedAt = time.Now().Add(-time.Minute)
	firstID := tab.pane.ID()
	tab.pane.Close()
	if cmd := a.respawnTermTab(0); cmd != nil {
		cmd()
	}
	if a.termTabs[0].dead {
		t.Error("an established inactive tab that exits should respawn, not die")
	}
	if a.termTabs[0].pane.ID() == firstID {
		t.Error("the respawned tab should have a fresh ID")
	}

	// A tab that dies within the window is crashing: it is marked dead.
	a.termTabs[0].spawnedAt = time.Now()
	a.termTabs[0].pane.Close()
	_ = a.respawnTermTab(0)
	if !a.termTabs[0].dead {
		t.Error("an instant re-exit should mark the tab dead")
	}
}
