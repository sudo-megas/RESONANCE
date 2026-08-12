package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- loadManifest / sanitizeManifestApps --------------------------------

// TestLoadManifest_RejectsHostileAppName plants a manifest.json — as if
// reached via AdoptVaultPath from a foreign or hostile vault — whose app
// Name is itself a path-traversal payload, not just one of its file paths.
// Every vault-side path this codebase builds is filepath.Join(vaultPath,
// app.Name, ...); without sanitizing app.Name at load time, that join
// escapes the vault regardless of any per-file homeRelative check further
// down each call site.
func TestLoadManifest_RejectsHostileAppName(t *testing.T) {
	vault := t.TempDir()
	m := Manifest{
		Version: manifestVersion,
		Apps: []ManifestApp{
			{Name: "../../../../tmp/escaped", Files: []ManifestFile{{Path: "secret.txt"}}},
			{Name: "safe-app", Files: []ManifestFile{{Path: "a.txt"}}},
		},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath(vault), data, 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadManifest(vault)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if len(loaded.Apps) != 1 || loaded.Apps[0].Name != "safe-app" {
		t.Fatalf("expected only the safe app to survive sanitization, got %+v", loaded.Apps)
	}
}

// TestUpdateFromSource_RejectsHostileAppName is the end-to-end version of
// the above: a hostile app Name must never be actionable through the real
// App-level API, not just filtered out of a raw loadManifest call.
func TestUpdateFromSource_RejectsHostileAppName(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const hostileName = "../../../../tmp/resonance-escape-test"

	m := Manifest{Version: manifestVersion, Apps: []ManifestApp{
		{Name: hostileName, Files: []ManifestFile{{Path: "secret.txt"}}},
	}}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath(vault), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "secret.txt"), []byte("victim-data"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.UpdateFromSource(hostileName); err == nil {
		t.Fatal("expected an error — a manifest entry with a path-traversal Name must never be actionable")
	}

	loaded, err := loadManifest(vault)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if len(loaded.Apps) != 0 {
		t.Fatalf("hostile app name should have been dropped on load, got %+v", loaded.Apps)
	}
}

// TestLoadManifest_DedupesDuplicateAppNames covers the sibling bug: two app
// entries sharing a name (case-insensitively, matching AddApp's own
// uniqueness check) used to resolve to whichever one every "find by name"
// loop happened to hit first — acting on the second silently mutated the
// first's on-disk files under the hood.
func TestLoadManifest_DedupesDuplicateAppNames(t *testing.T) {
	vault := t.TempDir()
	m := Manifest{Version: manifestVersion, Apps: []ManifestApp{
		{Name: "myapp", Files: []ManifestFile{{Path: "first.txt"}}},
		{Name: "MyApp", Files: []ManifestFile{{Path: "second.txt"}}},
	}}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath(vault), data, 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadManifest(vault)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if len(loaded.Apps) != 1 || loaded.Apps[0].Files[0].Path != "first.txt" {
		t.Fatalf("expected only the first occurrence to survive, got %+v", loaded.Apps)
	}
}

// --- GetMirrorRows --------------------------------------------------------

// TestGetMirrorRows_PropagatesUnreachableVaultError catches the bug where an
// unplugged drive, a stale saved path, or a corrupt manifest.json rendered
// identically to a genuinely empty, freshly-initialized vault — silently
// hiding "your backup is unreachable" behind what looked like a healthy,
// empty one.
func TestGetMirrorRows_PropagatesUnreachableVaultError(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", config)

	a := NewApp()
	missingVault := filepath.Join(t.TempDir(), "does-not-exist")
	if err := a.SaveSettings(Settings{Theme: defaultTheme, VaultPath: missingVault}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	rows, err := a.GetMirrorRows()
	if err == nil {
		t.Fatal("expected an error for an unreachable vault path, not a silent empty list")
	}
	if rows != nil {
		t.Fatalf("rows should be nil alongside an error, got %+v", rows)
	}
}

// --- UpdateFromSource: symlink write-through -----------------------------

// TestUpdateFromSource_RefusesToFollowSymlinkAtVaultDestination simulates
// an attacker who had write access to the vault at some point (a realistic
// precondition — vaults are designed to live on removable drives and be
// adopted or synced across machines): the tracked file's vault-side path is
// a symlink pointing outside the vault entirely. A routine Update must
// never write through it.
func TestUpdateFromSource_RefusesToFollowSymlinkAtVaultDestination(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const appName = "testapp"

	target := filepath.Join(t.TempDir(), "outside-target.txt")
	if err := os.WriteFile(target, []byte("must-not-be-overwritten"), 0644); err != nil {
		t.Fatal(err)
	}
	vaultFile := filepath.Join(vault, appName, "tracked.txt")
	if err := os.MkdirAll(filepath.Dir(vaultFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, vaultFile); err != nil {
		t.Fatal(err)
	}

	saveTestManifest(t, vault, appName, []ManifestFile{{Path: "tracked.txt"}})
	sourcePath := filepath.Join(home, "tracked.txt")
	if err := os.WriteFile(sourcePath, []byte("attacker-controlled-live-content"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.UpdateFromSource(appName); err != nil {
		t.Fatalf("UpdateFromSource: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil || string(got) != "must-not-be-overwritten" {
		t.Fatalf("the symlink target outside the vault was written through: got %q, err %v", got, err)
	}
	info, err := os.Lstat(vaultFile)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("vaultFile should now be a regular file, mode=%v err=%v", info.Mode(), err)
	}
	vaultGot, err := os.ReadFile(vaultFile)
	if err != nil || string(vaultGot) != "attacker-controlled-live-content" {
		t.Fatalf("vaultFile should hold the freshly-copied content, got %q, err %v", vaultGot, err)
	}
}

// --- fileDriftRow -------------------------------------------------------

// TestFileDriftRow_RejectsPathTraversal plants a real file outside $HOME
// whose checksum/size exactly matches what a hostile manifest entry
// claims. Without the homeRelative guard, fileDriftRow would happily stat
// and hash that outside file and report State "ok" -- a false "everything
// matches" for a path that was never supposed to be read at all. This is
// what makes the test meaningful: "missing" must come from the guard
// rejecting the path, not merely from the target not existing.
func TestFileDriftRow_RejectsPathTraversal(t *testing.T) {
	home := t.TempDir()
	const hostilePath = "../../etc/hostile"

	outsideHome := filepath.Join(home, filepath.FromSlash(hostilePath))
	if err := os.MkdirAll(filepath.Dir(outsideHome), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideHome, []byte("outside-home-secret"), 0644); err != nil {
		t.Fatal(err)
	}
	size, checksum, _, err := vaultFileMeta(outsideHome)
	if err != nil {
		t.Fatal(err)
	}

	row := fileDriftRow(home, ManifestFile{Path: hostilePath, Checksum: checksum, Size: size})
	if row.State != "missing" {
		t.Fatalf("State = %q, want missing for a path-traversal entry (must never read outside $HOME)", row.State)
	}
	if row.SourceModified != "" {
		t.Fatalf("SourceModified should stay empty for a rejected entry, got %q", row.SourceModified)
	}
}

// --- UpdateFromSource -----------------------------------------------------

// TestUpdateFromSource_RejectsPathTraversal simulates a crafted or foreign
// manifest.json -- e.g. one reachable via AdoptVaultPath -- whose file path
// escapes $HOME. Every other path-consuming function in this codebase
// (AddApp, RestoreApp, UndoRestore, GetDiffPair) re-validates manifest
// paths through homeRelative before touching disk; UpdateFromSource must
// too, or a hostile entry gets silently read from outside $HOME and copied
// into the vault.
func TestUpdateFromSource_RejectsPathTraversal(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const appName = "testapp"
	const hostilePath = "../../etc/hostile-target"

	saveTestManifest(t, vault, appName, []ManifestFile{{Path: hostilePath}})

	outsideHome := filepath.Join(home, filepath.FromSlash(hostilePath))
	if err := os.MkdirAll(filepath.Dir(outsideHome), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideHome, []byte("outside-home-secret"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := a.UpdateFromSource(appName)
	if err != nil {
		t.Fatalf("UpdateFromSource: %v", err)
	}
	if len(result.Updated) != 0 {
		t.Fatalf("a path-traversal entry must never be read and copied into the vault, got Updated=%v", result.Updated)
	}
	if len(result.Missing) != 1 || result.Missing[0] != hostilePath {
		t.Fatalf("expected the hostile entry to be rejected, got %+v", result)
	}

	vaultFile := filepath.Join(vault, appName, filepath.FromSlash(hostilePath))
	if _, err := os.Stat(vaultFile); !os.IsNotExist(err) {
		t.Fatalf("the hostile source should never have been copied into the vault, stat err = %v", err)
	}
}
