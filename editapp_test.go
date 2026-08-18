package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func saveTestManifestWithDirs(t *testing.T, vault, appName string, files []ManifestFile, dirs []string) {
	t.Helper()
	m := Manifest{Version: manifestVersion, Apps: []ManifestApp{{Name: appName, Files: files, Dirs: dirs}}}
	if err := saveManifest(vault, m); err != nil {
		t.Fatal(err)
	}
}

// --- RemoveFromApp: the headline invariant ----------------------------

// TestRemoveFromApp_NeverTouchesLiveFile is the one test this whole feature
// exists to satisfy. The maker's stated fear about a delete button is that it
// eats his real dotfiles, so the live file and the vault copy are seeded with
// different content and the live bytes are compared exactly.
func TestRemoveFromApp_NeverTouchesLiveFile(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	livePath := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(livePath, []byte("LIVE CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, vault, "bash", ".bashrc", "VAULT CONTENT")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	result, err := a.RemoveFromApp("bash", []string{".bashrc"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.RemovedFiles) != 1 {
		t.Fatalf("RemovedFiles = %v, want the vault copy removed (Failed = %v)", result.RemovedFiles, result.Failed)
	}

	got, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatalf("the live file was deleted: %v", err)
	}
	if string(got) != "LIVE CONTENT" {
		t.Fatalf("live file content = %q, want it untouched", got)
	}
	if _, err := os.Lstat(filepath.Join(vault, "bash", ".bashrc")); err == nil {
		t.Fatal("the vault copy should be gone")
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 0 {
		t.Fatalf("manifest still lists %v", m.Apps[0].Files)
	}
}

// TestRemoveFromApp_RefusesSymlinkedVaultParent is the catastrophe this
// guard exists for. unlink(2) declines to follow a symlink only at the FINAL
// component; every directory above it resolves normally. With
// <vault>/bash/.config planted as a link to the user's real ~/.config, an
// os.Remove of the manifest-listed ".config/init.lua" deletes their live
// config — while filepath.Rel and homeRelative both report a perfectly clean
// relative path, because they are purely lexical.
func TestRemoveFromApp_RefusesSymlinkedVaultParent(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	liveConfig := filepath.Join(home, ".config")
	if err := os.MkdirAll(liveConfig, 0755); err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(liveConfig, "init.lua")
	if err := os.WriteFile(liveFile, []byte("real config"), 0644); err != nil {
		t.Fatal(err)
	}

	appDir := filepath.Join(vault, "bash")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(liveConfig, filepath.Join(appDir, ".config")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	saveTestManifest(t, vault, "bash", []ManifestFile{{Path: ".config/init.lua"}})

	result, err := a.RemoveFromApp("bash", []string{".config/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %v, want the removal refused (Removed = %v)", result.Failed, result.RemovedFiles)
	}
	if _, err := os.Lstat(liveFile); err != nil {
		t.Fatal("the user's live config file was deleted through a symlinked vault parent")
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 1 {
		t.Fatal("a refused removal must keep its manifest entry")
	}
}

// TestRemoveFromApp_RefusesSymlinkedParentInsideVault covers the case that
// distinguishes this guard from v1.2.1's vaultDirEscapes. That one asks only
// whether a path resolves OUTSIDE the vault — the right question for reading
// and writing a tracked file. A link from one app's folder into another's
// resolves inside the vault and so passes it cleanly, yet deleting through it
// destroys a different app's backup.
func TestRemoveFromApp_RefusesSymlinkedParentInsideVault(t *testing.T) {
	a, _, vault := newRestoreFixture(t)

	otherFile := seedVaultFile(t, vault, "other", "init.lua", "other app's backup")
	_ = otherFile

	appDir := filepath.Join(vault, "bash")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(vault, "other"), filepath.Join(appDir, ".config")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	saveTestManifest(t, vault, "bash", []ManifestFile{{Path: ".config/init.lua"}})

	result, err := a.RemoveFromApp("bash", []string{".config/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %v, want refusal (Removed = %v)", result.Failed, result.RemovedFiles)
	}
	if _, err := os.Lstat(filepath.Join(vault, "other", "init.lua")); err != nil {
		t.Fatal("another app's backup was deleted through an in-vault symlink")
	}
}

// TestRemoveFromApp_LeafSymlinkIsUnlinkedNotFollowed pins the deliberate
// other half of the guard: the leaf is NOT checked, because os.Remove on a
// symlink unlinks the link itself. A hostile planting should be cleaned out
// of the vault without its target being touched.
func TestRemoveFromApp_LeafSymlinkIsUnlinkedNotFollowed(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	outside := t.TempDir()

	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("must survive"), 0644); err != nil {
		t.Fatal(err)
	}
	appDir := filepath.Join(vault, "bash")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(appDir, "tracked.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	saveTestManifest(t, vault, "bash", []ManifestFile{{Path: "tracked.txt"}})

	result, err := a.RemoveFromApp("bash", []string{"tracked.txt"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.RemovedFiles) != 1 {
		t.Fatalf("RemovedFiles = %v, want the planted link unlinked", result.RemovedFiles)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "must survive" {
		t.Fatalf("the symlink's target was followed and destroyed: %q, %v", got, err)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("the planted link should be gone from the vault")
	}
}

// TestRemoveFromApp_RejectsPathNotInManifest locks the whole-call refusal: a
// delete has no undo, so a path the manifest never claimed is refused
// outright rather than attempted.
func TestRemoveFromApp_RejectsPathNotInManifest(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "bash", ".bashrc", "vault")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	if _, err := a.RemoveFromApp("bash", []string{"../../etc/hostile"}, nil); err == nil {
		t.Fatal("a path the manifest never listed must be refused")
	}
	if _, err := os.Lstat(filepath.Join(vault, "bash", ".bashrc")); err != nil {
		t.Fatal("a refused call must remove nothing at all")
	}
}

// TestRemoveFromApp_RefusesFileCoveredByTrackedDir makes the resurrection
// loop unreachable rather than merely unlikely: refused in the backend, so a
// hand-written IPC call cannot reach it either.
func TestRemoveFromApp_RefusesFileCoveredByTrackedDir(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "nvim", ".config/nvim/init.lua", "cfg")
	saveTestManifestWithDirs(t, vault, "nvim", []ManifestFile{f}, []string{".config/nvim"})

	_, err := a.RemoveFromApp("nvim", []string{".config/nvim/init.lua"}, nil)
	if err == nil {
		t.Fatal("removing a file inside a tracked folder must be refused")
	}
	if _, err := os.Lstat(filepath.Join(vault, "nvim", ".config/nvim/init.lua")); err != nil {
		t.Fatal("the vault copy must survive a refused removal")
	}
}

// TestRemoveFromApp_MissingVaultCopyCountsAsRemoved is what makes a retry
// after a partial failure converge: the goal state is "not in the vault",
// and that is already true.
func TestRemoveFromApp_MissingVaultCopyCountsAsRemoved(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	saveTestManifest(t, vault, "bash", []ManifestFile{{Path: ".bashrc"}})

	result, err := a.RemoveFromApp("bash", []string{".bashrc"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.RemovedFiles) != 1 || len(result.Failed) != 0 {
		t.Fatalf("Removed = %v, Failed = %v; an already-absent copy is the goal state", result.RemovedFiles, result.Failed)
	}
}

// TestRemoveFromApp_PerEntryIndependent locks the RestoreApp-style
// independence: one planted symlink must not make every other file in the
// app unremovable.
func TestRemoveFromApp_PerEntryIndependent(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	liveConfig := filepath.Join(home, ".config")
	if err := os.MkdirAll(liveConfig, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveConfig, "init.lua"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}

	good := seedVaultFile(t, vault, "bash", ".bashrc", "vault")
	appDir := filepath.Join(vault, "bash")
	if err := os.Symlink(liveConfig, filepath.Join(appDir, ".config")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	saveTestManifest(t, vault, "bash", []ManifestFile{good, {Path: ".config/init.lua"}})

	result, err := a.RemoveFromApp("bash", []string{".bashrc", ".config/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.RemovedFiles) != 1 || result.RemovedFiles[0] != ".bashrc" {
		t.Fatalf("RemovedFiles = %v, want just .bashrc", result.RemovedFiles)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %v, want the symlinked one refused", result.Failed)
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 1 || m.Apps[0].Files[0].Path != ".config/init.lua" {
		t.Fatalf("manifest = %v, want the refused entry kept and the removed one dropped", m.Apps[0].Files)
	}
}

// TestRemoveFromApp_PrunesEmptyParentsButNotAppDir — an app with zero files
// is still a manifest entry, so deleting its folder is RemoveApp's job alone.
func TestRemoveFromApp_PrunesEmptyParentsButNotAppDir(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "nvim", ".config/nvim/init.lua", "cfg")
	saveTestManifest(t, vault, "nvim", []ManifestFile{f})

	if _, err := a.RemoveFromApp("nvim", []string{".config/nvim/init.lua"}, nil); err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(vault, "nvim", ".config")); err == nil {
		t.Fatal("emptied parent directories should be pruned")
	}
	if _, err := os.Lstat(filepath.Join(vault, "nvim")); err != nil {
		t.Fatal("the app directory itself must survive — an app with no files is still an app")
	}
}

// --- RemoveApp ---------------------------------------------------------

// TestRemoveApp_DeletesVaultSubtreeAndFreesName is the argument for
// RemoveApp being in scope, as an executable claim: without it, an app
// emptied to zero files would hold its name forever.
func TestRemoveApp_DeletesVaultSubtreeAndFreesName(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	livePath := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(livePath, []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, vault, "bash", ".bashrc", "vault")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	if _, err := a.RemoveApp("bash"); err != nil {
		t.Fatalf("RemoveApp: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(vault, "bash")); err == nil {
		t.Fatal("the app's vault folder should be gone")
	}
	if _, err := os.ReadFile(livePath); err != nil {
		t.Fatal("removing an app must not touch live files")
	}
	if err := a.AddApp("bash", []string{livePath}); err != nil {
		t.Fatalf("the name should be free for reuse: %v", err)
	}
}

// TestRemoveApp_ClearsUndoSnapshot guards the stale-snapshot hazard:
// snapshots are keyed by app name alone, so one left behind would be
// inherited by any future app reusing the name and offered for replay over
// that app's live files.
func TestRemoveApp_ClearsUndoSnapshot(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "bash", ".bashrc", "vault")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	snapDir := filepath.Join(root, "bash")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(RestoreSnapshot{
		App:       "bash",
		CreatedAt: "2026-01-01T00:00:00Z",
		Entries:   []SnapshotEntry{{Path: ".bashrc"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, snapshotFileName), data, 0644); err != nil {
		t.Fatal(err)
	}

	info, err := a.GetUndoInfo("bash")
	if err != nil || !info.Available {
		t.Fatalf("precondition: snapshot should be visible, got %+v err %v", info, err)
	}

	if _, err := a.RemoveApp("bash"); err != nil {
		t.Fatalf("RemoveApp: %v", err)
	}
	after, err := a.GetUndoInfo("bash")
	if err != nil {
		t.Fatal(err)
	}
	if after.Available {
		t.Fatal("a removed app's undo snapshot must not survive to be inherited by a new app of the same name")
	}
}

// --- RenameApp ---------------------------------------------------------

// TestRenameApp_MovesVaultFolderAndSnapshot — a rename that left the
// snapshot behind would strand it under a name nothing points at, waiting
// for the next app to take that name.
func TestRenameApp_MovesVaultFolderAndSnapshot(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	f := seedVaultFile(t, vault, "bash", ".bashrc", "vault")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	snapDir := filepath.Join(root, "bash")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(RestoreSnapshot{App: "bash", CreatedAt: "2026-01-01T00:00:00Z", Entries: []SnapshotEntry{{Path: ".bashrc"}}})
	if err := os.WriteFile(filepath.Join(snapDir, snapshotFileName), data, 0644); err != nil {
		t.Fatal(err)
	}

	if err := a.RenameApp("bash", "shell"); err != nil {
		t.Fatalf("RenameApp: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(vault, "shell", ".bashrc")); err != nil {
		t.Fatalf("the vault folder should have moved: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(vault, "bash")); err == nil {
		t.Fatal("the old vault folder should be gone")
	}
	old, err := a.GetUndoInfo("bash")
	if err != nil {
		t.Fatal(err)
	}
	if old.Available {
		t.Fatal("the snapshot must not stay behind under the old name")
	}
	moved, err := a.GetUndoInfo("shell")
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Available {
		t.Fatal("the snapshot should follow the app to its new name")
	}
}

// TestRenameApp_RefusesExistingName keeps AddApp's uniqueness rule intact.
func TestRenameApp_RefusesExistingName(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	f1 := seedVaultFile(t, vault, "bash", ".bashrc", "one")
	f2 := seedVaultFile(t, vault, "zsh", ".zshrc", "two")
	m := Manifest{Version: manifestVersion, Apps: []ManifestApp{
		{Name: "bash", Files: []ManifestFile{f1}},
		{Name: "zsh", Files: []ManifestFile{f2}},
	}}
	if err := saveManifest(vault, m); err != nil {
		t.Fatal(err)
	}

	if err := a.RenameApp("bash", "ZSH"); err == nil {
		t.Fatal("renaming onto an existing name (case-insensitively) must be refused")
	}
	if _, err := os.Lstat(filepath.Join(vault, "bash", ".bashrc")); err != nil {
		t.Fatal("a refused rename must change nothing")
	}
}

// --- AddToApp ----------------------------------------------------------

// TestAddToApp_ValidatesEverythingBeforeCopyingAnything preserves AddApp's
// contract through the extracted stageAdd/commitAdd split: a validation
// failure partway through must leave nothing copied.
func TestAddToApp_ValidatesEverythingBeforeCopyingAnything(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	outside := t.TempDir()

	good := filepath.Join(home, ".inputrc")
	if err := os.WriteFile(good, []byte("good"), 0644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(outside, "elsewhere.txt")
	if err := os.WriteFile(bad, []byte("bad"), 0644); err != nil {
		t.Fatal(err)
	}

	f := seedVaultFile(t, vault, "bash", ".bashrc", "vault")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	if err := a.AddToApp("bash", []string{good, bad}); err == nil {
		t.Fatal("a path outside $HOME must fail the whole call")
	}
	if _, err := os.Lstat(filepath.Join(vault, "bash", ".inputrc")); err == nil {
		t.Fatal("the valid path was copied before validation finished")
	}
	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 1 {
		t.Fatalf("manifest = %v, want it unchanged", m.Apps[0].Files)
	}
}

// TestAddToApp_ReAddingATrackedFileIsANoOp — the dedupe map now spans calls,
// not just one pick.
func TestAddToApp_ReAddingATrackedFileIsANoOp(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	live := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(live, []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.AddApp("bash", []string{live}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddToApp("bash", []string{live}); err != nil {
		t.Fatalf("re-adding a tracked file should be a no-op: %v", err)
	}
	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 1 {
		t.Fatalf("Files = %v, want exactly one entry", m.Apps[0].Files)
	}
}

// --- ExpandTrackedDir ---------------------------------------------------

// TestExpandTrackedDir_ThenRemove_DeletionSurvivesRediscovery is the whole
// point of the conversion. Without it the folder walk rediscovers the
// removed file on the next refresh and the next update copies it back, so
// the deletion silently undoes itself.
// TestUntrackDir_ThenRemove_SurvivesRefreshAndUpdate is the resurrection
// loop, end to end. While a folder is tracked, removing one file inside it
// undoes itself: the walk rediscovers the file on the next refresh and the
// next update copies it back. Both halves are checked here, because only the
// update actually re-materialises the bytes.
func TestUntrackDir_ThenRemove_SurvivesRefreshAndUpdate(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	dir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"init.lua", "lazy-lock.json"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.AddApp("nvim", []string{dir}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	// A file that appeared in the folder after the app was created. Until an
	// update runs it is tracked only by the folder walk, and this proves the
	// update materialises it into an entry of its own — which is what lets
	// untracking be a pure Dirs drop rather than a walk.
	if err := os.WriteFile(filepath.Join(dir, "plugins.lua"), []byte("plugins"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateFromSource("nvim"); err != nil {
		t.Fatalf("UpdateFromSource: %v", err)
	}

	if err := a.UntrackDir("nvim", ".config/nvim"); err != nil {
		t.Fatalf("UntrackDir: %v", err)
	}
	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Dirs) != 0 {
		t.Fatalf("Dirs = %v, want the folder no longer tracked as a folder", m.Apps[0].Dirs)
	}
	if len(m.Apps[0].Files) != 3 {
		t.Fatalf("Files = %v, want all three still tracked individually", m.Apps[0].Files)
	}

	if _, err := a.RemoveFromApp("nvim", []string{".config/nvim/lazy-lock.json"}, nil); err != nil {
		t.Fatalf("RemoveFromApp after untrack: %v", err)
	}

	const removed = ".config/nvim/lazy-lock.json"
	assertAbsent := func(when string) {
		t.Helper()
		rows, err := a.GetMirrorRows()
		if err != nil {
			t.Fatal(err)
		}
		for _, fr := range rows[0].Files {
			if fr.Path == removed {
				t.Fatalf("%s came back %s — the deletion undid itself", removed, when)
			}
		}
	}
	assertAbsent("on the next refresh")
	if _, err := a.UpdateFromSource("nvim"); err != nil {
		t.Fatalf("UpdateFromSource after removal: %v", err)
	}
	assertAbsent("on the next update")

	// And the live file was never the target of any of this.
	if _, err := os.Lstat(filepath.Join(dir, "lazy-lock.json")); err != nil {
		t.Fatal("the live file was deleted")
	}
}

// TestUntrackDir_DoesNotInventVaultMissingRows pins the reason UntrackDir is
// a pure Dirs drop rather than the walk-and-materialise it started as.
//
// Adding an entry for a file that has never been backed up produces a
// zero-checksum entry with no vault copy, and since v1.2.1 fileDriftRow
// checks the vault side before the checksum branch, that reads as
// vaultMissing — the app's most severe state, telling the user a backup went
// missing that never existed. Clicking a conversion advertised as changing
// nothing must not do that.
func TestUntrackDir_DoesNotInventVaultMissingRows(t *testing.T) {
	a, home, _ := newRestoreFixture(t)

	dir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte("init"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.AddApp("nvim", []string{dir}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	// Created after the add, with no update since: this file exists in the
	// folder but has no vault copy and no manifest entry.
	if err := os.WriteFile(filepath.Join(dir, "scratch.lua"), []byte("scratch"), 0644); err != nil {
		t.Fatal(err)
	}

	preview, err := a.PreviewUntrackDir("nvim", ".config/nvim")
	if err != nil {
		t.Fatalf("PreviewUntrackDir: %v", err)
	}
	if preview.KeepsTracked != 1 || preview.StopsTracking != 1 {
		t.Fatalf("preview = %+v, want KeepsTracked 1 / StopsTracking 1", preview)
	}

	if err := a.UntrackDir("nvim", ".config/nvim"); err != nil {
		t.Fatalf("UntrackDir: %v", err)
	}

	rows, err := a.GetMirrorRows()
	if err != nil {
		t.Fatal(err)
	}
	for _, fr := range rows[0].Files {
		if fr.State == "vaultMissing" {
			t.Fatalf("%s reports vaultMissing after untracking — the app is claiming a backup went missing that never existed", fr.Path)
		}
		if fr.Path == ".config/nvim/scratch.lua" {
			t.Fatal("a never-backed-up file was adopted into the manifest by untracking")
		}
	}
}

// --- refuseSymlinkedParents: the app directory position ------------------

// TestRemoveFromApp_RefusesSymlinkedAppDir covers the position every other
// symlink test in this file misses. The guard walks the components of rel
// BELOW the app directory, so for a single-segment path like ".bashrc" there
// are no intermediates to walk and the walk alone checks nothing at all — the
// app directory itself has to be checked explicitly.
//
// The vault here is the threat model the rest of the app already assumes: one
// that arrived from somewhere else, via AdoptVaultPath or a synced drive.
// Whoever last had it plants <vault>/evil -> $HOME and a manifest entry
// naming ".bashrc". Without the base check this test deletes the tester's own
// fixture ~/.bashrc, which is exactly what it would do to a real user.
func TestRemoveFromApp_RefusesSymlinkedAppDir(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	const liveContent = "the user's real shell config"
	liveFile := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(liveFile, []byte(liveContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(home, filepath.Join(vault, "evil")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	saveTestManifest(t, vault, "evil", []ManifestFile{{Path: ".bashrc"}})

	result, err := a.RemoveFromApp("evil", []string{".bashrc"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %v, want the removal refused (Removed = %v)", result.Failed, result.RemovedFiles)
	}

	got, err := os.ReadFile(liveFile)
	if err != nil {
		t.Fatal("the user's live ~/.bashrc was deleted through a symlinked app directory")
	}
	if string(got) != liveContent {
		t.Fatalf("live file = %q, want it untouched", got)
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 1 {
		t.Fatal("a refused removal must keep its manifest entry")
	}
}

// TestRemoveFromApp_WorksWhenVaultRootIsASymlink is the other side of the
// same fix, and the reason the base check stops where it does. A vault path
// that is itself a symlink — a removable drive reached through a stable
// alias, say — is an ordinary, valid setup. A guard that walked any higher
// than the app directory would lock those users out of removing anything,
// turning a security fix into a broken app for a legitimate configuration.
func TestRemoveFromApp_WorksWhenVaultRootIsASymlink(t *testing.T) {
	a, _, realVault := newRestoreFixture(t)

	link := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(realVault, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := a.SaveSettings(Settings{Theme: defaultTheme, VaultPath: link}); err != nil {
		t.Fatal(err)
	}

	f := seedVaultFile(t, realVault, "bash", ".bashrc", "backup bytes")
	saveTestManifest(t, realVault, "bash", []ManifestFile{f})

	result, err := a.RemoveFromApp("bash", []string{".bashrc"}, nil)
	if err != nil {
		t.Fatalf("RemoveFromApp: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("Failed = %v, want the removal to go through a symlinked vault root", result.Failed)
	}
	if _, err := os.Lstat(filepath.Join(realVault, "bash", ".bashrc")); !os.IsNotExist(err) {
		t.Fatal("the vault copy should have been removed")
	}
}
