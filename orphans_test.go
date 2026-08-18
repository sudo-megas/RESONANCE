package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Orphan removal is the only operation in the program that deletes a file
// nothing in the manifest names. Every test here exists because that
// asymmetry removes the usual safety net: there is no entry to check the
// deletion against afterwards, and nothing to restore from if it was wrong.

func writeVaultJunk(t *testing.T, vault, rel, content string) string {
	t.Helper()
	p := filepath.Join(vault, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRemoveVaultOrphans_DeletesTheOrphanAndPrunesItsDir(t *testing.T) {
	a, _, vault := newRestoreFixture(t)

	kept := seedVaultFile(t, vault, "bash", ".bashrc", "REAL BACKUP")
	saveTestManifest(t, vault, "bash", []ManifestFile{kept})
	junk := writeVaultJunk(t, vault, "ghost/leftover.conf", "JUNK")

	report, err := a.ScanVaultOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0] != "ghost/leftover.conf" {
		t.Fatalf("scan reported %v, want just the orphan", report.Files)
	}

	result, err := a.RemoveVaultOrphans([]string{"ghost/leftover.conf"})
	if err != nil {
		t.Fatalf("RemoveVaultOrphans: %v", err)
	}
	if len(result.RemovedFiles) != 1 {
		t.Fatalf("RemovedFiles = %v, Failed = %v", result.RemovedFiles, result.Failed)
	}
	if _, err := os.Lstat(junk); err == nil {
		t.Fatal("the orphan is still there")
	}
	// The now-empty directory is litter of exactly the same kind.
	if _, err := os.Lstat(filepath.Join(vault, "ghost")); err == nil {
		t.Fatal("the emptied directory should have been pruned")
	}
	if _, err := os.Lstat(filepath.Join(vault, "bash", ".bashrc")); err != nil {
		t.Fatalf("the real backup was destroyed: %v", err)
	}
}

// The guard that matters most. The frontend sends a list built from a scan
// that may be minutes old; if the backend trusted it, a file that became a
// real backup in between would be deleted as though it were still junk.
func TestRemoveVaultOrphans_RefusesAFileTheManifestNowClaims(t *testing.T) {
	a, _, vault := newRestoreFixture(t)

	// Exactly the sequence a stale list produces: junk at scan time...
	writeVaultJunk(t, vault, "bash/.bashrc", "WAS JUNK")
	report, err := a.ScanVaultOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("scan reported %v, want the file to look like an orphan", report.Files)
	}

	// ...and a genuine backup by the time the user hits the button.
	f := seedVaultFile(t, vault, "bash", ".bashrc", "REAL BACKUP NOW")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	result, err := a.RemoveVaultOrphans(report.Files)
	if err != nil {
		t.Fatalf("RemoveVaultOrphans: %v", err)
	}
	if len(result.RemovedFiles) != 0 {
		t.Fatalf("deleted %v — a manifest-backed file must never be swept", result.RemovedFiles)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %v, want the stale path reported back", result.Failed)
	}
	got, err := os.ReadFile(filepath.Join(vault, "bash", ".bashrc"))
	if err != nil {
		t.Fatalf("the backup was deleted: %v", err)
	}
	if string(got) != "REAL BACKUP NOW" {
		t.Fatalf("content = %q, want it untouched", got)
	}
}

// manifest.json is not in any app's file list, so a scan that did not
// special-case it would call the vault's own index an orphan.
func TestRemoveVaultOrphans_RefusesManifestJSON(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "bash", ".bashrc", "x")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	result, err := a.RemoveVaultOrphans([]string{"manifest.json"})
	if err != nil {
		t.Fatalf("RemoveVaultOrphans: %v", err)
	}
	if len(result.RemovedFiles) != 0 {
		t.Fatalf("deleted %v", result.RemovedFiles)
	}
	if _, err := os.Lstat(filepath.Join(vault, "manifest.json")); err != nil {
		t.Fatalf("manifest.json was deleted: %v", err)
	}
}

func TestRemoveVaultOrphans_RefusesPathEscape(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "bash", ".bashrc", "x")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	secret := filepath.Join(home, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0644); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(vault, secret)
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.RemoveVaultOrphans([]string{rel, "../../etc/passwd"})
	if err != nil {
		t.Fatalf("RemoveVaultOrphans: %v", err)
	}
	if len(result.RemovedFiles) != 0 {
		t.Fatalf("deleted %v", result.RemovedFiles)
	}
	if _, err := os.Lstat(secret); err != nil {
		t.Fatalf("a file outside the vault was deleted: %v", err)
	}
}

// A symlink in the vault is reported as the single entry it is, and removing
// it unlinks the link — never the directory it points at. Without unlink(2)'s
// refusal to follow a final component this would empty the target.
func TestRemoveVaultOrphans_UnlinksASymlinkWithoutFollowingIt(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "bash", ".bashrc", "x")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	target := filepath.Join(home, ".config")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(target, "keepme.conf")
	if err := os.WriteFile(live, []byte("LIVE"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(vault, "evil")); err != nil {
		t.Fatal(err)
	}

	report, err := a.ScanVaultOrphans()
	if err != nil {
		t.Fatal(err)
	}
	// The walk must not descend it: seeing "evil/keepme.conf" here would mean
	// the app had catalogued the user's live home folder as vault junk.
	for _, p := range report.Files {
		if p != "evil" {
			t.Fatalf("scan walked through the symlink and reported %v", report.Files)
		}
	}

	if _, err := a.RemoveVaultOrphans([]string{"evil"}); err != nil {
		t.Fatalf("RemoveVaultOrphans: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(vault, "evil")); err == nil {
		t.Fatal("the symlink is still there")
	}
	got, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("the live file behind the symlink was deleted: %v", err)
	}
	if string(got) != "LIVE" {
		t.Fatalf("live content = %q, want it untouched", got)
	}
}

// A vault path that is itself a symlink is an ordinary setup, and the rest of
// the program goes out of its way to keep working for it. WalkDir lstats its
// root, so without resolving it first the scan visits nothing and reports a
// clean vault — an absence that reads as an answer.
func TestRemoveVaultOrphans_WorksWhenVaultRootIsASymlink(t *testing.T) {
	home := t.TempDir()
	realVault := t.TempDir()
	linkVault := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(realVault, linkVault); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	a := NewApp()
	if err := a.SaveSettings(Settings{Theme: defaultTheme, VaultPath: linkVault}); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, realVault, "bash", ".bashrc", "REAL")
	saveTestManifest(t, realVault, "bash", []ManifestFile{f})
	junk := writeVaultJunk(t, realVault, "ghost/leftover.conf", "JUNK")

	report, err := a.ScanVaultOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0] != "ghost/leftover.conf" {
		t.Fatalf("scan reported %v through a symlinked vault root, want the orphan", report.Files)
	}
	if _, err := a.RemoveVaultOrphans([]string{"ghost/leftover.conf"}); err != nil {
		t.Fatalf("RemoveVaultOrphans: %v", err)
	}
	if _, err := os.Lstat(junk); err == nil {
		t.Fatal("the orphan is still there")
	}
	if _, err := os.Lstat(filepath.Join(realVault, "bash", ".bashrc")); err != nil {
		t.Fatalf("the real backup was destroyed: %v", err)
	}
}

// --- GetDiffPair ------------------------------------------------------

// The last read path without resolved containment. refuseSymlink declines
// only the final component, so a planted vault directory pointing at a real
// folder in $HOME had its contents read and shipped into the webview
// labelled as this app's backup.
func TestGetDiffPair_RefusesToReadThroughASymlinkedVaultDir(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssh, "id_rsa"), []byte("PRIVATE KEY"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vault, "bash"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ssh, filepath.Join(vault, "bash", "x")); err != nil {
		t.Fatal(err)
	}
	saveTestManifest(t, vault, "bash", []ManifestFile{{Path: "x/id_rsa"}})

	pair, err := a.GetDiffPair("bash", "x/id_rsa")
	if err == nil {
		t.Fatalf("GetDiffPair read through the symlink and returned %+v", pair.Vault)
	}
}
