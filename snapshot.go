package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const snapshotFileName = "snapshot.json"

// SnapshotEntry records one file's exact state immediately before
// RestoreApp overwrote or created it, so UndoRestore can put it back.
type SnapshotEntry struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"` // "absent" | "regular" | "symlink"
	LinkTarget string `json:"linkTarget,omitempty"`
}

// RestoreSnapshot is one app's full pre-restore state, written to
// undoRootDir()/<app>/snapshot.json. It lives entirely outside the vault —
// it describes a mutation to this machine's $HOME, not a portable property
// of the vault, so it must never travel on a vault Copy/Move.
type RestoreSnapshot struct {
	App       string          `json:"app"`
	CreatedAt string          `json:"createdAt"`
	Entries   []SnapshotEntry `json:"entries"`
}

// UndoInfo is the trimmed, IPC-safe summary of whether an app has an undo
// snapshot available — SnapshotEntry's internals never cross the boundary.
type UndoInfo struct {
	Available bool   `json:"available"`
	CreatedAt string `json:"createdAt"`
	FileCount int    `json:"fileCount"`
}

// UndoResult reports what UndoRestore actually did, file by file — the
// same shape as RestoreResult's Failed side, for the same
// per-entry-independent reason.
type UndoResult struct {
	Restored []string         `json:"restored"`
	Failed   []RestoreFailure `json:"failed"`
}

// undoRootDir is the per-machine state directory undo snapshots live
// under. Go's stdlib has no os.UserStateDir() (unlike UserConfigDir /
// UserCacheDir), so this follows the same XDG fallback convention by
// hand. Not ~/.cache — a cache is safe to lose, and these bytes exist
// nowhere else once a restore has run.
func undoRootDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "resonance", "undo"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "resonance", "undo"), nil
}

// captureEntry records destPath's current state into pendingDir before
// RestoreApp is about to mutate it. A regular file's bytes are copied in
// full; an absent path or a symlink only needs its metadata captured. Any
// error here means the caller must not mutate destPath either —
// fail-closed, matching CORE.md's requirement that prior state is tucked
// aside before it's overwritten, not best-effort.
func captureEntry(pendingDir, relPath, destPath string) (SnapshotEntry, error) {
	info, err := os.Lstat(destPath)
	if os.IsNotExist(err) {
		return SnapshotEntry{Path: relPath, Kind: "absent"}, nil
	}
	if err != nil {
		return SnapshotEntry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(destPath)
		if err != nil {
			return SnapshotEntry{}, err
		}
		return SnapshotEntry{Path: relPath, Kind: "symlink", LinkTarget: target}, nil
	}
	if !info.Mode().IsRegular() {
		return SnapshotEntry{}, errors.New(relPath + " is not a regular file")
	}
	if err := copyFile(destPath, filepath.Join(pendingDir, filepath.FromSlash(relPath))); err != nil {
		return SnapshotEntry{}, err
	}
	return SnapshotEntry{Path: relPath, Kind: "regular"}, nil
}

// writeSnapshot is the one point where a snapshot becomes durable. Called
// only on the pending directory — see RestoreApp's stage-then-commit
// sequence for why the canonical directory is never written to directly.
func writeSnapshot(dir string, snap RestoreSnapshot) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, snapshotFileName), data, 0644)
}

// readSnapshot loads dir/snapshot.json. Any failure — missing, corrupt,
// unreadable — is reported the same way, as "no snapshot", matching
// GetMirrorRows' philosophy for locally-cached state: a bad cache means
// nothing is offered, not a crash.
func readSnapshot(dir string) (RestoreSnapshot, bool) {
	data, err := os.ReadFile(filepath.Join(dir, snapshotFileName))
	if err != nil {
		return RestoreSnapshot{}, false
	}
	var snap RestoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return RestoreSnapshot{}, false
	}
	return snap, true
}

// removeAnythingAt clears whatever's at path — regular file or symlink —
// so a fresh write can land cleanly. Unlike RestoreApp's removeSymlinkAt,
// this doesn't care what's currently there: undo may need to replace a
// regular file (what a normal restore left behind) with a symlink.
func removeAnythingAt(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// commitSnapshot finishes the stage-then-commit sequence RestoreApp begins
// by capturing entries into pendingDir: only once the new snapshot is
// fully written to pendingDir does the canonical directory get replaced.
// If writeSnapshot fails, pendingDir is left in place (abandoned, cleaned
// up opportunistically by the next RestoreApp call) and canonicalDir is
// left completely untouched — the crash-safety property this whole design
// exists for: a disk-full write here can never destroy a still-valid old
// snapshot.
func commitSnapshot(pendingDir, canonicalDir string, snap RestoreSnapshot) error {
	if err := writeSnapshot(pendingDir, snap); err != nil {
		return err
	}
	if err := os.RemoveAll(canonicalDir); err != nil {
		return err
	}
	return os.Rename(pendingDir, canonicalDir)
}

// GetUndoInfo reports whether appName has a pending undo snapshot, for the
// restore-confirm overlay to check before falling back to its "nothing to
// restore" toast.
func (a *App) GetUndoInfo(appName string) (UndoInfo, error) {
	root, err := undoRootDir()
	if err != nil {
		return UndoInfo{}, err
	}
	snap, ok := readSnapshot(filepath.Join(root, appName))
	if !ok {
		return UndoInfo{}, nil
	}
	return UndoInfo{Available: true, CreatedAt: snap.CreatedAt, FileCount: len(snap.Entries)}, nil
}

// UndoRestore replays appName's snapshot back onto the live system,
// per-entry-independent like RestoreApp itself. Every path is
// re-validated through homeRelative before any write — snapshot.json is
// on-disk state, the same trust boundary as manifest.json, never trusted
// blindly.
func (a *App) UndoRestore(appName string) (UndoResult, error) {
	result := UndoResult{Restored: []string{}, Failed: []RestoreFailure{}}

	home, err := os.UserHomeDir()
	if err != nil {
		return result, err
	}
	root, err := undoRootDir()
	if err != nil {
		return result, err
	}
	canonicalDir := filepath.Join(root, appName)
	snap, ok := readSnapshot(canonicalDir)
	if !ok {
		return result, errors.New("no undo snapshot available")
	}

	allSucceeded := true
	for _, entry := range snap.Entries {
		destPath := filepath.Join(home, filepath.FromSlash(entry.Path))
		if _, err := homeRelative(destPath, home); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: entry.Path, Reason: err.Error()})
			allSucceeded = false
			continue
		}

		var restoreErr error
		switch entry.Kind {
		case "absent":
			restoreErr = removeAnythingAt(destPath)
		case "symlink":
			if err := removeAnythingAt(destPath); err != nil {
				restoreErr = err
			} else {
				restoreErr = os.Symlink(entry.LinkTarget, destPath)
			}
		case "regular":
			restoreErr = copyFile(filepath.Join(canonicalDir, filepath.FromSlash(entry.Path)), destPath)
		default:
			restoreErr = errors.New("unknown snapshot entry kind: " + entry.Kind)
		}

		if restoreErr != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: entry.Path, Reason: restoreErr.Error()})
			allSucceeded = false
			continue
		}
		result.Restored = append(result.Restored, entry.Path)
	}

	if allSucceeded {
		_ = os.RemoveAll(canonicalDir)
	}

	return result, nil
}
