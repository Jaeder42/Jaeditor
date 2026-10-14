package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// HunkSpan locates one hunk inside a unified diff by byte offset. Slicing the
// original string keeps the bytes exact, even around "\ No newline at end of
// file" markers that rebuilding lines would drop.
type HunkSpan struct {
	Start, End int
}

// Patch builds a standalone, applyable patch for the hunk: the file preamble
// (diff/index/---/+++ lines) followed by the hunk itself. preambleEnd is the
// offset SplitHunks reports; a zero hunk yields the diff unchanged.
func (h HunkSpan) Patch(diff string, preambleEnd int) string {
	if preambleEnd < 0 || preambleEnd > h.Start || h.End > len(diff) || h.Start > h.End {
		return diff
	}
	return diff[:preambleEnd] + diff[h.Start:h.End]
}

// SplitHunks finds the hunks in a unified diff and returns the offset where
// the first one begins. A diff with no hunks (binary, mode-only, or empty)
// returns (len(diff), nil), so the whole thing is preamble.
func SplitHunks(diff string) (preambleEnd int, hunks []HunkSpan) {
	preambleEnd = len(diff)
	for i := 0; i < len(diff); {
		line, next := readLine(diff, i)
		// A hunk's own lines are prefixed with ' ', '+', or '-', so "@@" at
		// the start of a line is unambiguously a hunk header.
		if strings.HasPrefix(line, "@@") {
			if len(hunks) == 0 {
				preambleEnd = i
			} else {
				hunks[len(hunks)-1].End = i
			}
			hunks = append(hunks, HunkSpan{Start: i, End: len(diff)})
		}
		i = next
	}
	return preambleEnd, hunks
}

// readLine returns the line at i without its newline and the offset of the
// next line (len(s) at the end).
func readLine(s string, i int) (line string, next int) {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return s[i : i+j], i + j + 1
	}
	return s[i:], len(s)
}

// StageFile adds a path to the index, untracked files included.
func StageFile(dir, path string) error {
	return runErr(dir, "add", "--", path)
}

// UnstageFile removes a path's staged changes from the index, leaving them in
// the worktree.
func UnstageFile(dir, path string) error {
	return runErr(dir, "restore", "--staged", "--", path)
}

// RevertFile discards a tracked path's worktree changes, restoring it from the
// index.
func RevertFile(dir, path string) error {
	return runErr(dir, "restore", "--", path)
}

// ApplyPatch applies a patch to the index (cached) or the worktree. reverse
// undoes it — the direction that unstages a staged hunk or reverts a worktree
// one.
func ApplyPatch(dir, patch string, cached, reverse bool) error {
	args := []string{"apply", "--whitespace=nowarn"}
	if cached {
		args = append(args, "--cached")
	}
	if reverse {
		args = append(args, "--reverse")
	}
	args = append(args, "-")
	return runStdin(dir, patch, args...)
}

// runErr runs git and surfaces its output as the error on failure.
func runErr(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// runStdin runs git with stdin piped into it, for patch application.
func runStdin(dir, stdin string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}
