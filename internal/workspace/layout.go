package workspace

import "math"

// Pane slots in the panes slice.
const (
	PaneMain  = 0 // nvim, top-left
	PaneTerm  = 1 // terminal, bottom-left
	PaneAgent = 2 // agent, right column, full height
	PaneCount = 3
)

const (
	gap        = 1    // cells between panes
	resizeStep = 0.03 // fraction of the screen per resize keypress
	minFrac    = 0.08
	maxFrac    = 0.80
)

// Rect is a pane's outer rectangle, border included, in content-area
// coordinates.
type Rect struct {
	X, Y, W, H int
}

// Layout holds the resize ratios of the 3-pane grid.
type Layout struct {
	TermFrac  float64 // height fraction of the terminal pane
	AgentFrac float64 // width fraction of the agent column
}

// DefaultLayout: terminal takes the bottom 30% of the left column, the
// agent column takes 35% of the width.
func DefaultLayout() Layout {
	return Layout{TermFrac: 0.30, AgentFrac: 0.35}
}

// Split lays out the grid into pane rectangles:
//
//	┌──────────────┬──────────┐
//	│  main        │  agent   │
//	│  (nvim)      │  (pi)    │
//	├──────────────┤          │
//	│  terminal    │          │
//	└──────────────┴──────────┘
func (l Layout) Split(width, height int) (main, term, agent Rect) {
	agentW := clamp(int(math.Round(float64(width)*l.AgentFrac)), 10, width-gap-14)
	termH := clamp(int(math.Round(float64(height)*l.TermFrac)), 3, height-gap-6)
	leftW := max(width-agentW-gap, 14)
	mainH := max(height-termH-gap, 6)

	main = Rect{X: 0, Y: 0, W: leftW, H: mainH}
	term = Rect{X: 0, Y: mainH + gap, W: leftW, H: height - mainH - gap}
	agent = Rect{X: leftW + gap, Y: 0, W: width - leftW - gap, H: height}
	return
}

// SplitVisible lays out the grid for the visible panes. Hidden panes get
// zero rects: the renderer skips them and their processes keep running.
func (l Layout) SplitVisible(width, height int, showTerm, showAgent bool) (main, term, agent Rect) {
	switch {
	case showTerm && showAgent:
		return l.Split(width, height)

	case showAgent: // terminal hidden: main takes the full column
		main, _, agent = l.Split(width, height)
		main.H = height
		return main, Rect{}, agent

	case showTerm: // agent hidden: the left column takes the full width
		termH := clamp(int(math.Round(float64(height)*l.TermFrac)), 3, height-gap-6)
		mainH := max(height-termH-gap, 6)
		main = Rect{X: 0, Y: 0, W: width, H: mainH}
		term = Rect{X: 0, Y: mainH + gap, W: width, H: height - mainH - gap}
		return main, term, Rect{}

	default: // everything but main hidden: main fullscreen
		return Rect{X: 0, Y: 0, W: width, H: height}, Rect{}, Rect{}
	}
}

// Resize adjusts the ratios from a chrome-mode keypress:
// h/l narrow/widen the agent column, k/j shrink/grow the terminal pane.
// It reports whether the key was a resize.
func (l Layout) Resize(key string) (Layout, bool) {
	switch key {
	case "h":
		l.AgentFrac = clampFrac(l.AgentFrac - resizeStep)
	case "l":
		l.AgentFrac = clampFrac(l.AgentFrac + resizeStep)
	case "k":
		l.TermFrac = clampFrac(l.TermFrac - resizeStep)
	case "j":
		l.TermFrac = clampFrac(l.TermFrac + resizeStep)
	default:
		return l, false
	}
	return l, true
}

// Neighbor returns the pane reached by moving direction ("h", "j", "k",
// "l") from the given pane, or -1 when there is nothing that way.
func Neighbor(from int, direction string) int {
	switch from {
	case PaneMain:
		switch direction {
		case "j":
			return PaneTerm
		case "l":
			return PaneAgent
		}
	case PaneTerm:
		switch direction {
		case "k":
			return PaneMain
		case "l":
			return PaneAgent
		}
	case PaneAgent:
		switch direction {
		case "h", "k":
			return PaneMain // upper half of the agent column
		case "j":
			return PaneTerm // lower half
		}
	}
	return -1
}

func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func clampFrac(v float64) float64 {
	return math.Min(maxFrac, math.Max(minFrac, v))
}
