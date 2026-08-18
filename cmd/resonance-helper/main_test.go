package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The system roots are real here and deliberately not swapped for a temp
// directory: what these tests check about /etc and /usr is classification,
// which is pure string work, and a compiled-in allowlist that a test could
// redirect would not be a compiled-in allowlist. Everything that actually
// touches disk happens in the vault slot, which is legitimately per-session.

func vaultSession(t *testing.T) (*session, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := resolveRoot(dir)
	if err != nil {
		t.Fatalf("resolveRoot(%s): %v", dir, err)
	}
	return &session{greeted: true, vaultRoot: root}, root
}

func TestRootFor_RefusesEverythingOutsideTheAllowedRoots(t *testing.T) {
	s, vault := vaultSession(t)

	refused := []string{
		"/etcpasswd",       // shares a prefix with /etc and nothing else
		"/usrlocal",        // same trick against /usr
		"/etc",             // a root itself is not a thing to write
		"/usr",             //
		"/etc/../root/key", // climbs out once cleaned
		"/root/.ssh/authorized_keys",
		"/home/someone/.bashrc", // $HOME is the app's business, not the helper's
		"/",
		"etc/passwd",            // not absolute
		vault + "/../elsewhere", // climbs out of the vault
		filepath.Dir(vault),     // the vault's parent is not the vault
	}
	for _, p := range refused {
		if _, _, err := s.rootFor(p); err == nil {
			t.Errorf("rootFor(%q) was allowed; it must be refused", p)
		}
	}

	allowed := map[string]string{
		"/etc/alsa/alsa.conf":                  "/etc",
		"/usr/share/applications/foo.desktop":  "/usr",
		"/etc/./systemd/user/x.service":        "/etc",
		vault + "/myapp/.system/etc/alsa.conf": vault,
	}
	for p, wantRoot := range allowed {
		root, rel, err := s.rootFor(p)
		if err != nil {
			t.Errorf("rootFor(%q): %v", p, err)
			continue
		}
		if root != wantRoot {
			t.Errorf("rootFor(%q) root = %q, want %q", p, root, wantRoot)
		}
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			t.Errorf("rootFor(%q) rel = %q, want a path inside the root", p, rel)
		}
	}
}

func TestRootFor_WithoutAVaultOnlyTheSystemRootsAreAllowed(t *testing.T) {
	s := &session{greeted: true}
	dir := t.TempDir()

	if _, _, err := s.rootFor(filepath.Join(dir, "file")); err == nil {
		t.Error("a session with no vault accepted a path in a temp directory")
	}
	if _, _, err := s.rootFor("/etc/alsa/alsa.conf"); err != nil {
		t.Errorf("/etc must still be allowed with no vault: %v", err)
	}
}

func TestDescend_RefusesASymlinkStandingWhereAFolderShouldBe(t *testing.T) {
	s, vault := vaultSession(t)

	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(vault, "alsa")); err != nil {
		t.Fatal(err)
	}

	err := s.write(filepath.Join(vault, "alsa", "alsa.conf"), []byte("x"), 0644)
	if err == nil {
		t.Fatal("wrote through a symlinked parent directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should say why it refused, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(elsewhere, "alsa.conf")); statErr == nil {
		t.Error("the write landed outside the vault")
	}
}

func TestWrite_ReplacesASymlinkInsteadOfFollowingIt(t *testing.T) {
	s, vault := vaultSession(t)

	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(vault, "alsa.conf")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if err := s.write(link, []byte("restored"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got, _ := os.ReadFile(outside); string(got) != "untouched" {
		t.Errorf("the write followed the link and changed %s to %q", outside, got)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("the destination is still a symlink")
	}
	if got, _ := os.ReadFile(link); string(got) != "restored" {
		t.Errorf("destination = %q, want %q", got, "restored")
	}
}

func TestWrite_CreatesMissingFoldersAndCarriesTheMode(t *testing.T) {
	s, vault := vaultSession(t)

	dest := filepath.Join(vault, "myapp", ".system", "etc", "alsa", "alsa.conf")
	if err := s.write(dest, []byte("hello"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	if got, _ := os.ReadFile(dest); string(got) != "hello" {
		t.Errorf("content = %q", got)
	}

	// The temp file it wrote through must not survive.
	entries, _ := os.ReadDir(filepath.Dir(dest))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".resonance-tmp-") {
			t.Errorf("left a temp file behind: %s", e.Name())
		}
	}
}

func TestRemove_DoesNotCreateFoldersOnItsWayToDeletingSomething(t *testing.T) {
	s, vault := vaultSession(t)

	if err := s.remove(filepath.Join(vault, "never", "existed"), false); err != nil {
		t.Fatalf("removing something absent should succeed quietly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vault, "never")); err == nil {
		t.Error("remove created the folder it was looking through")
	}

	file := filepath.Join(vault, "gone.conf")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.remove(file, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the file is still there")
	}
}

func TestRemoveAll_StaysInsideTheSubtreeItWasGiven(t *testing.T) {
	s, vault := vaultSession(t)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(vault, "myapp")
	if err := os.MkdirAll(filepath.Join(app, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app, "escape")); err != nil {
		t.Fatal(err)
	}

	if err := s.remove(app, true); err != nil {
		t.Fatalf("removeAll: %v", err)
	}
	if _, err := os.Stat(app); err == nil {
		t.Error("the app folder is still there")
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Error("removeAll followed a symlink out of the subtree")
	}
}

func TestRename_RefusesToCrossBetweenRoots(t *testing.T) {
	s, vault := vaultSession(t)

	file := filepath.Join(vault, "a.conf")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := s.rename(file, "/etc/a.conf"); err == nil {
		t.Error("renamed from the vault into /etc")
	}
	if err := s.rename("/etc/hostname", filepath.Join(vault, "hostname")); err == nil {
		t.Error("renamed from /etc into the vault")
	}
	if err := s.rename(file, filepath.Join(vault, "sub", "b.conf")); err != nil {
		t.Errorf("a rename within one root should work: %v", err)
	}
}

func TestSymlink_RecreatesALinkVerbatimWithoutResolvingIt(t *testing.T) {
	s, vault := vaultSession(t)

	dest := filepath.Join(vault, "resolv.conf")
	if err := s.symlink(dest, "../run/systemd/resolve/stub-resolv.conf"); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, err := os.Readlink(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got != "../run/systemd/resolve/stub-resolv.conf" {
		t.Errorf("target = %q, want it stored verbatim", got)
	}
}

// --- protocol -----------------------------------------------------------

// converse feeds a series of requests through serve and returns the replies,
// which is how RESONANCE will actually talk to this binary.
func converse(t *testing.T, reqs ...request) []response {
	t.Helper()
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for _, r := range reqs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := serve(&in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var got []response
	dec := json.NewDecoder(&out)
	for {
		var resp response
		if err := dec.Decode(&resp); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		got = append(got, resp)
	}
	if len(got) != len(reqs) {
		t.Fatalf("got %d replies for %d requests", len(got), len(reqs))
	}
	return got
}

func TestSession_RefusesWorkBeforeHelloAndASecondHello(t *testing.T) {
	dir := t.TempDir()

	got := converse(t,
		request{Op: "write", Path: filepath.Join(dir, "a"), Data: []byte("x")},
		request{Op: "hello", VaultRoot: dir},
		request{Op: "hello", VaultRoot: "/"},
		request{Op: "write", Path: filepath.Join(dir, "a"), Data: []byte("x"), Mode: 0644},
		request{Op: "teleport", Path: filepath.Join(dir, "a")},
	)

	if got[0].OK {
		t.Error("accepted work before the session was opened")
	}
	if !got[1].OK {
		t.Errorf("hello failed: %s", got[1].Error)
	}
	if got[2].OK {
		t.Error("a second hello was accepted; the vault root must be fixed once")
	}
	if !got[3].OK {
		t.Errorf("write after hello failed: %s", got[3].Error)
	}
	if got[4].OK {
		t.Error("an unknown operation was accepted")
	}

	// The refused second hello must not have moved the vault root to "/".
	if err := os.WriteFile(filepath.Join(dir, "probe"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	after := converse(t, request{Op: "hello", VaultRoot: dir}, request{Op: "remove", Path: "/tmp"})
	if after[1].OK {
		t.Error("the helper agreed to remove /tmp, which is under no allowed root")
	}
}

// A vault copy of a real config file goes well past the 64KB a line scanner
// would cap a token at, and it would fail only for large files and only in
// the field. json.Decoder streams instead; this is the test that pins it.
func TestProtocol_CarriesAPayloadLargerThanAScannerWouldAllow(t *testing.T) {
	dir := t.TempDir()
	big := bytes.Repeat([]byte("resonance "), 40_000) // 400KB

	dest := filepath.Join(dir, "big.conf")
	got := converse(t,
		request{Op: "hello", VaultRoot: dir},
		request{Op: "write", Path: dest, Data: big, Mode: 0644},
	)
	if !got[1].OK {
		t.Fatalf("write of %d bytes failed: %s", len(big), got[1].Error)
	}
	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, big) {
		t.Errorf("wrote %d bytes, want %d", len(written), len(big))
	}
}

func TestSession_ReportsRefusalsWithoutDyingOnThem(t *testing.T) {
	dir := t.TempDir()

	got := converse(t,
		request{Op: "hello", VaultRoot: dir},
		request{Op: "write", Path: "/root/.ssh/authorized_keys", Data: []byte("key"), Mode: 0600},
		request{Op: "write", Path: filepath.Join(dir, "fine.conf"), Data: []byte("x"), Mode: 0644},
	)
	if got[1].OK {
		t.Error("wrote outside every allowed root")
	}
	if got[1].Error == "" {
		t.Error("a refusal must say why")
	}
	if !got[2].OK {
		t.Errorf("one refusal ended the session: %s", got[2].Error)
	}
}
