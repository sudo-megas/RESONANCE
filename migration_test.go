package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateVault_RejectsDestinationInsideSource covers a destination
// that's an empty subfolder nested inside the current vault — fully
// reachable through the ordinary folder picker (navigate into the vault,
// pick or create an empty subfolder), and something ProbeVaultPath's
// IsEmpty check alone can't distinguish from any other empty folder.
// Without the guard, copyTree would walk into the copy it just made of
// itself and recurse without bound.
func TestMigrateVault_RejectsDestinationInsideSource(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", config)

	vault := t.TempDir()
	a := NewApp()
	if err := a.SaveSettings(Settings{Theme: defaultTheme, VaultPath: vault}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	saveTestManifest(t, vault, "app", []ManifestFile{{Path: "a.txt"}})

	nested := filepath.Join(vault, "nested-empty-dir")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if err := a.CopyVaultTo(nested); err == nil {
		t.Fatal("expected CopyVaultTo to reject a destination nested inside the current vault")
	}
	if err := a.MoveVaultTo(nested); err == nil {
		t.Fatal("expected MoveVaultTo to reject a destination nested inside the current vault")
	}
}
