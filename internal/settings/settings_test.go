package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := Load()
	if got != Default() {
		t.Fatalf("Load() = %+v, want defaults %+v", got, Default())
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := Settings{Theme: "catppuccin", Agent: "opencode"}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	if file := filepath.Join(os.Getenv("HOME"), ".config", "jaed", "settings.json"); !fileExists(file) {
		t.Fatalf("expected settings file at %s", file)
	}
	if got := Load(); got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadMalformedFallsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "jaed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got != Default() {
		t.Fatalf("malformed file should yield defaults, got %+v", got)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
