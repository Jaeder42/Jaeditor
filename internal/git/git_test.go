package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "committed.txt", "base\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(st Status, section Section, path string) (Entry, bool) {
	for _, e := range st.Entries {
		if e.Section == section && e.Path == path {
			return e, true
		}
	}
	return Entry{}, false
}

func TestReadStatus(t *testing.T) {
	dir := repo(t)
	write(t, dir, "committed.txt", "changed\n")     // unstaged modification
	write(t, dir, "staged.txt", "staged content\n") // new file
	gitRun(t, dir, "add", "staged.txt")
	write(t, dir, "untracked.txt", "untracked\n")

	st, err := ReadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if e, ok := find(st, Unstaged, "committed.txt"); !ok || e.Code != "M" {
		t.Errorf("unstaged committed.txt missing or wrong: %+v ok=%v", e, ok)
	}
	if e, ok := find(st, Staged, "staged.txt"); !ok || e.Code != "A" {
		t.Errorf("staged staged.txt missing or wrong: %+v ok=%v", e, ok)
	}
	if e, ok := find(st, Untracked, "untracked.txt"); !ok || e.Code != "??" {
		t.Errorf("untracked untracked.txt missing or wrong: %+v ok=%v", e, ok)
	}
}

func TestReadStatusNotARepo(t *testing.T) {
	if _, err := ReadStatus(t.TempDir()); err == nil {
		t.Fatal("expected an error outside a repository")
	}
}

func TestDiff(t *testing.T) {
	dir := repo(t)
	write(t, dir, "committed.txt", "changed\n") // replaces base: -base +changed
	write(t, dir, "untracked.txt", "whole file is new\n")

	diff := Diff(dir, Entry{Section: Unstaged, Path: "committed.txt", Code: "M"})
	if !strings.Contains(diff, "+changed") || !strings.Contains(diff, "-base") {
		t.Errorf("worktree diff wrong:\n%s", diff)
	}

	diff = Diff(dir, Entry{Section: Untracked, Path: "untracked.txt", Code: "??"})
	if !strings.Contains(diff, "+whole file is new") {
		t.Errorf("untracked diff should show the file as added:\n%s", diff)
	}

	write(t, dir, "staged.txt", "staged\n")
	gitRun(t, dir, "add", "staged.txt")
	diff = Diff(dir, Entry{Section: Staged, Path: "staged.txt", Code: "A"})
	if !strings.Contains(diff, "+staged") {
		t.Errorf("staged diff wrong:\n%s", diff)
	}
}

func TestDiffEmpty(t *testing.T) {
	dir := repo(t)
	if diff := Diff(dir, Entry{Section: Unstaged, Path: "committed.txt", Code: "M"}); diff != "" {
		t.Errorf("clean file diff = %q, want empty", diff)
	}
}
