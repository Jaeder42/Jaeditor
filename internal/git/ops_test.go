package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitHunks(t *testing.T) {
	diff := "diff --git a/x b/x\n" +
		"index 000..111 100644\n" +
		"--- a/x\n" +
		"+++ b/x\n" +
		"@@ -1,3 +1,3 @@\n" +
		" a\n" +
		"-b\n" +
		"+B\n" +
		" c\n" +
		"@@ -10,2 +10,2 @@\n" +
		"-d\n" +
		"+D\n"

	pre, hunks := SplitHunks(diff)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}
	if !strings.HasSuffix(diff[:pre], "+++ b/x\n") {
		t.Errorf("preamble should end after +++: %q", diff[:pre])
	}
	// A single hunk's patch is the preamble plus just that hunk.
	want := "diff --git a/x b/x\nindex 000..111 100644\n--- a/x\n+++ b/x\n@@ -10,2 +10,2 @@\n-d\n+D\n"
	if got := hunks[1].Patch(diff, pre); got != want {
		t.Errorf("second hunk patch:\n%q\nwant:\n%q", got, want)
	}
}

func TestSplitHunksNone(t *testing.T) {
	diff := "diff --git a/x b/x\nindex 000..111 100644\nBinary files a/x and b/x differ\n"
	pre, hunks := SplitHunks(diff)
	if len(hunks) != 0 {
		t.Fatalf("binary diff hunks = %d, want 0", len(hunks))
	}
	if pre != len(diff) {
		t.Errorf("preambleEnd = %d, want %d", pre, len(diff))
	}
}

// twoHunkFile writes a 20-line file into a fresh repo, then changes its first
// and last lines so `git diff` reports two separated hunks.
func twoHunkFile(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	var base strings.Builder
	for i := 1; i <= 20; i++ {
		base.WriteString(string(rune('a'+i-1)) + "\n")
	}
	write(t, dir, "f.txt", base.String())
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	lines := strings.Split(strings.TrimSuffix(base.String(), "\n"), "\n")
	lines[0] = "A"
	lines[19] = "Z"
	write(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")
	return dir, "f.txt"
}

func TestStageAndUnstageHunk(t *testing.T) {
	dir, path := twoHunkFile(t)

	diff := Diff(dir, Entry{Section: Unstaged, Path: path, Code: "M"})
	pre, hunks := SplitHunks(diff)
	if len(hunks) != 2 {
		t.Fatalf("worktree diff hunks = %d, want 2:\n%s", len(hunks), diff)
	}

	// Stage only the first hunk.
	if err := ApplyPatch(dir, hunks[0].Patch(diff, pre), true, false); err != nil {
		t.Fatalf("stage hunk: %v", err)
	}
	staged, _ := run(dir, "diff", "--cached", "--no-color", "--", path)
	if !strings.Contains(staged, "+A") {
		t.Errorf("staged diff should carry the first change:\n%s", staged)
	}
	if strings.Contains(staged, "+Z") {
		t.Errorf("staged diff should not carry the last change:\n%s", staged)
	}
	worktree, _ := run(dir, "diff", "--no-color", "--", path)
	if strings.Contains(worktree, "+A") {
		t.Errorf("worktree diff should have dropped the staged hunk:\n%s", worktree)
	}
	if !strings.Contains(worktree, "+Z") {
		t.Errorf("worktree diff should keep the second change:\n%s", worktree)
	}

	// Unstage that hunk again.
	cdiff := Diff(dir, Entry{Section: Staged, Path: path, Code: "M"})
	cpre, chunks := SplitHunks(cdiff)
	if len(chunks) != 1 {
		t.Fatalf("cached diff hunks = %d, want 1:\n%s", len(chunks), cdiff)
	}
	if err := ApplyPatch(dir, chunks[0].Patch(cdiff, cpre), true, true); err != nil {
		t.Fatalf("unstage hunk: %v", err)
	}
	if staged, _ = run(dir, "diff", "--cached", "--no-color", "--", path); staged != "" {
		t.Errorf("index should be empty after unstaging, got:\n%s", staged)
	}
}

func TestRevertHunk(t *testing.T) {
	dir, path := twoHunkFile(t)

	diff := Diff(dir, Entry{Section: Unstaged, Path: path, Code: "M"})
	pre, hunks := SplitHunks(diff)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}
	if err := ApplyPatch(dir, hunks[0].Patch(diff, pre), false, true); err != nil {
		t.Fatalf("revert hunk: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.HasPrefix(got, "A\n") {
		t.Errorf("revert should have restored the first line: %q", got)
	}
	if !strings.Contains(got, "Z\n") {
		t.Errorf("revert should have left the last line changed: %q", got)
	}
}

func TestStageUnstageRevertFile(t *testing.T) {
	dir, path := twoHunkFile(t)

	if err := StageFile(dir, path); err != nil {
		t.Fatalf("stage file: %v", err)
	}
	st, err := ReadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := find(st, Staged, path); !ok {
		t.Error("stage file should move it into the staged section")
	}

	if err := UnstageFile(dir, path); err != nil {
		t.Fatalf("unstage file: %v", err)
	}
	if st, _ = ReadStatus(dir); func() bool { _, ok := find(st, Staged, path); return ok }() {
		t.Error("unstage file should clear the staged section")
	}

	if err := RevertFile(dir, path); err != nil {
		t.Fatalf("revert file: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, path))
	if strings.Contains(string(data), "A\n") || strings.Contains(string(data), "Z\n") {
		t.Errorf("revert file should restore the committed content: %q", string(data))
	}
}
