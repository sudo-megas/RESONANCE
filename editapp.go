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

	// System marks a folder under /etc or /usr, whose Path is absolute.
	System bool `json:"system"`
}

// UntrackPreview is what untracking a folder would do, counted before the
// user commits to it. KeepsTracked is the files that already have their own
// manifest entry and so survive untouched; StopsTracking is the files in the
// folder that have never been backed up and would simply stop being watched.
type UntrackPreview struct {
	Dir           string `json:"dir"`
	KeepsTracked  int    `json:"keepsTracked"`
	StopsTracking int    `json:"stopsTracking"`
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
		Files: make([]string, 0, len(app.Files)+len(app.SystemFiles)),
		Dirs:  make([]TrackedDir, 0, len(app.Dirs)+len(app.SystemDirs)),
	}
	for _, f := range app.Files {
		comp.Files = append(comp.Files, f.Path)
	}
	for _, f := range app.SystemFiles {
		comp.Files = append(comp.Files, f.Path)
	}
	// Counted within its own scope: a system folder counts the system files
	// under it and a home folder the home files, so "/etc" cannot claim a
	// coincidentally similar-looking relative path as one of its own.
	countDirs := func(dirs []string, entries []ManifestFile, system bool) {
		for _, d := range dirs {
			td := TrackedDir{Path: d, System: system}
			for _, f := range entries {
				if pathCoveredByDir(f.Path, d) {
					td.FileCount++
				}
			}
			comp.Dirs = append(comp.Dirs, td)
		}
	}
	countDirs(app.Dirs, app.Files, false)
	countDirs(app.SystemDirs, app.SystemFiles, true)
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
	manifestMu.Lock()
	defer manifestMu.Unlock()
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
	st, err := stageAdd(home, settings.VaultPath, absPaths, app)
	if err != nil {
		return err
	}
	if len(st.Files) == 0 && len(st.Dirs) == 0 && len(st.SystemDirs) == 0 {
		// Everything picked is already tracked. Saving here would rewrite
		// manifest.json and restamp the machine info to say this machine
		// backed something up, which it did not.
		return nil
	}

	appDir := filepath.Join(settings.VaultPath, app.Name)
	if err := commitAdd(appDir, settings.VaultPath, &st); err != nil {
		return err
	}

	homeFiles, systemFiles := st.manifestFiles()
	app.Files = append(app.Files, homeFiles...)
	app.Dirs = append(app.Dirs, st.Dirs...)
	app.SystemFiles = append(app.SystemFiles, systemFiles...)
	app.SystemDirs = append(app.SystemDirs, st.SystemDirs...)
	m.Apps[idx] = app
	stampMachineInfo(&m)
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return err
	}
	recordActivity("add", name, summarizeAddToActivity(name, len(st.Files), len(st.Dirs)+len(st.SystemDirs)))
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
	manifestMu.Lock()
	defer manifestMu.Unlock()
	result := RemoveResult{RemovedFiles: []string{}, RemovedDirs: []string{}, Failed: []RestoreFailure{}}

	settings, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return result, err
	}
	app := m.Apps[idx]
	vaultAppDir := filepath.Join(settings.VaultPath, app.Name)

	// Both lists, and the scope each entry came from. The scope is looked up
	// from the manifest rather than guessed from the string's shape: what
	// decides where a delete lands must be what the app recorded, not what
	// the path happens to look like when it arrives over IPC.
	haveFile := make(map[string]bool, len(app.Files)+len(app.SystemFiles))
	fileScope := make(map[string]pathScope, len(app.Files)+len(app.SystemFiles))
	for _, f := range app.Files {
		haveFile[f.Path] = true
		fileScope[f.Path] = scopeHome
	}
	for _, f := range app.SystemFiles {
		haveFile[f.Path] = true
		fileScope[f.Path] = scopeSystem
	}
	haveDir := make(map[string]bool, len(app.Dirs)+len(app.SystemDirs))
	dirScope := make(map[string]pathScope, len(app.Dirs)+len(app.SystemDirs))
	for _, d := range app.Dirs {
		haveDir[d] = true
		dirScope[d] = scopeHome
	}
	for _, d := range app.SystemDirs {
		haveDir[d] = true
		dirScope[d] = scopeSystem
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
		// Every covering folder, not just the first: stageAdd permits nested
		// tracked folders, so ".config" and ".config/nvim" can both be in
		// Dirs. Naming only one would send the user to untrack it, then
		// refuse the same removal again naming the next one out — a dead end
		// that looks like the app changing its mind.
		// Only folders in the same scope can cover it: a home file is never
		// inside a tracked /etc folder, and comparing across scopes would
		// match on nothing but string coincidence.
		coveringDirs := app.Dirs
		if fileScope[p] == scopeSystem {
			coveringDirs = app.SystemDirs
		}
		var covering []string
		for _, d := range coveringDirs {
			if pathCoveredByDir(p, d) {
				covering = append(covering, d)
			}
		}
		if len(covering) > 0 {
			return result, fmt.Errorf(
				"%s is inside the tracked %s %s — untrack %s first, or remove the whole folder",
				p, plural(len(covering), "folder", "folders"),
				strings.Join(covering, " and "),
				plural(len(covering), "it", "those"))
		}
		if _, err := relativeUnder(filepath.Join(vaultAppDir, filepath.FromSlash(vaultRelFor(fileScope[p], p))), vaultAppDir); err != nil {
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
		if _, err := relativeUnder(filepath.Join(vaultAppDir, filepath.FromSlash(vaultRelFor(dirScope[d], d))), vaultAppDir); err != nil {
			return result, fmt.Errorf("%s isn't inside this app's vault folder", d)
		}
		dirs = append(dirs, d)
	}

	// --- per-entry execution ---------------------------------------------

	removedFile := make(map[string]bool, len(files))
	for _, p := range files {
		vaultRel := vaultRelFor(fileScope[p], p)
		abs := filepath.Join(vaultAppDir, filepath.FromSlash(vaultRel))
		if err := refuseSymlinkedParents(vaultAppDir, vaultRel); err != nil {
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
		vaultRel := vaultRelFor(dirScope[d], d)
		abs := filepath.Join(vaultAppDir, filepath.FromSlash(vaultRel))
		if err := refuseSymlinkedParents(vaultAppDir, vaultRel); err != nil {
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

	// A removed folder takes every file under it: those vault copies were
	// inside the subtree that just went, so keeping their entries would
	// describe backups that no longer exist. Matched within one scope, so a
	// removed /etc folder cannot sweep away a home entry that merely reads
	// like it sits underneath.
	keepFiles := func(entries []ManifestFile, scope pathScope) []ManifestFile {
		kept := make([]ManifestFile, 0, len(entries))
		for _, f := range entries {
			if removedFile[f.Path] {
				continue
			}
			covered := false
			for d := range removedDir {
				if dirScope[d] == scope && pathCoveredByDir(f.Path, d) {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			kept = append(kept, f)
		}
		return kept
	}
	keepDirs := func(dirs []string) []string {
		kept := make([]string, 0, len(dirs))
		for _, d := range dirs {
			if removedDir[d] {
				continue
			}
			kept = append(kept, d)
		}
		return kept
	}

	app.Files = keepFiles(app.Files, scopeHome)
	app.Dirs = keepDirs(app.Dirs)
	app.SystemFiles = keepFiles(app.SystemFiles, scopeSystem)
	app.SystemDirs = keepDirs(app.SystemDirs)
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

// UntrackDir stops tracking a folder as a folder. It moves no bytes, deletes
// nothing, and leaves every individual manifest entry exactly as it was.
//
// This is what makes removing a single file from inside a tracked folder
// possible at all. While the folder is tracked, the walk rediscovers a
// removed file on the next refresh and the next update copies it back, so
// the deletion silently undoes itself; once the folder is no longer walked,
// the removal sticks.
//
// Dropping the Dirs entry is the whole operation, and that is not a
// shortcut. stageAdd records a picked folder in Dirs AND expands it into
// individual Files entries, and UpdateFromSource materialises any file the
// walk later finds the same way — so every file in the folder that has ever
// been backed up already has its own entry with its own checksum, and those
// entries are untouched here.
//
// An earlier version of this function walked the folder and added an entry
// for every file it found, including files that had never been backed up,
// on the reasoning that an empty Checksum already means "needs backing up".
// That reasoning was a release out of date and the result was actively
// harmful: v1.2.1 made fileDriftRow check the vault side BEFORE the checksum
// branch, so a zero-checksum entry with no vault copy now reports
// vaultMissing — "Backup missing from the vault". Those files would have
// gone from the mildest badge in the app to its most severe, and RestoreApp
// would have started reporting each one as a failure, all because the user
// clicked a conversion that was advertised as changing nothing. A backup
// tool claiming it lost a file it never held is the same lie v1.2.1 was
// written to kill, just pointed the other way.
//
// So files in the folder that were never backed up simply stop being
// tracked. That is honest, and PreviewUntrackDir exists to say so in advance.
func (a *App) UntrackDir(name, relDir string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	settings, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return err
	}
	app := m.Apps[idx]

	drop := func(dirs []string) ([]string, bool) {
		kept := make([]string, 0, len(dirs))
		found := false
		for _, d := range dirs {
			if d == relDir {
				found = true
				continue
			}
			kept = append(kept, d)
		}
		return kept, found
	}
	keptDirs, found := drop(app.Dirs)
	keptSystemDirs, foundSystem := drop(app.SystemDirs)
	if !found && !foundSystem {
		return fmt.Errorf("%s isn't a tracked folder of %s", relDir, app.Name)
	}
	app.Dirs = keptDirs
	app.SystemDirs = keptSystemDirs

	m.Apps[idx] = app
	// No stampMachineInfo: nothing was copied, so this machine has no claim
	// to being the one that backed the app up.
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return err
	}
	recordActivity("edit", app.Name, fmt.Sprintf("Stopped tracking the folder %s", relDir))
	return nil
}

// PreviewUntrackDir reports what untracking a folder would cost, so the
// overlay can state it before the user commits rather than after.
//
// This is the one call in the edit surface that walks the disk, which is why
// it is separate from GetAppComposition: opening the overlay stays free, and
// only a user actually reaching for this specific folder pays for the walk.
func (a *App) PreviewUntrackDir(name, relDir string) (UntrackPreview, error) {
	settings, m, idx, err := a.loadAppForEdit(name)
	if err != nil {
		return UntrackPreview{}, err
	}
	app := m.Apps[idx]

	scope, found := trackedDirScope(app, relDir)
	if !found {
		return UntrackPreview{}, fmt.Errorf("%s isn't a tracked folder of %s", relDir, app.Name)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return UntrackPreview{}, err
	}

	entries := app.Files
	if scope == scopeSystem {
		entries = app.SystemFiles
	}
	tracked := make(map[string]bool, len(entries))
	for _, f := range entries {
		if pathCoveredByDir(f.Path, relDir) {
			tracked[f.Path] = true
		}
	}
	p := UntrackPreview{Dir: relDir, KeepsTracked: len(tracked)}
	for _, stored := range expandTrackedDir(home, scope, relDir, trackedDirSkips(settings.VaultPath)) {
		if !tracked[stored] {
			p.StopsTracking++
		}
	}
	return p, nil
}

// trackedDirScope finds which of an app's two folder lists holds dir. Both
// are searched from one place so no caller can check only the home list and
// report a tracked /etc folder as "isn't a tracked folder of this app".
func trackedDirScope(app ManifestApp, dir string) (pathScope, bool) {
	for _, d := range app.Dirs {
		if d == dir {
			return scopeHome, true
		}
	}
	for _, d := range app.SystemDirs {
		if d == dir {
			return scopeSystem, true
		}
	}
	return scopeHome, false
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
	manifestMu.Lock()
	defer manifestMu.Unlock()
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
	if _, err := relativeUnder(appDir, settings.VaultPath); err != nil {
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

	// The undo snapshot is deliberately NOT discarded here.
	//
	// Removing an app is a vault-side operation, and this overlay's headline
	// promise is that nothing in $HOME changes. A snapshot is the only thing
	// that can revert a PRIOR change to $HOME, and UndoRestore never reads
	// the vault — so the snapshot stays exactly as valid and exactly as
	// useful after its app leaves the vault as it was before. Deleting it
	// here would be the one genuinely destructive thing this call does to
	// the user's home folder, done silently, in the name of tidiness.
	//
	// The name-reuse hazard that once justified discarding it is handled
	// properly now: snapshots carry the vault they were taken from, and
	// GetUndoInfo flags a mismatch as Stale so the offer is labelled rather
	// than blindly presented. ListUndoSnapshots is what keeps this one
	// reachable, since after removal it has no app row left to hang off, and
	// DiscardUndoSnapshot is how the user throws it away on purpose.

	recordActivity("remove", name, summarizeRemoveActivity(result))
	return result, nil
}

// RenameApp changes an app's name, moving its vault folder and its undo
// snapshot with it so nothing is left keyed to the old name.
func (a *App) RenameApp(oldName, newName string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
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
	if _, err := relativeUnder(newDir, settings.VaultPath); err != nil {
		return fmt.Errorf("%s isn't a usable folder name", newName)
	}
	if _, err := os.Lstat(newDir); err == nil {
		return fmt.Errorf("the vault already has a folder called %q", newName)
	}
	// An app with no files yet has no vault folder, and that is not an
	// error — there is simply nothing to move.
	moved := false
	if _, err := os.Lstat(oldDir); err == nil {
		if err := os.Rename(oldDir, newDir); err != nil {
			return err
		}
		moved = true
	}

	m.Apps[idx].Name = newName
	if err := saveManifest(settings.VaultPath, m); err != nil {
		// Put the folder back. Without this the vault directory sits under
		// the new name while the manifest still says the old one: every file
		// in the app reads as vaultMissing, "update from source" rebuilds the
		// whole tree under the old name, and the moved subtree becomes
		// orphans with no route back from inside the app. commitAdd sets the
		// precedent — a half-applied vault change gets unwound, not reported.
		if moved {
			_ = os.Rename(newDir, oldDir)
		}
		return err
	}
	renameUndoSnapshot(oldName, newName)
	// "edit", not "remove": the activity log is the one surface that records
	// what this app has done to the user's data, and a rename rendered beside
	// a delete icon reads as a deletion that never happened.
	recordActivity("edit", newName, fmt.Sprintf("Renamed %s to %s", oldName, newName))
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
