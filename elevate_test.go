package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openElevatedSessionAt starts a real helper session rooted at dir and returns
// the path the helper resolved that root to.
//
// The helper permits any path under the session's vault root, so a temp
// directory stands in for /etc below. That is not a weakening: restoreSystemFile
// and undoSystemEntry choose their request before any privilege is involved,
// and the choice is identical wherever the destination sits. What these tests
// prove is which request each one sends and what it carries — the part a manual
// pass checks once and then never again.
//
// The helper's own containment is proved in cmd/resonance-helper, against the
// real roots. The polkit prompt is the one link nothing here stands in for.
func openElevatedSessionAt(t *testing.T, dir string) string {
	t.Helper()
	withRoutedVault(t, dir)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := elevatedSession(resolved); err != nil {
		t.Fatalf("opening a helper session: %v", err)
	}
	return resolved
}

// The three tests below are undoSystemEntry's whole dispatch. Each kind is a
// different operation on the destination, and confusing them would leave a file
// where none belonged, or none where a file did.

func TestUndoSystemEntry_AbsentRemovesWhatTheRestoreCreated(t *testing.T) {
	root := openElevatedSessionAt(t, t.TempDir())

	dest := filepath.Join(root, "etc", "resonance-undo.conf")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("written by the restore\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := undoSystemEntry(SnapshotEntry{Kind: "absent", System: true}, dest, ""); err != nil {
		t.Fatalf("undoing an absent entry: %v", err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Errorf("the file the restore created survived the undo (lstat: %v)", err)
	}
}

func TestUndoSystemEntry_SymlinkGoesBackVerbatim(t *testing.T) {
	root := openElevatedSessionAt(t, t.TempDir())

	dest := filepath.Join(root, "etc", "resolv.conf")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	// The restore replaced a link with a regular file, so the undo has to put a
	// link back rather than write bytes over it.
	if err := os.WriteFile(dest, []byte("a regular file now\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Relative on purpose. /etc is full of relative links, and one rewritten
	// absolute is a different link that merely resolves the same way today.
	const target = "../run/systemd/resolve/stub-resolv.conf"
	if err := undoSystemEntry(SnapshotEntry{Kind: "symlink", LinkTarget: target, System: true}, dest, ""); err != nil {
		t.Fatalf("undoing a symlink entry: %v", err)
	}

	got, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("the link was not put back: %v", err)
	}
	if got != target {
		t.Errorf("link target = %q, want %q", got, target)
	}
}

func TestUndoSystemEntry_RegularCarriesBytesAndMode(t *testing.T) {
	root := openElevatedSessionAt(t, t.TempDir())

	backing := filepath.Join(t.TempDir(), "backing")
	want := []byte("what was there before the restore\n")
	// 0600 rather than 0644: a private file handed back world-readable is a
	// quieter failure than wrong bytes, and a mode dropped on the way through
	// would not show up in a content comparison at all.
	if err := os.WriteFile(backing, want, 0600); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(root, "etc", "resonance-undo-regular.conf")
	if err := undoSystemEntry(SnapshotEntry{Kind: "regular", System: true}, dest, backing); err != nil {
		t.Fatalf("undoing a regular entry: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading what the undo wrote: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("content = %q, want %q", got, want)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestUndoSystemEntry_RefusesAKindItDoesNotKnow(t *testing.T) {
	root := openElevatedSessionAt(t, t.TempDir())

	err := undoSystemEntry(SnapshotEntry{Kind: "fifo", System: true}, filepath.Join(root, "etc", "x"), "")
	if err == nil {
		t.Fatal("an unrecognised entry kind was accepted")
	}
	if !strings.Contains(err.Error(), "fifo") {
		t.Errorf("error %q does not name the kind it refused", err)
	}
}

func TestUndoSystemEntry_WillNotReadThroughASymlinkedBacking(t *testing.T) {
	root := openElevatedSessionAt(t, t.TempDir())

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("not the backup\n"), 0600); err != nil {
		t.Fatal(err)
	}
	backing := filepath.Join(t.TempDir(), "backing")
	if err := os.Symlink(secret, backing); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(root, "etc", "resonance-undo-symlinked.conf")
	if err := undoSystemEntry(SnapshotEntry{Kind: "regular", System: true}, dest, backing); err == nil {
		t.Fatal("a symlinked backing file was read through")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Error("the destination was written even though the backing was refused")
	}
}

func TestRestoreSystemFile_CarriesBytesAndMode(t *testing.T) {
	root := openElevatedSessionAt(t, t.TempDir())

	vaultFile := filepath.Join(t.TempDir(), "alsa.conf")
	want := []byte("pcm.!default {\n    type pulse\n}\n")
	if err := os.WriteFile(vaultFile, want, 0640); err != nil {
		t.Fatal(err)
	}

	// The alsa/ folder does not exist, so this also proves the restore creates
	// the parent chain rather than failing on a folder /etc happens not to have.
	dest := filepath.Join(root, "etc", "alsa", "alsa.conf")
	if err := restoreSystemFile(vaultFile, dest); err != nil {
		t.Fatalf("restoring a system file: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading the restored file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("content = %q, want %q", got, want)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0640 {
		t.Errorf("mode = %o, want 640", perm)
	}
}
