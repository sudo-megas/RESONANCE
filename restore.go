package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"time"
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

	liveAbs := filepath.Join(home, filepath.FromSlash(relPath))
	if _, err := homeRelative(liveAbs, home); err != nil {
		return DiffPair{}, err
	}

	vaultAppDir := filepath.Join(settings.VaultPath, appName)
	vaultAbs := filepath.Join(vaultAppDir, filepath.FromSlash(relPath))
	if _, err := homeRelative(vaultAbs, vaultAppDir); err != nil {
		return DiffPair{}, err
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
// instead of acting on a possibly-stale snapshot.
//
// Before any file is mutated, its current state is captured into a
// pending undo directory (never the canonical one — see the commit step
// below). Capture failure is fail-closed: that file is skipped, same as
// any other per-file failure, so a file is never mutated without its
// prior state safely tucked aside first.
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

	undoRoot, err := undoRootDir()
	if err != nil {
		return result, err
	}
	canonicalUndoDir := filepath.Join(undoRoot, name)
	pendingUndoDir := filepath.Join(undoRoot, name+".pending")
	// Abandoned by a previous run that never reached the commit step below
	// (crash, or the final writeSnapshot itself failing) — the old
	// canonical snapshot, if any, was left untouched, so this is just
	// clearing stale scratch space, never data anyone still needs.
	_ = os.RemoveAll(pendingUndoDir)

	var entries []SnapshotEntry

	app := m.Apps[appIndex]
	for _, f := range app.Files {
		row := fileDriftRow(home, f)
		if row.State == "ok" {
			result.Skipped = append(result.Skipped, f.Path)
			continue
		}

		destPath := filepath.Join(home, filepath.FromSlash(f.Path))
		if _, err := homeRelative(destPath, home); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: f.Path, Reason: err.Error()})
			continue
		}

		entry, err := captureEntry(pendingUndoDir, f.Path, destPath)
		if err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: f.Path, Reason: err.Error()})
			continue
		}
		// Recorded as soon as capture succeeds, before the mutation below
		// is attempted — if removeSymlinkAt or copyFile then fails
		// partway, destPath may already have been altered, so its prior
		// state still belongs in the snapshot even though this file also
		// lands in Failed.
		entries = append(entries, entry)

		vaultFile := filepath.Join(settings.VaultPath, app.Name, filepath.FromSlash(f.Path))

		if err := removeSymlinkAt(destPath); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: f.Path, Reason: err.Error()})
			continue
		}
		if err := copyFile(vaultFile, destPath); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: f.Path, Reason: err.Error()})
			continue
		}

		if row.State == "missing" {
			result.New = append(result.New, f.Path)
		} else {
			result.Overwritten = append(result.Overwritten, f.Path)
		}
	}

	// Stage-then-commit: only touch the canonical snapshot once the new
	// one has fully succeeded. A fully no-op restore (nothing captured)
	// leaves any existing snapshot exactly as it was — that's what makes
	// retention "keep 1 per app" without a separate prune step. If
	// writeSnapshot itself fails, pendingUndoDir is simply abandoned and
	// the old canonical snapshot survives untouched; worst case this run's
	// undo capability is lost, never "undo replays wrong bytes."
	if len(entries) > 0 {
		snap := RestoreSnapshot{
			App:       name,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Entries:   entries,
		}
		_ = commitSnapshot(pendingUndoDir, canonicalUndoDir, snap)
	}

	return result, nil
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
