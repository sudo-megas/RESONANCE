package main

import (
	"os"
	"path/filepath"
)

// resonanceStateDir is the per-machine directory RESONANCE's own on-disk
// state lives under. Go's stdlib has no os.UserStateDir() (unlike
// UserConfigDir and UserCacheDir), so this follows the same XDG fallback
// convention by hand. Not ~/.cache: a cache is safe to lose.
//
// It held two things until v1.4.0 — the activity log and undo snapshots. Undo
// is gone, so the activity log is all that is left here. The function moved
// out of snapshot.go with the rest of that file deleted; it never belonged to
// undo, it was only shelved there.
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

// clearRemovedUndoState deletes the undo snapshot directory that versions
// before v1.4.0 left behind. Those directories hold the captured bytes of
// files from past restores; nothing reads them any more, and nobody could
// reasonably be expected to know what they were or that they were safe to
// delete by hand.
//
// Run on every start rather than once behind a marker file. RemoveAll on a
// path that is not there costs a failed stat and returns nil, so a marker
// would buy nothing except a second piece of state to keep consistent — and
// it would be wrong the moment someone restored an older copy of this
// directory from a backup.
//
// Best-effort on purpose. A state directory that cannot be cleaned is not a
// reason to refuse to start, and the only cost of failing is disk space.
func clearRemovedUndoState() {
	dir, err := resonanceStateDir()
	if err != nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(dir, "undo"))
}
