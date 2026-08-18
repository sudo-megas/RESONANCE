package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RemoveResult reports what a removal actually did, entry by entry — the
// same shape as RestoreResult (restore.go) and UndoResult (snapshot.go), and
// for the same reason: one entry's problem must not decide the fate of every
// other entry in the same call.
type RemoveResult struct {
	RemovedFiles []string         `json:"removedFiles"`
	RemovedDirs  []string         `json:"removedDirs"`
	Failed       []RestoreFailure `json:"failed"`
}

// TrackedDir is one tracked folder plus how many manifest entries currently
// sit under it. The count comes from the manifest, never from a fresh walk,
// so opening the edit overlay costs no disk I/O and cannot stall on a slow
// removable drive.
type TrackedDir struct {
	Path      string `json:"path"`
	FileCount int    `json:"fileCount"`
}

// AppComposition is the edit overlay's read model: what an app is made of,
// in the same two units AddApp accepts.
//
// Deliberately not AppRow. AppRow carries drift state, which means it only
// exists after drift computation has run against both $HOME and the vault —
// but whether a file has drifted has nothing to do with whether you want it
// tracked, and the overlay must stay usable when the drift pass is failing.
type AppComposition struct {
	Name  string       `json:"name"`
	Files []string     `json:"files"`
	Dirs  []TrackedDir `json:"dirs"`
}

// loadAppForEdit is the shared front half of every method in this file:
// prove a vault is set, load the manifest, and locate the app by exact name.
//
// Going through loadManifest rather than reading the file directly is what
// guarantees app.Name is a single safe path segment by the time any path is
// built from it — sanitizeManifestApps has already dropped anything that
// isn't (manifest.go). Every method here then joins that name onto the vault
// path, so this is not a convenience wrapper; it is where that guarantee
// enters.
func (a *App) loadAppForEdit(name string) (Settings, Manifest, int, error) {
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return settings, Manifest{}, -1, errors.New("no vault path set")
	}
	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return settings, Manifest{}, -1, err
	}
	for i, app := range m.Apps {
		if app.Name == name {
			return settings, m, i, nil
		}
	}
	return settings, Manifest{}, -1, errors.New("no such app")
}

// GetAppComposition reports exactly what the named app is made of, for the
// edit overlay to render.
func (a *App) GetAppComposition(name string) (AppComposition, error) {
	_, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return AppComposition{}, err
	}
	app := m.Apps[idx]

	comp := AppComposition{
		Name:  app.Name,
		Files: make([]string, 0, len(app.Files)),
		Dirs:  make([]TrackedDir, 0, len(app.Dirs)),
	}
	for _, f := range app.Files {
		comp.Files = append(comp.Files, f.Path)
	}
	for _, d := range app.Dirs {
		td := TrackedDir{Path: d}
		for _, f := range app.Files {
			if pathCoveredByDir(f.Path, d) {
				td.FileCount++
			}
		}
		comp.Dirs = append(comp.Dirs, td)
	}
	return comp, nil
}

// pathCoveredByDir reports whether the manifest path p sits inside tracked
// folder d. The trailing separator is required, not cosmetic: without it
// ".config/nvim" would also claim ".config/nvim-old".
func pathCoveredByDir(p, d string) bool {
	return p == d || strings.HasPrefix(p, d+"/")
}

// AddToApp adds more files or folders to an app that already exists. It is
// AddApp minus the app creation, sharing stageAdd/commitAdd so the two can
// never disagree about what a picked folder means.
func (a *App) AddToApp(name string, absPaths []string) error {
	settings, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return err
	}
	if len(absPaths) == 0 {
		return errors.New("choose at least one file")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	app := m.Apps[idx]

	// The app's current contents are passed in so re-picking something
	// already tracked collapses to nothing instead of a duplicate entry.
	st, err := stageAdd(home, settings.VaultPath, absPaths, app.Files, app.Dirs)
	if err != nil {
		return err
	}
	if len(st.Files) == 0 && len(st.Dirs) == 0 {
		// Everything picked is already tracked. Saving here would rewrite
		// manifest.json and restamp the machine info to say this machine
		// backed something up, which it did not.
		return nil
	}

	appDir := filepath.Join(settings.VaultPath, app.Name)
	if err := commitAdd(appDir, settings.VaultPath, &st); err != nil {
		return err
	}

	app.Files = append(app.Files, st.Files...)
	app.Dirs = append(app.Dirs, st.Dirs...)
	m.Apps[idx] = app
	stampMachineInfo(&m)
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return err
	}
	recordActivity("add", name, summarizeAddToActivity(name, len(st.Files), len(st.Dirs)))
	return nil
}

// RemoveFromApp deletes the VAULT's copy of each named file or folder and
// drops its manifest entry. It never touches the live file in $HOME.
//
// That guarantee is structural, not a promise in a comment: this function
// never calls os.UserHomeDir, never computes a $HOME-side path, and has no
// variable holding one. There is no line here that could delete a live file
// even if every check below were removed.
//
// Validation is whole-call — nothing is removed unless every named entry is
// one the manifest itself claims. Execution is then per-entry, matching
// RestoreApp: one planted symlink must not make every other file in the app
// unremovable. The ordering within an entry is always delete-the-vault-copy
// THEN drop-the-manifest-entry, never the reverse, because the two possible
// inconsistent states are not equally bad. An entry whose vault copy is gone
// is reported honestly as vaultMissing and a retry fixes it; an entry
// dropped while its vault copy survives is unreferenced garbage that nothing
// in the app can ever find or remove again.
func (a *App) RemoveFromApp(name string, relPaths, relDirs []string) (RemoveResult, error) {
	result := RemoveResult{RemovedFiles: []string{}, RemovedDirs: []string{}, Failed: []RestoreFailure{}}

	settings, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return result, err
	}
	app := m.Apps[idx]
	vaultAppDir := filepath.Join(settings.VaultPath, app.Name)

	haveFile := make(map[string]bool, len(app.Files))
	for _, f := range app.Files {
		haveFile[f.Path] = true
	}
	haveDir := make(map[string]bool, len(app.Dirs))
	for _, d := range app.Dirs {
		haveDir[d] = true
	}

	// --- whole-call validation ------------------------------------------

	seenFile := make(map[string]bool, len(relPaths))
	files := make([]string, 0, len(relPaths))
	for _, p := range relPaths {
		if seenFile[p] {
			continue
		}
		seenFile[p] = true

		// Only ever delete something the manifest itself claims. This is
		// stricter than GetDiffPair, which validates the app name and then
		// trusts the path: a delete has no undo, so an unknown path is a
		// whole-call refusal rather than a best-effort attempt.
		if !haveFile[p] {
			return result, fmt.Errorf("%s isn't tracked by %s", p, app.Name)
		}
		// A file inside a tracked folder cannot be removed on its own. The
		// folder walk would rediscover it on the next refresh, mark the app
		// drifted, and the next update would copy it straight back — the
		// deletion would silently undo itself. Enforced here and not only in
		// the overlay, so the loop is unreachable even from a hand-written
		// IPC call.
		for _, d := range app.Dirs {
			if pathCoveredByDir(p, d) {
				return result, fmt.Errorf(
					"%s is inside the tracked folder %s — track that folder's files individually first, or remove the whole folder",
					p, d)
			}
		}
		if _, err := homeRelative(filepath.Join(vaultAppDir, filepath.FromSlash(p)), vaultAppDir); err != nil {
			return result, fmt.Errorf("%s isn't inside this app's vault folder", p)
		}
		files = append(files, p)
	}

	seenDir := make(map[string]bool, len(relDirs))
	dirs := make([]string, 0, len(relDirs))
	for _, d := range relDirs {
		if seenDir[d] {
			continue
		}
		seenDir[d] = true
		if !haveDir[d] {
			return result, fmt.Errorf("%s isn't a tracked folder of %s", d, app.Name)
		}
		if _, err := homeRelative(filepath.Join(vaultAppDir, filepath.FromSlash(d)), vaultAppDir); err != nil {
			return result, fmt.Errorf("%s isn't inside this app's vault folder", d)
		}
		dirs = append(dirs, d)
	}

	// --- per-entry execution ---------------------------------------------

	removedFile := make(map[string]bool, len(files))
	for _, p := range files {
		abs := filepath.Join(vaultAppDir, filepath.FromSlash(p))
		if err := refuseSymlinkedParents(vaultAppDir, p); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: p, Reason: err.Error()})
			continue
		}
		// Already absent counts as removed: the goal state is "not in the
		// vault", which is what makes a retry after a partial failure
		// converge instead of failing forever on the entries that worked.
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			result.Failed = append(result.Failed, RestoreFailure{Path: p, Reason: err.Error()})
			continue
		}
		pruneEmptyDirs(filepath.Dir(abs), vaultAppDir)
		removedFile[p] = true
		result.RemovedFiles = append(result.RemovedFiles, p)
	}

	removedDir := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		abs := filepath.Join(vaultAppDir, filepath.FromSlash(d))
		if err := refuseSymlinkedParents(vaultAppDir, d); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: d, Reason: err.Error()})
			continue
		}
		// os.RemoveAll unlinks a symlink rather than recursing through it,
		// so a link planted at the folder itself is cleaned out of the vault
		// without its target being touched.
		if err := os.RemoveAll(abs); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: d, Reason: err.Error()})
			continue
		}
		pruneEmptyDirs(filepath.Dir(abs), vaultAppDir)
		removedDir[d] = true
		result.RemovedDirs = append(result.RemovedDirs, d)
	}

	if len(removedFile) == 0 && len(removedDir) == 0 {
		return result, nil
	}

	keptFiles := make([]ManifestFile, 0, len(app.Files))
	for _, f := range app.Files {
		if removedFile[f.Path] {
			continue
		}
		// A removed folder takes every file under it: those vault copies were
		// inside the subtree that just went, so keeping their entries would
		// describe backups that no longer exist.
		covered := false
		for d := range removedDir {
			if pathCoveredByDir(f.Path, d) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		keptFiles = append(keptFiles, f)
	}
	keptDirs := make([]string, 0, len(app.Dirs))
	for _, d := range app.Dirs {
		if removedDir[d] {
			continue
		}
		keptDirs = append(keptDirs, d)
	}

	app.Files = keptFiles
	app.Dirs = keptDirs
	m.Apps[idx] = app
	// No stampMachineInfo: this machine deleted bytes, it did not back any
	// up, and the restore preview's machine card would otherwise claim it
	// was the backupper.
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return result, err
	}
	recordActivity("remove", name, summarizeRemoveActivity(result))
	return result, nil
}

// ExpandTrackedDir converts a tracked folder into the list of files it
// currently holds: every file the folder walk finds becomes its own manifest
// entry, and the folder stops being tracked as a folder.
//
// This is what makes removing a single file from inside a tracked folder
// possible at all. It moves no bytes and deletes nothing — it changes only
// which of the manifest's two existing representations describes these
// files. Afterwards the folder is no longer walked, so a later removal
// sticks instead of being rediscovered and copied back.
//
// Files the walk finds that were never backed up go in with no checksum.
// That needs no special handling anywhere: an empty Checksum already means
// "needs backing up" throughout this codebase, so the result is a state the
// existing machinery was built for.
func (a *App) ExpandTrackedDir(name, relDir string) error {
	settings, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	app := m.Apps[idx]

	found := false
	for _, d := range app.Dirs {
		if d == relDir {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%s isn't a tracked folder of %s", relDir, app.Name)
	}

	known := make(map[string]bool, len(app.Files))
	for _, f := range app.Files {
		known[f.Path] = true
	}
	for _, rel := range expandTrackedDir(home, relDir, trackedDirSkips(settings.VaultPath)) {
		if known[rel] {
			continue
		}
		known[rel] = true
		app.Files = append(app.Files, ManifestFile{Path: rel})
	}

	keptDirs := make([]string, 0, len(app.Dirs))
	for _, d := range app.Dirs {
		if d == relDir {
			continue
		}
		keptDirs = append(keptDirs, d)
	}
	app.Dirs = keptDirs

	m.Apps[idx] = app
	// No stampMachineInfo: nothing was copied.
	return saveManifest(settings.VaultPath, m)
}

// RemoveApp deletes an app's entire vault subtree and its manifest entry.
// Live files in $HOME are untouched — as with RemoveFromApp, no $HOME path
// is ever computed here.
//
// This is not optional alongside per-file removal. AddApp and
// sanitizeManifestApps both enforce case-insensitive name uniqueness, so an
// app emptied to zero files by per-file removal would otherwise sit there
// forever holding its name: "bash" could never be created again. Shipping
// removal without this would make the app worse than it was.
func (a *App) RemoveApp(name string) (RemoveResult, error) {
	result := RemoveResult{RemovedFiles: []string{}, RemovedDirs: []string{}, Failed: []RestoreFailure{}}

	settings, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return result, err
	}
	app := m.Apps[idx]

	appDir := filepath.Join(settings.VaultPath, app.Name)
	// Defence in depth on top of loadManifest's name sanitization: prove the
	// directory about to be deleted recursively is genuinely inside the
	// vault before deleting it.
	if _, err := homeRelative(appDir, settings.VaultPath); err != nil {
		return result, fmt.Errorf("%s isn't inside the vault", app.Name)
	}
	if err := os.RemoveAll(appDir); err != nil {
		result.Failed = append(result.Failed, RestoreFailure{Path: app.Name, Reason: err.Error()})
		return result, err
	}
	for _, f := range app.Files {
		result.RemovedFiles = append(result.RemovedFiles, f.Path)
	}
	result.RemovedDirs = append(result.RemovedDirs, app.Dirs...)

	m.Apps = append(m.Apps[:idx], m.Apps[idx+1:]...)
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return result, err
	}

	// Undo snapshots are keyed by app name alone, with no record of which
	// vault or which app instance they came from. Leaving one behind means a
	// later app that happens to reuse this name inherits it, and the restore
	// overlay would offer to replay a different app's pre-restore bytes over
	// live $HOME files. Best-effort and after the vault work: failing to
	// clear it must not undo a removal that already succeeded.
	discardUndoSnapshot(name)

	recordActivity("remove", name, summarizeRemoveActivity(result))
	return result, nil
}

// RenameApp changes an app's name, moving its vault folder and its undo
// snapshot with it so nothing is left keyed to the old name.
func (a *App) RenameApp(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if err := validAppName(newName); err != nil {
		return err
	}

	settings, m, idx, err := a.loadAppForEdit(oldName)
	if err != nil {
		return err
	}
	if newName == oldName {
		return nil
	}
	// Case-insensitive, matching AddApp and sanitizeManifestApps — but the
	// app being renamed is excluded, so correcting "bash" to "Bash" is
	// allowed rather than colliding with itself.
	for i, app := range m.Apps {
		if i != idx && strings.EqualFold(app.Name, newName) {
			return fmt.Errorf("an app named %q already exists", newName)
		}
	}

	oldDir := filepath.Join(settings.VaultPath, oldName)
	newDir := filepath.Join(settings.VaultPath, newName)
	if _, err := homeRelative(newDir, settings.VaultPath); err != nil {
		return fmt.Errorf("%s isn't a usable folder name", newName)
	}
	if _, err := os.Lstat(newDir); err == nil {
		return fmt.Errorf("the vault already has a folder called %q", newName)
	}
	// An app with no files yet has no vault folder, and that is not an
	// error — there is simply nothing to move.
	if _, err := os.Lstat(oldDir); err == nil {
		if err := os.Rename(oldDir, newDir); err != nil {
			return err
		}
	}

	m.Apps[idx].Name = newName
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return err
	}
	renameUndoSnapshot(oldName, newName)
	recordActivity("remove", newName, fmt.Sprintf("Renamed %s to %s", oldName, newName))
	return nil
}

// summarizeAddToActivity describes an add into an existing app. AddApp's own
// summarizer states a total ("Added bash (3 files)"), which would be a lie
// here — this call added three files to an app that may already hold twenty.
func summarizeAddToActivity(name string, files, dirs int) string {
	var parts []string
	if files > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", files, plural(files, "file", "files")))
	}
	if dirs > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", dirs, plural(dirs, "folder", "folders")))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Nothing new to add to %s", name)
	}
	return fmt.Sprintf("Added %s to %s", strings.Join(parts, ", "), name)
}

// summarizeRemoveActivity follows the shape every other summarizer in the
// codebase uses: build the non-zero parts, join them, say "no changes" when
// there are none.
func summarizeRemoveActivity(r RemoveResult) string {
	var parts []string
	if n := len(r.RemovedFiles); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s removed from the vault", n, plural(n, "file", "files")))
	}
	if n := len(r.RemovedDirs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s removed", n, plural(n, "folder", "folders")))
	}
	if n := len(r.Failed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
