package main

import (
	"os"
	"path/filepath"
	"testing"
)

// newRestoreFixture isolates HOME, XDG_STATE_HOME, and XDG_CONFIG_HOME to
// fresh per-test directories, then persists a vault path via the real
// SaveSettings so RestoreApp/GetUndoInfo/UndoRestore exercise their actual
// production code paths, not a mock.
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

// --- captureEntry -----------------------------------------------------

func TestCaptureEntry_Absent(t *testing.T) {
	dir := t.TempDir()
	entry, err := captureEntry(filepath.Join(dir, "pending"), ".missing", filepath.Join(dir, ".missing"))
	if err != nil {
		t.Fatalf("captureEntry: %v", err)
	}
	if entry.Kind != "absent" {
		t.Fatalf("Kind = %q, want absent", entry.Kind)
	}
}

func TestCaptureEntry_Regular(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".file")
	if err := os.WriteFile(destPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	pendingDir := filepath.Join(dir, "pending")

	entry, err := captureEntry(pendingDir, ".file", destPath)
	if err != nil {
		t.Fatalf("captureEntry: %v", err)
	}
	if entry.Kind != "regular" {
		t.Fatalf("Kind = %q, want regular", entry.Kind)
	}
	got, err := os.ReadFile(filepath.Join(pendingDir, ".file"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("captured bytes = %q, err %v", got, err)
	}
}

func TestCaptureEntry_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(dir, ".link")
	if err := os.Symlink(target, destPath); err != nil {
		t.Fatal(err)
	}

	entry, err := captureEntry(filepath.Join(dir, "pending"), ".link", destPath)
	if err != nil {
		t.Fatalf("captureEntry: %v", err)
	}
	if entry.Kind != "symlink" || entry.LinkTarget != target {
		t.Fatalf("entry = %+v, want symlink -> %s", entry, target)
	}
}

func TestCaptureEntry_ErrorsWhenPendingDirCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".file")
	if err := os.WriteFile(destPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	pendingDir := filepath.Join(blocker, "pending")

	if _, err := captureEntry(pendingDir, ".file", destPath); err == nil {
		t.Fatal("expected an error when the pending directory can't be created")
	}
}

// --- commitSnapshot: the crash-safety property caught during planning ---

func TestCommitSnapshot_Success(t *testing.T) {
	root := t.TempDir()
	canonicalDir := filepath.Join(root, "app")
	pendingDir := filepath.Join(root, "app.pending")

	snap := RestoreSnapshot{App: "app", CreatedAt: "2020-01-01T00:00:00Z", Entries: []SnapshotEntry{{Path: "a", Kind: "absent"}}}
	if err := commitSnapshot(pendingDir, canonicalDir, snap); err != nil {
		t.Fatalf("commitSnapshot: %v", err)
	}

	got, ok := readSnapshot(canonicalDir)
	if !ok || got.CreatedAt != snap.CreatedAt {
		t.Fatalf("readSnapshot = %+v, ok=%v", got, ok)
	}
	if _, err := os.Stat(pendingDir); !os.IsNotExist(err) {
		t.Fatalf("pendingDir should have been renamed away, stat err = %v", err)
	}
}

func TestCommitSnapshot_OldSnapshotSurvivesWriteFailure(t *testing.T) {
	root := t.TempDir()
	canonicalDir := filepath.Join(root, "app")
	pendingDir := filepath.Join(root, "app.pending")

	oldSnap := RestoreSnapshot{App: "app", CreatedAt: "2020-01-01T00:00:00Z", Entries: []SnapshotEntry{{Path: "old", Kind: "absent"}}}
	if err := writeSnapshot(canonicalDir, oldSnap); err != nil {
		t.Fatal(err)
	}

	// Sabotage: pre-seed pendingDir so writeSnapshot's own WriteFile call
	// fails — "snapshot.json" already exists there as a directory.
	if err := os.MkdirAll(filepath.Join(pendingDir, snapshotFileName), 0755); err != nil {
		t.Fatal(err)
	}

	newSnap := RestoreSnapshot{App: "app", CreatedAt: "2020-02-02T00:00:00Z", Entries: []SnapshotEntry{{Path: "new", Kind: "absent"}}}
	if err := commitSnapshot(pendingDir, canonicalDir, newSnap); err == nil {
		t.Fatal("expected commitSnapshot to fail")
	}

	got, ok := readSnapshot(canonicalDir)
	if !ok {
		t.Fatal("the old snapshot should still be readable")
	}
	if got.CreatedAt != oldSnap.CreatedAt {
		t.Fatalf("old snapshot was replaced: got CreatedAt=%q, want %q", got.CreatedAt, oldSnap.CreatedAt)
	}
}

// TestCommitSnapshot_RecoversStaleSnapshotInsteadOfDiscardingIt models the
// state a crash between commitSnapshot's two renames leaves behind: the
// previous snapshot survives under canonicalDir+".stale" (a real rename
// already completed it there) while canonicalDir itself is gone. The old
// code unconditionally os.RemoveAll'd that ".stale" directory the moment
// the next commitSnapshot call started, before checking whether canonicalDir
// even existed — silently destroying a still-valid previous snapshot rather
// than reinstating it. The fix promotes it back to canonicalDir first, so
// it's carried forward as the thing the new commit legitimately supersedes,
// never as something deleted out from under a still-recoverable state.
func TestCommitSnapshot_RecoversStaleSnapshotInsteadOfDiscardingIt(t *testing.T) {
	root := t.TempDir()
	canonicalDir := filepath.Join(root, "app")
	pendingDir := filepath.Join(root, "app.pending")
	staleDir := canonicalDir + ".stale"

	oldSnap := RestoreSnapshot{App: "app", CreatedAt: "2020-01-01T00:00:00Z", Entries: []SnapshotEntry{{Path: "old", Kind: "absent"}}}
	if err := writeSnapshot(staleDir, oldSnap); err != nil {
		t.Fatal(err)
	}

	newSnap := RestoreSnapshot{App: "app", CreatedAt: "2020-02-02T00:00:00Z", Entries: []SnapshotEntry{{Path: "new", Kind: "absent"}}}
	if err := commitSnapshot(pendingDir, canonicalDir, newSnap); err != nil {
		t.Fatalf("commitSnapshot: %v", err)
	}

	got, ok := readSnapshot(canonicalDir)
	if !ok || got.CreatedAt != newSnap.CreatedAt {
		t.Fatalf("expected the new snapshot at canonicalDir, got %+v ok=%v", got, ok)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("staleDir should be cleaned up after a successful commit, stat err = %v", err)
	}
	if _, err := os.Stat(pendingDir); !os.IsNotExist(err) {
		t.Fatalf("pendingDir should have been renamed away, stat err = %v", err)
	}
}

func TestReadSnapshot_MissingOrCorrupt(t *testing.T) {
	if _, ok := readSnapshot(filepath.Join(t.TempDir(), "nope")); ok {
		t.Fatal("expected ok=false for a directory with no snapshot.json")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, snapshotFileName), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSnapshot(dir); ok {
		t.Fatal("expected ok=false for corrupt JSON")
	}
}

// --- RestoreApp integration --------------------------------------------

func TestRestoreApp_CapturesSnapshotAcrossAllKinds(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const appName = "testapp"

	absentFile := seedVaultFile(t, vault, appName, ".absent-file", "vault-absent")
	regularFile := seedVaultFile(t, vault, appName, ".regular-file", "vault-regular")
	symlinkFile := seedVaultFile(t, vault, appName, ".symlink-file", "vault-symlink")
	saveTestManifest(t, vault, appName, []ManifestFile{absentFile, regularFile, symlinkFile})

	regularDest := filepath.Join(home, ".regular-file")
	if err := os.WriteFile(regularDest, []byte("old-regular"), 0644); err != nil {
		t.Fatal(err)
	}
	linkTarget := filepath.Join(home, "elsewhere.txt")
	if err := os.WriteFile(linkTarget, []byte("elsewhere"), 0644); err != nil {
		t.Fatal(err)
	}
	symlinkDest := filepath.Join(home, ".symlink-file")
	if err := os.Symlink(linkTarget, symlinkDest); err != nil {
		t.Fatal(err)
	}

	result, err := a.RestoreApp(appName)
	if err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("unexpected failures: %+v", result.Failed)
	}
	if len(result.New) != 1 || result.New[0] != ".absent-file" {
		t.Fatalf("New = %v", result.New)
	}
	if len(result.Overwritten) != 2 {
		t.Fatalf("Overwritten = %v", result.Overwritten)
	}

	for _, tc := range []struct{ path, want string }{
		{".absent-file", "vault-absent"},
		{".regular-file", "vault-regular"},
		{".symlink-file", "vault-symlink"},
	} {
		got, err := os.ReadFile(filepath.Join(home, tc.path))
		if err != nil {
			t.Fatalf("reading %s: %v", tc.path, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
	if info, err := os.Lstat(symlinkDest); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf(".symlink-file should now be a regular file, mode=%v err=%v", info.Mode(), err)
	}

	info, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if !info.Available || info.FileCount != 3 {
		t.Fatalf("GetUndoInfo = %+v", info)
	}
}

func TestRestoreApp_NoOpPreservesExistingSnapshot(t *testing.T) {
	a, _, vault := newRestoreFixture(t)
	const appName = "testapp"
	f := seedVaultFile(t, vault, appName, ".file", "vault-content")
	saveTestManifest(t, vault, appName, []ManifestFile{f})

	if _, err := a.RestoreApp(appName); err != nil {
		t.Fatalf("first RestoreApp: %v", err)
	}
	before, err := a.GetUndoInfo(appName)
	if err != nil || !before.Available {
		t.Fatalf("expected an undo snapshot after the first restore: %+v, err=%v", before, err)
	}

	result, err := a.RestoreApp(appName)
	if err != nil {
		t.Fatalf("second RestoreApp: %v", err)
	}
	if len(result.Skipped) != 1 || len(result.New) != 0 || len(result.Overwritten) != 0 {
		t.Fatalf("expected a fully skipped no-op restore, got %+v", result)
	}

	after, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo after no-op: %v", err)
	}
	if after != before {
		t.Fatalf("a no-op restore touched the snapshot: before=%+v after=%+v", before, after)
	}
}

func TestRestoreApp_FailClosedWhenUndoStorageUnavailable(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const appName = "testapp"
	f := seedVaultFile(t, vault, appName, ".file", "vault-content")
	saveTestManifest(t, vault, appName, []ManifestFile{f})

	destPath := filepath.Join(home, ".file")
	if err := os.WriteFile(destPath, []byte("old-live-content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Sabotage undo storage after settings/vault are already set up: point
	// XDG_STATE_HOME at a plain file, so captureEntry's copyFile-into-
	// pending step can never create a directory under it.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", blocker)

	result, err := a.RestoreApp(appName)
	if err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	if len(result.Failed) != 1 || len(result.New) != 0 || len(result.Overwritten) != 0 {
		t.Fatalf("expected the file to fail closed, got %+v", result)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading destPath: %v", err)
	}
	if string(got) != "old-live-content" {
		t.Fatalf("the file should not have been mutated, got %q", got)
	}

	info, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if info.Available {
		t.Fatalf("no snapshot should have been written, got %+v", info)
	}
}

// --- UndoRestore ---------------------------------------------------------

func TestUndoRestore_RoundTripsAllKinds(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const appName = "testapp"

	absentFile := seedVaultFile(t, vault, appName, ".absent-file", "vault-absent")
	regularFile := seedVaultFile(t, vault, appName, ".regular-file", "vault-regular")
	symlinkFile := seedVaultFile(t, vault, appName, ".symlink-file", "vault-symlink")
	saveTestManifest(t, vault, appName, []ManifestFile{absentFile, regularFile, symlinkFile})

	regularDest := filepath.Join(home, ".regular-file")
	if err := os.WriteFile(regularDest, []byte("old-regular"), 0644); err != nil {
		t.Fatal(err)
	}
	linkTarget := filepath.Join(home, "elsewhere.txt")
	if err := os.WriteFile(linkTarget, []byte("elsewhere"), 0644); err != nil {
		t.Fatal(err)
	}
	symlinkDest := filepath.Join(home, ".symlink-file")
	if err := os.Symlink(linkTarget, symlinkDest); err != nil {
		t.Fatal(err)
	}

	if _, err := a.RestoreApp(appName); err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}

	undoResult, err := a.UndoRestore(appName)
	if err != nil {
		t.Fatalf("UndoRestore: %v", err)
	}
	if len(undoResult.Failed) != 0 || len(undoResult.Restored) != 3 {
		t.Fatalf("UndoRestore = %+v", undoResult)
	}

	if _, err := os.Stat(filepath.Join(home, ".absent-file")); !os.IsNotExist(err) {
		t.Fatalf(".absent-file should be gone again after undo, err=%v", err)
	}
	got, err := os.ReadFile(regularDest)
	if err != nil || string(got) != "old-regular" {
		t.Fatalf(".regular-file = %q, err %v", got, err)
	}
	info, err := os.Lstat(symlinkDest)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".symlink-file should be a symlink again, mode=%v err=%v", info.Mode(), err)
	}
	target, err := os.Readlink(symlinkDest)
	if err != nil || target != linkTarget {
		t.Fatalf("symlink target = %q, want %q, err %v", target, linkTarget, err)
	}

	after, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo after undo: %v", err)
	}
	if after.Available {
		t.Fatalf("a fully successful undo should clear the snapshot, got %+v", after)
	}
}

func TestRestoreApp_DoesNotMutateLiveFilesWhenCommitFails(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	const appName = "testapp"

	// A manifest entry whose relative path is itself "snapshot.json/nested"
	// makes captureEntry's copyFile MkdirAll a *directory* at
	// <pendingDir>/snapshot.json. The later writeSnapshot call then tries
	// to create a plain file at that exact path and fails -- forcing
	// commitSnapshot to fail from within a real RestoreApp run, without
	// reaching into its internals.
	sabotage := seedVaultFile(t, vault, appName, "snapshot.json/nested", "vault-sabotage")
	plain := seedVaultFile(t, vault, appName, ".plain-file", "vault-plain")
	saveTestManifest(t, vault, appName, []ManifestFile{sabotage, plain})

	sabotageDest := filepath.Join(home, "snapshot.json", "nested")
	if err := os.MkdirAll(filepath.Dir(sabotageDest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sabotageDest, []byte("old-sabotage-live"), 0644); err != nil {
		t.Fatal(err)
	}
	plainDest := filepath.Join(home, ".plain-file")
	if err := os.WriteFile(plainDest, []byte("old-live-content"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := a.RestoreApp(appName)
	if err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	if len(result.New) != 0 || len(result.Overwritten) != 0 {
		t.Fatalf("no file should be mutated when the snapshot commit fails, got %+v", result)
	}
	if len(result.Failed) != 2 {
		t.Fatalf("expected both files reported failed, got %+v", result.Failed)
	}

	if got, err := os.ReadFile(sabotageDest); err != nil || string(got) != "old-sabotage-live" {
		t.Fatalf("snapshot.json/nested was mutated despite the commit failing: got %q, err %v", got, err)
	}
	if got, err := os.ReadFile(plainDest); err != nil || string(got) != "old-live-content" {
		t.Fatalf(".plain-file was mutated despite the commit failing: got %q, err %v", got, err)
	}

	info, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if info.Available {
		t.Fatalf("no snapshot should have been committed, got %+v", info)
	}
}

func TestUndoRestore_RegularEntryClearsSymlinkFirst(t *testing.T) {
	a, home, _ := newRestoreFixture(t)
	const appName = "testapp"

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := filepath.Join(root, appName)
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, ".file"), []byte("captured-content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Between the restore that produced this snapshot and the undo, .file
	// at destPath was replaced by a symlink -- e.g. a dotfile manager
	// re-ran and re-linked it. It now points outside $HOME entirely.
	outside := filepath.Join(t.TempDir(), "canary.txt")
	if err := os.WriteFile(outside, []byte("must-not-be-touched"), 0644); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(home, ".file")
	if err := os.Symlink(outside, destPath); err != nil {
		t.Fatal(err)
	}

	snap := RestoreSnapshot{
		App:       appName,
		CreatedAt: "2020-01-01T00:00:00Z",
		Entries:   []SnapshotEntry{{Path: ".file", Kind: "regular"}},
	}
	if err := writeSnapshot(canonicalDir, snap); err != nil {
		t.Fatal(err)
	}

	result, err := a.UndoRestore(appName)
	if err != nil {
		t.Fatalf("UndoRestore: %v", err)
	}
	if len(result.Failed) != 0 || len(result.Restored) != 1 {
		t.Fatalf("UndoRestore = %+v", result)
	}

	// destPath must now be a real regular file holding the captured
	// content -- not a write-through that landed in the symlink's target.
	info, err := os.Lstat(destPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf(".file should be a regular file after undo, mode=%v err=%v", info.Mode(), err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil || string(got) != "captured-content" {
		t.Fatalf(".file = %q, err %v", got, err)
	}

	canary, err := os.ReadFile(outside)
	if err != nil || string(canary) != "must-not-be-touched" {
		t.Fatalf("the symlink's target outside $HOME was written through: got %q, err %v", canary, err)
	}
}

func TestUndoRestore_RevalidatesPathsAndPrunesAppliedEntriesOnPartialFailure(t *testing.T) {
	a, home, _ := newRestoreFixture(t)
	const appName = "testapp"

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := filepath.Join(root, appName)
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, ".good-file"), []byte("captured-good"), 0644); err != nil {
		t.Fatal(err)
	}
	goodDest := filepath.Join(home, ".good-file")
	if err := os.WriteFile(goodDest, []byte("live-before-undo"), 0644); err != nil {
		t.Fatal(err)
	}

	// A hostile entry, as if snapshot.json had been tampered with — never
	// trusted blindly, re-validated through homeRelative like anything else.
	snap := RestoreSnapshot{
		App:       appName,
		CreatedAt: "2020-01-01T00:00:00Z",
		Entries: []SnapshotEntry{
			{Path: ".good-file", Kind: "regular"},
			{Path: "../../etc/hostile", Kind: "regular"},
		},
	}
	if err := writeSnapshot(canonicalDir, snap); err != nil {
		t.Fatal(err)
	}

	result, err := a.UndoRestore(appName)
	if err != nil {
		t.Fatalf("UndoRestore: %v", err)
	}
	if len(result.Restored) != 1 || result.Restored[0] != ".good-file" {
		t.Fatalf("Restored = %v", result.Restored)
	}
	if len(result.Failed) != 1 || result.Failed[0].Path != "../../etc/hostile" {
		t.Fatalf("Failed = %+v", result.Failed)
	}
	got, err := os.ReadFile(goodDest)
	if err != nil || string(got) != "captured-good" {
		t.Fatalf(".good-file = %q, err %v", got, err)
	}

	info, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if !info.Available {
		t.Fatalf("a partial-failure undo must leave the snapshot in place, got %+v", info)
	}
	// Left in place, but shrunk to what still needs applying. Keeping the
	// applied entry would make the retry re-apply work that already
	// succeeded — see TestUndoRestore_RetryDoesNotRedoAppliedEntries.
	if info.FileCount != 1 {
		t.Fatalf("FileCount = %d, want the succeeded entry pruned out", info.FileCount)
	}
	// And the one entry left is unrestorable, so the UI can stop offering it
	// instead of presenting the same failing undo on every visit.
	if info.Restorable != 0 {
		t.Fatalf("Restorable = %d, want 0 — the only entry left can never succeed", info.Restorable)
	}
}

func TestGetUndoInfo_NoneAvailable(t *testing.T) {
	a, _, _ := newRestoreFixture(t)
	info, err := a.GetUndoInfo("nonexistent-app")
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if info.Available {
		t.Fatalf("expected no undo info, got %+v", info)
	}
}

// --- B6: an undo offer that still means something --------------------------

// TestUndoRestore_RetryDoesNotRedoAppliedEntries is why the snapshot is
// pruned rather than merely kept. Retrying a partly-failed undo is supposed
// to be the safe move; replaying an already-applied "absent" entry would
// delete a file the user recreated in between, which makes the retry the
// destructive one.
func TestUndoRestore_RetryDoesNotRedoAppliedEntries(t *testing.T) {
	a, home, _ := newRestoreFixture(t)
	const appName = "testapp"

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := filepath.Join(root, appName)
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatal(err)
	}

	// One entry that succeeds (the file did not exist before the restore, so
	// undo deletes it) and one that can never succeed.
	gone := filepath.Join(home, ".created-by-restore")
	if err := os.WriteFile(gone, []byte("written by the restore"), 0644); err != nil {
		t.Fatal(err)
	}
	snap := RestoreSnapshot{
		App:       appName,
		CreatedAt: "2020-01-01T00:00:00Z",
		Entries: []SnapshotEntry{
			{Path: ".created-by-restore", Kind: "absent"},
			{Path: "../../etc/hostile", Kind: "regular"},
		},
	}
	if err := writeSnapshot(canonicalDir, snap); err != nil {
		t.Fatal(err)
	}

	if _, err := a.UndoRestore(appName); err != nil {
		t.Fatalf("UndoRestore: %v", err)
	}
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Fatal("the absent-entry undo should have deleted the file")
	}

	// The user makes the file again, deliberately, then retries the undo to
	// clear the entry that failed.
	const recreated = "the user wrote this again on purpose"
	if err := os.WriteFile(gone, []byte(recreated), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UndoRestore(appName); err != nil {
		t.Fatalf("second UndoRestore: %v", err)
	}

	got, err := os.ReadFile(gone)
	if err != nil {
		t.Fatal("retrying the undo deleted a file the user had recreated")
	}
	if string(got) != recreated {
		t.Fatalf("file = %q, want the user's own content untouched", got)
	}
}

// TestGetUndoInfo_FlagsSnapshotFromAnotherVault covers the offer that is
// valid but no longer means what it appears to mean. Snapshots are keyed by
// app name alone, so a "bash" in a different vault would inherit this one
// and the overlay would offer to replay a foreign app's bytes over $HOME.
func TestGetUndoInfo_FlagsSnapshotFromAnotherVault(t *testing.T) {
	a, _, _ := newRestoreFixture(t)
	const appName = "testapp"

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := filepath.Join(root, appName)
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshot(canonicalDir, RestoreSnapshot{
		App:       appName,
		CreatedAt: "2020-01-01T00:00:00Z",
		Entries:   []SnapshotEntry{{Path: ".bashrc", Kind: "absent"}},
		VaultPath: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}

	info, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if !info.Available {
		t.Fatal("a snapshot from another vault is still a real snapshot")
	}
	if !info.Stale {
		t.Fatal("Stale = false, want the snapshot flagged as taken from a different vault")
	}
}

// TestGetUndoInfo_TreatsUnstampedSnapshotAsCurrent pins the compatibility
// half: every snapshot written before v1.2.2 has no vaultPath, and absent
// must mean unknown-so-keep-offering, not foreign.
func TestGetUndoInfo_TreatsUnstampedSnapshotAsCurrent(t *testing.T) {
	a, _, _ := newRestoreFixture(t)
	const appName = "testapp"

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := filepath.Join(root, appName)
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshot(canonicalDir, RestoreSnapshot{
		App:       appName,
		CreatedAt: "2020-01-01T00:00:00Z",
		Entries:   []SnapshotEntry{{Path: ".bashrc", Kind: "absent"}},
	}); err != nil {
		t.Fatal(err)
	}

	info, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if info.Stale {
		t.Fatal("an unstamped pre-v1.2.2 snapshot must not be treated as foreign")
	}
	if info.Restorable != 1 {
		t.Fatalf("Restorable = %d, want 1", info.Restorable)
	}
}

// TestGetUndoInfo_ReportsZeroRestorableWhenSnapshotBytesAreGone covers the
// undo that can never succeed. A "regular" entry replays captured bytes
// sitting beside snapshot.json; with those gone the offer is permanent and
// permanently useless, and the user meets it on every visit.
func TestGetUndoInfo_ReportsZeroRestorableWhenSnapshotBytesAreGone(t *testing.T) {
	a, _, _ := newRestoreFixture(t)
	const appName = "testapp"

	root, err := undoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := filepath.Join(root, appName)
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		t.Fatal(err)
	}
	// snapshot.json written, but the captured file beside it never was.
	if err := writeSnapshot(canonicalDir, RestoreSnapshot{
		App:       appName,
		CreatedAt: "2020-01-01T00:00:00Z",
		Entries:   []SnapshotEntry{{Path: ".bashrc", Kind: "regular"}},
	}); err != nil {
		t.Fatal(err)
	}

	info, err := a.GetUndoInfo(appName)
	if err != nil {
		t.Fatalf("GetUndoInfo: %v", err)
	}
	if !info.Available || info.FileCount != 1 {
		t.Fatalf("info = %+v, want the snapshot still reported", info)
	}
	if info.Restorable != 0 {
		t.Fatalf("Restorable = %d, want 0 — the captured bytes are gone", info.Restorable)
	}
}

// TestGetUndoInfo_RejectsPathEscape pins the IPC boundary. The frontend only
// ever passes sanitized manifest names, but the boundary itself must not
// depend on that.
func TestGetUndoInfo_RejectsPathEscape(t *testing.T) {
	a, _, _ := newRestoreFixture(t)
	if _, err := a.GetUndoInfo("../../etc"); err == nil {
		t.Fatal("GetUndoInfo accepted a path-escaping app name")
	}
	if _, err := a.UndoRestore("../../etc"); err == nil {
		t.Fatal("UndoRestore accepted a path-escaping app name")
	}
	// The one that matters most: this is the entry point that calls
	// os.RemoveAll on three joined paths.
	if err := a.DiscardUndoSnapshot("../../etc"); err == nil {
		t.Fatal("DiscardUndoSnapshot accepted a path-escaping app name")
	}
}
