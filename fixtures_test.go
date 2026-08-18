package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The fixtures seven test files share. They lived in snapshot_test.go until
// v1.4.0 deleted it, which was never the right home for them — they set up a
// vault and a home directory, and had nothing to do with undo beyond having
// been written at the same time as it.

// newRestoreFixture isolates HOME, XDG_STATE_HOME, and XDG_CONFIG_HOME to
// fresh per-test directories, then persists a vault path through the real
// SaveSettings so the code under test exercises its actual production path
// rather than a mock.
func newRestoreFixture(t *testing.T) (app *App, home, vault string) {
	t.Helper()
	home = t.TempDir()
	vault = t.TempDir()
	state := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", config)

	app = NewApp()
	if err := app.SaveSettings(Settings{Theme: defaultTheme, VaultPath: vault}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	return app, home, vault
}

func seedVaultFile(t *testing.T, vault, appName, relPath, content string) ManifestFile {
	t.Helper()
	vaultFile := filepath.Join(vault, appName, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(vaultFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vaultFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	size, checksum, backedUpAt, err := vaultFileMeta(vaultFile)
	if err != nil {
		t.Fatal(err)
	}
	return ManifestFile{Path: relPath, Size: size, Checksum: checksum, BackedUpAt: backedUpAt}
}

func saveTestManifest(t *testing.T, vault, appName string, files []ManifestFile) {
	t.Helper()
	m := Manifest{Version: manifestVersion, Apps: []ManifestApp{{Name: appName, Files: files}}}
	if err := saveManifest(vault, m); err != nil {
		t.Fatal(err)
	}
}
