package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCutRowStyled(t *testing.T) {
	// The style continues across the cut: the suffix must re-assert it.
	row := "hello" + "\x1b[31m" + "worldwide"

	pre, suf := cutRow(row, 5, 8)
	if pre != "hello\x1b[31m" {
		t.Errorf("prefix = %q, want %q", pre, "hello\x1b[31m")
	}
	if !strings.HasPrefix(suf, "\x1b[31m") {
		t.Errorf("suffix should re-assert the style active at the cut, got %q", suf)
	}
	if !strings.Contains(suf, "ldwide") {
		t.Errorf("suffix should keep the tail, got %q", suf)
	}
	if w := ansi.StringWidth(suf); w != 6 {
		t.Errorf("suffix visible width = %d, want 6", w)
	}
}

func TestCutRowAfterReset(t *testing.T) {
	// The cut point sits after a full reset: the suffix lead must NOT
	// re-assert the earlier style.
	row := "\x1b[31mred\x1b[mplain"
	_, suf := cutRow(row, 3, 6)
	if strings.HasPrefix(suf, "\x1b[31m") {
		t.Errorf("suffix re-asserted a reset style, got %q", suf)
	}
	if suf != "in" {
		t.Errorf("suffix = %q, want %q", suf, "in")
	}
}

func TestOverlayCenter(t *testing.T) {
	content := strings.Repeat("x", 20) + "\n" + strings.Repeat("y", 20)
	popup := []string{"AAAA", "BBBB"}
	out := overlayCenter(content, popup, 20, 2)

	want0 := "xxxxxxxxAAAAxxxxxxxx"
	want1 := "yyyyyyyyBBBByyyyyyyy"
	got := strings.Split(out, "\n")
	for i, want := range []string{want0, want1} {
		plain := stripANSI(got[i])
		if plain != want {
			t.Errorf("line %d = %q, want %q", i, plain, want)
		}
	}
}

func TestOverlayCenterPadsShortRows(t *testing.T) {
	// A row shorter than the popup's left edge: pad, stay centered.
	content := "ab\ncd"
	out := overlayCenter(content, []string{"ZZZZZZ"}, 20, 2)
	plain := stripANSI(strings.Split(out, "\n")[0])
	if !strings.Contains(plain, "ZZZZZZ") {
		t.Fatalf("popup missing from short row: %q", plain)
	}
	if !strings.HasPrefix(plain, "       ZZZZZZ") && !strings.Contains(plain, "ZZZZZZ") {
		t.Fatalf("popup not centered: %q", plain)
	}
}
