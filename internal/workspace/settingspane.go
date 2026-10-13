package workspace

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/agents"
	"jaed/internal/settings"
	"jaed/internal/theme"
)

// The settings popup: theme and default agent, opened from chrome mode
// with "S". j/k moves, enter applies (theme changes live, agent swaps
// the pane), esc closes. Every change persists to
// ~/.config/jaed/settings.json immediately.

// settingsRow is one row of the settings popup. Header rows organize the
// sections; selectable rows carry an apply action.
type settingsRow struct {
	text      string
	header    bool
	apply     func(a *App) tea.Cmd // what enter does, nil for headers
	agentName string               // for agent rows: what to persist
}

// settingsRows builds the popup's rows: theme options, then agent
// options, with the current selections marked.
func (a App) settingsRows() []settingsRow {
	rows := []settingsRow{
		{text: " settings ", header: true},
		{text: " theme ", header: true},
	}
	for _, name := range theme.Names() {
		label := "  " + name
		if name == a.theme.Name {
			label = "● " + name
		}
		themeName := name
		rows = append(rows, settingsRow{text: label, apply: func(app *App) tea.Cmd {
			if app.theme.Name == themeName {
				return nil
			}
			if th, ok := theme.Get(themeName); ok {
				app.theme = th
				app.cfg.Theme = themeName
				_ = settings.Save(app.cfg)
			}
			return nil
		}})
	}
	rows = append(rows, settingsRow{text: " agent ", header: true})
	for _, ag := range agents.List() {
		label := "  " + ag.Name
		if ag.Command == a.specs[PaneAgent].Command {
			label = "● " + ag.Name
		}
		agent := ag
		rows = append(rows, settingsRow{
			text:      label,
			agentName: agent.Name,
			apply: func(app *App) tea.Cmd {
				if app.specs[PaneAgent].Command == agent.Command {
					return nil
				}
				app.cfg.Agent = agent.Name
				_ = settings.Save(app.cfg)
				return app.swapAgent(agent)
			},
		})
	}
	rows = append(rows, settingsRow{text: " interface ", header: true})
	statsLabel := "  cpu/ram/net  off"
	if a.cfg.Stats {
		statsLabel = "● cpu/ram/net  on"
	}
	rows = append(rows, settingsRow{text: statsLabel, apply: func(app *App) tea.Cmd {
		app.cfg.Stats = !app.cfg.Stats
		_ = settings.Save(app.cfg)
		cmd := app.applyLayout()
		if app.cfg.Stats {
			return tea.Batch(app.sampleStats(), cmd)
		}
		return cmd
	}})
	rows = append(rows, settingsRow{text: " j/k move · enter apply · esc close ", header: true})
	return rows
}

// settingsMove shifts the selection to the next selectable row in the
// given direction.
func (a *App) settingsMove(delta int) {
	rows := a.settingsRows()
	dir := 1
	if delta < 0 {
		dir = -1
	}
	for next := a.settingsSel + dir; next >= 0 && next < len(rows); next += dir {
		if rows[next].apply != nil {
			a.settingsSel = next
			return
		}
	}
}

// settingsKey handles keys while the settings popup is open.
func (a *App) settingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := a.settingsRows()
	switch msg.Code {
	case 'j', tea.KeyDown:
		if msg.Mod == 0 {
			a.settingsMove(1)
		}
	case 'k', tea.KeyUp:
		if msg.Mod == 0 {
			a.settingsMove(-1)
		}
	case tea.KeyEnter:
		if row := rows[a.settingsSel]; row.apply != nil {
			return *a, row.apply(a)
		}
	case tea.KeyEscape:
		a.settingsOpen = false
	case 'Q', 'q':
		// real input reports shift+q as code 'q' with the shift
		// modifier; quit on either form.
		switch {
		case msg.Code == 'Q' && msg.Mod&tea.ModCtrl == 0,
			msg.Mod == tea.ModShift:
			a.closeAll()
			return *a, tea.Quit
		case msg.String() == "ctrl+q":
			a.settingsOpen = false
		}
	default:
		if msg.String() == "ctrl+q" {
			a.settingsOpen = false
		}
	}
	return *a, nil
}

// renderSettings draws the popup as styled lines of identical visible
// width, ready for overlayCenter.
func (a App) renderSettings() []string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	base := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))
	title := lipgloss.NewStyle().Bold(true).Background(surface).Foreground(lipgloss.Color(t.Accent))
	selected := lipgloss.NewStyle().Background(lipgloss.Color(t.Accent)).Foreground(lipgloss.Color(t.Bg))

	rows := a.settingsRows()
	width := 30
	for _, row := range rows {
		width = max(width, ansi.StringWidth(row.text)+2)
	}

	lines := make([]string, len(rows))
	for i, row := range rows {
		text, style := row.text, base
		switch {
		case i == 0:
			style = title
		case row.header:
			style = dim
		case i == a.settingsSel:
			style = selected
			text = "▸ " + strings.TrimSpace(strings.TrimPrefix(row.text, "●"))
		}
		lines[i] = style.Width(width).Render(text) + resetStyle
	}
	return lines
}
