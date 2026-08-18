package main

import "errors"

// This file is RESONANCE's side of the privilege boundary. Nothing here runs
// as root; it decides what to ask the helper for, and the helper — a separate
// binary launched through pkexec — decides whether to do it.
//
// The boundary exists for one reason. /etc and /usr became valid sources in
// v1.3.0, and backing them up needs nothing at all: reading a world-readable
// file and writing into your own vault are both things you can already do.
// Putting one back is the only operation in the program that a user cannot
// perform as themselves, so it is the only operation that crosses this line.

// errNeedsAdmin is returned wherever a write outside $HOME is asked for and
// no helper session is available to perform it.
var errNeedsAdmin = errors.New(
	"this file lives in a folder that belongs to root, so putting it back needs administrator rights")

// restoreSystemFile writes one vault copy back to a destination under /etc or
// /usr. The unlink-then-write happens inside the helper rather than here: at
// this end the app cannot unlink a planted symlink in /etc anyway, and
// checking a path here and writing it a moment later at root is exactly the
// race O_NOFOLLOW inside the helper exists to avoid.
func restoreSystemFile(vaultFile, destPath string) error {
	return errNeedsAdmin
}

// undoSystemEntry replays one snapshot entry onto a destination under /etc or
// /usr. backing is the captured bytes beside snapshot.json, used only by the
// "regular" kind; "absent" deletes and "symlink" recreates a link, and all
// three are privileged because the folder is not ours.
func undoSystemEntry(entry SnapshotEntry, destPath, backing string) error {
	return errNeedsAdmin
}
