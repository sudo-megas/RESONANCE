package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// resonanceStateDir is the per-machine state directory RESONANCE's own
// on-disk state (undo snapshots, the activity log) lives under. Go's
// stdlib has no os.UserStateDir() (unlike UserConfigDir / UserCacheDir),
// so this follows the same XDG fallback convention by hand. Not
// ~/.cache — a cache is safe to lose, and undo snapshots exist nowhere
// else once a restore has run.
func resonanceStateDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "resonance"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "resonance"), nil
}

// undoRootDir is where undo snapshots live, one subdirectory below
// resonanceStateDir() — kept separate so undo's directory-per-app layout
// can never collide with the activity log or any future state stored
// alongside it.
func undoRootDir() (string, error) {
	dir, err := resonanceStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "undo"), nil
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
//
// The old canonicalDir is moved aside rather than os.RemoveAll'd in place:
// RemoveAll walks and deletes the tree file by file, so a crash partway
// through can corrupt the old snapshot before the new one has replaced it.
// A rename is a single atomic step either way, so the old snapshot is
// never in a half-deleted state — worst case after a crash, a harmless
// ".stale" directory is left for the next commitSnapshot call to clean up.
//
// That cleanup is a recovery, not a blind delete: a crash landing between
// the two renames below leaves canonicalDir gone and staleDir holding the
// only surviving copy of the *previous* snapshot, with nothing in between
// GetUndoInfo/readSnapshot to find. The next commitSnapshot call restores
// staleDir to canonicalDir first, before doing anything else, so that
// snapshot is reinstated rather than silently deleted the moment this
// function's cleanup would otherwise have run again. The same recovery
// covers a failed final rename: if pendingDir can't be renamed onto
// canonicalDir, the old snapshot is moved back into place so a valid
// snapshot stays reachable instead of disappearing.
func commitSnapshot(pendingDir, canonicalDir string, snap RestoreSnapshot) error {
	if err := writeSnapshot(pendingDir, snap); err != nil {
		return err
	}
	staleDir := canonicalDir + ".stale"

	if _, err := os.Stat(canonicalDir); err != nil {
		if _, staleErr := os.Stat(staleDir); staleErr == nil {
			if err := os.Rename(staleDir, canonicalDir); err != nil {
				return err
			}
		}
	}

	if _, err := os.Stat(canonicalDir); err == nil {
		_ = os.RemoveAll(staleDir)
		if err := os.Rename(canonicalDir, staleDir); err != nil {
			return err
		}
	}
	if err := os.Rename(pendingDir, canonicalDir); err != nil {
		if _, statErr := os.Stat(staleDir); statErr == nil {
			_ = os.Rename(staleDir, canonicalDir)
		}
		return err
	}
	_ = os.RemoveAll(staleDir)
	return nil
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
			if err := removeAnythingAt(destPath); err != nil {
				restoreErr = err
			} else {
				restoreErr = copyFile(filepath.Join(canonicalDir, filepath.FromSlash(entry.Path)), destPath)
			}
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

	recordActivity("undo", appName, summarizeUndoActivity(result))
	return result, nil
}

// summarizeUndoActivity turns UndoResult's counts into a short
// human-readable description for the activity log, e.g. "3 restored, 1
// failed". Counts of zero are omitted entirely.
func summarizeUndoActivity(result UndoResult) string {
	var parts []string
	if n := len(result.Restored); n > 0 {
		parts = append(parts, fmt.Sprintf("%d restored", n))
	}
	if n := len(result.Failed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// discardUndoSnapshot removes appName's undo snapshot, including any pending
// directory abandoned by an interrupted capture.
//
// Best-effort on purpose. It runs after an app removal has already committed
// its vault work, and a snapshot directory that refuses to go must not turn a
// removal that genuinely succeeded into a reported failure.
func discardUndoSnapshot(appName string) {
	root, err := undoRootDir()
	if err != nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(root, appName))
	_ = os.RemoveAll(filepath.Join(root, appName+".pending"))
}

// renameUndoSnapshot moves an undo snapshot so it follows a renamed app.
//
// Snapshots are keyed by app name and nothing else. Without this the
// snapshot would stay behind under the old name: unreachable from the
// renamed app, and waiting to be inherited by whatever app next takes that
// name — at which point the restore overlay would offer to replay one app's
// pre-restore bytes over another app's live files.
func renameUndoSnapshot(oldName, newName string) {
	root, err := undoRootDir()
	if err != nil {
		return
	}
	oldDir := filepath.Join(root, oldName)
	if _, err := os.Lstat(oldDir); err != nil {
		return // nothing was ever captured for this app
	}
	newDir := filepath.Join(root, newName)
	// Anything already sitting under the new name belongs to a different app
	// instance. Adopting it would be the exact confusion this function exists
	// to prevent, so it is discarded rather than kept.
	_ = os.RemoveAll(newDir)
	_ = os.Rename(oldDir, newDir)
	_ = os.RemoveAll(filepath.Join(root, oldName+".pending"))
}
