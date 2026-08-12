package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileRow is one backed-up file's live state, computed fresh on every call
// to GetMirrorRows — never cached, never stale.
type FileRow struct {
	Path           string `json:"path"`
	State          string `json:"state"`          // "ok" | "drifted" | "missing"
	SourceModified string `json:"sourceModified"` // RFC3339 UTC; "" if source missing
	VaultModified  string `json:"vaultModified"`  // == ManifestFile.BackedUpAt
}

// AppRow is one app's entire mirror-row data — every file's live drift
// state, already computed. The SYSTEM-side and VAULT-side of a rendered row
// both come from this one object, so there is no code path that can build
// one side inconsistently with the other.
type AppRow struct {
	Name    string    `json:"name"`
	Files   []FileRow `json:"files"`
	Drifted bool      `json:"drifted"` // true if any file is drifted or missing
}

// UpdateResult reports what UpdateFromSource actually did, file by file.
type UpdateResult struct {
	Updated []string `json:"updated"` // re-copied
	Skipped []string `json:"skipped"` // already identical, untouched
	Missing []string `json:"missing"` // source gone; vault copy left untouched, reported not failed
}

// backfillChecksums fills in checksum/size/backedUpAt for any ManifestFile
// left over from a STEP2-era manifest.json (checksum == ""). It hashes only
// the vault's own copy — never the live source — so a legacy entry's
// checksum always reflects what STEP2 actually copied, never anything the
// live file happens to be now. A vault-side file that's itself missing or
// unreadable doesn't abort the whole pass; that one entry is left with an
// empty checksum, which GetMirrorRows then reports as "drifted" (no valid
// baseline) rather than crashing.
func backfillChecksums(vaultPath string, m *Manifest) bool {
	changed := false
	for ai := range m.Apps {
		for fi := range m.Apps[ai].Files {
			f := &m.Apps[ai].Files[fi]
			if f.Checksum != "" {
				continue
			}
			vaultFile := filepath.Join(vaultPath, m.Apps[ai].Name, filepath.FromSlash(f.Path))
			size, checksum, backedUpAt, err := vaultFileMeta(vaultFile)
			if err != nil {
				continue
			}
			f.Size = size
			f.Checksum = checksum
			f.BackedUpAt = backedUpAt
			changed = true
		}
	}
	return changed
}

// GetMirrorRows loads the manifest, backfills any legacy entries, and
// computes each file's live drift state against its stored checksum. This
// is the mirror's sole data source going forward — replacing STEP2's
// ListApps — so SYSTEM-side and VAULT-side rendering are structurally
// incapable of diverging: both come from the same AppRow.
func (a *App) GetMirrorRows() ([]AppRow, error) {
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return []AppRow{}, nil
	}
	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return []AppRow{}, nil
	}
	if backfillChecksums(settings.VaultPath, &m) {
		_ = saveManifest(settings.VaultPath, m)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return []AppRow{}, nil
	}

	rows := make([]AppRow, 0, len(m.Apps))
	for _, app := range m.Apps {
		row := AppRow{Name: app.Name, Files: make([]FileRow, 0, len(app.Files))}
		for _, f := range app.Files {
			row.Files = append(row.Files, fileDriftRow(home, f))
		}
		for _, fr := range row.Files {
			if fr.State != "ok" {
				row.Drifted = true
				break
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// fileDriftRow compares one manifest entry's stored baseline against its
// live source file. Size is checked before checksum as a cheap
// short-circuit — a mismatch already proves drift without hashing — but a
// size match alone can't prove identical content, so a full hash read
// still happens whenever sizes agree.
func fileDriftRow(home string, f ManifestFile) FileRow {
	fr := FileRow{Path: f.Path, VaultModified: f.BackedUpAt}

	sourcePath := filepath.Join(home, filepath.FromSlash(f.Path))
	if _, err := homeRelative(sourcePath, home); err != nil {
		fr.State = "missing"
		return fr
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		fr.State = "missing"
		return fr
	}
	fr.SourceModified = info.ModTime().UTC().Format(time.RFC3339)

	if f.Checksum == "" || info.Size() != f.Size {
		fr.State = "drifted"
		return fr
	}
	sum, err := fileChecksum(sourcePath)
	if err != nil || sum != f.Checksum {
		fr.State = "drifted"
		return fr
	}
	fr.State = "ok"
	return fr
}

// UpdateFromSource re-copies every file in the named app from its live
// source into the vault, refreshing checksum/size/backedUpAt. Unlike
// AddApp — which validates every path before copying any of them, because
// a partial failure there would leave orphan files with no manifest entry
// — each file here is already an independent, established manifest entry,
// so one file's source having vanished must not block re-syncing the rest.
func (a *App) UpdateFromSource(name string) (UpdateResult, error) {
	// A nil slice marshals to JSON null, not []; initializing these here
	// (rather than leaving the zero value) keeps the frontend's
	// result.missing.length-style access safe without a null check at every
	// call site — the same reasoning loadManifest already applies to Apps.
	result := UpdateResult{Updated: []string{}, Skipped: []string{}, Missing: []string{}}

	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return result, errors.New("no vault path set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return result, err
	}
	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return result, err
	}

	appIndex := -1
	for i, app := range m.Apps {
		if app.Name == name {
			appIndex = i
			break
		}
	}
	if appIndex == -1 {
		return result, errors.New("no such app")
	}

	app := &m.Apps[appIndex]
	for i := range app.Files {
		f := &app.Files[i]
		sourcePath := filepath.Join(home, filepath.FromSlash(f.Path))
		vaultAppDir := filepath.Join(settings.VaultPath, app.Name)
		vaultFile := filepath.Join(vaultAppDir, filepath.FromSlash(f.Path))

		if _, err := homeRelative(sourcePath, home); err != nil {
			result.Missing = append(result.Missing, f.Path)
			continue
		}
		if _, err := homeRelative(vaultFile, vaultAppDir); err != nil {
			result.Missing = append(result.Missing, f.Path)
			continue
		}

		srcInfo, err := os.Stat(sourcePath)
		if err != nil || !srcInfo.Mode().IsRegular() {
			result.Missing = append(result.Missing, f.Path)
			continue
		}

		if f.Checksum != "" && srcInfo.Size() == f.Size {
			if sum, err := fileChecksum(sourcePath); err == nil && sum == f.Checksum {
				result.Skipped = append(result.Skipped, f.Path)
				continue
			}
		}

		if err := copyFile(sourcePath, vaultFile); err != nil {
			return result, err
		}
		size, checksum, backedUpAt, err := vaultFileMeta(vaultFile)
		if err != nil {
			return result, err
		}
		f.Size = size
		f.Checksum = checksum
		f.BackedUpAt = backedUpAt
		result.Updated = append(result.Updated, f.Path)
	}

	stampMachineInfo(&m)
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return result, err
	}
	recordActivity("update", name, summarizeUpdateActivity(result))
	return result, nil
}

// summarizeUpdateActivity turns UpdateResult's counts into a short
// human-readable description for the activity log, e.g. "3 updated, 1
// source missing". Counts of zero are omitted entirely.
func summarizeUpdateActivity(result UpdateResult) string {
	var parts []string
	if n := len(result.Updated); n > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", n))
	}
	if n := len(result.Skipped); n > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", n))
	}
	if n := len(result.Missing); n > 0 {
		parts = append(parts, fmt.Sprintf("%d source missing", n))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}
