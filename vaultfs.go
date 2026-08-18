package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// vaultfs.go is the one place in RESONANCE that knows a vault might not
// belong to you.
//
// A vault that is root-owned but still readable — /opt/dotfiles as root:root
// 755, the ordinary shape — refuses writes and nothing else. Reads, drift
// scans, checksums, diffs and manifest loads all keep working as plain os
// calls, so only the handful of operations that put bytes into the vault come
// through here. That is the whole reason this is six small functions rather
// than a filesystem abstraction underneath the backend: a vault you cannot
// *read* would need that, and it is not supported, because it would put the
// entire program behind root and does nothing for /etc or /usr.
//
// Two decisions worth knowing before reading further.
//
// Writability is decided once per vault, not per failed write. Falling back
// to elevation whenever a write happens to fail with EACCES would mean a vault
// of yours containing one directory someone sudo'd into root ownership
// silently starts raising password prompts mid-backup. Today that fails
// honestly, and it should keep failing honestly: needing rights is a property
// of the vault you adopted, decided when you adopted it.
//
// Every path is rebased onto the resolved vault root before it is sent. The
// helper pins EvalSymlinks(vaultRoot) at the start of a session and matches
// against that resolved form, while settings.VaultPath is whatever the user
// picked — and a vault on a removable drive is routinely reached through
// /run/media, which has symlinked segments on plenty of systems. Without the
// rebase every request would be refused by our own helper.

// vaultAccess remembers, per resolved vault root, whether writing there needs
// administrator rights.
var (
	vaultAccessMu sync.Mutex
	vaultAccess   = map[string]bool{}
)

// forgetVaultAccess drops what is known about vault writability. Called when
// the vault changes, so that adopting a different folder — or the same folder
// after its ownership changed — is probed again rather than inheriting the
// last answer.
func forgetVaultAccess() {
	vaultAccessMu.Lock()
	defer vaultAccessMu.Unlock()
	vaultAccess = map[string]bool{}
}

// probeVaultWritable is ensureVaultWritable's check without the explanatory
// sentence wrapped around it. The raw error is the point: it is what
// separates "this folder belongs to root", which administrator rights can
// answer, from "this filesystem is mounted read-only" or "the drive is gone",
// which they cannot.
func probeVaultWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// vaultWritability resolves a vault path and answers whether writes there
// need the helper. The answer is cached per resolved root; anything that is
// not a permission problem is returned as the error it is.
func vaultWritability(vaultPath string) (resolved string, needsAdmin bool, err error) {
	resolved, err = filepath.EvalSymlinks(vaultPath)
	if err != nil {
		return "", false, describePathProblem(vaultPath, err)
	}

	vaultAccessMu.Lock()
	defer vaultAccessMu.Unlock()
	if known, ok := vaultAccess[resolved]; ok {
		return resolved, known, nil
	}

	probeErr := probeVaultWritable(resolved)
	switch {
	case probeErr == nil:
		vaultAccess[resolved] = false
		return resolved, false, nil
	case errors.Is(probeErr, fs.ErrPermission):
		vaultAccess[resolved] = true
		return resolved, true, nil
	default:
		// A read-only mount or a vanished drive is not something rights fix,
		// and caching it would make a replugged drive stay broken.
		return "", false, describePathProblem(vaultPath, probeErr)
	}
}

// errVaultBelongsToRoot is what a root-owned vault gets when the caller has
// not been told it may ask for rights. It replaces the older flat refusal,
// which said RESONANCE runs as you and stopped there — true, and no longer
// the whole story now that it can ask.
func errVaultBelongsToRoot(path string) error {
	return fmt.Errorf("%s belongs to root — RESONANCE can use it, but only with administrator rights", path)
}

// proveElevatedVaultWritable does through the helper exactly what
// ensureVaultWritable does directly: land a probe file and remove it again.
// A vault is accepted because a write really worked, not because a mode bit
// suggested it would — and the polkit dialog this raises is where the user
// actually consents to the whole arrangement.
func proveElevatedVaultWritable(resolvedRoot string) error {
	s, err := elevatedSession(resolvedRoot)
	if err != nil {
		return err
	}
	probe := filepath.Join(resolvedRoot, ".tmp-admin-probe")
	if err := s.send(helperRequest{Op: "write", Path: probe, Data: []byte{}, Mode: 0600}); err != nil {
		return err
	}
	return s.send(helperRequest{Op: "remove", Path: probe})
}

// vaultTarget is the front of every operation below. It returns the path to
// act on and, when the vault needs administrator rights, the session to act
// through; a nil session means do it directly, as yourself.
//
// target must be inside the vault. That is an invariant of every caller, not
// a user-supplied value, and it is checked because getting it wrong would
// mean handing the helper a path outside the vault entirely.
func vaultTarget(vaultPath, target string) (string, *helperSession, error) {
	resolved, needsAdmin, err := vaultWritability(vaultPath)
	if err != nil {
		return "", nil, err
	}
	if !needsAdmin {
		return target, nil, nil
	}

	rel, relErr := relativeUnder(target, vaultPath)
	if relErr != nil {
		// Callers that already work in resolved terms hand us a resolved
		// path; both spellings name the same file and both are accepted.
		rel, relErr = relativeUnder(target, resolved)
		if relErr != nil {
			return "", nil, fmt.Errorf("%s is not inside the vault at %s", target, vaultPath)
		}
	}

	s, err := elevatedSession(resolved)
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(resolved, filepath.FromSlash(rel)), s, nil
}

// vaultWriteFile replaces a file in the vault with data, creating the folders
// above it if they aren't there.
//
// Both branches create those folders, and that symmetry is deliberate: the
// helper's write does it because restoring /etc/alsa/conf.d/x has to work
// when conf.d is gone, and a direct write that quietly did less would mean
// the same call succeeding or failing depending on who owns the vault. That
// is the kind of difference nobody finds until it is in someone's hands.
func vaultWriteFile(vaultPath, target string, data []byte, perm os.FileMode) error {
	dest, s, err := vaultTarget(vaultPath, target)
	if err != nil {
		return err
	}
	if s == nil {
		dir := filepath.Dir(target)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		return writeFileAtomic(dir, target, data, perm)
	}
	return s.send(helperRequest{Op: "write", Path: dest, Data: data, Mode: uint32(perm.Perm())})
}

// vaultCopyFileAtomic copies a live file into the vault.
//
// The source is read here, as you, because a file you are backing up is one
// you can already read — this release adds no elevated read anywhere. Only
// the landing of the bytes is privileged, and it lands the same way
// copyFileAtomic's does: replacing whatever is at the destination rather than
// writing through it.
func vaultCopyFileAtomic(vaultPath, src, dst string) error {
	dest, s, err := vaultTarget(vaultPath, dst)
	if err != nil {
		return err
	}
	if s == nil {
		return copyFileAtomic(src, dst)
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(src + " is not a regular file")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return s.send(helperRequest{Op: "write", Path: dest, Data: data, Mode: uint32(info.Mode().Perm())})
}

// vaultRemove unlinks one file or one empty directory in the vault.
func vaultRemove(vaultPath, target string) error {
	dest, s, err := vaultTarget(vaultPath, target)
	if err != nil {
		return err
	}
	if s == nil {
		return os.Remove(target)
	}
	return s.send(helperRequest{Op: "remove", Path: dest})
}

// vaultRemoveAll deletes a subtree of the vault.
func vaultRemoveAll(vaultPath, target string) error {
	dest, s, err := vaultTarget(vaultPath, target)
	if err != nil {
		return err
	}
	if s == nil {
		return os.RemoveAll(target)
	}
	return s.send(helperRequest{Op: "removeAll", Path: dest})
}

// vaultRename moves something within the vault — an app folder taking a new
// name, and nothing else.
func vaultRename(vaultPath, from, to string) error {
	fromDest, s, err := vaultTarget(vaultPath, from)
	if err != nil {
		return err
	}
	if s == nil {
		return os.Rename(from, to)
	}
	toDest, _, err := vaultTarget(vaultPath, to)
	if err != nil {
		return err
	}
	return s.send(helperRequest{Op: "rename", Path: fromDest, To: toDest})
}
