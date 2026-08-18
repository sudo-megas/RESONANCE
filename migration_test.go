package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestMigrateVault_MissingOldVaultReportsSource covers the v1.2.1 bug
// report. copyTree is filepath.WalkDir(src) and WalkDir lstats its root, so
// a vanished OLD vault used to surface as "copy to new vault failed, old
// vault untouched: lstat <old>: no such file or directory" — which names the
// source but reads as though the destination were at fault, and leaks a
// syscall name into the UI. The source must be checked before anything is
// created anywhere.
func TestMigrateVault_MissingOldVaultReportsSource(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	if err := os.RemoveAll(vault); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(home, "new-vault")
	err := a.CopyVaultTo(dest)
	if err == nil {
		t.Fatal("expected CopyVaultTo to fail when the current vault is gone")
	}
	if strings.Contains(err.Error(), "lstat") {
		t.Fatalf("error still leaks a syscall name: %v", err)
	}
	if !strings.Contains(err.Error(), vault) {
		t.Fatalf("error should name the missing vault %q, got: %v", vault, err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("destination must not be created when the source vault is missing")
	}
}

// TestMigrateVault_RefusesHomeAsDestination is the regression for the
// blocker that dropping the empty-folder rule opened up. Nothing else in
// migrateVault constrains the destination, and Move ends in
// os.RemoveAll(oldPath) — so pointing the vault at $HOME and later moving it
// away would delete the user's entire home directory. The folder picker's
// default landing directory is $HOME, which is what makes this reachable
// rather than theoretical.
func TestMigrateVault_RefusesHomeAsDestination(t *testing.T) {
	a, home, _ := newRestoreFixture(t)
	canary := filepath.Join(home, "precious.txt")
	if err := os.WriteFile(canary, []byte("do not delete"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{home, filepath.Dir(home)} {
		if err := a.MoveVaultTo(target); err == nil {
			t.Fatalf("MoveVaultTo(%q) must be refused", target)
		}
		if err := a.CopyVaultTo(target); err == nil {
			t.Fatalf("CopyVaultTo(%q) must be refused", target)
		}
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("home contents were destroyed: %v", err)
	}
}

// TestMigrateVault_RefusesSymlinkToHome covers the same blocker reached
// through a symlink. Every containment check is filepath.Rel on strings, so
// the guard has to run on the resolved path or ~/docs -> $HOME walks
// straight past it.
func TestMigrateVault_RefusesSymlinkToHome(t *testing.T) {
	a, home, _ := newRestoreFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := a.MoveVaultTo(alias); err == nil {
		t.Fatal("MoveVaultTo through a symlink to $HOME must be refused")
	}
}

// TestMigrateVault_RefusesStateDirAsDestination guards the undo snapshots.
// A vault at ~/.local/state would have Move delete every snapshot for every
// app — the safety net removing itself.
func TestMigrateVault_RefusesStateDirAsDestination(t *testing.T) {
	a, _, _ := newRestoreFixture(t)
	stateDir, err := resonanceStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Copy, not Move: Move into a non-empty folder is already refused for a
	// different reason, so asserting on it would pass even with this guard
	// removed and prove nothing.
	if err := a.CopyVaultTo(filepath.Dir(stateDir)); err == nil {
		t.Fatal("a folder containing RESONANCE's own state must be refused")
	}
}

// TestMigrateVault_CopyIntoNonEmptyMoveRefused pins the exact split the
// maker's ruling implies. Choosing a folder that already holds files is the
// user's business, so Copy is allowed into one. Move is not — it finishes by
// deleting whatever it moved away from, so allowing it here would set up a
// later Move to destroy files that were never part of the vault.
func TestMigrateVault_CopyIntoNonEmptyMoveRefused(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	saveTestManifest(t, vault, "app", []ManifestFile{})

	dest := filepath.Join(home, "already-used")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	bystander := filepath.Join(dest, "unrelated.txt")
	if err := os.WriteFile(bystander, []byte("not mine"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := a.MoveVaultTo(dest); err == nil {
		t.Fatal("Move into a non-empty folder must be refused")
	}
	if err := a.CopyVaultTo(dest); err != nil {
		t.Fatalf("Copy into a non-empty folder must be allowed: %v", err)
	}
	if _, err := os.Stat(bystander); err != nil {
		t.Fatalf("pre-existing file was destroyed by Copy: %v", err)
	}
	if got := a.GetSettings().VaultPath; got != dest {
		t.Fatalf("VaultPath = %q, want %q", got, dest)
	}
}

// TestMigrateVault_RefusesSourceInsideDestination covers the direction the
// old one-way lexical check missed. It was unreachable only because the
// empty-folder rule rejected it first.
func TestMigrateVault_RefusesSourceInsideDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	parent := filepath.Join(home, "parent")
	vault := filepath.Join(parent, "vault")
	if err := os.MkdirAll(vault, 0755); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	if err := a.SaveSettings(Settings{Theme: defaultTheme, VaultPath: vault}); err != nil {
		t.Fatal(err)
	}
	if err := a.CopyVaultTo(parent); err == nil {
		t.Fatal("a destination that contains the current vault must be refused")
	}
}

// TestCheckVaultDir_SeparatesUnreachableFromUnparseable is the regression
// for a dead end the recovery UI would otherwise have introduced.
//
// ProbeVaultPath errors both when the directory can't be read AND when
// manifest.json exists but won't parse. Driving recovery from that composite
// would put a hand-edited manifest behind a modal headed "vault not found" —
// false — that Escape cannot close, and every way out of it that keeps the
// user's vault would fail: re-probing hits the same parse error, and
// recreating the folder refuses on it too. The only working button would be
// the one that abandons the vault. That state is a dismissable message today,
// so conflating the two would be a strict regression.
func TestCheckVaultDir_SeparatesUnreachableFromUnparseable(t *testing.T) {
	a, _, vault := newRestoreFixture(t)

	if status := a.CheckVaultDir(); !status.Reachable || !status.ManifestReadable {
		t.Fatalf("a healthy vault should be both reachable and readable, got %+v", status)
	}

	// Corrupt the manifest: the folder is still perfectly reachable.
	if err := os.WriteFile(manifestPath(vault), []byte("{ not json"), 0644); err != nil {
		t.Fatal(err)
	}
	status := a.CheckVaultDir()
	if !status.Reachable {
		t.Fatal("an unparseable manifest must not make the vault folder read as unreachable — that is what strands the user")
	}
	if status.ManifestReadable {
		t.Fatal("the manifest is corrupt and should be reported as such")
	}
	if status.Message == "" {
		t.Fatal("the corrupt-manifest case needs its own honest message")
	}

	// Now the folder really is gone — this is the case recovery is for.
	if err := os.RemoveAll(vault); err != nil {
		t.Fatal(err)
	}
	if status := a.CheckVaultDir(); status.Reachable {
		t.Fatal("a deleted vault folder must report as unreachable")
	}
}

// TestLoadManifest_PreV121ManifestWithoutDirs covers backward compatibility:
// a manifest written before tracked folders existed must keep working
// untouched, with no Dirs and no rewrite.
func TestLoadManifest_PreV121ManifestWithoutDirs(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, vault, "bash", ".bashrc", "live")

	// Written by hand in the v1.2.0 shape — no "dirs" key at all.
	legacy := `{"version":1,"apps":[{"name":"bash","files":[{"path":".bashrc","size":` +
		fmt.Sprintf("%d", f.Size) + `,"checksum":"` + f.Checksum + `","backedUpAt":"` + f.BackedUpAt + `"}]}]}`
	if err := os.WriteFile(manifestPath(vault), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatalf("a pre-v1.2.1 manifest must still load: %v", err)
	}
	if len(m.Apps) != 1 || len(m.Apps[0].Files) != 1 {
		t.Fatalf("legacy manifest was not read faithfully: %+v", m.Apps)
	}
	if m.Apps[0].Dirs != nil {
		t.Fatalf("Dirs should stay nil for a legacy manifest, got %v", m.Apps[0].Dirs)
	}

	rows, err := a.GetMirrorRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].Files) != 1 || rows[0].Files[0].State != "ok" {
		t.Fatalf("legacy manifest should render exactly as before: %+v", rows)
	}
	if rows[0].Drifted {
		t.Fatal("a healthy legacy app must not read as drifted")
	}
}

// TestUseVaultPath_CreatesOneLevelOnly is the "administrative mkdir" the
// report asked for, and the limit on it. The reported path was
// /run/media/megas/DOTFILES/TEST with only TEST missing, so one level is
// what actually unsticks the user — while MkdirAll would have invented the
// whole chain over an unmounted mountpoint and silently written every future
// backup to a directory that disappears when the drive is plugged in.
func TestUseVaultPath_CreatesOneLevelOnly(t *testing.T) {
	a, home, _ := newRestoreFixture(t)

	fresh := filepath.Join(home, "brand-new-vault")
	if err := a.UseVaultPath(fresh); err != nil {
		t.Fatalf("UseVaultPath on a missing folder with an existing parent: %v", err)
	}
	if info, err := os.Stat(fresh); err != nil || !info.IsDir() {
		t.Fatalf("folder was not created: %v", err)
	}
	if got := a.GetSettings().VaultPath; got != fresh {
		t.Fatalf("VaultPath = %q, want %q", got, fresh)
	}

	deep := filepath.Join(home, "no", "such", "chain")
	if err := a.UseVaultPath(deep); err == nil {
		t.Fatal("UseVaultPath must refuse when the parent folder is missing too")
	}
	if _, err := os.Stat(filepath.Join(home, "no")); err == nil {
		t.Fatal("no part of a missing chain may be created")
	}
}
