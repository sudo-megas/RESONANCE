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
}

// ProbeVaultPath inspects a folder the maker picked via Change Path, so the
// frontend can decide between Adopt / Copy-or-Move / Refuse without ever
// touching a single file first.
func (a *App) ProbeVaultPath(path string) (VaultProbe, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return VaultProbe{}, fmt.Errorf("can't read %s: %w", path, err)
	}

	if len(entries) == 0 {
		return VaultProbe{IsEmpty: true}, nil
	}

	if _, err := os.Stat(manifestPath(path)); err == nil {
		m, err := loadManifest(path)
		if err != nil {
			return VaultProbe{}, err
		}
		return VaultProbe{HasManifest: true, AppCount: len(m.Apps)}, nil
	}

	return VaultProbe{}, nil // non-empty, not a vault
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
	settings := a.GetSettings()
	settings.VaultPath = newPath
	return a.SaveSettings(settings)
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
	// ProbeVaultPath's IsEmpty check alone can't tell "an empty folder
	// nested inside the vault" from "an empty folder anywhere else" — and
	// the former is completely reachable through the ordinary folder
	// picker (navigate into the current vault, pick/create an empty
	// subfolder). Without this guard, copyTree would walk oldPath, copy
	// newPath's own directory entry partway through, and then recurse into
	// the copy it just made of itself — unboundedly, until it exhausts
	// PATH_MAX or disk space.
	if rel, err := filepath.Rel(oldPath, newPath); err == nil {
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("new path can't be inside the current vault")
		}
	}

	probe, err := a.ProbeVaultPath(newPath)
	if err != nil {
		return err
	}
	if probe.HasManifest {
		return errors.New("target already contains a vault — use Adopt instead")
	}
	if !probe.IsEmpty {
		return errors.New("target folder is not empty — choose an empty folder")
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
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target)
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
