package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rymdport/portal/filechooser"
)

// uriToPath converts a portal-returned file:// URI into a plain filesystem
// path. url.Parse already percent-decodes the result.
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme: %s", u.Scheme)
	}
	return u.Path, nil
}

// ChooseVaultPath opens the portal folder picker. Returns "" (not an error)
// if the user cancels.
func (a *App) ChooseVaultPath() (string, error) {
	uris, err := filechooser.OpenFile("", "Choose vault folder", &filechooser.OpenFileOptions{
		Directory: true,
	})
	if err != nil {
		return "", err
	}
	if len(uris) == 0 {
		return "", nil
	}
	return uriToPath(uris[0])
}

// PickFiles opens the portal multi-select file picker. Returns an empty
// slice (not an error) if the user cancels.
func (a *App) PickFiles() ([]string, error) {
	uris, err := filechooser.OpenFile("", "Choose files to back up", &filechooser.OpenFileOptions{
		Multiple: true,
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(uris))
	for _, u := range uris {
		if p, err := uriToPath(u); err == nil {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// PickFolders opens the portal folder picker in multi-select mode. It is a
// second button rather than a flag on PickFiles because the XDG desktop
// portal's `directory` option is a mode switch, not a filter: a dialog
// either returns files or returns folders, and no single dialog can return a
// mixed selection. Both pickers feed the same pending list, so the mix
// happens in the app instead.
func (a *App) PickFolders() ([]string, error) {
	uris, err := filechooser.OpenFile("", "Choose folders to back up", &filechooser.OpenFileOptions{
		Multiple:  true,
		Directory: true,
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(uris))
	for _, u := range uris {
		if p, err := uriToPath(u); err == nil {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// PathPreview describes what a pending selection would actually add, so the
// Add overlay can state the size of the commitment before anything is
// copied. Choosing a folder is the user's business — the maker was explicit
// that the app must not police the choice — but "~/.config" can mean tens of
// thousands of files, and finding that out afterwards is not the same as
// being told first.
type PathPreview struct {
	FileCount   int      `json:"fileCount"`
	FolderCount int      `json:"folderCount"`
	Folders     []string `json:"folders"` // $HOME-relative, for display
}

// PreviewPaths counts what AddApp would take on, using the same expansion
// AddApp itself uses so the number shown is the number that lands.
func (a *App) PreviewPaths(absPaths []string) (PathPreview, error) {
	preview := PathPreview{Folders: []string{}}
	home, err := os.UserHomeDir()
	if err != nil {
		return preview, err
	}
	settings := a.GetSettings()
	skip := trackedDirSkips(settings.VaultPath)

	seen := make(map[string]bool)
	for _, abs := range absPaths {
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if rel, err := homeRelative(abs, home); err == nil && !seen[rel] {
				seen[rel] = true
				preview.FileCount++
			}
			continue
		}
		rel, err := homeRelative(abs, home)
		if err != nil {
			continue
		}
		preview.FolderCount++
		preview.Folders = append(preview.Folders, filepath.ToSlash(rel))
		for _, f := range expandTrackedDir(home, rel, skip) {
			if !seen[f] {
				seen[f] = true
				preview.FileCount++
			}
		}
	}
	return preview, nil
}

// trackedDirSkips returns the resolved paths a tracked-folder walk must
// never descend into: the vault itself and RESONANCE's own state directory.
func trackedDirSkips(vaultPath string) []string {
	skip := []string{}
	if vaultPath != "" {
		skip = append(skip, resolveDir(vaultPath))
	}
	if stateDir, err := resonanceStateDir(); err == nil {
		skip = append(skip, resolveDir(stateDir))
	}
	return skip
}

// AddApp validates every picked path before copying any of them — a
// validation failure partway through must never leave some files already
// copied onto disk with no manifest entry pointing at them.
//
// A picked path may be a folder. The folder is recorded in ManifestApp.Dirs
// and every regular file under it is added, so "add this app" means the
// obvious thing when a user points at ~/.config/nvim rather than failing
// with "is not a regular file" and abandoning the entire add, which is what
// v1.2.0 did.
func (a *App) AddApp(name string, absPaths []string) error {
	// validAppName trims before validating; trimming here too, once, up
	// front, means the trimmed value is what actually gets compared,
	// stored, and used to build every path below — not the untrimmed
	// original, which would let a whitespace-padded name slip past the
	// duplicate check below.
	name = strings.TrimSpace(name)
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return errors.New("no vault path set")
	}
	if err := validAppName(name); err != nil {
		return err
	}
	if len(absPaths) == 0 {
		return errors.New("choose at least one file")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return err
	}
	for _, app := range m.Apps {
		if strings.EqualFold(app.Name, name) {
			return fmt.Errorf("an app named %q already exists", name)
		}
	}

	// Validate everything before copying anything.
	//
	// Deduplication is on the $HOME-relative path rather than the picked
	// string, so picking both ~/.config/nvim and ~/.config/nvim/init.lua
	// yields one entry for that file instead of copying it twice.
	skip := trackedDirSkips(settings.VaultPath)
	vaultResolved := resolveDir(settings.VaultPath)

	seen := make(map[string]bool, len(absPaths))
	files := make([]ManifestFile, 0, len(absPaths))
	sources := make([]string, 0, len(absPaths))
	dirs := make([]string, 0)
	seenDir := make(map[string]bool)

	addFile := func(rel, abs string) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		files = append(files, ManifestFile{Path: rel})
		sources = append(sources, abs)
	}

	for _, abs := range absPaths {
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s is not a regular file", abs)
			}
			rel, err := homeRelative(abs, home)
			if err != nil {
				return fmt.Errorf("%s is outside your home folder", abs)
			}
			addFile(filepath.ToSlash(rel), abs)
			continue
		}

		rel, err := homeRelative(abs, home)
		if err != nil {
			return fmt.Errorf("%s is outside your home folder", abs)
		}
		relSlash := filepath.ToSlash(rel)

		// Both directions are hazards when the vault lives under $HOME.
		// A folder containing the vault would enumerate the vault into
		// itself; a folder inside the vault would start tracking the vault's
		// own stored copies as though they were live system files. Neither
		// runs away unbounded, but both write nonsense into the manifest, and
		// failing loudly beats silently adding nothing.
		realRoot := resolveDir(abs)
		if vaultResolved != "" {
			if containsPath(realRoot, vaultResolved) {
				return fmt.Errorf("%s contains your vault — choose a folder that doesn't", abs)
			}
			if containsPath(vaultResolved, realRoot) {
				return fmt.Errorf("%s is inside your vault — choose a folder on the system side", abs)
			}
		}

		if !seenDir[relSlash] {
			seenDir[relSlash] = true
			dirs = append(dirs, relSlash)
		}
		for _, f := range expandTrackedDir(home, relSlash, skip) {
			addFile(f, filepath.Join(home, filepath.FromSlash(f)))
		}
	}

	// Validation is complete, so nothing below can be rejected — but the copy
	// itself can still fail on a full disk or a drive pulled mid-write, and
	// until v1.2.1 that left every file already written sitting in the vault
	// with no manifest entry naming it: invisible to the app, unremovable
	// from inside it, and duplicated by every later Copy or Move. Unwinding
	// only the paths this call actually wrote is provably safe — each was
	// created moments earlier by the loop below — unlike deleting the app
	// directory wholesale, which could take leftovers from an earlier failed
	// attempt that the user may still want to inspect.
	appDir := filepath.Join(settings.VaultPath, name)
	written := make([]string, 0, len(files))
	unwind := func() {
		for i := len(written) - 1; i >= 0; i-- {
			_ = os.Remove(written[i])
		}
		pruneEmptyDirs(appDir, settings.VaultPath)
	}

	for i, f := range files {
		dst := filepath.Join(appDir, filepath.FromSlash(f.Path))
		if err := copyFileAtomic(sources[i], dst); err != nil {
			unwind()
			return err
		}
		written = append(written, dst)
		size, checksum, backedUpAt, err := vaultFileMeta(dst)
		if err != nil {
			unwind()
			return err
		}
		files[i].Size = size
		files[i].Checksum = checksum
		files[i].BackedUpAt = backedUpAt
	}

	m.Apps = append(m.Apps, ManifestApp{Name: name, Files: files, Dirs: dirs})
	stampMachineInfo(&m)
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return err
	}
	recordActivity("add", name, summarizeAddActivity(name, len(files)))
	return nil
}

// pruneEmptyDirs removes dir and each now-empty parent, stopping before
// stopAt and never touching it. The containment test is what keeps a bug
// here from walking up and deleting the vault: it climbs only while the
// candidate is genuinely underneath stopAt.
func pruneEmptyDirs(dir, stopAt string) {
	if stopAt == "" {
		return
	}
	for containsPath(stopAt, dir) && filepath.Clean(dir) != filepath.Clean(stopAt) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// OrphanReport lists vault content that no manifest entry accounts for.
type OrphanReport struct {
	Files []string `json:"files"` // vault-relative paths
	Bytes int64    `json:"bytes"`
}

// ScanVaultOrphans finds files in the vault that nothing in manifest.json
// points at. These arise when a copy fails partway (a full disk, a drive
// pulled mid-write) and from hand-editing manifest.json, which was the only
// way to remove anything before app editing existed. They are invisible to
// every other view, and Copy/Move duplicate them faithfully forever, so at
// minimum the app should be able to say they are there.
//
// v1.2.1 reports only. Removing them needs the delete surface that lands
// with app editing, and inventing a second one here would have to be undone.
func (a *App) ScanVaultOrphans() (OrphanReport, error) {
	report := OrphanReport{Files: []string{}}
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return report, nil
	}
	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return report, err
	}

	// Keyed by the same vault-relative form the walk produces, so a
	// case-differing app name on a case-sensitive filesystem is two distinct
	// directories here — which is what the filesystem itself believes, even
	// though sanitizeManifestApps collapses such names case-insensitively.
	known := make(map[string]bool)
	for _, app := range m.Apps {
		for _, f := range app.Files {
			// sanitizeManifestApps vets app NAMES; nothing vets the per-file
			// paths, and this map is what decides whether a real file on disk
			// counts as accounted for. A non-local entry would normalise on
			// the way into the key and could mask a genuine orphan, so it is
			// skipped rather than trusted.
			if !filepath.IsLocal(filepath.FromSlash(f.Path)) {
				continue
			}
			known[path.Join(app.Name, f.Path)] = true
		}
	}

	_ = filepath.WalkDir(settings.VaultPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(settings.VaultPath, p)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == "manifest.json" || known[relSlash] {
			return nil
		}
		// Interrupted atomic writes leave these behind; they are litter, not
		// lost backups, and reporting them as orphans would cry wolf.
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		report.Files = append(report.Files, relSlash)
		if info, err := d.Info(); err == nil {
			report.Bytes += info.Size()
		}
		return nil
	})
	return report, nil
}

// summarizeAddActivity builds AddApp's activity-log summary from the file
// count already in scope at its call site — AddApp has no result struct to
// draw from, unlike Update/Restore/Undo.
func summarizeAddActivity(name string, fileCount int) string {
	return fmt.Sprintf("Added %s (%d files)", name, fileCount)
}
