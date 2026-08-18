package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxDiffBytes = 1 << 20 // 1 MiB
	maxDiffLines = 20000
)

// FileContents is one side of a diff pair, gated server-side before
// anything crosses the Wails IPC boundary — oversized or binary content
// never reaches the frontend as bytes, only these flags.
type FileContents struct {
	Text     string `json:"text"`
	Binary   bool   `json:"binary"`
	TooLarge bool   `json:"tooLarge"`
	Missing  bool   `json:"missing"`
	Size     int64  `json:"size"` // populated whenever the file exists, even if TooLarge or Binary
}

// DiffPair is one file's live ($HOME) and vault content, read together so
// the frontend's diff always compares a matched pair.
type DiffPair struct {
	Live  FileContents `json:"live"`
	Vault FileContents `json:"vault"`
}

// RestoreFailure reports one file RestoreApp couldn't restore.
type RestoreFailure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// RestoreResult reports what RestoreApp actually did, file by file.
type RestoreResult struct {
	New         []string         `json:"new"`
	Overwritten []string         `json:"overwritten"`
	Skipped     []string         `json:"skipped"`
	Failed      []RestoreFailure `json:"failed"`
}

// readFileContents loads a file for diffing, applying the size/line/binary
// gates before any content is returned. A file that can't be read reports
// Missing rather than an error — that's a valid, expected diff state (the
// live side of a "New" restore has nothing there yet).
func readFileContents(path string) FileContents {
	info, err := os.Stat(path)
	if err != nil {
		return FileContents{Missing: true}
	}
	if !info.Mode().IsRegular() || info.Size() > maxDiffBytes {
		return FileContents{TooLarge: true, Size: info.Size()}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileContents{Missing: true}
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return FileContents{Binary: true, Size: info.Size()}
	}
	if bytes.Count(data, []byte{'\n'}) > maxDiffLines {
		return FileContents{TooLarge: true, Size: info.Size()}
	}
	return FileContents{Text: string(data), Size: info.Size()}
}

// GetDiffPair reads both sides of one file for the restore-preview's
// expandable diff. Called lazily, once per file, only when an
// already-open "Overwrite" row is expanded — never prefetched for a whole
// app. appName is checked against the manifest's own app list (not just
// used to build a path directly) for the same reason relPath is
// re-validated below: this reads real files from an app-name/path pair
// supplied across the IPC boundary.
func (a *App) GetDiffPair(appName, relPath string) (DiffPair, error) {
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return DiffPair{}, errors.New("no vault path set")
	}
	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return DiffPair{}, err
	}
	found := false
	for _, app := range m.Apps {
		if app.Name == appName {
			found = true
			break
		}
	}
	if !found {
		return DiffPair{}, errors.New("no such app")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return DiffPair{}, err
	}

	// The scope is read off the path itself and never taken from the caller.
	// relPath arrives across the IPC boundary, and what this function does
	// with it is read a file and ship the bytes into the webview — so the
	// gate it passes here is classifySource, the same one that let it into
	// the manifest in the first place. An absolute path is a system path; a
	// relative one is a home path; a path under neither is refused.
	liveAbs := filepath.FromSlash(relPath)
	if !filepath.IsAbs(liveAbs) {
		liveAbs = filepath.Join(home, liveAbs)
	}
	scope, _, err := classifySource(liveAbs, home)
	if err != nil {
		return DiffPair{}, err
	}

	vaultAppDir := filepath.Join(settings.VaultPath, appName)
	vaultAbs := filepath.Join(vaultAppDir, filepath.FromSlash(vaultRelFor(scope, relPath)))
	if _, err := relativeUnder(vaultAbs, vaultAppDir); err != nil {
		return DiffPair{}, err
	}
	// A symlink planted at vaultAbs by whoever last had write access to the
	// vault would otherwise have its target's content silently read and
	// shipped across the IPC boundary as this file's "vault" side.
	if err := refuseSymlink(vaultAbs); err != nil {
		return DiffPair{}, err
	}
	// refuseSymlink declines only the final component; every directory above
	// it is resolved normally. <vault>/bash/x -> $HOME/.ssh with a manifest
	// path of "x/id_rsa" would therefore read a private key and ship it into
	// the webview labelled as this app's vault copy. This is the same
	// resolved-containment question v1.2.1 answered for every other read and
	// write, asked here for the last read path that was still missing it.
	if vaultDirEscapes(settings.VaultPath, vaultAbs) {
		return DiffPair{}, errors.New("can't read the vault copy — something in the vault points outside it")
	}

	return DiffPair{
		Live:  readFileContents(liveAbs),
		Vault: readFileContents(vaultAbs),
	}, nil
}

// RestoreApp copies every file in the named app from the vault onto the
// live system. Per-file-independent, matching UpdateFromSource's safety
// philosophy — not validate-everything-first like AddApp: one file's
// problem shouldn't block restoring the rest, and restore never mutates
// manifest.json, so there's no shared state a partial failure could leave
// inconsistent.
//
// Each file's state is recomputed here via fileDriftRow rather than
// trusting whatever the frontend's preview fetched earlier — opening the
// preview and clicking Restore aren't atomic, so the commit re-checks
// instead of acting on a picture the frontend drew earlier.
//
// Nothing is tucked aside first. Until v1.4.0 every file's prior state was
// captured and committed before any of them were written, so that one button
// could put it all back; that button is gone and so is the capture. A restore
// now does exactly what it says and nothing else, and the preview and the diff
// are where it gets reconsidered.
func (a *App) RestoreApp(name string) (RestoreResult, error) {
	result := RestoreResult{New: []string{}, Overwritten: []string{}, Skipped: []string{}, Failed: []RestoreFailure{}}

	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return result, errors.New("no vault path set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return result, err
	}
	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return result, err
	}

	appIndex := -1
	for i, app := range m.Apps {
		if app.Name == name {
			appIndex = i
			break
		}
	}
	if appIndex == -1 {
		return result, errors.New("no such app")
	}

	// pendingMutation is a file that has passed every check and is queued to
	// be written to the live system. Collecting first and writing second is
	// kept from the days when a snapshot had to be committed in between: it
	// still means a bad path anywhere in the app is found before any file is
	// written, rather than half way through.
	type pendingMutation struct {
		path       string
		destPath   string
		vaultFile  string
		wasMissing bool
		scope      pathScope
	}

	var mutations []pendingMutation

	app := m.Apps[appIndex]
	vaultAppDir := filepath.Join(settings.VaultPath, app.Name)

	// One body over both scopes, for the same reason UpdateFromSource shares
	// one: a system file must not be able to reach the live filesystem on a
	// different set of checks from a home file. Everything here — reading the
	// vault copy, checking the path — is unprivileged. The single privileged
	// step is the write itself, further down.
	collect := func(scope pathScope, f ManifestFile) {
		row := fileDriftRow(home, settings.VaultPath, app.Name, scope, f)
		if row.State == "ok" {
			result.Skipped = append(result.Skipped, f.Path)
			return
		}
		// Nothing to restore from, so say that plainly instead of reaching the
		// copy and failing there with a bare errno.
		if row.State == "vaultMissing" || row.State == "vaultDamaged" {
			reason := "the vault's copy of this file is missing — update from source to back it up again"
			if row.State == "vaultDamaged" {
				reason = "the vault's copy of this file doesn't match what was backed up — update from source to replace it"
			}
			result.Failed = append(result.Failed, RestoreFailure{Path: f.Path, Reason: reason})
			return
		}

		destPath := sourceAbs(home, scope, f.Path)
		if gotScope, _, err := classifySource(destPath, home); err != nil || gotScope != scope {
			reason := "this entry's path is outside everywhere RESONANCE writes"
			if err != nil {
				reason = err.Error()
			}
			result.Failed = append(result.Failed, RestoreFailure{Path: f.Path, Reason: reason})
			return
		}

		vaultFile := filepath.Join(vaultAppDir, filepath.FromSlash(vaultRelFor(scope, f.Path)))
		mutations = append(mutations, pendingMutation{
			path:       f.Path,
			destPath:   destPath,
			vaultFile:  vaultFile,
			wasMissing: row.State == "missing",
			scope:      scope,
		})
	}
	for _, f := range app.Files {
		collect(scopeHome, f)
	}
	for _, f := range app.SystemFiles {
		collect(scopeSystem, f)
	}

	// A restore is final. Nothing is tucked aside first and there is no way
	// back from here, which is the whole shape of the app after v1.4.0: one
	// direction into the vault, one direction out of it. The preview and the
	// diff are where a restore is reconsidered, and they are before this
	// point, not after it.
	for _, mut := range mutations {
		if err := writeRestoredFile(mut.scope, mut.vaultFile, mut.destPath); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: mut.path, Reason: err.Error()})
			continue
		}

		if mut.wasMissing {
			result.New = append(result.New, mut.path)
		} else {
			result.Overwritten = append(result.Overwritten, mut.path)
		}
	}

	recordActivity("restore", name, summarizeRestoreActivity(result))
	return result, nil
}

// writeRestoredFile puts one vault copy back onto the live filesystem. It is
// the only place in the program that writes outside $HOME.
//
// The vault-side guard runs first and identically for both scopes, because
// the vault is ours to read either way: a symlink planted at vaultFile by
// whoever last had write access to the drive would otherwise have its target
// read and copied straight onto destPath — arbitrary file disclosure, and on
// the system side it would be disclosure into a root-owned folder.
//
// The two scopes then split on who does the writing, not on how carefully it
// is done. A home destination is unlinked and copied here, unprivileged. A
// system destination is handed to the helper whole — unlinking a planted
// symlink in /etc and creating the file there are both privileged, and doing
// them in one place at root closes the window between checking a path here
// and writing it a moment later. The helper walks the path down from /etc one
// component at a time, refusing any symlink standing where a folder should
// be, and lands the file with a rename, which replaces a symlink at the
// destination rather than following it.
func writeRestoredFile(scope pathScope, vaultFile, destPath string) error {
	if err := refuseSymlink(vaultFile); err != nil {
		return err
	}
	if scope == scopeSystem {
		return restoreSystemFile(vaultFile, destPath)
	}
	if err := removeSymlinkAt(destPath); err != nil {
		return err
	}
	return copyFile(vaultFile, destPath)
}

// summarizeRestoreActivity turns RestoreResult's counts into a short
// human-readable description for the activity log, e.g. "2 new, 1
// overwritten, 1 failed". Counts of zero are omitted entirely.
func summarizeRestoreActivity(result RestoreResult) string {
	var parts []string
	if n := len(result.New); n > 0 {
		parts = append(parts, fmt.Sprintf("%d new", n))
	}
	if n := len(result.Overwritten); n > 0 {
		parts = append(parts, fmt.Sprintf("%d overwritten", n))
	}
	if n := len(result.Skipped); n > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", n))
	}
	if n := len(result.Failed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// removeSymlinkAt clears a symlink standing at path — including a broken
// one whose target no longer exists — so the following copyFile always
// creates a fresh regular file there instead of writing through it into
// whatever it points at, possibly outside $HOME entirely. Lstat (not
// Stat) is used because it reports the symlink itself rather than
// following it: a Stat-based check would see a broken symlink's absent
// target and wrongly conclude there's nothing at path at all.
func removeSymlinkAt(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return nil // nothing at path yet — copyFile creates it cleanly
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil // a real file (or directory) — copyFile overwrites/rejects normally
	}
	return os.Remove(path)
}
