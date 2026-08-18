package main

import (
	"os"
	"path/filepath"
	"testing"
)

// openElevatedSessionAt starts a real helper session rooted at dir and returns
// the path the helper resolved that root to.
//
// The helper permits any path under the session's vault root, so a temp
// directory stands in for /etc below. That is not a weakening: restoreSystemFile
// chooses its request before any privilege is involved, and the choice is
// identical wherever the destination sits. What this proves is which request it
// sends and what that request carries — the part a manual pass checks once and
// then never again.
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
