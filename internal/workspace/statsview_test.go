package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jaed/internal/settings"
	"jaed/internal/stats"
)

func TestStatsBar(t *testing.T) {
	a := newTestApp(t) // settings.Default() has Stats on
	if a.topRows() != 1 {
		t.Fatal("the resource bar should be on by default")
	}
	if a.bodyHeight() != 38 {
		t.Errorf("bodyHeight = %d, want 38 (40 rows, minus status and resource bars)", a.bodyHeight())
	}

	a.usage = stats.Usage{CPUPercent: 12.6, MemUsed: 5 << 30, MemTotal: 8 << 30,
		NetRx: 1<<20 + 200<<10, NetTx: 300 << 10, NetOK: true}
	a.statsOK = true
	bar := stripANSI(a.statsBar())
	if !strings.Contains(bar, "cpu 13%") {
		t.Errorf("bar should round CPU: %q", bar)
	}
	if !strings.Contains(bar, "ram 5.0G/8.0G") {
		t.Errorf("bar should format memory: %q", bar)
	}
	if !strings.Contains(bar, "net ↓1.2M/s ↑300K/s") {
		t.Errorf("bar should format network rates: %q", bar)
	}
	if !strings.Contains(stripANSI(a.View().Content), "cpu 13%") {
		t.Error("the view should draw the resource bar")
	}

	// Before the first sample the values are placeholders, and the network
	// segment stays a placeholder until the first rate delta exists.
	a.statsOK = false
	if !strings.Contains(stripANSI(a.statsBar()), "cpu —") {
		t.Error("unsampled stats should render placeholders")
	}
	a.statsOK = true
	a.usage.NetOK = false
	if !strings.Contains(stripANSI(a.statsBar()), "net —") {
		t.Error("a sample without a network delta should still show a placeholder")
	}
}

func TestStatsToggle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := newTestApp(t)
	if !a.cfg.Stats {
		t.Fatal("stats should default on")
	}

	// Open settings in chrome mode and select the cpu/ram row.
	model, _ := a.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	a = model.(App)
	model, _ = a.Update(tea.KeyPressMsg{Code: 'S'})
	a = model.(App)

	sel := -1
	for i, row := range a.settingsRows() {
		if strings.Contains(row.text, "cpu/ram") {
			sel = i
		}
	}
	if sel < 0 {
		t.Fatal("settings should offer a cpu/ram row")
	}
	a.settingsSel = sel

	model, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if a.cfg.Stats {
		t.Error("enter should toggle the resource bar off")
	}
	if a.topRows() != 0 || a.bodyHeight() != 39 {
		t.Errorf("bar off: topRows=%d bodyHeight=%d, want 0/39", a.topRows(), a.bodyHeight())
	}
	if settings.Load().Stats {
		t.Error("toggling stats off should persist")
	}
	if strings.Contains(stripANSI(a.View().Content), " sys ") {
		t.Error("the resource bar should be gone from the view")
	}

	// Toggling back on restores the row.
	a.settingsSel = sel
	model, cmd = a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = model.(App)
	if cmd != nil {
		cmd()
	}
	if !a.cfg.Stats {
		t.Error("enter should toggle the resource bar back on")
	}
	if a.topRows() != 1 {
		t.Error("the resource bar should occupy a row again")
	}
}
