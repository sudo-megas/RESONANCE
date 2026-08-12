package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
