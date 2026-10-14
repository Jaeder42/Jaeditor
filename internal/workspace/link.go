package workspace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"jaed/internal/theme"
)

// The editor ↔ agent link. Instead of sockets (a plugin-spawned
// `nvim --embed` helper copies the parent's argv and steals any
// --listen socket), jaed starts nvim with a titlestring of
// expand("%:p"): the pane's OSC title becomes the active buffer's
// absolute path, which jaed already captures for the status bar. The
// embedded helper cannot emit titles (no UI), so there is no race.
// Bonus: the terminal tab shows the edited file.

// NvimTitleArgs are the editor args that make nvim report its buffer:
// a titlestring of the buffer's absolute path, set after the user's
// config loads so it wins.
func NvimTitleArgs() []string {
	return []string{
		// titlelen=0: no truncation — default 85 would chop long paths
		// to "<…tail" and break the @-mention.
		"+set title titlelen=0 titlestring=%{expand('%:p')}",
		// Re-assert after the user's config and plugins run: session
		// managers (e.g. persistence.nvim) restore saved option values
		// that would otherwise switch titles off again.
		"+autocmd BufEnter,SessionLoadPost * set title titlelen=0 | let &titlestring = expand('%:p')",
	}
}

// editorBufferTitle reads the editor pane's guest title as the active
// file. Titles that are not absolute paths (themed or customized by
// the user's config) are treated as "no file open".
func (a App) editorBufferTitle() (string, error) {
	title := a.panes[PaneMain].GuestTitle()
	if title != "" && !filepath.IsAbs(title) {
		return "", nil
	}
	return title, nil
}

// The live link: jaed continuously publishes the file being edited so
// the agent can be passively aware of it. pi's jaed extension (shipped
// at assets/jaed.ts) reads the same path and injects it as context on
// every prompt. The path is keyed by the workspace's resolved directory
// — getcwd and EvalSymlinks agree there — so both sides compute it
// identically without any configuration.

// activeFilePath is jaed's live "what is being edited" file for a
// workspace directory.
func activeFilePath(cwd string) string {
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		resolved = cwd
	}
	sum := sha256.Sum256([]byte(resolved))
	return filepath.Join(os.TempDir(), fmt.Sprintf("jaed-active-%x", sum[:8]))
}

// syncActiveFile publishes the editor's active buffer to the live-link
// file. It runs on every Update and writes only when the title changed:
// empty when no file is open, the buffer's absolute path otherwise.
func (a *App) syncActiveFile() {
	if a.activeFile == "" {
		return
	}
	title := ""
	if !a.dead[PaneMain] {
		title = a.panes[PaneMain].GuestTitle()
	}
	if title == a.activeSeen {
		return
	}
	a.activeSeen = title
	content := title
	if !filepath.IsAbs(content) {
		content = ""
	}
	_ = os.WriteFile(a.activeFile, []byte(content), 0o644)
}

// The reverse channel: the editor reports its colorscheme, and jaed can
// follow it. nvim writes g:colors_name to the theme file (below); the
// workspace maps the name onto a built-in palette while in auto mode.

// themeAutoName is the cfg.Theme value that follows the editor's
// colorscheme instead of a fixed palette.
const themeAutoName = "auto"

// themeFilePath is jaed's per-workspace "which colorscheme is the editor
// on" file: same keying as activeFilePath, so nvim and jaed agree on the
// path without any configuration.
func themeFilePath(cwd string) string {
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		resolved = cwd
	}
	sum := sha256.Sum256([]byte(resolved))
	return filepath.Join(os.TempDir(), fmt.Sprintf("jaed-theme-%x", sum[:8]))
}

// NvimThemeArgs are the editor args that make nvim report its colorscheme:
// the name lands in the workspace's theme file once at startup and again
// on every :colorscheme. Like the title args, these run after the user's
// config, so a plugin-loaded scheme is what gets reported.
func NvimThemeArgs(cwd string) []string {
	p := strings.ReplaceAll(themeFilePath(cwd), "'", "''") // '' quotes '
	return []string{
		"+silent! call writefile([g:colors_name], '" + p + "')",
		"+autocmd ColorScheme * silent! call writefile([g:colors_name], '" + p + "')",
	}
}

// themeForColorscheme maps an editor colorscheme onto a built-in theme:
// exact names and variant suffixes ("catppuccin-mocha", "gruvbox-material",
// "tokyonight_storm"). Unknown schemes report false — the workspace keeps
// whatever it is on, so switching to a scheme jaed has no palette for
// never drops the user into unstyled chrome.
func themeForColorscheme(name string) (theme.Theme, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return theme.Theme{}, false
	}
	for _, want := range theme.Names() {
		if name == want || strings.HasPrefix(name, want+"-") || strings.HasPrefix(name, want+"_") {
			return theme.Get(want)
		}
	}
	return theme.Theme{}, false
}

// syncTheme applies the editor's reported colorscheme while the workspace
// runs in auto theme mode. The file's mtime gates the read — Update runs
// hot, the file changes only when the user switches schemes.
func (a *App) syncTheme() {
	if !strings.EqualFold(a.cfg.Theme, themeAutoName) || a.themeFile == "" {
		return
	}
	info, err := os.Stat(a.themeFile)
	if err != nil || info.ModTime().Equal(a.themeMod) {
		return
	}
	a.themeMod = info.ModTime()
	data, err := os.ReadFile(a.themeFile)
	if err != nil {
		return
	}
	if th, ok := themeForColorscheme(string(data)); ok && th.Name != a.theme.Name {
		a.theme = th
	}
}

// The agent → workspace channel: the agents' jaed plugins
// (assets/jaed.ts for pi, assets/jaed-opencode.ts for opencode)
// publish their session state — which model, whether it is working,
// what it is doing, and how full its context is — to the state file,
// and the status bar shows it so the agent can be monitored without
// looking at its pane. Agents without a plugin (a shell) never write
// the file and nothing shows.

// agentState is the agent's self-reported session state.
type agentState struct {
	Status   string `json:"status"`             // "running", "retry", or "idle"
	Model    string `json:"model"`              // the model id, "" when unreported
	Activity string `json:"activity,omitempty"` // the tool/step in flight, "" when thinking or idle
	Context  *int   `json:"context,omitempty"`  // context-window use percent; nil when unreported
}

// agentStateFilePath is jaed's per-workspace "what is the agent doing"
// file: same keying as activeFilePath, so the extension and jaed agree
// on the path without configuration.
func agentStateFilePath(cwd string) string {
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		resolved = cwd
	}
	sum := sha256.Sum256([]byte(resolved))
	return filepath.Join(os.TempDir(), fmt.Sprintf("jaed-state-%x", sum[:8]))
}

// syncAgentState reads the agent's live self-report. Like the theme
// channel, the file's mtime gates the read — Update runs hot, the file
// changes only when the agent starts, turns, or settles. A malformed
// report never clobbers the last good one.
func (a *App) syncAgentState() {
	if a.agentStateFile == "" {
		return
	}
	info, err := os.Stat(a.agentStateFile)
	if err != nil || info.ModTime().Equal(a.agentMod) {
		return
	}
	a.agentMod = info.ModTime()
	data, err := os.ReadFile(a.agentStateFile)
	if err != nil {
		return
	}
	var s agentState
	if json.Unmarshal(data, &s) == nil {
		a.agentState = s
	}
}
