// Package git reads repository state for the git view: changed files
// and their diffs, via the git CLI run in the workspace directory.
package git

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Section is where a change lives: the index, the worktree, or nowhere
// tracked yet.
type Section int

const (
	Staged Section = iota
	Unstaged
	Untracked
)

// String implements fmt.Stringer.
func (s Section) String() string {
	switch s {
	case Staged:
		return "staged"
	case Unstaged:
		return "unstaged"
	default:
		return "untracked"
	}
}

// Entry is one changed file.
type Entry struct {
	Section Section
	Path    string
	Code    string // porcelain code: M, A, D, ??, ...
}

// Status is the repository's branch and its changed files, ordered
// staged, unstaged, untracked.
type Status struct {
	Branch  string
	Entries []Entry
}

// ReadStatus reads `git status --porcelain=v1 -b`. A non-repository or
// a missing git binary surfaces as an error.
func ReadStatus(dir string) (Status, error) {
	out, err := run(dir, "status", "--porcelain=v1", "-b")
	if err != nil {
		return Status{}, err
	}
	return parse(out), nil
}

// Diff returns the diff for an entry: staged changes for staged
// entries, worktree changes otherwise. Untracked files diff against
// /dev/null so the whole file reads as added.
func Diff(dir string, e Entry) string {
	switch e.Section {
	case Untracked:
		out, _ := run(dir, "diff", "--no-color", "--no-index", "--", "/dev/null", e.Path)
		return out
	case Staged:
		out, _ := run(dir, "diff", "--cached", "--no-color", "--", e.Path)
		return out
	default:
		out, _ := run(dir, "diff", "--no-color", "--", e.Path)
		return out
	}
}

// run executes git in dir and returns stdout. A nonzero exit with stderr
// is an error; a nonzero exit with empty stderr is success with output
// (git diff --no-index exits 1 to report differences).
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if len(ee.Stderr) == 0 {
				return string(out), nil
			}
			return string(out), fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// parse turns porcelain v1 output into a Status. Lines are XYSPpath;
// a file can appear in two sections (e.g. "MM" = staged and unstaged).
func parse(out string) Status {
	var st Status
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			st.Branch = branchName(strings.TrimSpace(line[3:]))
		case len(line) >= 4 && line[2] == ' ' && line[0] != '#':
			x, y, path := line[0], line[1], unquote(line[3:])
			if x == '?' && y == '?' {
				st.Entries = append(st.Entries, Entry{Section: Untracked, Path: path, Code: "??"})
				continue
			}
			if x != ' ' {
				st.Entries = append(st.Entries, Entry{Section: Staged, Path: path, Code: string(x)})
			}
			if y != ' ' {
				st.Entries = append(st.Entries, Entry{Section: Unstaged, Path: path, Code: string(y)})
			}
		}
	}
	return st
}

// branchName extracts the short branch from the porcelain header:
// "main...origin/main [ahead 1]" -> "main"; "No commits yet on main" ->
// "main · no commits"; detached HEAD stays as-is.
func branchName(header string) string {
	if strings.HasPrefix(header, "No commits yet on ") {
		return strings.TrimPrefix(header, "No commits yet on ") + " · no commits"
	}
	if i := strings.Index(header, "..."); i >= 0 {
		header = header[:i]
	}
	return header
}

// unquote strips the C-style quoting porcelain applies to paths with
// special characters. Ordinary paths pass through untouched.
func unquote(path string) string {
	if !strings.HasPrefix(path, "\"") || !strings.HasSuffix(path, "\"") {
		return path
	}
	path = strings.TrimSuffix(strings.TrimPrefix(path, "\""), "\"")
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\t`, "\t").Replace(path)
}
