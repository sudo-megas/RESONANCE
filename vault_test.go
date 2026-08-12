package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAddApp_TrimsNameConsistently covers the gap where validAppName
// trimmed the name to validate it but the untrimmed original was what
// actually got stored — a whitespace-padded name could bypass AddApp's own
// EqualFold duplicate check against an already-stored, already-trimmed name.
func TestAddApp_TrimsNameConsistently(t *testing.T) {
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

	tracked := filepath.Join(home, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := a.AddApp("  myapp  ", []string{tracked}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if len(m.Apps) != 1 || m.Apps[0].Name != "myapp" {
		t.Fatalf("expected the stored name to be trimmed to %q, got %+v", "myapp", m.Apps)
	}
	if _, err := os.Stat(filepath.Join(vault, "myapp", "tracked.txt")); err != nil {
		t.Fatalf("expected the vault-side copy to live under the trimmed name: %v", err)
	}

	if err := a.AddApp("myapp", []string{tracked}); err == nil {
		t.Fatal("expected a duplicate-name rejection once the name is stored trimmed")
	}
}
