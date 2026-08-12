package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRestoreApp_RefusesToReadThroughSymlinkAtVaultSource is the read-side
// counterpart to drift_test.go's write-through test: a symlink planted at
// the vault-side path (by whoever last had write access to the vault) must
// never be opened and copied onto a live $HOME file — that would silently
// disclose the symlink's target, which could be anywhere on the machine.
func TestRestoreApp_RefusesToReadThroughSymlinkAtVaultSource(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const appName = "testapp"

	secret := filepath.Join(t.TempDir(), "secret-outside-vault.txt")
	if err := os.WriteFile(secret, []byte("must-not-be-disclosed"), 0644); err != nil {
		t.Fatal(err)
	}
	vaultFile := filepath.Join(vault, appName, "tracked.txt")
	if err := os.MkdirAll(filepath.Dir(vaultFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, vaultFile); err != nil {
		t.Fatal(err)
	}
	saveTestManifest(t, vault, appName, []ManifestFile{{Path: "tracked.txt"}})

	result, err := a.RestoreApp(appName)
	if err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("expected the symlinked vault entry to fail closed, got %+v", result)
	}
	destPath := filepath.Join(home, "tracked.txt")
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatalf("destPath should never have been created from a symlinked vault source, stat err = %v", err)
	}
}

// TestGetDiffPair_RefusesToReadThroughSymlinkAtVaultSource covers the same
// vulnerability class at GetDiffPair's vault-side read — the restore
// preview's expandable diff must not silently ship a symlink target's
// content across the IPC boundary as the "vault" side of a diff.
func TestGetDiffPair_RefusesToReadThroughSymlinkAtVaultSource(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	const appName = "testapp"

	secret := filepath.Join(t.TempDir(), "secret-outside-vault.txt")
	if err := os.WriteFile(secret, []byte("must-not-be-disclosed"), 0644); err != nil {
		t.Fatal(err)
	}
	vaultFile := filepath.Join(vault, appName, "tracked.txt")
	if err := os.MkdirAll(filepath.Dir(vaultFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, vaultFile); err != nil {
		t.Fatal(err)
	}
	saveTestManifest(t, vault, appName, []ManifestFile{{Path: "tracked.txt"}})

	if _, err := a.GetDiffPair(appName, "tracked.txt"); err == nil {
		t.Fatal("expected an error — GetDiffPair must refuse to read through a vault-side symlink")
	}
}
