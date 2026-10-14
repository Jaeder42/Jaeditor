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

// batteryLowPercent is the charge at or below which the battery reads in the
// warn color (only while discharging).
const batteryLowPercent = 20

// statsBar renders the resource bar as one full-width line: machine resources
// on the left, the clock and battery on the right, both the same height.
func (a App) statsBar() string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	base := lipgloss.NewStyle().Background(surface)
	value := base.Foreground(lipgloss.Color(t.Fg))
	dim := base.Foreground(lipgloss.Color(t.Dim))
	warn := base.Foreground(lipgloss.Color(t.Warn))

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
	left := dim.Render(" sys ") + cpu + sep + ram + sep + net

	// The right cluster: the clock, then the battery — the level in the warn
	// color when it is low and off the charger, tagged "ac" while charging. A
	// desktop with no battery shows the clock alone.
	right := value.Render(time.Now().Format("15:04"))
	if a.statsOK && a.usage.BatteryOK {
		bat := value
		if !a.usage.BatteryCharging && a.usage.BatteryPercent <= batteryLowPercent {
			bat = warn
		}
		label := fmt.Sprintf("bat %d%%", a.usage.BatteryPercent)
		if a.usage.BatteryCharging {
			label += " ac"
		}
		right += sep + bat.Render(label)
	}

	// Each segment's Render ends in a reset, so the gap and any trailing
	// padding must be re-backgrounded explicitly or the surface drops out
	// from under the empty space.
	content := left
	if gap := a.width - ansi.StringWidth(left) - ansi.StringWidth(right); gap >= 1 {
		content += base.Render(strings.Repeat(" ", gap)) + right
	}
	if w := ansi.StringWidth(content); w < a.width {
		content += base.Render(strings.Repeat(" ", a.width-w))
	}
	return base.Width(a.width).Render(content)
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
