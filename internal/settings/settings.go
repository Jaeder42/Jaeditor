// Package settings persists jaed's user preferences in
// ~/.config/jaed/settings.json, next to agents.conf.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings are jaed's persisted user preferences.
type Settings struct {
	Theme string `json:"theme"`
	Agent string `json:"agent"`
	// Stats shows the top CPU/memory resource bar.
	Stats bool `json:"stats"`
}

// Default returns the built-in preferences.
func Default() Settings {
	return Settings{Theme: "everforest", Agent: "pi", Stats: true}
}

// path is the settings file: ~/.config/jaed/settings.json.
func path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "jaed", "settings.json"), nil
}

// Load reads the settings file over the defaults. A missing file is not
// an error; a malformed one falls back to the defaults.
func Load() Settings {
	s := Default()
	file, err := path()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return s
	}
	var loaded struct {
		Theme string `json:"theme"`
		Agent string `json:"agent"`
		// Stats is a pointer so an older settings file without the field
		// keeps the default rather than reading as "off".
		Stats *bool `json:"stats"`
	}
	if json.Unmarshal(data, &loaded) == nil {
		if loaded.Theme != "" {
			s.Theme = loaded.Theme
		}
		if loaded.Agent != "" {
			s.Agent = loaded.Agent
		}
		if loaded.Stats != nil {
			s.Stats = *loaded.Stats
		}
	}
	return s
}

// Save writes the settings file, creating the directory if needed.
func Save(s Settings) error {
	file, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}
