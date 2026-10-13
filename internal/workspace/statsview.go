package workspace

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/stats"
)

// The top resource bar: whole-machine CPU and memory, shown above the grid
// when enabled in settings (see statsBar). Sampling shells out on macOS, so
// it runs off the update loop on a timer and the bar is repainted with the
// latest sample.

// statsInterval is how often the resource bar resamples.
const statsInterval = 3 * time.Second

// statsSampleMsg carries one background resource sample.
type statsSampleMsg struct {
	usage stats.Usage
	ok    bool
}

// statsTickMsg asks for the next sample.
type statsTickMsg struct{}

// sampleStats reads one resource sample off the update loop.
func (a App) sampleStats() tea.Cmd {
	c := a.statsCollector
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		u, err := c.Sample()
		return statsSampleMsg{usage: u, ok: err == nil}
	}
}

// statsTick schedules the next resource sample.
func statsTick() tea.Cmd {
	return tea.Tick(statsInterval, func(time.Time) tea.Msg { return statsTickMsg{} })
}

// topRows is the number of rows the resource bar occupies (0 or 1).
func (a App) topRows() int {
	if a.cfg.Stats {
		return 1
	}
	return 0
}

// bodyHeight is the content height available to the grid and overlays:
// the screen minus the status bar and the resource bar.
func (a App) bodyHeight() int {
	return max(0, a.height-statusRows-a.topRows())
}

// statsBar renders the resource bar as one full-width line.
func (a App) statsBar() string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	value := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))

	var cpu, ram, net string
	if a.statsOK {
		cpu = value.Render(fmt.Sprintf("cpu %d%%", int(math.Round(a.usage.CPUPercent))))
		ram = value.Render("ram " + fmtBytes(a.usage.MemUsed) + "/" + fmtBytes(a.usage.MemTotal))
	} else {
		cpu = dim.Render("cpu —")
		ram = dim.Render("ram —")
	}
	if a.statsOK && a.usage.NetOK {
		net = value.Render("net ↓" + fmtRate(a.usage.NetRx) + " ↑" + fmtRate(a.usage.NetTx))
	} else {
		net = dim.Render("net —")
	}
	sep := dim.Render("  ·  ")
	content := dim.Render(" sys ") + cpu + sep + ram + sep + net
	if w := ansi.StringWidth(content); w < a.width {
		content += strings.Repeat(" ", a.width-w)
	}
	return lipgloss.NewStyle().Background(surface).Width(a.width).Render(content)
}

// fmtBytes renders a byte count as gibibytes with one decimal, e.g. 5.1G.
func fmtBytes(b uint64) string {
	return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
}

// fmtRate renders a bytes-per-second rate with a compact unit, e.g. 1.2M/s.
func fmtRate(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG/s", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fM/s", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0fK/s", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%dB/s", b)
	}
}
