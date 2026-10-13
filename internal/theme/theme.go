// Package theme defines jaed's semantic palette: the colors the TUI chrome
// renders with, and the palette advertised to guest programs over the
// PTY. One theme drives everything.
package theme

import (
	"sort"
	"strings"
)

// Theme is a semantic palette. Every color is a hex string ("#2b3339").
type Theme struct {
	Name          string // shown in the status bar
	Bg            string // workspace ground; paints pane default background
	Fg            string // primary text
	Surface       string // status bar, popups
	Selection     string // selected rows, input highlights
	Subtext       string // secondary text
	Dim           string // hints, unfocused chrome
	Accent        string // focused chrome highlights
	Border        string // unfocused pane border
	BorderFocused string // focused pane border
	Success       string // running state, ok
	Warn          string // warnings, blurred state
	Error         string // errors, failure
}

var builtin = map[string]Theme{
	Catppuccin.Name: Catppuccin,
	Everforest.Name: Everforest,
	Gruvbox.Name:    Gruvbox,
	Tokyonight.Name: Tokyonight,
}

// Get returns a built-in theme by name (case-insensitive).
func Get(name string) (Theme, bool) {
	t, ok := builtin[strings.ToLower(name)]
	return t, ok
}

// Names lists the built-in themes, sorted.
func Names() []string {
	names := make([]string, 0, len(builtin))
	for name := range builtin {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
