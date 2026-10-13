package workspace

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
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
