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

// --- folders ------------------------------------------------------------

// TestAddApp_AddsFolderAndContents is the reported v1.2.1 CRITICAL: picking
// a directory used to hit "is not a regular file" and abandon the entire
// add, including the ordinary files picked alongside it.
func TestAddApp_AddsFolderAndContents(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	dir := filepath.Join(home, ".config", "nvim", "lua")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opts.lua"), []byte("opts"), 0644); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(home, ".vimrc")
	if err := os.WriteFile(loose, []byte("set nocompatible"), 0644); err != nil {
		t.Fatal(err)
	}

	// A folder and a plain file together — the mix the two pickers produce.
	if err := a.AddApp("vim", []string{filepath.Join(home, ".config", "nvim"), loose}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	app := m.Apps[0]
	if len(app.Dirs) != 1 || app.Dirs[0] != ".config/nvim" {
		t.Fatalf("Dirs = %v, want [.config/nvim]", app.Dirs)
	}
	if len(app.Files) != 2 {
		t.Fatalf("Files = %+v, want the folder's contents plus the loose file", app.Files)
	}
	for _, rel := range []string{".config/nvim/lua/opts.lua", ".vimrc"} {
		if _, err := os.Stat(filepath.Join(vault, "vim", filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s was not copied into the vault: %v", rel, err)
		}
	}
}

// TestAddApp_DeduplicatesOverlappingPicks covers picking both a folder and a
// file inside it — trivially done across two dialogs.
func TestAddApp_DeduplicatesOverlappingPicks(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	dir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(dir, "init.lua")
	if err := os.WriteFile(inner, []byte("-- init"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := a.AddApp("nvim", []string{dir, inner}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}
	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 1 {
		t.Fatalf("Files = %+v, want one deduplicated entry", m.Apps[0].Files)
	}
}

// TestAddApp_RefusesFolderStraddlingVault covers both directions of the
// vault-inside-$HOME hazard. A folder containing the vault would enumerate
// the vault into itself; a folder inside it would start tracking the vault's
// own stored copies as though they were live system files.
func TestAddApp_RefusesFolderStraddlingVault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dots := filepath.Join(home, "dots")
	vault := filepath.Join(dots, "vault")
	if err := os.MkdirAll(vault, 0755); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	if err := a.SaveSettings(Settings{Theme: defaultTheme, VaultPath: vault}); err != nil {
		t.Fatal(err)
	}

	if err := a.AddApp("outer", []string{dots}); err == nil {
		t.Fatal("a folder containing the vault must be refused")
	}
	if err := a.AddApp("inner", []string{vault}); err == nil {
		t.Fatal("a folder inside the vault must be refused")
	}
}

// --- orphans ------------------------------------------------------------

// TestScanVaultOrphans finds vault content no manifest entry accounts for —
// what an interrupted copy leaves behind, invisible to every other view and
// duplicated by every later Copy or Move.
func TestScanVaultOrphans(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, vault, "bash", ".bashrc", "live")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	report, err := a.ScanVaultOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 0 {
		t.Fatalf("a healthy vault has no orphans, got %v", report.Files)
	}

	// Left by a copy that died partway through.
	stray := filepath.Join(vault, "bash", ".bash_profile")
	if err := os.WriteFile(stray, []byte("stranded"), 0644); err != nil {
		t.Fatal(err)
	}
	// Litter from an interrupted atomic write — not a lost backup, and
	// reporting it would cry wolf.
	if err := os.WriteFile(filepath.Join(vault, "bash", ".tmp-123"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	report, err = a.ScanVaultOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0] != "bash/.bash_profile" {
		t.Fatalf("orphans = %v, want exactly [bash/.bash_profile]", report.Files)
	}
	if report.Bytes == 0 {
		t.Fatal("orphan size should be reported")
	}
}
