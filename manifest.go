package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const manifestVersion = 1

// Manifest is the vault's own self-description, stored at
// <vault>/manifest.json. It travels with the vault, not with any one
// machine's config — a vault must be restorable by any user on any machine.
type Manifest struct {
	Version int           `json:"version"`
	Apps    []ManifestApp `json:"apps"`
}

type ManifestApp struct {
	Name  string         `json:"name"`
	Files []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path string `json:"path"`

	// Size, Checksum, and BackedUpAt are additive — absent (zero-valued) on
	// any manifest.json written before STEP3. backfillChecksums (drift.go)
	// fills them in for legacy entries on first load.
	Size       int64  `json:"size"`
	Checksum   string `json:"checksum"`   // hex sha256 of the vault-side copy at backup time
	BackedUpAt string `json:"backedUpAt"` // RFC3339 UTC, the vault-side copy's write time
}

func manifestPath(vaultPath string) string {
	return filepath.Join(vaultPath, "manifest.json")
}

// loadManifest distinguishes "the vault directory itself doesn't exist"
// (a real error — unmounted drive, stale saved path) from "the directory
// exists but has no manifest.json yet" (a fresh, valid, empty vault).
func loadManifest(vaultPath string) (Manifest, error) {
	if _, err := os.Stat(vaultPath); err != nil {
		return Manifest{}, errors.New("vault not found — is the drive connected?")
	}

	data, err := os.ReadFile(manifestPath(vaultPath))
	if os.IsNotExist(err) {
		return Manifest{Version: manifestVersion, Apps: []ManifestApp{}}, nil
	}
	if err != nil {
		return Manifest{}, err
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	if m.Apps == nil {
		m.Apps = []ManifestApp{}
	}
	return m, nil
}

// saveManifest never creates the vault directory itself — by the time this
// is called, loadManifest has already proven the directory exists.
func saveManifest(vaultPath string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(vaultPath), data, 0644)
}

// homeRelative converts an absolute path into one relative to home,
// rejecting anything that isn't actually under home.
func homeRelative(absPath, home string) (string, error) {
	rel, err := filepath.Rel(home, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("outside your home folder")
	}
	return rel, nil
}

func validAppName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("app name can't be empty")
	case name == "." || name == "..":
		return errors.New("that name isn't allowed")
	case strings.ContainsAny(name, `/\`):
		return errors.New("app name can't contain a slash")
	case strings.HasPrefix(name, "."):
		return errors.New("app name can't start with a dot")
	case strings.EqualFold(name, "manifest.json"):
		return errors.New("that name is reserved")
	}
	return nil
}

// copyFile copies src to dst byte-identical, preserving the source's
// permission bits. Uses Stat (not Lstat) so a symlinked dotfile — common in
// existing Stow-style setups — copies its real target content, not a
// dangling link that wouldn't resolve on another machine.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(src + " is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode().Perm())
}
