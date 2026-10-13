package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureLayout(t *testing.T) {
	cwd := t.TempDir()
	if err := EnsureLayout(cwd); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{ToolsDir(cwd), SkillsDir(cwd), TmpDir(cwd)} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s should exist as a directory", dir)
		}
	}
}

func TestEnsureLayoutExcludesFromGit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chess")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	if err := EnsureLayout(dir); err != nil {
		t.Fatal(err)
	}
	// Running twice must not duplicate the line.
	if err := EnsureLayout(dir); err != nil {
		t.Fatal(err)
	}

	exclude, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(exclude), ".jaed/") != 1 {
		t.Errorf(".jaed/ should appear exactly once, have:\n%s", exclude)
	}

	// git confirms the folder is ignored.
	check := exec.Command("git", "check-ignore", "-q", ".jaed/tools/x.js")
	check.Dir = dir
	if err := check.Run(); err != nil {
		t.Errorf("git should ignore .jaed contents: %v", err)
	}
}

func TestEnsureLayoutOutsideGit(t *testing.T) {
	cwd := t.TempDir() // no repository
	if err := EnsureLayout(cwd); err != nil {
		t.Fatalf("outside a repository EnsureLayout should still create the folders: %v", err)
	}
	if _, err := os.Stat(ToolsDir(cwd)); err != nil {
		t.Errorf("tools folder should exist without git: %v", err)
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "block tag",
			src:  "/**\n * @description Count TODO comments in the workspace.\n */\nexport default () => {}",
			want: "Count TODO comments in the workspace.",
		},
		{
			name: "multiline value",
			src:  "/**\n * @description Summarize the repository.\n * Continues here.\n * @param x\n */\n",
			want: "Summarize the repository. Continues here.",
		},
		{
			name: "other tags first",
			src:  "/**\n * @param foo bar\n * @description Late tag wins.\n */\n",
			want: "Late tag wins.",
		},
		{
			name: "no block comment uses leading slash comment",
			src:  "// Quick note about this tool\nconst x = 1",
			want: "Quick note about this tool",
		},
		{
			name: "missing description",
			src:  "export default () => {}",
			want: "",
		},
		{
			name: "description never swallows describe",
			src:  "/**\n * @describe not it\n */\n",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Describe(tc.src); got != tc.want {
				t.Errorf("Describe() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestList(t *testing.T) {
	cwd := t.TempDir()
	if err := EnsureLayout(cwd); err != nil {
		t.Fatal(err)
	}
	write(t, ToolsDir(cwd), "b-tool.js", "/**\n * @description The B tool.\n */\nexport default () => 1")
	write(t, ToolsDir(cwd), "a-tool.js", "/**\n * @description The A tool.\n */\nexport default () => 2")
	write(t, ToolsDir(cwd), "notes.txt", "ignored")
	write(t, ToolsDir(cwd), "nested", "ignored")

	list, err := List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("List() returned %d tools, want 2: %+v", len(list), list)
	}
	if list[0].Name != "a-tool" || list[1].Name != "b-tool" {
		t.Errorf("tools should be sorted by name, got %q then %q", list[0].Name, list[1].Name)
	}
	if list[0].Description != "The A tool." {
		t.Errorf("description not parsed: %q", list[0].Description)
	}
	if !strings.Contains(list[1].Code, "() => 1") {
		t.Errorf("code not read: %q", list[1].Code)
	}
}

func TestListMissingFolder(t *testing.T) {
	list, err := List(t.TempDir())
	if err != nil {
		t.Fatalf("missing folder should not error: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("missing folder should list nothing, got %d", len(list))
	}
}
