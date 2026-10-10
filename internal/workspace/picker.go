package workspace

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"aw/internal/agents"
	"aw/internal/theme"
)

// picker is the agent-swap popup: the registered agents with one
// highlighted. Opened from chrome mode with "s".
type picker struct {
	entries  []agents.Agent
	selected int
}

func newPicker(currentCommand string) picker {
	entries := agents.List()
	selected := 0
	for i, a := range entries {
		if a.Command == currentCommand {
			selected = i
		}
	}
	return picker{entries: entries, selected: selected}
}

func (p *picker) move(delta int) {
	if len(p.entries) == 0 {
		return
	}
	p.selected = ((p.selected+delta)%len(p.entries) + len(p.entries)) % len(p.entries)
}

// pickerRow is one line of the popup: text plus the style to render it in.
type pickerRow struct {
	text  string
	style lipgloss.Style
}

// render draws the popup as styled lines of identical visible width,
// ready for overlayCenter.
func (p picker) render(t theme.Theme) []string {
	surface := lipgloss.Color(t.Surface)
	base := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))
	header := lipgloss.NewStyle().
		Bold(true).
		Background(surface).
		Foreground(lipgloss.Color(t.Accent))
	selected := lipgloss.NewStyle().
		Background(lipgloss.Color(t.Accent)).
		Foreground(lipgloss.Color(t.Bg))

	rows := []pickerRow{{text: " agent ", style: header}}
	for i, a := range p.entries {
		if i == p.selected {
			rows = append(rows, pickerRow{text: "▸ " + a.Name + " ", style: selected})
		} else {
			rows = append(rows, pickerRow{text: "  " + a.Name, style: base})
		}
	}
	rows = append(rows, pickerRow{text: " j/k select · enter swap · esc close ", style: dim})

	width := 24
	for _, row := range rows {
		width = max(width, ansi.StringWidth(row.text))
	}

	lines := make([]string, len(rows))
	for i, row := range rows {
		lines[i] = row.style.Width(width).Render(row.text) + resetStyle
	}
	return lines
}
