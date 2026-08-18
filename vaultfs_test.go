package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// These tests drive the real cmd/resonance-helper binary over a real pipe,
// unprivileged. `go test` cannot answer a polkit dialog, so what is skipped
// is pkexec and nothing else: the protocol, the path proving and every
// refusal below are the same code that runs as root on an installed system.

var (
	helperBuildOnce sync.Once
	helperBuildPath string
	helperBuildErr  error
)

func buildHelperBinary(t *testing.T) string {
	t.Helper()
	helperBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "resonance-helper-test-*")
		if err != nil {
			helperBuildErr = err
			return
		}
		helperBuildPath = filepath.Join(dir, "resonance-helper")
		out, err := exec.Command("go", "build", "-o", helperBuildPath, "./cmd/resonance-helper").CombinedOutput()
		if err != nil {
			helperBuildErr = err
			t.Logf("go build: %s", out)
		}
	})
	if helperBuildErr != nil {
		t.Fatalf("building the helper: %v", helperBuildErr)
	}
	return helperBuildPath
}

// withRoutedVault makes a perfectly ordinary temp vault take the elevated
// route, so the routing itself can be tested.
//
// The writability answer is planted rather than produced, because the two
// halves cannot be tested at once without root: a directory made unwritable
// with chmod would refuse the helper as readily as the app, since in a test
// they are the same user. vaultWritability's own reading of a read-only
// directory is tested separately, below.
func withRoutedVault(t *testing.T, vaultPath string) {
	t.Helper()
	bin := buildHelperBinary(t)

	resolved, err := filepath.EvalSymlinks(vaultPath)
	if err != nil {
		t.Fatal(err)
	}

	originalCommand := helperCommand
	helperCommand = func() (*exec.Cmd, error) { return exec.Command(bin), nil }

	forgetVaultAccess()
	vaultAccessMu.Lock()
	vaultAccess[resolved] = true
	vaultAccessMu.Unlock()

	t.Cleanup(func() {
		closeHelperSession()
		forgetVaultAccess()
		helperCommand = originalCommand
	})
}

func TestVaultWritability_TellsRightsApartFromEverythingElse(t *testing.T) {
	forgetVaultAccess()
	t.Cleanup(forgetVaultAccess)

	writable := t.TempDir()
	if _, needsAdmin, err := vaultWritability(writable); err != nil || needsAdmin {
		t.Errorf("an ordinary folder: needsAdmin=%v err=%v, want false and no error", needsAdmin, err)
	}

	// A directory we may enter and read but not write is the shape of a
	// root-owned vault, which is the case rights can answer.
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(readOnly, 0755) })

	if os.Geteuid() == 0 {
		t.Skip("running as root, where nothing is unwritable")
	}
	_, needsAdmin, err := vaultWritability(readOnly)
	if err != nil {
		t.Fatalf("a read-only folder should be reported, not errored: %v", err)
	}
	if !needsAdmin {
		t.Error("a folder we cannot write should need administrator rights")
	}

	// A folder that is not there is not a rights problem, and must not be
	// remembered as one — a drive that comes back should work again.
	gone := filepath.Join(t.TempDir(), "never")
	if _, _, err := vaultWritability(gone); err == nil {
		t.Error("a missing folder should be an error, not a needsAdmin")
	}
	vaultAccessMu.Lock()
	_, cached := vaultAccess[gone]
	vaultAccessMu.Unlock()
	if cached {
		t.Error("a missing folder was cached; a replugged drive would stay broken")
	}
}

func TestVaultWrites_GoThroughTheHelperWhenTheVaultNeedsRights(t *testing.T) {
	vault := t.TempDir()
	withRoutedVault(t, vault)

	// write
	manifest := filepath.Join(vault, "manifest.json")
	if err := vaultWriteFile(vault, manifest, []byte(`{"version":1}`), 0644); err != nil {
		t.Fatalf("vaultWriteFile: %v", err)
	}
	if got, _ := os.ReadFile(manifest); string(got) != `{"version":1}` {
		t.Errorf("manifest = %q", got)
	}

	// copy a live file in, creating the folders under it
	src := filepath.Join(t.TempDir(), "alsa.conf")
	if err := os.WriteFile(src, []byte("live bytes\n"), 0640); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(vault, "alsa", ".system", "etc", "alsa.conf")
	if err := vaultCopyFileAtomic(vault, src, dst); err != nil {
		t.Fatalf("vaultCopyFileAtomic: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "live bytes\n" {
		t.Errorf("copied = %q", got)
	}
	if info, err := os.Stat(dst); err != nil || info.Mode().Perm() != 0640 {
		t.Errorf("the source's mode should survive the copy, got %v (err %v)", info.Mode().Perm(), err)
	}

	// rename an app folder
	if err := vaultRename(vault, filepath.Join(vault, "alsa"), filepath.Join(vault, "pipewire")); err != nil {
		t.Fatalf("vaultRename: %v", err)
	}
	moved := filepath.Join(vault, "pipewire", ".system", "etc", "alsa.conf")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the folder did not move: %v", err)
	}

	// remove one file, then the whole app
	if err := vaultRemove(vault, moved); err != nil {
		t.Fatalf("vaultRemove: %v", err)
	}
	if _, err := os.Stat(moved); err == nil {
		t.Error("the file is still there")
	}
	if err := vaultRemoveAll(vault, filepath.Join(vault, "pipewire")); err != nil {
		t.Fatalf("vaultRemoveAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vault, "pipewire")); err == nil {
		t.Error("the app folder is still there")
	}
}

// TestVaultWrites_SurviveAVaultPathReachedThroughASymlink is the integration
// this layer exists for. The helper pins the resolved vault root at the start
// of a session and matches paths against that, while settings.VaultPath is
// whatever the user picked — and a vault on a removable drive is routinely
// reached through /run/media, which is a symlink on plenty of systems.
// Without the rebase, every request here would be refused by our own helper.
func TestVaultWrites_SurviveAVaultPathReachedThroughASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	withRoutedVault(t, link)

	target := filepath.Join(link, "myapp", "config")
	if err := vaultWriteFile(link, target, []byte("through a link\n"), 0644); err != nil {
		t.Fatalf("vaultWriteFile through a symlinked vault path: %v", err)
	}
	// Landed in the real directory, under the name the user's path implies.
	if got, err := os.ReadFile(filepath.Join(real, "myapp", "config")); err != nil || string(got) != "through a link\n" {
		t.Errorf("content = %q (err %v)", got, err)
	}
}

func TestVaultTarget_RefusesAPathOutsideTheVault(t *testing.T) {
	vault := t.TempDir()
	withRoutedVault(t, vault)

	outside := filepath.Join(t.TempDir(), "elsewhere.conf")
	err := vaultWriteFile(vault, outside, []byte("x"), 0644)
	if err == nil {
		t.Fatal("wrote outside the vault")
	}
	if !strings.Contains(err.Error(), "not inside the vault") {
		t.Errorf("the refusal should name the problem, said: %v", err)
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Error("the file was written anyway")
	}
}

// An ordinary vault must not touch any of this. The seam is left pointing at
// a command that would fail loudly if it were ever reached.
func TestVaultWrites_StayDirectWhenTheVaultIsYours(t *testing.T) {
	vault := t.TempDir()

	forgetVaultAccess()
	originalCommand := helperCommand
	helperCommand = func() (*exec.Cmd, error) {
		t.Error("an ordinary vault asked for administrator rights")
		return exec.Command("false"), nil
	}
	t.Cleanup(func() {
		helperCommand = originalCommand
		forgetVaultAccess()
	})

	target := filepath.Join(vault, "app", "file.conf")
	if err := vaultWriteFile(vault, target, []byte("mine\n"), 0644); err != nil {
		t.Fatalf("vaultWriteFile: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "mine\n" {
		t.Errorf("content = %q", got)
	}
	if err := vaultRemove(vault, target); err != nil {
		t.Fatalf("vaultRemove: %v", err)
	}
}
