package workspace

import "testing"

func TestSplitGrid(t *testing.T) {
	l := DefaultLayout() // TermFrac 0.30, AgentFrac 0.35
	main, term, agent := l.Split(100, 40)

	if main.X != 0 || main.Y != 0 {
		t.Errorf("main should sit at the origin, got %+v", main)
	}
	if agent.X+agent.W != 100 {
		t.Errorf("agent should end at the right edge: %+v", agent)
	}
	if term.Y+term.H != 40 {
		t.Errorf("terminal should end at the bottom: %+v", term)
	}
	if main.W != term.W {
		t.Errorf("main and terminal should share a column: %d vs %d", main.W, term.W)
	}
	if agent.X != main.W+gap {
		t.Errorf("gap between columns should be %d: %+v vs %+v", gap, agent, main)
	}
	if term.Y != main.H+gap {
		t.Errorf("gap between rows should be %d: %+v vs %+v", gap, term, main)
	}
	// Fractions: 35% of 100 wide, 30% of 40 tall.
	if agent.W != 35 || term.H != 12 {
		t.Errorf("agent width = %d, terminal height = %d; want 35 and 12", agent.W, term.H)
	}
}

func TestSplitTinyScreen(t *testing.T) {
	l := DefaultLayout()
	main, term, agent := l.Split(20, 8)
	if main.W < 4 || main.H < 4 || agent.W < 4 || term.W < 4 {
		t.Fatalf("panes collapsed on a tiny screen: %+v %+v %+v", main, term, agent)
	}
}

func TestResizeClamps(t *testing.T) {
	l := DefaultLayout()
	for range 100 {
		l, _ = l.Resize("l") // agent wider
	}
	if _, ok := l.Resize("x"); ok {
		t.Error("x should not be a resize key")
	}
	if l.AgentFrac > maxFrac {
		t.Errorf("agent fraction not clamped: %v", l.AgentFrac)
	}
	for range 100 {
		l, _ = l.Resize("h") // agent narrower
	}
	if l.AgentFrac < minFrac {
		t.Errorf("agent fraction not clamped: %v", l.AgentFrac)
	}
}

func TestNeighborGraph(t *testing.T) {
	cases := []struct {
		from  int
		dir   string
		want  int
		exact bool
	}{
		{PaneMain, "j", PaneTerm, true},
		{PaneMain, "l", PaneAgent, true},
		{PaneMain, "h", -1, true},
		{PaneTerm, "k", PaneMain, true},
		{PaneTerm, "l", PaneAgent, true},
		{PaneAgent, "h", PaneMain, true},
		{PaneAgent, "j", PaneTerm, true},
		{PaneAgent, "l", -1, true},
	}
	for _, c := range cases {
		if got := Neighbor(c.from, c.dir); got != c.want {
			t.Errorf("Neighbor(%d, %q) = %d, want %d", c.from, c.dir, got, c.want)
		}
	}
}

func TestSplitVisible(t *testing.T) {
	l := DefaultLayout()

	// everything visible: same as Split
	main, term, agent := l.SplitVisible(100, 40, true, true)
	m2, t2, a2 := l.Split(100, 40)
	if main != m2 || term != t2 || agent != a2 {
		t.Errorf("all-visible should match Split: %+v %+v %+v", main, term, agent)
	}

	// terminal hidden: main grows to full column height, agent unchanged
	main, term, agent = l.SplitVisible(100, 40, false, true)
	if term != (Rect{}) {
		t.Errorf("hidden terminal should get a zero rect, have %+v", term)
	}
	if main.H != 40 {
		t.Errorf("main should fill the column, have %+v", main)
	}
	if agent.W != 35 {
		t.Errorf("agent should keep its width, have %+v", agent)
	}

	// agent hidden: left column fills the width
	main, term, agent = l.SplitVisible(100, 40, true, false)
	if agent != (Rect{}) {
		t.Errorf("hidden agent should get a zero rect, have %+v", agent)
	}
	if main.W != 100 || term.W != 100 {
		t.Errorf("left column should span the full width: %+v %+v", main, term)
	}
	if term.Y+term.H != 40 {
		t.Errorf("terminal should end at the bottom, have %+v", term)
	}

	// both hidden: main fullscreen
	main, term, agent = l.SplitVisible(100, 40, false, false)
	if main != (Rect{X: 0, Y: 0, W: 100, H: 40}) || term != (Rect{}) || agent != (Rect{}) {
		t.Errorf("main should be fullscreen: %+v %+v %+v", main, term, agent)
	}
}
