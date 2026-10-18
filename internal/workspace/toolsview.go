package workspace

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jaed/internal/tools"
)

// toolsView is the fullscreen tools mode: the agent-authored scripts in
// .jaed/tools on the left, the selected script's source on the right.
// The panes keep running behind it. Read-only by design — the tools are
// for the agent to run, not the TUI.
type toolsView struct {
	entries  []tools.Tool
	err      string // why the tools folder could not be read, if it could not
	selected int
	scroll   int

	notice    string // transient result of the last action
	noticeErr bool
}

// setNotice records the outcome of an action for the code view's footer.
func (v *toolsView) setNotice(msg string, isErr bool) {
	v.notice, v.noticeErr = " "+msg+" ", isErr
}

// reload re-reads the tools folder.
func (v *toolsView) reload(cwd string) {
	list, err := tools.List(cwd)
	if err != nil {
		v.err = strings.TrimSuffix(err.Error(), "\n")
		v.entries, v.selected, v.scroll = nil, 0, 0
		return
	}
	v.err = ""
	v.entries = list
	if v.selected >= len(list) {
		v.selected, v.scroll = 0, 0
	}
}

// move shifts the tool selection, reporting whether it changed. The code
// pane scrolls back to the top of the new tool.
func (v *toolsView) move(delta int) bool {
	if len(v.entries) == 0 {
		return false
	}
	next := v.selected + delta
	if next < 0 || next >= len(v.entries) {
		return false
	}
	v.selected = next
	v.scroll = 0
	return true
}

// scrollBy shifts the code view, clamped to the source's height.
func (v *toolsView) scrollBy(delta, viewHeight int) {
	lines := v.selectedLines()
	if viewHeight < 1 || len(lines) <= viewHeight {
		v.scroll = 0
		return
	}
	v.scroll = clamp(v.scroll+delta, 0, len(lines)-viewHeight)
}

// selectedLines is the selected tool's source as lines, or nil.
func (v toolsView) selectedLines() []string {
	if v.selected < 0 || v.selected >= len(v.entries) {
		return nil
	}
	return strings.Split(v.entries[v.selected].Code, "\n")
}

// openToolsView enters the fullscreen tools mode and scans the folder.
func (a *App) openToolsView() {
	a.setFocus(-1)
	a.sel.clear()
	a.paneSel.clear()
	a.toolsMode = true
	a.tools.reload(a.cwd)
}

// toolsKey handles keys while the tools view owns the screen. Selection
// and scrolling echo the git view so the movement vocabulary is shared.
func (a *App) toolsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Close on T (either the shifted code or the kitty shift+t form) or esc.
	if msg.Code == tea.KeyEscape ||
		(msg.Code == 'T' && msg.Mod&tea.ModCtrl == 0) ||
		(msg.Code == 't' && msg.Mod == tea.ModShift) {
		a.toolsMode = false
		return *a, nil
	}

	switch msg.Code {
	case 'j', tea.KeyDown:
		if msg.Mod == 0 {
			a.tools.move(1)
		}
	case 'k', tea.KeyUp:
		if msg.Mod == 0 {
			a.tools.move(-1)
		}
	case 'r':
		if msg.Mod == 0 {
			a.tools.reload(a.cwd)
		}
	case 'd':
		if msg.Mod == tea.ModCtrl {
			a.tools.scrollBy(diffPageSize(a.panelBody()), a.panelBody())
		}
	case 'u':
		if msg.Mod == tea.ModCtrl {
			a.tools.scrollBy(-diffPageSize(a.panelBody()), a.panelBody())
		}
	case tea.KeyPgDown:
		a.tools.scrollBy(diffPageSize(a.panelBody()), a.panelBody())
	case tea.KeyPgUp:
		a.tools.scrollBy(-diffPageSize(a.panelBody()), a.panelBody())
	case 'Q', 'q':
		switch {
		case msg.Code == 'Q' && msg.Mod&tea.ModCtrl == 0,
			msg.Mod == tea.ModShift:
			a.closeAll()
			return *a, tea.Quit
		case msg.String() == "ctrl+q":
			a.toolsMode = false
		}
	default:
		if msg.String() == "ctrl+q" {
			a.toolsMode = false
		}
	}
	return *a, nil
}

// toolsListWidth is the tool list's column width in the tools view, shared
// with the selection's pane geometry so they agree.
func (a App) toolsListWidth() int {
	return clamp(a.width/3, 28, a.width-24)
}

// renderTools composes the fullscreen tools view: list left, code right.
func (a App) renderTools() string {
	t := a.theme
	contentW, contentH := a.width, a.bodyHeight()
	listW := a.toolsListWidth()

	listBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent)).
		Render(a.toolsListContent(listW-2, contentH-2))
	codeBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent)).
		Render(a.toolsCodeContent(listW+1, contentW-listW-2, contentH-2))

	rows := make([]string, contentH)
	listLines := strings.Split(listBox, "\n")
	codeLines := strings.Split(codeBox, "\n")
	for y := range rows {
		left, right := "", ""
		if y < len(listLines) {
			left = listLines[y]
		}
		if y < len(codeLines) {
			right = codeLines[y]
		}
		rows[y] = padRow(left, listW) + right
	}
	return strings.Join(rows, "\n")
}

// toolsListContent renders the list box's inner content.
func (a App) toolsListContent(w, h int) string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	base := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))
	header := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Accent))
	selected := lipgloss.NewStyle().Background(lipgloss.Color(t.Accent)).Foreground(lipgloss.Color(t.Bg))

	lines := []string{header.Render(" tools ")}
	if a.tools.err != "" {
		lines = append(lines, "", dim.Render(" "+a.tools.err))
	} else if len(a.tools.entries) == 0 {
		lines = append(lines, "",
			dim.Render(" no tools yet "),
			dim.Render(" ask the agent to write one "),
			dim.Render(" into .jaed/tools/ "))
	}

	// Keep the selected row on screen.
	visible := h - len(lines)
	top := 0
	if a.tools.selected >= visible && visible > 0 {
		top = a.tools.selected - visible + 1
	}
	for i, tool := range a.tools.entries[top:] {
		if len(lines) >= h {
			break
		}
		idx := top + i
		label := " " + tool.Name
		desc := tool.Description
		if desc == "" {
			desc = "(no description)"
		}
		if room := w - ansi.StringWidth(label) - 2; room > 0 {
			label += "  " + ansi.Truncate(desc, room, "…")
		}
		if ansi.StringWidth(label) > w {
			label = ansi.Truncate(label, w, "…")
		}
		if idx == a.tools.selected {
			lines = append(lines, selected.Width(w).Render(label))
		} else {
			lines = append(lines, base.Render(label))
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, line := range lines[:h] {
		lines[i] = padToWidth(line, w, base)
	}
	return strings.Join(lines[:h], "\n")
}

// toolsCodeContent renders the code box's inner content, with line numbers.
func (a App) toolsCodeContent(x, w, h int) string {
	t := a.theme
	surface := lipgloss.Color(t.Surface)
	dim := lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Dim))

	title := " code "
	if a.tools.err == "" && len(a.tools.entries) > 0 {
		title = " " + filepathBase(a.tools.entries[a.tools.selected].Path) + " "
		if ansi.StringWidth(title) > w {
			title = ansi.Truncate(title, w-1, "…")
		}
	}
	lines := []string{dim.Render(title)}

	// Reserve the last row for a notice, or the whole box for the code.
	body := h - 1
	if a.tools.notice != "" {
		body = max(0, h-2)
	}

	src := a.tools.selectedLines()
	if len(src) > 0 {
		end := min(a.tools.scroll+body, len(src))
		numW := len(strconv.Itoa(len(src)))
		for i := a.tools.scroll; i < end; i++ {
			num := dim.Render(fmt.Sprintf(" %*d ", numW, i+1))
			line := num + a.codeLine(src[i])
			if ansi.StringWidth(line) > w {
				line = ansi.Truncate(line, w, "")
			}
			lines = append(lines, line)
		}
	}
	if a.tools.notice != "" {
		style := dim
		if a.tools.noticeErr {
			style = lipgloss.NewStyle().Background(surface).Foreground(lipgloss.Color(t.Error))
		}
		lines = lines[:min(len(lines), h-1)]
		lines = append(lines, style.Render(a.tools.notice))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, line := range lines[:h] {
		lines[i] = padToWidth(line, w, dim)
	}
	return strings.Join(lines[:h], "\n")
}

// filepathBase is filepath.Base, kept local so this file needs no import
// for a single call.
func filepathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// codeKeywords are the JavaScript words the code view highlights. A tiny,
// deliberately incomplete set: enough to read a tool at a glance.
var codeKeywords = map[string]bool{
	"const": true, "let": true, "var": true, "function": true, "return": true,
	"async": true, "await": true, "export": true, "default": true, "import": true,
	"from": true, "if": true, "else": true, "for": true, "of": true, "in": true,
	"while": true, "class": true, "new": true, "try": true, "catch": true,
	"finally": true, "throw": true, "typeof": true, "instanceof": true,
	"true": true, "false": true, "null": true, "undefined": true, "this": true,
}

// codeLine colorizes one line of JavaScript: comments dim, strings green,
// keywords accent, everything else foreground. Purely cosmetic.
func (a App) codeLine(line string) string {
	t := a.theme
	bg := lipgloss.Color(t.Surface)
	fg := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(t.Fg))
	dim := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(t.Dim))
	kw := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(t.Accent))
	str := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(t.Success))

	var out strings.Builder
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out.WriteString(fg.Render(plain.String()))
			plain.Reset()
		}
	}

	runes := []rune(line)
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case c == '/' && i+1 < len(runes) && runes[i+1] == '/':
			flush()
			out.WriteString(dim.Render(string(runes[i:])))
			i = len(runes)
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			flush()
			end := i + 2
			for end < len(runes)-1 && !(runes[end] == '*' && runes[end+1] == '/') {
				end++
			}
			if end < len(runes)-1 {
				end += 2
			} else {
				end = len(runes)
			}
			out.WriteString(dim.Render(string(runes[i:end])))
			i = end
		case c == '"' || c == '\'' || c == '`':
			flush()
			end := i + 1
			for end < len(runes) && runes[end] != c {
				if runes[end] == '\\' && end+1 < len(runes) {
					end++
				}
				end++
			}
			if end < len(runes) {
				end++
			}
			out.WriteString(str.Render(string(runes[i:end])))
			i = end
		case isWordStart(c):
			end := i + 1
			for end < len(runes) && isWordRune(runes[end]) {
				end++
			}
			word := string(runes[i:end])
			flush()
			if codeKeywords[word] {
				out.WriteString(kw.Render(word))
			} else {
				out.WriteString(fg.Render(word))
			}
			i = end
		default:
			plain.WriteRune(c)
			i++
		}
	}
	flush()
	return out.String()
}

func isWordStart(c rune) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWordRune(c rune) bool {
	return isWordStart(c) || (c >= '0' && c <= '9')
}
