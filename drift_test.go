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

	// An empty vault root isolates this to the source-side guard, which is
	// what the test is about.
	row := fileDriftRow(home, "", "", ManifestFile{Path: hostilePath, Checksum: checksum, Size: size})
	if row.State != "missing" {
		t.Fatalf("State = %q, want missing for a path-traversal entry (must never read outside $HOME)", row.State)
	}
	if row.SourceModified != "" {
		t.Fatalf("SourceModified should stay empty for a rejected entry, got %q", row.SourceModified)
	}
}

// --- tracked folders ----------------------------------------------------

// TestExpandTrackedDir_RefusesIntermediateSymlink is the regression that a
// "../../etc" test cannot give you, and the distinction is the whole point.
//
// Every guard on a tracked-folder path is lexical: homeRelative is
// filepath.Rel plus a ".." prefix test, resolving nothing. filepath.WalkDir
// lstats its root, and lstat declines to follow only the FINAL component —
// every intermediate one is followed. So "link/sub", where link is a symlink
// pointing outside $HOME, passes filepath.Clean, passes IsLocal, passes
// homeRelative, and then walks the target anyway. Every file discovered
// under it passes a second homeRelative check too, because those strings
// really do begin with $HOME.
//
// This is not a contrived shape: ~/.wine/dosdevices/z: -> / ships with wine
// by default, and ~/.steam/root and any hand-made ~/mnt behave identically.
// The payoff would be someone else's files listed as this app's, one click
// from being copied into the vault.
func TestExpandTrackedDir_RefusesIntermediateSymlink(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()

	secretDir := filepath.Join(outside, "sub")
	if err := os.MkdirAll(secretDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "secret"), []byte("not yours"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Lexically flawless: no "..", no absolute path, strictly under $HOME.
	if got := expandTrackedDir(home, "link/sub", nil); len(got) != 0 {
		t.Fatalf("walked outside $HOME through an intermediate symlink: %v", got)
	}
	if got := expandTrackedDir(home, "link", nil); len(got) != 0 {
		t.Fatalf("walked outside $HOME through a symlinked root: %v", got)
	}
}

// TestExpandTrackedDir_SkipsVaultAndSymlinkedFiles keeps the walk from
// enumerating the vault's own stored copies as if they were live system
// files, and from emitting symlinks as though they were regular files.
func TestExpandTrackedDir_SkipsVaultAndSymlinkedFiles(t *testing.T) {
	home := t.TempDir()
	dots := filepath.Join(home, "dots")
	vault := filepath.Join(dots, "vault")
	if err := os.MkdirAll(vault, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dots, "real.conf"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "stored.conf"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dots, "real.conf"), filepath.Join(dots, "alias.conf")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := expandTrackedDir(home, "dots", []string{vault})
	if len(got) != 1 || got[0] != "dots/real.conf" {
		t.Fatalf("expandTrackedDir = %v, want exactly [dots/real.conf]", got)
	}
}

// TestFileDriftRow_ReportsMissingVaultCopy covers the failure a backup tool
// must never have: the vault's copy is gone, but the row claims the file is
// safely backed up because the live file still matches a checksum recorded
// in manifest.json. Nothing looked at the vault at all before v1.2.1.
func TestFileDriftRow_ReportsMissingVaultCopy(t *testing.T) {
	home := t.TempDir()
	vault := t.TempDir()

	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, vault, "bash", ".bashrc", "live")
	vaultAppDir := filepath.Join(vault, "bash")

	if row := fileDriftRow(home, vault, "bash", f); row.State != "ok" {
		t.Fatalf("State = %q, want ok while both copies are present", row.State)
	}

	if err := os.Remove(filepath.Join(vaultAppDir, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	row := fileDriftRow(home, vault, "bash", f)
	if row.State != "vaultMissing" {
		t.Fatalf("State = %q, want vaultMissing once the backup is gone", row.State)
	}
	// Distinct from "missing", which means the LIVE file is gone — the
	// opposite problem, with the opposite remedy.
	if row.State == "missing" {
		t.Fatal("vault-missing must not be reported as source-missing")
	}
}

// TestFileDriftRow_RefusesVaultPathEscapingViaSymlink covers the vault side
// of the same lesson as the tracked-folder walk: containment proved on
// strings is not containment. Lstat follows every component except the last,
// so a symlinked directory planted inside the vault would let a file outside
// it supply the size and existence this row reports as a healthy backup.
func TestFileDriftRow_RefusesVaultPathEscapingViaSymlink(t *testing.T) {
	home := t.TempDir()
	vault := t.TempDir()
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	// The decoy outside the vault, with content matching what the manifest
	// will claim, so a naive check would call this "ok".
	if err := os.WriteFile(filepath.Join(outside, ".bashrc"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	size, checksum, backedUpAt, err := vaultFileMeta(filepath.Join(outside, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}

	vaultAppDir := filepath.Join(vault, "bash")
	if err := os.Symlink(outside, vaultAppDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	row := fileDriftRow(home, vault, "bash", ManifestFile{
		Path: ".bashrc", Size: size, Checksum: checksum, BackedUpAt: backedUpAt,
	})
	if row.State == "ok" {
		t.Fatal("a vault path resolving outside the vault must never read as a healthy backup")
	}
}

// TestUpdateFromSource_RefusesToWriteThroughVaultSymlink is the write-side
// twin of the test above, and it exists because closing only the read side
// would have made things worse, not better: the row now reports vaultDamaged
// and the UI tells the user Update repairs that, so Update is exactly where
// the user is sent. copyFileAtomic calls MkdirAll and creates its temp file
// inside the resulting directory, and neither declines to follow a directory
// symlink — so without this guard the "repair" writes the backup out of the
// vault and into whatever the planted link points at. The skip path is just
// as wrong: it would follow the same link, find a matching file at the far
// end and report "already identical" on every future refresh, so the damaged
// row could never converge.
func TestUpdateFromSource_RefusesToWriteThroughVaultSymlink(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, vault, "bash", ".bashrc", "live")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	// Replace the app's vault directory with a link out of the vault, the way
	// whoever last had write access to the drive could.
	vaultAppDir := filepath.Join(vault, "bash")
	if err := os.RemoveAll(vaultAppDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, vaultAppDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result, err := a.UpdateFromSource("bash")
	if err != nil {
		t.Fatalf("UpdateFromSource: %v", err)
	}
	if len(result.Blocked) != 1 {
		t.Fatalf("Blocked = %v, want the write refused (Updated = %v, Skipped = %v)",
			result.Blocked, result.Updated, result.Skipped)
	}
	if _, err := os.Lstat(filepath.Join(outside, ".bashrc")); err == nil {
		t.Fatal("a backup was written outside the vault through a planted directory symlink")
	}
	// Nothing may be left behind at the far end either — copyFileAtomic's
	// temp file lands in the same directory as its destination.
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("wrote %d entries outside the vault: %v", len(entries), entries)
	}
}

// TestUpdateFromSource_RestoresMissingVaultCopy pins the other half: a
// source that still matches its recorded checksum used to short-circuit to
// "skipped, already identical", which would leave a deleted backup missing
// forever precisely because the live file was fine.
func TestUpdateFromSource_RestoresMissingVaultCopy(t *testing.T) {
	a, home, vault := newRestoreFixture(t)
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	f := seedVaultFile(t, vault, "bash", ".bashrc", "live")
	saveTestManifest(t, vault, "bash", []ManifestFile{f})

	vaultFile := filepath.Join(vault, "bash", ".bashrc")
	if err := os.Remove(vaultFile); err != nil {
		t.Fatal(err)
	}

	result, err := a.UpdateFromSource("bash")
	if err != nil {
		t.Fatalf("UpdateFromSource: %v", err)
	}
	if len(result.Updated) != 1 {
		t.Fatalf("Updated = %v, want the missing backup re-copied (Skipped = %v)", result.Updated, result.Skipped)
	}
	if _, err := os.Stat(vaultFile); err != nil {
		t.Fatalf("vault copy was not restored: %v", err)
	}
}

// TestGetMirrorRows_ReportsUntrackedFileInTrackedDir covers the maker's
// requirement that a folder keeps tracking its contents: a file dropped in
// later must show up, must mark the app drifted (or the Update button
// refuses to run at all), and must not be written into manifest.json by the
// read path.
func TestGetMirrorRows_ReportsUntrackedFileInTrackedDir(t *testing.T) {
	a, home, vault := newRestoreFixture(t)

	confDir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "init.lua"), []byte("-- init"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.AddApp("nvim", []string{confDir}); err != nil {
		t.Fatalf("AddApp with a folder: %v", err)
	}

	rows, err := a.GetMirrorRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].Files) != 1 {
		t.Fatalf("rows = %+v, want one app with one file", rows)
	}
	if rows[0].Drifted {
		t.Fatal("app should be clean immediately after AddApp")
	}

	// A file appears in the tracked folder after the fact.
	if err := os.WriteFile(filepath.Join(confDir, "plugins.lua"), []byte("-- plugins"), 0644); err != nil {
		t.Fatal(err)
	}
	rows, err = a.GetMirrorRows()
	if err != nil {
		t.Fatal(err)
	}
	var untracked int
	for _, fr := range rows[0].Files {
		if fr.State == "untracked" {
			untracked++
		}
	}
	if untracked != 1 {
		t.Fatalf("files = %+v, want one untracked entry", rows[0].Files)
	}
	if !rows[0].Drifted {
		t.Fatal("a newly-found file must mark the app drifted, or Update refuses to run")
	}

	// The read path must not have rewritten the manifest.
	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 1 {
		t.Fatalf("GetMirrorRows mutated the manifest: %+v", m.Apps[0].Files)
	}

	// Update is what materialises it.
	if _, err := a.UpdateFromSource("nvim"); err != nil {
		t.Fatalf("UpdateFromSource: %v", err)
	}
	m, err = loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Files) != 2 {
		t.Fatalf("manifest files = %+v, want the new file materialised", m.Apps[0].Files)
	}
	if _, err := os.Stat(filepath.Join(vault, "nvim", ".config", "nvim", "plugins.lua")); err != nil {
		t.Fatalf("new file was not copied into the vault: %v", err)
	}
}

// TestSanitizeTrackedDirs_DropsEscapes keeps manifest.json honest as
// untrusted input. Lexical only — see expandTrackedDir for what actually
// contains the walk.
func TestSanitizeTrackedDirs_DropsEscapes(t *testing.T) {
	got := sanitizeTrackedDirs([]string{
		"../../etc", "/etc", ".", "..", "", "  .config/nvim  ", ".config/nvim",
	})
	if len(got) != 1 || got[0] != ".config/nvim" {
		t.Fatalf("sanitizeTrackedDirs = %v, want exactly [.config/nvim]", got)
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
