package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSystemRoots points the /etc and /usr allowlist at a temp directory for
// the duration of one test. The real roots are not writable by the test
// process — which is the entire premise of this release — so the only way to
// exercise the system path end to end is to move the goalposts, not the
// files.
func withSystemRoots(t *testing.T, roots ...string) {
	t.Helper()
	original := systemRoots
	systemRoots = roots
	t.Cleanup(func() { systemRoots = original })
}

// newSystemFixture is newRestoreFixture plus a stand-in system root.
func newSystemFixture(t *testing.T) (app *App, home, vault, sysRoot string) {
	t.Helper()
	app, home, vault = newRestoreFixture(t)
	sysRoot = t.TempDir()
	withSystemRoots(t, sysRoot)
	return app, home, vault, sysRoot
}

// TestAddApp_SystemFileLandsInItsOwnArray is the shape the whole release
// rests on: a path with no $HOME-relative form is stored absolute, in a
// separate array, at a separate place in the vault. An older RESONANCE
// reading this manifest drops the array and sees an app with fewer files —
// which is the point, because the alternative is it joining "/etc/x" onto
// $HOME and restoring to the wrong place.
func TestAddApp_SystemFileLandsInItsOwnArray(t *testing.T) {
	a, home, vault, sysRoot := newSystemFixture(t)

	sysFile := filepath.Join(sysRoot, "alsa", "alsa.conf")
	if err := os.MkdirAll(filepath.Dir(sysFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sysFile, []byte("pcm.!default hw:0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	homeFile := filepath.Join(home, ".asoundrc")
	if err := os.WriteFile(homeFile, []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := a.AddApp("alsa", []string{sysFile, homeFile}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	m, err := loadManifest(vault)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	app := m.Apps[0]
	if len(app.Files) != 1 || app.Files[0].Path != ".asoundrc" {
		t.Fatalf("Files = %+v, want one entry .asoundrc", app.Files)
	}
	if len(app.SystemFiles) != 1 || app.SystemFiles[0].Path != sysFile {
		t.Fatalf("SystemFiles = %+v, want one entry %s", app.SystemFiles, sysFile)
	}
	if app.SystemFiles[0].Checksum == "" {
		t.Error("a system entry must carry a checksum baseline like any other")
	}

	// The vault copy sits under the reserved segment, not at the raw path.
	want := filepath.Join(vault, "alsa", systemVaultSegment, strings.TrimPrefix(sysFile, "/"))
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("vault copy not at %s: %v", want, err)
	}
	if string(got) != "pcm.!default hw:0\n" {
		t.Fatalf("vault copy = %q", string(got))
	}
	// And the home file is still exactly where it always was.
	if _, err := os.Stat(filepath.Join(vault, "alsa", ".asoundrc")); err != nil {
		t.Fatalf("home-side layout must be unchanged: %v", err)
	}
}

// TestAddApp_RefusesOutsideEveryRoot keeps the widening honest: /etc and /usr
// became valid, everything else did not.
func TestAddApp_RefusesOutsideEveryRoot(t *testing.T) {
	a, _, _, _ := newSystemFixture(t)

	outside := filepath.Join(t.TempDir(), "somewhere.conf")
	if err := os.WriteFile(outside, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	err := a.AddApp("nope", []string{outside})
	if err == nil {
		t.Fatal("AddApp must refuse a path under no allowed root")
	}
	if !strings.Contains(err.Error(), "/etc") || !strings.Contains(err.Error(), "/usr") {
		t.Errorf("the refusal should name where RESONANCE does look, said: %v", err)
	}
}

// TestAddApp_UnreadableSystemFileFailsWholeAdd covers the release's decision
// to ship no elevated read. The failure has to happen in the validation pass,
// before anything is copied — an app half-added and then unwound is exactly
// what stageAdd/commitAdd are split apart to prevent.
func TestAddApp_UnreadableSystemFileFailsWholeAdd(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, so nothing is unreadable")
	}
	a, _, vault, sysRoot := newSystemFixture(t)

	readable := filepath.Join(sysRoot, "public.conf")
	if err := os.WriteFile(readable, []byte("fine"), 0644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(sysRoot, "shadow")
	if err := os.WriteFile(secret, []byte("hashes"), 0000); err != nil {
		t.Fatal(err)
	}

	err := a.AddApp("sys", []string{readable, secret})
	if err == nil {
		t.Fatal("AddApp must refuse a file it cannot read")
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Errorf("the refusal should say why, said: %v", err)
	}
	// Nothing copied, no app created — the readable file must not be sitting
	// in the vault with no manifest entry naming it.
	if _, err := os.Stat(filepath.Join(vault, "sys")); !os.IsNotExist(err) {
		t.Errorf("a refused add must leave no app directory behind, stat err = %v", err)
	}
	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 0 {
		t.Errorf("a refused add must create no app, got %+v", m.Apps)
	}
}

// TestTrackedSystemFolder_ExpandsAndDrifts proves a tracked folder works the
// same under a system root as under $HOME, and that its files are reported
// with the absolute paths the manifest will store.
func TestTrackedSystemFolder_ExpandsAndDrifts(t *testing.T) {
	a, _, vault, sysRoot := newSystemFixture(t)

	dir := filepath.Join(sysRoot, "conf.d")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(dir, "50-default.conf")
	if err := os.WriteFile(one, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := a.AddApp("sysconf", []string{dir}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}
	m, err := loadManifest(vault)
	if err != nil {
		t.Fatal(err)
	}
	app := m.Apps[0]
	if len(app.SystemDirs) != 1 || app.SystemDirs[0] != dir {
		t.Fatalf("SystemDirs = %+v, want %s", app.SystemDirs, dir)
	}
	if len(app.Dirs) != 0 {
		t.Fatalf("Dirs must stay empty for a system folder, got %+v", app.Dirs)
	}
	if len(app.SystemFiles) != 1 || app.SystemFiles[0].Path != one {
		t.Fatalf("SystemFiles = %+v, want %s", app.SystemFiles, one)
	}

	// A file appearing in the folder afterwards is reported, never silently
	// materialised by a read path.
	two := filepath.Join(dir, "99-local.conf")
	if err := os.WriteFile(two, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	rows, err := a.GetMirrorRows()
	if err != nil {
		t.Fatalf("GetMirrorRows: %v", err)
	}
	var found *FileRow
	for i := range rows[0].Files {
		if rows[0].Files[i].Path == two {
			found = &rows[0].Files[i]
		}
	}
	if found == nil {
		t.Fatalf("the new file should appear as untracked, rows = %+v", rows[0].Files)
	}
	if found.State != "untracked" {
		t.Errorf("State = %q, want untracked", found.State)
	}
	if !found.System {
		t.Error("a row under a system folder must be marked System")
	}
	if !rows[0].Drifted {
		t.Error("an untracked file inside a tracked folder must mark the app drifted")
	}
}

// TestScanVaultOrphans_IgnoresSystemBackups is the trap worth a test of its
// own. Orphan detection keys on the vault-relative path; a system entry's
// manifest path is absolute and would never match, so without the mapping
// every backed-up system file reads as unaccounted-for — and the delete
// surface would offer to destroy all of them at once.
func TestScanVaultOrphans_IgnoresSystemBackups(t *testing.T) {
	a, _, vault, sysRoot := newSystemFixture(t)

	sysFile := filepath.Join(sysRoot, "pacman.conf")
	if err := os.WriteFile(sysFile, []byte("[options]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.AddApp("pacman", []string{sysFile}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	report, err := a.ScanVaultOrphans()
	if err != nil {
		t.Fatalf("ScanVaultOrphans: %v", err)
	}
	if len(report.Files) != 0 {
		t.Fatalf("a freshly backed-up system file must not read as an orphan, got %v", report.Files)
	}

	// A file that genuinely is unaccounted for, inside the reserved subtree,
	// must still be found — the mapping must not blanket-exempt .system.
	stray := filepath.Join(vault, "pacman", systemVaultSegment, "etc", "stray.conf")
	if err := os.MkdirAll(filepath.Dir(stray), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err = a.ScanVaultOrphans()
	if err != nil {
		t.Fatalf("ScanVaultOrphans: %v", err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("an unreferenced file under .system is still an orphan, got %v", report.Files)
	}
}

// TestRestoreApp_SystemFileNeedsAdminRights pins the boundary this release
// draws. Backing up needs nothing; putting back is the one operation that
// cannot be done as the user. The home file in the same app must still
// restore — one file's need for rights must not fail the others.
func TestRestoreApp_SystemFileNeedsAdminRights(t *testing.T) {
	a, home, _, sysRoot := newSystemFixture(t)

	sysFile := filepath.Join(sysRoot, "alsa.conf")
	if err := os.WriteFile(sysFile, []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	homeFile := filepath.Join(home, ".asoundrc")
	if err := os.WriteFile(homeFile, []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.AddApp("alsa", []string{sysFile, homeFile}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	// Drift both, so both are candidates for restore.
	if err := os.WriteFile(sysFile, []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeFile, []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := a.RestoreApp("alsa")
	if err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}

	if len(result.Overwritten) != 1 || result.Overwritten[0] != ".asoundrc" {
		t.Errorf("the home file must restore normally, Overwritten = %v", result.Overwritten)
	}
	if got, err := os.ReadFile(homeFile); err != nil || string(got) != "original\n" {
		t.Errorf("home file = %q (err %v), want the backed-up bytes", got, err)
	}

	if len(result.Failed) != 1 || result.Failed[0].Path != sysFile {
		t.Fatalf("the system file must fail honestly, Failed = %+v", result.Failed)
	}
	if !strings.Contains(result.Failed[0].Reason, "administrator rights") {
		t.Errorf("the failure should say what is needed, said: %s", result.Failed[0].Reason)
	}
	// And the live system file is untouched — a refusal must not be a
	// half-write.
	if got, err := os.ReadFile(sysFile); err != nil || string(got) != "edited\n" {
		t.Errorf("system file = %q (err %v), want it left exactly as it was", got, err)
	}
}

// TestGetDiffPair_ReadsSystemPathsAndRefusesOthers covers the last read path.
// The scope is decided by the path, never by the caller, because what this
// returns is file contents shipped into the webview.
func TestGetDiffPair_ReadsSystemPathsAndRefusesOthers(t *testing.T) {
	a, _, _, sysRoot := newSystemFixture(t)

	sysFile := filepath.Join(sysRoot, "readable.conf")
	if err := os.WriteFile(sysFile, []byte("live bytes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.AddApp("cfg", []string{sysFile}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}
	if err := os.WriteFile(sysFile, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	pair, err := a.GetDiffPair("cfg", sysFile)
	if err != nil {
		t.Fatalf("GetDiffPair: %v", err)
	}
	if pair.Live.Text != "changed\n" {
		t.Errorf("Live = %q, want the current file", pair.Live.Text)
	}
	if pair.Vault.Text != "live bytes\n" {
		t.Errorf("Vault = %q, want the backed-up copy", pair.Vault.Text)
	}

	// An absolute path under no allowed root is refused even though the app
	// name is valid — the app name is not authority over the path.
	if _, err := a.GetDiffPair("cfg", "/var/log/anything"); err == nil {
		t.Error("GetDiffPair must refuse a path outside every allowed root")
	}
}
