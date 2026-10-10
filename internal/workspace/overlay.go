package workspace

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const resetStyle = "\x1b[m"

// cutRow splits a styled row at visible-cell offsets [start, end):
// prefix holds everything before start, suffix holds everything from end
// on with the style active at the cut re-asserted, so a replacement region
// can be dropped in between without bleeding colors either way.
func cutRow(row string, start, end int) (prefix, suffix string) {
	var pre, suf, active strings.Builder
	visible := 0
	state := byte(0)
	for i := 0; i < len(row); {
		seq, width, n, newState := ansi.DecodeSequence(row[i:], state, nil)
		i += n
		state = newState

		if isSGR(seq) {
			if isSGRReset(seq) {
				active.Reset()
			} else {
				active.WriteString(seq)
			}
		}

		switch {
		case visible+width <= start:
			pre.WriteString(seq)
		case visible >= end:
			suf.WriteString(seq)
		default:
			// Inside (or straddling) the replaced region: dropped. The
			// running style still accumulates, so the suffix lead stays
			// correct.
		}
		visible += width
	}
	return pre.String(), active.String() + suf.String()
}

// overlayCenter draws popup lines over the center of a screen's content.
// Every popup line must render to the same visible width (picker.render
// guarantees it).
func overlayCenter(content string, popup []string, width, height int) string {
	popW, popH := 0, len(popup)
	for _, line := range popup {
		popW = max(popW, ansi.StringWidth(line))
	}
	if popW == 0 || popH == 0 {
		return content
	}
	x := max(0, (width-popW)/2)
	y := max(0, (height-popH)/2)

	lines := strings.Split(content, "\n")
	for i, line := range popup {
		target := y + i
		if target < 0 || target >= len(lines) {
			continue
		}
		pre, suf := cutRow(lines[target], x, x+popW)
		// A row can end (or straddle a wide cell) before the popup's left
		// edge; pad so the popup stays centered.
		if pad := x - ansi.StringWidth(pre); pad > 0 {
			pre += strings.Repeat(" ", pad)
		}
		lines[target] = pre + resetStyle + line + resetStyle + suf
	}
	return strings.Join(lines, "\n")
}

func isSGR(seq string) bool {
	return strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m")
}

func isSGRReset(seq string) bool {
	params := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
	params = strings.TrimPrefix(params, "00")
	return strings.TrimPrefix(params, "0") == ""
}
