// Package tools manages a workspace's .jaed folder: the personal,
// never-committed home for agent-authored scripts and skills.
//
// Layout:
//
//	<workspace>/.jaed/tools/   runnable JavaScript (the agent writes these)
//	<workspace>/.jaed/skills/  markdown skills (reference material)
//	<workspace>/.jaed/tmp/     scratch space tools may write to
//
// EnsureLayout creates the folders on jaed startup and adds .jaed/ to
// the repository's local exclude (.git/info/exclude), so the folder is
// invisible to git without touching the tracked .gitignore.
package tools

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Dir is the per-workspace jaed folder: personal to the user.
const Dir = ".jaed"

// Tool is one agent-authored script discovered under .jaed/tools.
type Tool struct {
	Name        string // filename stem, without the .js extension
	Description string // the @description tag of the leading block comment
	Path        string // absolute path to the source file
	Code        string // full source
}

// Root is the .jaed folder for a workspace.
func Root(cwd string) string { return filepath.Join(cwd, Dir) }

// ToolsDir is where agent-authored scripts live.
func ToolsDir(cwd string) string { return filepath.Join(Root(cwd), "tools") }

// SkillsDir is where agent-authored markdown skills live.
func SkillsDir(cwd string) string { return filepath.Join(Root(cwd), "skills") }

// TmpDir is the only place a sandboxed tool may write.
func TmpDir(cwd string) string { return filepath.Join(Root(cwd), "tmp") }

// EnsureLayout creates the .jaed folders and keeps them out of version
// control. It is best-effort: a failure leaves jaed usable, just without
// the agent tooling. The local exclude means the user never has to see
// or commit this folder.
func EnsureLayout(cwd string) error {
	for _, dir := range []string{ToolsDir(cwd), SkillsDir(cwd), TmpDir(cwd)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return excludeLocal(cwd)
}

// excludeLocal appends ".jaed/" to the repository's .git/info/exclude if
// cwd is inside a git repository and the line is not already there. It
// handles both ordinary repositories (.git is a directory) and linked
// worktrees (.git is a file pointing at the real gitdir).
func excludeLocal(cwd string) error {
	git := filepath.Join(cwd, ".git")
	info, err := os.Stat(git)
	if err != nil {
		return nil // not a repository
	}

	gitdir := git
	if !info.IsDir() {
		data, err := os.ReadFile(git)
		if err != nil {
			return err
		}
		const marker = "gitdir:"
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, marker) {
			return nil
		}
		gitdir = strings.TrimSpace(strings.TrimPrefix(line, marker))
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(cwd, gitdir)
		}
	}

	dir := filepath.Join(gitdir, "info")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, "exclude")
	existing, _ := os.ReadFile(file)
	if hasExcludeLine(string(existing)) {
		return nil
	}

	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + "# jaed agent tools — personal, never committed\n" + Dir + "/\n")
	return err
}

// hasExcludeLine reports whether a .git/info/exclude already covers .jaed.
func hasExcludeLine(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		switch strings.TrimSpace(line) {
		case Dir, Dir + "/", "/" + Dir, "/" + Dir + "/":
			return true
		}
	}
	return false
}

// List returns the scripts under .jaed/tools, sorted by name. A missing
// tools folder is not an error: it yields an empty list.
func List(cwd string) ([]Tool, error) {
	dir := ToolsDir(cwd)
	dirents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	list := make([]Tool, 0, len(dirents))
	for _, de := range dirents {
		if de.IsDir() || !isScript(de.Name()) {
			continue
		}
		path := filepath.Join(dir, de.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue // a tool that vanished mid-scan is skipped
		}
		list = append(list, Tool{
			Name:        strings.TrimSuffix(de.Name(), filepath.Ext(de.Name())),
			Description: Describe(string(data)),
			Path:        path,
			Code:        string(data),
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// isScript reports whether a file name is a runnable tool.
func isScript(name string) bool {
	switch filepath.Ext(name) {
	case ".js", ".mjs":
		return true
	}
	return false
}

// Describe extracts the tool's one-line description from the leading
// block comment: an @description tag, whose value continues across lines
// until the next @tag or the end of the comment. Absent a block comment,
// the first line of a leading // comment is used. Nothing is executed.
func Describe(src string) string {
	s := strings.TrimLeft(src, "\ufeff")
	if strings.HasPrefix(strings.TrimSpace(s), "/**") {
		start := strings.Index(s, "/**")
		if end := strings.Index(s[start:], "*/"); end >= 0 {
			if v := tagValue(s[start+3:start+end], "description"); v != "" {
				return v
			}
		}
	}

	// Fallback: a run of leading // lines; use the first non-empty one.
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if v, ok := strings.CutPrefix(line, "//"); ok {
			return strings.TrimSpace(v)
		}
		break
	}
	return ""
}

// tagValue pulls a JSDoc tag's value from a comment body, joining
// continuation lines with spaces. The tag matches "@name" followed by
// whitespace or the end of the line, so @description never swallows
// @describe.
func tagValue(body, tag string) string {
	var out []string
	capturing := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "*"))
		if line == "" && !capturing {
			continue
		}
		if v, ok := cutTag(line, tag); ok {
			capturing = true
			if v != "" {
				out = append(out, v)
			}
			continue
		}
		if capturing {
			if strings.HasPrefix(line, "@") {
				break // the next tag ends this value
			}
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, " "))
}

// cutTag splits "@tag value" from a comment line.
func cutTag(line, tag string) (string, bool) {
	prefix := "@" + tag
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(line, prefix)
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return "", false // e.g. "@descriptionx"
	}
	return strings.TrimSpace(rest), true
}
