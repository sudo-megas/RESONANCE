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
	Folders     []string `json:"folders"` // $HOME-relative, or absolute for /etc and /usr

	// SystemFolderCount is how many of Folders sit under /etc or /usr. The
	// overlay says so before the add, because a system folder's files can be
	// backed up freely but only put back with administrator rights — which is
	// a thing to learn while choosing, not at the first restore.
	SystemFolderCount int `json:"systemFolderCount"`
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
		scope, stored, err := classifySource(abs, home)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if !seen[stored] {
				seen[stored] = true
				preview.FileCount++
			}
			continue
		}
		preview.FolderCount++
		preview.Folders = append(preview.Folders, stored)
		if scope == scopeSystem {
			preview.SystemFolderCount++
		}
		for _, f := range expandTrackedDir(home, scope, stored, skip) {
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

	manifestMu.Lock()
	defer manifestMu.Unlock()

	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return err
	}
	for _, app := range m.Apps {
		if strings.EqualFold(app.Name, name) {
			return fmt.Errorf("an app named %q already exists", name)
		}
	}

	st, err := stageAdd(home, settings.VaultPath, absPaths, ManifestApp{})
	if err != nil {
		return err
	}

	appDir := filepath.Join(settings.VaultPath, name)
	if err := commitAdd(appDir, settings.VaultPath, &st); err != nil {
		return err
	}

	homeFiles, systemFiles := st.manifestFiles()
	m.Apps = append(m.Apps, ManifestApp{
		Name:        name,
		Files:       homeFiles,
		Dirs:        st.Dirs,
		SystemFiles: systemFiles,
		SystemDirs:  st.SystemDirs,
	})
	stampMachineInfo(&m)
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return err
	}
	recordActivity("add", name, summarizeAddActivity(name, len(st.Files)))
	return nil
}

// stagedFile is one validated, not-yet-copied file.
//
// Entry.Path and VaultRel stopped being the same string in v1.3.0, which is
// why VaultRel is carried rather than derived at copy time. A home file's
// manifest entry is its $HOME-relative path and it is stored in the vault at
// that same path. A system file's entry is the absolute /etc/alsa/alsa.conf,
// but it cannot be written to the vault there — it lands at
// .system/etc/alsa/alsa.conf, under the reserved segment that keeps it from
// colliding with a literal $HOME/etc file of the same name.
type stagedFile struct {
	Entry    ManifestFile
	Source   string // live path to copy from
	VaultRel string // slash-separated, inside the app's vault folder
	Scope    pathScope
}

// stagedAdd is a fully validated, not-yet-copied add.
type stagedAdd struct {
	Files []stagedFile

	// Dirs are tracked folders under $HOME, stored relative; SystemDirs are
	// tracked folders under /etc or /usr, stored absolute. Two fields rather
	// than one tagged list because that is how the manifest holds them, and
	// a single list would have to be split at every save anyway.
	Dirs       []string
	SystemDirs []string
}

// manifestFiles splits the staged entries into the two arrays the manifest
// stores them in. Done once, here, rather than at each of the two call sites.
func (st *stagedAdd) manifestFiles() (home, system []ManifestFile) {
	home = make([]ManifestFile, 0, len(st.Files))
	system = make([]ManifestFile, 0)
	for _, f := range st.Files {
		if f.Scope == scopeSystem {
			system = append(system, f.Entry)
			continue
		}
		home = append(home, f.Entry)
	}
	return home, system
}

// stageAdd validates every picked path and touches no disk state — the
// "validate everything before copying anything" half of AddApp's contract.
//
// haveFiles and haveDirs are what the app already holds, so re-picking
// something already tracked collapses to a no-op instead of a second entry
// for the same path. AddApp passes nil for both, because a new app holds
// nothing yet; AddToApp passes the app's current contents. That is the only
// difference between the two operations, and keeping it to one parameter is
// what stops "add" and "add to" from drifting into two classifiers that
// disagree about what a folder means.
func stageAdd(home, vaultPath string, absPaths []string, app ManifestApp) (stagedAdd, error) {
	st := stagedAdd{
		Files:      make([]stagedFile, 0, len(absPaths)),
		Dirs:       make([]string, 0),
		SystemDirs: make([]string, 0),
	}

	// Deduplication is on the stored path rather than the picked string, so
	// picking both ~/.config/nvim and ~/.config/nvim/init.lua yields one entry
	// for that file instead of copying it twice. Seeding the map with what the
	// app already holds extends the same collapse across calls, so re-adding a
	// tracked file is a no-op rather than a duplicate.
	//
	// Home and system paths share one map safely: a stored home path is always
	// relative and a stored system path is always absolute, so the two can
	// never collide on a string.
	skip := trackedDirSkips(vaultPath)
	vaultResolved := resolveDir(vaultPath)

	seen := make(map[string]bool, len(absPaths)+len(app.Files)+len(app.SystemFiles))
	for _, f := range app.Files {
		seen[f.Path] = true
	}
	for _, f := range app.SystemFiles {
		seen[f.Path] = true
	}
	seenDir := make(map[string]bool, len(app.Dirs)+len(app.SystemDirs))
	for _, d := range app.Dirs {
		seenDir[d] = true
	}
	for _, d := range app.SystemDirs {
		seenDir[d] = true
	}

	addFile := func(scope pathScope, stored, abs string) {
		if seen[stored] {
			return
		}
		seen[stored] = true
		st.Files = append(st.Files, stagedFile{
			Entry:    ManifestFile{Path: stored},
			Source:   abs,
			VaultRel: vaultRelFor(scope, stored),
			Scope:    scope,
		})
	}

	for _, abs := range absPaths {
		info, err := os.Stat(abs)
		if err != nil {
			return st, err
		}

		scope, stored, err := classifySource(abs, home)
		if err != nil {
			return st, err
		}

		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return st, fmt.Errorf("%s is not a regular file", abs)
			}
			// Readability is proved here, in the validation pass, rather than
			// discovered by the copy. Most of /etc is world-readable, but
			// shadow, sudoers and the private keys under ssl are not, and
			// RESONANCE reads as you — v1.3.0 deliberately ships no elevated
			// read, because those files are secrets and a vault lives on a
			// stick you carry around. Finding out during commitAdd would mean
			// an app half-added and then unwound; finding out now is one
			// sentence and nothing written.
			if err := proveReadable(abs); err != nil {
				return st, err
			}
			addFile(scope, stored, abs)
			continue
		}

		// Both directions are hazards when the vault lives under $HOME.
		// A folder containing the vault would enumerate the vault into
		// itself; a folder inside the vault would start tracking the vault's
		// own stored copies as though they were live system files. Neither
		// runs away unbounded, but both write nonsense into the manifest, and
		// failing loudly beats silently adding nothing.
		realRoot := resolveDir(abs)
		if vaultResolved != "" {
			if containsPath(realRoot, vaultResolved) {
				return st, fmt.Errorf("%s contains your vault — choose a folder that doesn't", abs)
			}
			if containsPath(vaultResolved, realRoot) {
				return st, fmt.Errorf("%s is inside your vault — choose a folder on the system side", abs)
			}
		}

		if !seenDir[stored] {
			seenDir[stored] = true
			if scope == scopeSystem {
				st.SystemDirs = append(st.SystemDirs, stored)
			} else {
				st.Dirs = append(st.Dirs, stored)
			}
		}
		for _, f := range expandTrackedDir(home, scope, stored, skip) {
			addFile(scope, f, sourceAbs(home, scope, f))
		}
	}

	return st, nil
}

// proveReadable opens a file for reading and closes it again. Nothing else
// in the program answers "can I actually read this?" honestly — a mode-bit
// or ACL test can disagree with the read that follows, exactly as
// ensureVaultWritable exists rather than a permission calculation on the
// write side.
func proveReadable(abs string) error {
	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf(
				"no permission to read %s — RESONANCE runs as you, and this release doesn't ask for administrator rights to read (the files under /etc you can't read are passwords and private keys, which don't belong in a vault you carry around)",
				abs)
		}
		return err
	}
	return f.Close()
}

// commitAdd copies every staged source into appDir and fills in each entry's
// Size/Checksum/BackedUpAt from the vault-side copy, so the recorded baseline
// always describes what actually landed rather than what was read.
//
// Validation is complete by the time this runs, so nothing here can be
// rejected — but the copy itself can still fail on a full disk or a drive
// pulled mid-write, and until v1.2.1 that left every file already written
// sitting in the vault with no manifest entry naming it: invisible to the
// app, unremovable from inside it, and duplicated by every later Copy or
// Move. Unwinding only the paths this call actually wrote is provably safe —
// each was created moments earlier by the loop below — unlike deleting the
// app directory wholesale, which could take leftovers from an earlier failed
// attempt that the user may still want to inspect.
func commitAdd(appDir, vaultPath string, st *stagedAdd) error {
	written := make([]string, 0, len(st.Files))
	unwind := func() {
		for i := len(written) - 1; i >= 0; i-- {
			_ = os.Remove(written[i])
		}
		pruneEmptyDirs(appDir, vaultPath)
	}

	for i := range st.Files {
		// VaultRel, not Entry.Path: for a system file those differ, and only
		// VaultRel is a path that may be joined onto the vault. Joining the
		// absolute /etc/alsa/alsa.conf onto appDir would discard appDir
		// entirely and write straight to /etc — filepath.Join(a, "/etc/x")
		// does not escape, but the equivalent slip elsewhere would, and the
		// staged VaultRel exists so this call site never has to think about it.
		dst := filepath.Join(appDir, filepath.FromSlash(st.Files[i].VaultRel))
		// The write-side containment guard UpdateFromSource applies before
		// every copy (drift.go). copyFileAtomic calls MkdirAll and then
		// creates its temp file inside that directory, and neither declines
		// to follow a directory symlink, so a planted vault directory
		// pointing outside would send this backup out of the vault entirely.
		// Until now this was the one remaining write path without the check.
		if vaultDirEscapes(vaultPath, dst) {
			unwind()
			return fmt.Errorf("%s can't be written — something in the vault points outside it", st.Files[i].Entry.Path)
		}
		if err := copyFileAtomic(st.Files[i].Source, dst); err != nil {
			unwind()
			return err
		}
		written = append(written, dst)
		size, checksum, backedUpAt, err := vaultFileMeta(dst)
		if err != nil {
			unwind()
			return err
		}
		st.Files[i].Entry.Size = size
		st.Files[i].Entry.Checksum = checksum
		st.Files[i].Entry.BackedUpAt = backedUpAt
	}
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
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return OrphanReport{Files: []string{}}, nil
	}
	return scanVaultOrphans(settings.VaultPath)
}

// scanVaultOrphans is the body of ScanVaultOrphans, split out so the remover
// can re-derive the set for itself while holding manifestMu. It deliberately
// does not take that lock: every caller either already holds it or is a pure
// reader.
func scanVaultOrphans(vaultPath string) (OrphanReport, error) {
	report := OrphanReport{Files: []string{}}
	m, err := loadManifest(vaultPath)
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
		// System files are accounted for at their vault location, not their
		// manifest path — an absolute /etc/alsa/alsa.conf is not local and
		// would be skipped by the test above, so without this every single
		// backed-up system file reads as an orphan and the delete surface
		// offers to destroy all of them.
		for _, f := range app.SystemFiles {
			vaultRel := systemVaultRel(f.Path)
			if !filepath.IsLocal(filepath.FromSlash(vaultRel)) {
				continue
			}
			known[path.Join(app.Name, vaultRel)] = true
		}
	}

	// WalkDir lstats its root, so a vault path that is itself a symlink makes
	// the root a non-directory and the walk returns having visited nothing —
	// reporting "nothing unaccounted for" for every such vault, which is a
	// lie rather than an absence. A symlinked vault path is an ordinary valid
	// setup that refuseSymlinkedParents goes out of its way to keep working,
	// so it has to work here too. Only the root is resolved; symlinks found
	// inside the vault are still never followed, and are reported as the
	// single entries they are.
	root := vaultPath
	if resolved, err := filepath.EvalSymlinks(vaultPath); err == nil {
		root = resolved
	}

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
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

// RemoveVaultOrphans deletes vault files that no manifest entry accounts for.
//
// This is the only operation in the program that deletes a file nothing
// references — every other removal has a manifest entry naming it, and so has
// something to be checked against. That asymmetry is why the list the
// frontend sends is treated as a request rather than an instruction: the scan
// the user looked at may be minutes old, and the set is re-derived here, under
// the lock, immediately before anything is unlinked. A path that is no longer
// an orphan is refused per-file rather than deleted.
//
// Re-deriving is also what makes the whole surface safe against a hand-written
// IPC call, without a single bespoke check: a manifest-backed file,
// manifest.json itself, a ".." traversal and a path that never existed are all
// simply absent from the set the scan just built.
//
// manifestMu is held for a reason specific to this operation. commitAdd copies
// every file into the vault BEFORE it saves the manifest, so for the duration
// of an add those freshly written backups are, by the only definition
// available, orphans. A scan racing an add would offer to delete the very
// bytes being added. Serialising against every manifest writer closes that
// window, and it is the one place where an orphan sweep could destroy a
// backup the user was in the middle of making.
func (a *App) RemoveVaultOrphans(relPaths []string) (RemoveResult, error) {
	manifestMu.Lock()
	defer manifestMu.Unlock()

	result := RemoveResult{RemovedFiles: []string{}, RemovedDirs: []string{}, Failed: []RestoreFailure{}}
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return result, errors.New("no vault is configured")
	}
	report, err := scanVaultOrphans(settings.VaultPath)
	if err != nil {
		return result, err
	}
	orphan := make(map[string]bool, len(report.Files))
	for _, f := range report.Files {
		orphan[f] = true
	}

	for _, rel := range relPaths {
		if !orphan[rel] {
			result.Failed = append(result.Failed, RestoreFailure{
				Path:   rel,
				Reason: "nothing unaccounted-for at this path any more — close this and look again",
			})
			continue
		}
		// The membership test above already refuses anything the walk did not
		// produce, and the walk never follows a symlink — so on its own this
		// guard is unreachable, and it is kept deliberately anyway. It covers
		// the window between that rescan and this unlink, in which another
		// process can replace a directory in the path with a link to
		// somewhere else. That window is small, but it is the only kind of
		// mistake this program can make that leaves nothing to restore from.
		// The vault root is exempt because a symlinked vault path is an
		// ordinary setup; everything inside the vault is not.
		if err := refuseSymlinkedIntermediates(settings.VaultPath, rel); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: rel, Reason: err.Error()})
			continue
		}
		abs := filepath.Join(settings.VaultPath, filepath.FromSlash(rel))
		// os.Remove is unlink(2), which declines to follow a symlink at the
		// final component — so an orphan that is itself a link is unlinked,
		// never followed to whatever it points at.
		if err := os.Remove(abs); err != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: rel, Reason: err.Error()})
			continue
		}
		result.RemovedFiles = append(result.RemovedFiles, rel)
		pruneEmptyDirs(filepath.Dir(abs), settings.VaultPath)
	}

	if len(result.RemovedFiles) > 0 {
		recordActivity("remove", "vault", summarizeOrphanActivity(result))
	}
	return result, nil
}

// summarizeOrphanActivity phrases the log line for an orphan sweep. It names
// the vault rather than an app because an app it belongs to is exactly the
// thing an orphan does not have.
func summarizeOrphanActivity(result RemoveResult) string {
	noun := "files"
	if len(result.RemovedFiles) == 1 {
		noun = "file"
	}
	line := fmt.Sprintf("Deleted %d unaccounted-for %s from the vault", len(result.RemovedFiles), noun)
	if len(result.Failed) > 0 {
		line += fmt.Sprintf(", %d could not be deleted", len(result.Failed))
	}
	return line
}

// summarizeAddActivity builds AddApp's activity-log summary from the file
// count already in scope at its call site — AddApp has no result struct to
// draw from, unlike Update/Restore/Undo.
func summarizeAddActivity(name string, fileCount int) string {
	return fmt.Sprintf("Added %s (%d files)", name, fileCount)
}
