package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileRow is one backed-up file's live state, computed fresh on every call
// to GetMirrorRows — never cached, never stale.
type FileRow struct {
	Path string `json:"path"`

	// System marks a row whose Path is an absolute /etc or /usr path rather
	// than a $HOME-relative one. The UI needs it for two different reasons:
	// to render the path as what it is, and to know that restoring this row
	// will ask for administrator rights — RESONANCE runs as you, and those
	// folders do not belong to you.
	System bool `json:"system"`

	// State is one of:
	//   "ok"            source and vault agree
	//   "drifted"       source has changed since it was backed up
	//   "missing"       the live source file is gone
	//   "vaultMissing"  the source is fine but the vault's copy is gone
	//   "untracked"     found inside a tracked folder, never backed up yet
	//
	// "missing" and "vaultMissing" are deliberately distinct because they are
	// opposite problems with opposite remedies: a vault-missing file is fixed
	// by Update (copy it again), while a source-missing file cannot be, and
	// its vault copy is the only surviving one. Collapsing them would tell
	// the user to do the one thing that doesn't help.
	State string `json:"state"`

	SourceModified string `json:"sourceModified"` // RFC3339 UTC; "" if source missing
	VaultModified  string `json:"vaultModified"`  // == ManifestFile.BackedUpAt

	// Size is the vault copy's size, surfaced so the VAULT pane can describe
	// what it actually holds instead of showing a bare date. 0 when there is
	// no vault copy.
	Size int64 `json:"size"`

	// SourceSize is the live file's size. One size was enough while the only
	// question a row answered was "what does the vault hold". The differences
	// view asks a comparison question instead, and a comparison showing one
	// side's size against the other's blank space is the ambiguity that view
	// exists to remove. 0 when the source is missing.
	SourceSize int64 `json:"sourceSize"`
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
	// Blocked is a file whose destination inside the vault is no longer a
	// place this app is willing to write — a directory symlink standing in
	// the vault that redirects the write outside it. Deliberately not folded
	// into Missing: the source is present and fine, and the row's
	// vaultDamaged badge tells the user Update will repair it, so silently
	// counting this as "source missing" would be the second lie in a row.
	Blocked []string `json:"blocked"`
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
	backfill := func(appName string, entries []ManifestFile, scope pathScope) {
		for fi := range entries {
			f := &entries[fi]
			if f.Checksum != "" {
				continue
			}
			vaultFile := filepath.Join(vaultPath, appName, filepath.FromSlash(vaultRelFor(scope, f.Path)))
			if err := refuseSymlink(vaultFile); err != nil {
				continue
			}
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
	for ai := range m.Apps {
		// System entries only ever arrive with a checksum already set — they
		// are newer than checksums themselves — but they are passed through
		// here anyway rather than assumed clean. A manifest is untrusted
		// input, and an entry that reaches this function with an empty
		// checksum would otherwise be reported drifted forever with nothing
		// able to give it a baseline.
		backfill(m.Apps[ai].Name, m.Apps[ai].Files, scopeHome)
		backfill(m.Apps[ai].Name, m.Apps[ai].SystemFiles, scopeSystem)
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
	// loadManifest's error distinguishes a genuinely fresh/empty vault
	// (nil error, empty Apps) from a vault that can't be read right now —
	// unplugged drive, stale saved path, corrupt manifest.json. Swallowing
	// that error here used to render both cases identically as "nothing
	// tracked yet," which silently hid a disconnected vault behind what
	// looked like a healthy, empty one. Propagating it lets the frontend
	// tell the difference and say so.
	// Scoped tight on purpose: this is the one manifest writer that runs on
	// every refresh, and holding manifestMu across the hashing below would
	// make a routine refresh block on a long update. Everything after the
	// unlock works from this call's own copy of m.
	manifestMu.Lock()
	m, err := loadManifest(settings.VaultPath)
	if err == nil && backfillChecksums(settings.VaultPath, &m) {
		_ = saveManifest(settings.VaultPath, m)
	}
	manifestMu.Unlock()
	if err != nil {
		return nil, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Resolved once per call, not per directory: these are what the tracked
	// folder walk must never wander into.
	skip := []string{resolveDir(settings.VaultPath)}
	if stateDir, err := resonanceStateDir(); err == nil {
		skip = append(skip, resolveDir(stateDir))
	}

	rows := make([]AppRow, 0, len(m.Apps))
	for _, app := range m.Apps {
		row := AppRow{Name: app.Name, Files: make([]FileRow, 0, len(app.Files))}

		known := make(map[string]bool, len(app.Files)+len(app.SystemFiles))
		for _, f := range app.Files {
			known[f.Path] = true
			row.Files = append(row.Files, fileDriftRow(home, settings.VaultPath, app.Name, scopeHome, f))
		}
		for _, f := range app.SystemFiles {
			known[f.Path] = true
			row.Files = append(row.Files, fileDriftRow(home, settings.VaultPath, app.Name, scopeSystem, f))
		}

		// Tracked folders are reported, never materialised here: this is a
		// read path, and it must not rewrite manifest.json behind the user's
		// back. A file found under a tracked folder that has no manifest
		// entry yet shows up as "untracked", which marks the app drifted, and
		// the existing "Update from source" button is what actually copies it
		// in and records it.
		trackedIn := func(scope pathScope, dirs []string) {
			for _, d := range dirs {
				for _, stored := range expandTrackedDir(home, scope, d, skip) {
					if known[stored] {
						continue
					}
					known[stored] = true
					fr := FileRow{Path: stored, State: "untracked", System: scope == scopeSystem}
					if info, err := os.Stat(sourceAbs(home, scope, stored)); err == nil {
						fr.SourceModified = info.ModTime().UTC().Format(time.RFC3339)
						fr.SourceSize = info.Size()
					}
					row.Files = append(row.Files, fr)
				}
			}
		}
		trackedIn(scopeHome, app.Dirs)
		trackedIn(scopeSystem, app.SystemDirs)

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
// vaultRoot may be "" to skip the vault-side check entirely; every caller
// inside the app passes a real one. It is the vault ROOT rather than the
// app's subdirectory on purpose: containment has to be measured against the
// vault itself, or an app directory that is a symlink pointing outside would
// be compared against its own target and trivially "contain" it.
func fileDriftRow(home, vaultRoot, appName string, scope pathScope, f ManifestFile) FileRow {
	fr := FileRow{Path: f.Path, VaultModified: f.BackedUpAt, Size: f.Size, System: scope == scopeSystem}

	sourcePath := sourceAbs(home, scope, f.Path)
	// Re-validated through the same classifier that accepted it, rather than
	// through a check hand-written for this one call site. A manifest is
	// untrusted input — a foreign one arrives with every AdoptVaultPath — and
	// an entry hand-edited to "../../etc/shadow" or to a system path under no
	// allowed root must read as missing here, not be stat'd. Comparing the
	// scope back guards the other direction too: an absolute path filed in
	// Files, or a relative one filed in SystemFiles, is a manifest lying
	// about what it holds.
	if gotScope, _, err := classifySource(sourcePath, home); err != nil || gotScope != scope {
		fr.State = "missing"
		return fr
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		fr.State = "missing"
		return fr
	}
	fr.SourceModified = info.ModTime().UTC().Format(time.RFC3339)
	fr.SourceSize = info.Size()

	// Until v1.2.1 this function never looked at the vault at all — it
	// compared the live file against a checksum recorded in manifest.json and
	// reported "ok" on a match. So a vault copy deleted by hand, or lost to a
	// failing drive, left the row rendering as a healthy backup with a valid
	// date, and the user only discovered otherwise when they tried to restore
	// it — possibly on another machine, after the source was gone. A backup
	// tool asserting it holds a file it does not hold is the worst thing this
	// program can do, so the vault side is now checked too.
	//
	// One os.Stat per file, no hashing: GetMirrorRows already hashes every
	// source file on every refresh, and the vault often lives on slow
	// removable media, so existence and size are checked here and full
	// content verification is left to the explicit per-app differences view.
	if vaultRoot != "" {
		vaultAppDir := filepath.Join(vaultRoot, appName)
		vaultFile := filepath.Join(vaultAppDir, filepath.FromSlash(vaultRelFor(scope, f.Path)))
		// Lexical containment first — cheap, and it rejects the obvious
		// "../.." shapes without touching the disk.
		if _, err := relativeUnder(vaultFile, vaultAppDir); err != nil {
			fr.State = "vaultDamaged"
			return fr
		}
		// Then containment that actually holds — see vaultDirEscapes. A
		// symlinked directory planted inside the vault by whoever last had the
		// drive would otherwise let a file outside the vault supply the size
		// and existence this row reports as a healthy backup.
		if vaultDirEscapes(vaultRoot, vaultFile) {
			fr.State = "vaultDamaged"
			return fr
		}
		vInfo, err := os.Lstat(vaultFile)
		switch {
		case err != nil:
			fr.State = "vaultMissing"
			return fr
		case !vInfo.Mode().IsRegular():
			// A symlink or directory standing where the backup should be is
			// not a backup. Lstat, never Stat: following a symlink planted by
			// whoever last had the drive would let an outside file decide
			// whether this row renders as healthy.
			fr.State = "vaultDamaged"
			return fr
		case f.Size != 0 && vInfo.Size() != f.Size:
			// Present, but not what was backed up. Reporting this as
			// "missing" would be inaccurate in the one direction that
			// matters: the file is there, so a user checking by hand would
			// see it and conclude the app was wrong.
			fr.State = "vaultDamaged"
			return fr
		}
	}

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

// vaultCopyIntact reports whether the vault still holds a plain file of the
// expected size at path. Lstat, not Stat: a symlink left where the backup
// should be is not the backup, and following it would let whatever it points
// at masquerade as one.
func vaultCopyIntact(path string, wantSize int64) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return wantSize == 0 || info.Size() == wantSize
}

// vaultDirEscapes reports whether the DIRECTORY chain above vaultFile, once
// resolved, lands outside the vault.
//
// Both the read path (fileDriftRow) and the write path (UpdateFromSource)
// need this, and neither can get it from filepath.Rel: that compares strings,
// while Lstat, MkdirAll and os.CreateTemp all decline to follow only the
// FINAL component and resolve every directory above it. A directory symlink
// planted inside the vault by whoever last had the drive therefore redirects
// reads (an outside file supplying the size a row reports as a healthy
// backup) and writes (copyFileAtomic creating its temp file, and renaming it,
// outside the vault entirely).
//
// The final component is deliberately NOT resolved here, because at that one
// position both callers are already safe and one of them depends on it: Lstat
// reports a symlink as a symlink, which fileDriftRow already rejects as
// vaultDamaged; and copyFileAtomic writes a temp file into the parent and
// renames over the link, replacing it rather than writing through it — which
// is how a tampered vault entry gets repaired, and is pinned by
// TestUpdateFromSource_RefusesToFollowSymlinkAtVaultDestination. Resolving
// the last component would turn that repair into a refusal.
//
// Containment is measured against the vault ROOT, because the app's own
// subdirectory may be the symlink — compared against itself it would
// trivially pass.
func vaultDirEscapes(vaultRoot, vaultFile string) bool {
	if vaultRoot == "" {
		return false
	}
	root := resolveDir(vaultRoot)
	// Walk up to the nearest ancestor that actually exists: the directories
	// below it don't exist yet, so MkdirAll is about to create them inside
	// whatever this one resolves to, and that is the thing to judge.
	dir := filepath.Dir(vaultFile)
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return !containsPath(root, resolved)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Nothing along the chain exists at all — the vault itself is
			// gone. Not an escape; the caller's own missing-vault handling
			// reports that far more accurately than this can.
			return false
		}
		dir = parent
	}
}

// expandTrackedDir lists every regular file currently under one tracked
// folder, in the form the manifest stores it: $HOME-relative and slash
// separated for a folder under $HOME, absolute for one under /etc or /usr.
//
// It resolves symlinks before trusting anything, and that is the whole point
// of the function rather than a detail. relativeUnder is filepath.Rel plus a
// ".." prefix test — pure string work, resolving nothing — and filepath.
// WalkDir lstats its root, which under POSIX declines to follow only the
// FINAL component while following every intermediate one. So a stored entry
// like ".wine/dosdevices/z:/etc" passes every lexical check and then walks
// all of /etc, because ~/.wine/dosdevices/z: really is a symlink to / that
// wine ships by default (~/.steam/root and any hand-made ~/mnt -> /mnt
// behave the same). Worse, every path discovered under it also passes a
// second relativeUnder check, because the strings genuinely do start with
// $HOME. The result would be /etc/* listed as this app's files, one click
// away from being copied into the vault.
//
// Resolving the root and deriving every result from the resolved scope root
// is what actually contains the walk. Symlinked entries are skipped outright
// rather than emitted, and skipResolved (the vault and RESONANCE's own state
// directory, already resolved) is compared against resolved paths too — a
// vault at ~/dots reached through ~/backup would otherwise enumerate itself.
//
// The containment root is per scope, and that matters more here than
// anywhere else in v1.3.0. /etc is full of symlinks that leave it —
// resolv.conf into /run, os-release into /usr/lib — so a walk anchored to
// "any allowed root" would happily carry a file from /usr into an app that
// tracks /etc, and one anchored to $HOME would reject every system file
// there is. Each tracked folder is contained against the one root it was
// classified under and no other.
func expandTrackedDir(home string, scope pathScope, storedDir string, skipResolved []string) []string {
	scopeRoot, err := sourceScopeRoot(home, scope, storedDir)
	if err != nil {
		return nil
	}
	// The logical root is what results are reported against: the walk runs on
	// resolved paths, but a stored entry has to name the path a user would
	// recognise and that classifySource will accept again later.
	logicalRoot := home
	if scope == scopeSystem {
		root, ok := systemRootOf(filepath.FromSlash(storedDir))
		if !ok {
			return nil
		}
		logicalRoot = root
	}

	absDir := sourceAbs(home, scope, storedDir)
	realRoot, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return nil // gone, or unreadable — nothing to report
	}
	if _, err := relativeUnder(realRoot, scopeRoot); err != nil {
		return nil // escapes its own root once resolved, whatever the string said
	}
	// A root sitting INSIDE the vault (or inside RESONANCE's state directory)
	// has nothing to offer but the app's own stored copies, so the whole walk
	// is abandoned. The reverse — a root that merely contains the vault — is
	// still walked, with the vault subtree skipped as it is reached below;
	// refusing outright there would throw away every legitimate file
	// alongside it.
	for _, skip := range skipResolved {
		if skip != "" && containsPath(skip, realRoot) {
			return nil
		}
	}

	var out []string
	_ = filepath.WalkDir(realRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable corner of the tree — report the rest
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			for _, skip := range skipResolved {
				if skip != "" && containsPath(skip, path) {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := relativeUnder(path, scopeRoot)
		if err != nil {
			return nil
		}
		if scope == scopeSystem {
			out = append(out, filepath.ToSlash(filepath.Join(logicalRoot, rel)))
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
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
	result := UpdateResult{Updated: []string{}, Skipped: []string{}, Missing: []string{}, Blocked: []string{}}

	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return result, errors.New("no vault path set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return result, err
	}
	// Held across the whole copy loop. That serializes a long update against
	// an edit rather than letting the edit be silently reverted by this
	// function's own save at the end.
	manifestMu.Lock()
	defer manifestMu.Unlock()

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

	// Materialise anything new that has appeared inside a tracked folder
	// since the last update. GetMirrorRows only reports these; this is the
	// one place they become real manifest entries, so "→ Update from source"
	// keeps its existing single meaning — bring the vault in line with the
	// system — and no separate "refresh sources" button has to exist.
	if len(app.Dirs) > 0 || len(app.SystemDirs) > 0 {
		skip := []string{resolveDir(settings.VaultPath)}
		if stateDir, err := resonanceStateDir(); err == nil {
			skip = append(skip, resolveDir(stateDir))
		}
		known := make(map[string]bool, len(app.Files)+len(app.SystemFiles))
		for _, f := range app.Files {
			known[f.Path] = true
		}
		for _, f := range app.SystemFiles {
			known[f.Path] = true
		}
		materialise := func(scope pathScope, dirs []string, into *[]ManifestFile) {
			for _, d := range dirs {
				for _, stored := range expandTrackedDir(home, scope, d, skip) {
					if known[stored] {
						continue
					}
					known[stored] = true
					*into = append(*into, ManifestFile{Path: stored})
				}
			}
		}
		materialise(scopeHome, app.Dirs, &app.Files)
		materialise(scopeSystem, app.SystemDirs, &app.SystemFiles)
	}

	// One body, run over both scopes, so a system file can never end up on a
	// different set of guards from a home file by accident. Everything below
	// reads the source and writes the vault — both unprivileged operations —
	// which is why backing up /etc needs no rights at all. Only putting it
	// back does.
	updateOne := func(scope pathScope, f *ManifestFile) error {
		sourcePath := sourceAbs(home, scope, f.Path)
		vaultAppDir := filepath.Join(settings.VaultPath, app.Name)
		vaultFile := filepath.Join(vaultAppDir, filepath.FromSlash(vaultRelFor(scope, f.Path)))

		if gotScope, _, err := classifySource(sourcePath, home); err != nil || gotScope != scope {
			result.Missing = append(result.Missing, f.Path)
			return nil
		}
		if _, err := relativeUnder(vaultFile, vaultAppDir); err != nil {
			result.Missing = append(result.Missing, f.Path)
			return nil
		}
		// The write-side twin of fileDriftRow's check, and it must be here
		// rather than only there: copyFileAtomic below calls MkdirAll and then
		// creates its temp file inside that directory, and neither declines to
		// follow a directory symlink. A planted vault/<app> -> /outside would
		// otherwise send this backup out of the vault entirely — and the skip
		// test just below would follow the same symlink, find a matching file
		// at the far end, and report "already identical" forever, so the
		// vaultDamaged row the UI says Update will fix could never converge.
		if vaultDirEscapes(settings.VaultPath, vaultFile) {
			result.Blocked = append(result.Blocked, f.Path)
			return nil
		}

		srcInfo, err := os.Stat(sourcePath)
		if err != nil || !srcInfo.Mode().IsRegular() {
			result.Missing = append(result.Missing, f.Path)
			return nil
		}

		// "The source still matches the checksum we recorded" is only a
		// reason to skip if the vault copy that checksum describes is
		// actually still there. Without this, a vault copy deleted by hand or
		// lost to a bad drive could never be re-created: the source matches,
		// so every Update would report "skipped, already identical" while the
		// backup stayed missing — which is precisely the false confidence the
		// vaultMissing state exists to expose.
		if f.Checksum != "" && srcInfo.Size() == f.Size && vaultCopyIntact(vaultFile, f.Size) {
			if sum, err := fileChecksum(sourcePath); err == nil && sum == f.Checksum {
				result.Skipped = append(result.Skipped, f.Path)
				return nil
			}
		}

		// A system file readable a moment ago can stop being readable — a
		// package update tightening a mode is the ordinary way — and
		// copyFileAtomic would surface that as a bare errno that aborts the
		// whole update. Reporting it as this one file's problem keeps the
		// other files in the app updating, which is the same shape every
		// other per-entry failure in this program takes.
		if err := copyFileAtomic(sourcePath, vaultFile); err != nil {
			if errors.Is(err, fs.ErrPermission) {
				result.Missing = append(result.Missing, f.Path)
				return nil
			}
			return err
		}
		size, checksum, backedUpAt, err := vaultFileMeta(vaultFile)
		if err != nil {
			return err
		}
		f.Size = size
		f.Checksum = checksum
		f.BackedUpAt = backedUpAt
		result.Updated = append(result.Updated, f.Path)
		return nil
	}

	for i := range app.Files {
		if err := updateOne(scopeHome, &app.Files[i]); err != nil {
			return result, err
		}
	}
	for i := range app.SystemFiles {
		if err := updateOne(scopeSystem, &app.SystemFiles[i]); err != nil {
			return result, err
		}
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
	if n := len(result.Blocked); n > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked by the vault", n))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}
