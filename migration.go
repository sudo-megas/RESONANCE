package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// VaultProbe describes what's at a candidate vault path, before RESONANCE
// commits to using it.
type VaultProbe struct {
	HasManifest bool `json:"hasManifest"`
	IsEmpty     bool `json:"isEmpty"`
	AppCount    int  `json:"appCount"`

	// EntryCount is how many things already sit in the folder. Emptiness no
	// longer gates anything — the maker's ruling is that choosing a folder is
	// the user's business — so the count exists to let a confirmation state
	// the real stake ("3,412 existing items…") instead of the vague "this
	// folder isn't empty" that used to be a refusal.
	EntryCount int `json:"entryCount"`

	// NeedsAdmin marks a folder RESONANCE can read but not write, because it
	// belongs to root. It is not a refusal — it is what the Change Path
	// overlay turns into an offer, which UseVaultPathWithAdmin accepts.
	// Anything that is not a permission problem (a read-only mount, a drive
	// that went away) leaves this false and surfaces as an error instead,
	// because administrator rights would not help.
	NeedsAdmin bool `json:"needsAdmin"`
}

// ProbeVaultPath inspects a folder the maker picked via Change Path, so the
// frontend can decide between Adopt / Copy-or-Move / Refuse without ever
// touching a single file first.
func (a *App) ProbeVaultPath(path string) (VaultProbe, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return VaultProbe{}, describePathProblem(path, err)
	}

	// Asked here rather than at the moment of the first write, so the overlay
	// can offer administrator rights while the user is still choosing, not
	// halfway through a backup. A folder we cannot write for some other
	// reason leaves this false and fails later with the reason it really has.
	needsAdmin := false
	if _, na, err := vaultWritability(path); err == nil {
		needsAdmin = na
	}

	if len(entries) == 0 {
		return VaultProbe{IsEmpty: true, NeedsAdmin: needsAdmin}, nil
	}

	if _, err := os.Stat(manifestPath(path)); err == nil {
		m, err := loadManifest(path)
		if err != nil {
			return VaultProbe{}, err
		}
		return VaultProbe{HasManifest: true, AppCount: len(m.Apps), EntryCount: len(entries), NeedsAdmin: needsAdmin}, nil
	}

	// Non-empty, not a vault.
	return VaultProbe{EntryCount: len(entries), NeedsAdmin: needsAdmin}, nil
}

// VaultDirStatus answers one narrow question — is the vault FOLDER there
// and readable — deliberately kept separate from whether its manifest.json
// parses.
//
// ProbeVaultPath conflates the two: it errors both when the directory can't
// be read and when manifest.json exists but won't load. Driving the recovery
// UI from that composite would put a hand-edited or truncated manifest
// behind a modal headed "vault not found", which is false, and every way out
// of that modal that preserves the user's vault would fail — re-probing hits
// the same parse error, and recreating the folder refuses on it too. The
// only working button would be the one that abandons the vault. Today that
// same condition is a dismissable message with the app still usable, so
// conflating them would be a strict regression.
type VaultDirStatus struct {
	Path             string `json:"path"`
	Set              bool   `json:"set"`
	Reachable        bool   `json:"reachable"`
	ManifestReadable bool   `json:"manifestReadable"`
	Message          string `json:"message"`
}

// CheckVaultDir stats the vault directory and, only if that succeeds, tries
// the manifest. Recovery is offered for the first failure; the second is
// reported without ever taking the app away from the user.
func (a *App) CheckVaultDir() VaultDirStatus {
	path := a.GetSettings().VaultPath
	if path == "" {
		return VaultDirStatus{Set: false}
	}
	status := VaultDirStatus{Path: path, Set: true}

	info, err := os.Stat(path)
	switch {
	case err != nil:
		status.Message = describePathProblem(path, err).Error()
		return status
	case !info.IsDir():
		status.Message = fmt.Sprintf("%s is a file, not a folder", path)
		return status
	}
	if _, err := os.ReadDir(path); err != nil {
		status.Message = describePathProblem(path, err).Error()
		return status
	}
	status.Reachable = true

	if _, err := loadManifest(path); err != nil {
		status.Message = fmt.Sprintf("The vault folder is there, but its manifest can't be read: %s", err.Error())
		return status
	}
	status.ManifestReadable = true
	return status
}

// resolveDir returns path with every symlink component resolved, falling
// back to a lexical clean when it can't be resolved (the path may not exist
// yet — a destination about to be created). Every containment decision below
// is made on resolved paths: filepath.Rel compares strings, and a string
// comparison cannot see that ~/backup is a symlink to the vault.
func resolveDir(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// containsPath reports whether child is parent or sits underneath it.
func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// rejectNested refuses a migration where one of the two paths sits inside
// the other, in either direction.
func rejectNested(oldPath, newPath string) error {
	if containsPath(oldPath, newPath) {
		return errors.New("new path can't be inside the current vault")
	}
	if containsPath(newPath, oldPath) {
		return errors.New("that folder contains the current vault — choose a folder outside it")
	}
	return nil
}

// rejectDangerousVaultPath refuses the handful of locations that must never
// become the vault, because migrateVault's Move ends in
// os.RemoveAll(oldPath) and would take them with it.
//
// This is not hypothetical, and it is not covered by any other guard. Before
// v1.2.1 the "target folder must be empty" rule made these unreachable by
// accident; removing that rule — which the maker asked for, and which is
// right — is what exposes them. $HOME itself passes every remaining check in
// migrateVault, and the folder picker's default landing directory IS $HOME,
// so "Copy the vault here, change my mind, Move it to the real drive" ends
// with os.RemoveAll("/home/you"). Pointing the vault at ~/.local/state would
// likewise have Move delete RESONANCE's own activity log, which is the only
// record of what the program has done to your files.
//
// The check runs on the symlink-resolved path so that ~/docs -> /home/you is
// caught as readily as /home/you itself.
func rejectDangerousVaultPath(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	resolved := resolveDir(path)
	resolvedHome := resolveDir(home)

	if resolved == resolvedHome {
		return errors.New("your home folder can't be the vault — choose a folder inside it, or on another drive")
	}
	// Any ancestor of home: "/", "/home", and so on.
	if containsPath(resolved, resolvedHome) {
		return fmt.Errorf("%s contains your home folder — choose somewhere more specific", path)
	}
	if stateDir, err := resonanceStateDir(); err == nil {
		if containsPath(resolved, resolveDir(stateDir)) {
			return errors.New("that folder holds RESONANCE's own activity log — choose somewhere else")
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		if containsPath(resolved, resolveDir(filepath.Join(cfg, "resonance"))) {
			return errors.New("that folder holds RESONANCE's own settings — choose somewhere else")
		}
	}
	// A vault inside a source root would back itself up. Track /etc as a
	// folder with the vault sitting at /etc/vault, and the walk carries the
	// vault's own copies in as tracked files — which then get copied again
	// on the next backup, and again.
	//
	// This has nothing to do with ownership. A vault that belongs to root is
	// supported and lives wherever you like; what is refused here is the
	// place, not the permissions, which is why the sentence says so.
	for _, root := range systemRoots {
		if containsPath(resolveDir(root), resolved) {
			return fmt.Errorf("%s is inside %s, which RESONANCE backs up — a vault there would end up backing itself up", path, root)
		}
	}
	return nil
}

// UseVaultPath points RESONANCE at a folder, creating it if needed, and
// moves nothing. It is the single place a folder is accepted as the vault:
// first launch, "Recreate it" after the saved folder went missing, and
// "Start fresh here" all land here, so the create/writable/validate sequence
// exists once rather than three times.
//
// Deliberately NOT built by giving SaveSettings a mkdir side effect: the
// theme picker calls SaveSettings too, and changing a theme with the vault
// drive unplugged would then create directories over the unmounted
// mountpoint. Creating a directory has to be the consequence of a user
// action that means it.
func (a *App) UseVaultPath(path string) error { return a.useVaultPath(path, false) }

// UseVaultPathWithAdmin is UseVaultPath for a folder that belongs to root.
// Separate rather than a flag on the first, because it is a different thing
// to agree to: the frontend offers it only after ProbeVaultPath reports
// NeedsAdmin, and taking it up raises the password dialog.
//
// Note what it does not do. It never creates the folder — a vault under a
// root-owned parent cannot be brought into existence by a helper whose reach
// is the vault itself, so an elevated vault is one you adopt, never one
// RESONANCE makes for you. ensureVaultDir below already refuses that, and the
// refusal is the honest one.
func (a *App) UseVaultPathWithAdmin(path string) error { return a.useVaultPath(path, true) }

func (a *App) useVaultPath(path string, allowAdmin bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("choose a folder first")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s isn't a full path", path)
	}
	if err := rejectDangerousVaultPath(path); err != nil {
		return err
	}
	if err := ensureVaultDir(path); err != nil {
		return err
	}
	// Pointing at a different folder makes everything remembered about the
	// last one wrong, including whether writing there needed rights — and a
	// root session opened for a vault RESONANCE is no longer using has no
	// reason to still be running.
	forgetVaultAccess()
	closeHelperSession()

	resolved, needsAdmin, err := vaultWritability(path)
	if err != nil {
		return err
	}
	if needsAdmin {
		if !allowAdmin {
			return errVaultBelongsToRoot(path)
		}
		if err := proveElevatedVaultWritable(resolved); err != nil {
			return err
		}
	}
	// Refuse a folder whose manifest.json is there but unreadable, rather
	// than adopting it and having every later refresh fail.
	if _, err := loadManifest(path); err != nil {
		return err
	}

	settings := a.GetSettings()
	settings.VaultPath = path
	return a.SaveSettings(settings)
}

// AdoptVaultPath points Settings at an existing vault. Zero files move; the
// vault that was active before is left completely untouched at its old
// location.
func (a *App) AdoptVaultPath(newPath string) error {
	probe, err := a.ProbeVaultPath(newPath)
	if err != nil {
		return err
	}
	if !probe.HasManifest {
		return errors.New("that folder isn't a RESONANCE vault")
	}
	// The HasManifest refusal above is what makes this "adopt an existing
	// vault" rather than "point anywhere" — UseVaultPath is the method for
	// the latter. Everything after that decision (validate, then save) is
	// identical, so it is shared rather than duplicated.
	return a.UseVaultPath(newPath)
}

// CopyVaultTo duplicates the current vault into newPath and switches to it.
// The old vault is left fully intact.
func (a *App) CopyVaultTo(newPath string) error { return a.migrateVault(newPath, false) }

// MoveVaultTo duplicates the current vault into newPath, switches to it,
// and only then removes the old vault's contents.
func (a *App) MoveVaultTo(newPath string) error { return a.migrateVault(newPath, true) }

func (a *App) migrateVault(newPath string, remove bool) error {
	settings := a.GetSettings()
	oldPath := settings.VaultPath
	if oldPath == "" {
		return errors.New("no current vault to migrate")
	}
	if newPath == oldPath {
		return errors.New("new path is the same as the current vault path")
	}
	// The reported v1.2.1 bug lands here. copyTree is filepath.WalkDir(src)
	// and WalkDir lstats its root, so a missing OLD vault surfaced as
	// "copy to new vault failed, old vault untouched: lstat <old path>: no
	// such file or directory" — which reads like the destination is at
	// fault. Checking the source up front reports what actually happened,
	// before anything is created anywhere.
	if err := requireVaultDir(oldPath); err != nil {
		return err
	}
	if err := rejectDangerousVaultPath(newPath); err != nil {
		return err
	}
	// Move ends in os.RemoveAll(oldPath). If the current vault is somewhere
	// it should never have been, that is the moment it becomes destructive,
	// so the source is held to the same standard as the destination.
	if remove {
		if err := rejectDangerousVaultPath(oldPath); err != nil {
			return err
		}
		// Move finishes by deleting the old vault folder itself, and that is
		// the one write the helper structurally cannot do: its whole reach is
		// the vault, and deleting a folder is a write to that folder's
		// parent, which belongs to root and is none of RESONANCE's business.
		//
		// Copy is unaffected and does the same job: reading a root-owned
		// vault needs nothing, and the destination is somewhere you own.
		if _, needsAdmin, err := vaultWritability(oldPath); err == nil && needsAdmin {
			return errors.New("this vault belongs to root, so RESONANCE can't move it away — use Copy, which leaves the original where it is")
		}
	}
	// Containment is checked twice, on purpose. Once lexically, here, BEFORE
	// ensureVaultDir can create anything — otherwise a destination inside the
	// vault gets created inside the vault and only then rejected, leaving
	// litter behind. Then again below on resolved paths, once the destination
	// exists and its symlinks can actually be followed.
	//
	// Both directions matter. Destination-inside-source is the original
	// hazard: copyTree would walk oldPath, copy newPath's own directory entry
	// partway through, then recurse into the copy it just made of itself,
	// unboundedly. Source-inside-destination is the mirror image, and it was
	// unreachable only because the emptiness rule used to reject it.
	if err := rejectNested(oldPath, newPath); err != nil {
		return err
	}

	if err := ensureVaultDir(newPath); err != nil {
		return err
	}
	if err := ensureVaultWritable(newPath); err != nil {
		return err
	}
	if err := rejectNested(resolveDir(oldPath), resolveDir(newPath)); err != nil {
		return err
	}

	probe, err := a.ProbeVaultPath(newPath)
	if err != nil {
		return err
	}
	if probe.HasManifest {
		return errors.New("target already contains a vault — use Adopt instead")
	}
	// The emptiness gate is gone: choosing a folder that already has things
	// in it is the user's call, per the maker's ruling on v1.2.1 #2. Copy is
	// therefore allowed into any folder.
	//
	// Move is the one exception, and not as a re-litigation of that ruling.
	// Copy never deletes anything; Move finishes by deleting the folder it
	// moved away from, so allowing Move INTO a folder that already holds
	// unrelated files sets up a later Move to destroy them. Refusing it here
	// costs the user nothing — Copy does the same job and leaves the old
	// vault in place.
	if remove && !probe.IsEmpty {
		return fmt.Errorf(
			"%s already contains %d items — use Copy instead, so nothing there can be deleted later",
			newPath, probe.EntryCount)
	}

	// 1. Copy everything first. Old vault untouched throughout.
	if err := copyTree(oldPath, newPath); err != nil {
		return fmt.Errorf("copy to new vault failed, old vault untouched: %w", err)
	}
	// 2. Verify before trusting the copy enough to switch or delete anything.
	if err := verifyTreesMatch(oldPath, newPath); err != nil {
		return fmt.Errorf("copy verification failed, old vault untouched, new path left for inspection: %w", err)
	}
	// 3. Only now does Settings point at the new path.
	settings.VaultPath = newPath
	if err := a.SaveSettings(settings); err != nil {
		return err
	}
	// 4. Move only: delete old contents, now that new is confirmed good and active.
	if remove {
		if err := os.RemoveAll(oldPath); err != nil {
			return fmt.Errorf("vault switched to new path, but could not remove old vault at %s: %w", oldPath, err)
		}
	}
	return nil
}

// copyTree recursively copies every file and directory under src into dst,
// byte-identical, preserving permission bits — the same guarantee copyFile
// gives a single file.
//
// Both writes clear a symlink standing in the way first. Until v1.2.1 the
// destination was guaranteed empty, so copyTree could never meet one; now
// that any folder may be chosen, the destination can already contain
// <dst>/app/.bashrc -> /etc/shadow, which os.Create would follow, or a
// symlinked directory, which os.MkdirAll would follow — redirecting an
// entire subtree outside the vault. WalkDir's top-down order is what makes
// the directory case safe: the link is cleared before any of its children
// are written. This is the same invariant copyFileAtomic and removeSymlinkAt
// already exist to hold elsewhere.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := removeSymlinkAt(target); err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFileAtomic(path, target)
	})
}

// verifyTreesMatch re-walks both trees and compares file presence, size, and
// checksum. Size is checked first as a cheap short-circuit — a mismatch is
// already conclusive, no need to hash — but a size match alone doesn't
// prove identical content, so both copies are hashed and compared whenever
// sizes agree. This is migration integrity (comparing two current
// filesystem trees), a distinct concern from manifest drift (comparing a
// current source file against a historical stored checksum) — the two
// never share a call site, only the fileChecksum primitive.
func verifyTreesMatch(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		srcInfo, err := os.Stat(path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		dstInfo, err := os.Stat(dstPath)
		if err != nil {
			return fmt.Errorf("missing in copy: %s", rel)
		}
		if srcInfo.Size() != dstInfo.Size() {
			return fmt.Errorf("size mismatch for %s", rel)
		}
		srcSum, err := fileChecksum(path)
		if err != nil {
			return err
		}
		dstSum, err := fileChecksum(dstPath)
		if err != nil {
			return err
		}
		if srcSum != dstSum {
			return fmt.Errorf("checksum mismatch for %s", rel)
		}
		return nil
	})
}
