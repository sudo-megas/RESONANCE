package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const defaultTheme = "default-dark"

// Settings is the persisted user configuration, stored at
// $XDG_CONFIG_HOME/resonance/settings.json (~/.config/resonance/settings.json).
type Settings struct {
	Theme     string `json:"theme"`
	VaultPath string `json:"vaultPath"`
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "resonance", "settings.json"), nil
}

// GetSettings returns the persisted settings, falling back to defaults if
// none have been saved yet or the file can't be read.
func (a *App) GetSettings() Settings {
	path, err := settingsPath()
	if err != nil {
		return Settings{Theme: defaultTheme}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{Theme: defaultTheme}
	}

	var s Settings
	if err := json.Unmarshal(data, &s); err != nil || s.Theme == "" {
		return Settings{Theme: defaultTheme}
	}
	return s
}

// SaveSettings persists the given settings to disk, creating the config
// directory on first save.
func (a *App) SaveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return writeFileAtomic(dir, path, data, 0644)
}
