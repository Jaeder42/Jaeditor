package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sandboxRuntime is the runtime shipped in the repo (this package lives in
// internal/tools).
func sandboxRuntime() string {
	return filepath.Join("..", "..", "assets", "jaed-tools")
}

// requireNode skips the test unless a Node with the permission model is
// available.
func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	if err := exec.Command(node, "--permission", "-e", "").Run(); err != nil {
		t.Skipf("node lacks the permission model: %v", err)
	}
	return node
}

// runSandbox runs a tool the way the agent plugins do and returns the
// parsed result line.
func runSandbox(t *testing.T, node, workspace, tool, args string) map[string]any {
	t.Helper()
	runtime, err := filepath.Abs(sandboxRuntime())
	if err != nil {
		t.Fatalf("runtime path: %v", err)
	}
	if runtime, err = filepath.EvalSymlinks(runtime); err != nil {
		t.Fatalf("runtime path: %v", err)
	}
	ws, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("workspace path: %v", err)
	}
	cmd := exec.Command(node,
		"--permission",
		"--allow-fs-read="+ws,
		"--allow-fs-read="+runtime,
		"--allow-fs-write="+filepath.Join(ws, ".jaed", "tmp"),
		"--max-old-space-size=256",
		filepath.Join(runtime, "run.mjs"),
		tool,
		args,
	)
	cmd.Dir = ws
	out, _ := cmd.Output() // a tool failure exits nonzero but still prints JSON
	line := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	if line == "" {
		t.Fatalf("no result line from sandbox (stderr suppressed)")
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, line)
	}
	return result
}

func TestSandboxRun(t *testing.T) {
	node := requireNode(t)
	cwd := t.TempDir()
	if err := EnsureLayout(cwd); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "data.txt"), []byte("workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ToolsDir(cwd), "ok.js"), []byte(`
/**
 * @description Reads a file and writes scratch.
 */
import { readFile, writeTmp } from "jaed";
export default async (input, jaed) => {
  const data = readFile("data.txt").trim();
  const path = writeTmp("out.txt", "hi");
  return data + ":" + (path ? "wrote" : "?");
};
`), 0o644); err != nil {
		t.Fatal(err)
	}

	result := runSandbox(t, node, cwd, "ok", "{}")
	if result["ok"] != true {
		t.Fatalf("tool should succeed, got %+v", result)
	}
	if out, _ := result["output"].(string); !strings.Contains(out, "workspace:wrote") {
		t.Errorf("unexpected output: %q", out)
	}

	// The scratch file landed in .jaed/tmp.
	if _, err := os.Stat(filepath.Join(TmpDir(cwd), "out.txt")); err != nil {
		t.Errorf("writeTmp should have written into .jaed/tmp: %v", err)
	}
}

func TestSandboxDeniesEscape(t *testing.T) {
	node := requireNode(t)
	cwd := t.TempDir()
	if err := EnsureLayout(cwd); err != nil {
		t.Fatal(err)
	}
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(ToolsDir(cwd), name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("read-outside.js", `import * as fs from "node:fs";
export default async () => fs.readFileSync("/etc/passwd", "utf8");`)
	write("proc.js", `import { execSync } from "node:child_process";
export default async () => execSync("id").toString();`)
	write("unlisted.js", `import "left-pad";
export default async () => "no";`)

	for _, tc := range []struct{ name, want string }{
		{"read-outside", "restricted"},
		{"proc", "not available to tools"},
		{"unlisted", "not on the allowlist"},
	} {
		result := runSandbox(t, node, cwd, tc.name, "{}")
		if result["ok"] != false {
			t.Errorf("%s should be denied, got %+v", tc.name, result)
			continue
		}
		if err, _ := result["error"].(string); !strings.Contains(err, tc.want) {
			t.Errorf("%s error %q should mention %q", tc.name, err, tc.want)
		}
	}
}
