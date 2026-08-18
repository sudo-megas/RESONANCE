package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const snapshotFileName = "snapshot.json"

// SnapshotEntry records one file's exact state immediately before
// RestoreApp overwrote or created it, so UndoRestore can put it back.
type SnapshotEntry struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"` // "absent" | "regular" | "symlink"
	LinkTarget string `json:"linkTarget,omitempty"`

	// System marks an entry whose Path is absolute, under /etc or /usr.
	// Additive, on the same footing as RestoreSnapshot.VaultPath below:
	// absent on every snapshot written before v1.3.0, and absent means a
	// $HOME-relative path, which is what every one of those snapshots holds.
	//
	// Without it, undo would join an absolute /etc path onto $HOME and write
	// the old bytes to ~/etc/alsa/alsa.conf — the same silent wrong-place
	// failure the manifest avoids by keeping SystemFiles a separate array.
	System bool `json:"system,omitempty"`
}

// RestoreSnapshot is one app's full pre-restore state, written to
// undoRootDir()/<app>/snapshot.json. It lives entirely outside the vault —
// it describes a mutation to this machine's $HOME, not a portable property
// of the vault, so it must never travel on a vault Copy/Move.
type RestoreSnapshot struct {
	App       string          `json:"app"`
	CreatedAt string          `json:"createdAt"`
	Entries   []SnapshotEntry `json:"entries"`

	// VaultPath is the vault this restore pulled from, recorded so undo can
	// tell whether it still relates to the vault the user is looking at.
	// Snapshots are keyed by app name and nothing else, so without it an app
	// named "bash" in a different vault inherits the offer to replay another
	// vault's pre-restore bytes over live $HOME files.
	//
	// Additive, on the same footing as ManifestFile's Size and Checksum:
	// absent on every snapshot written before v1.2.2, and absent means
	// UNKNOWN — keep offering. The next restore stamps it.
	VaultPath string `json:"vaultPath,omitempty"`
}

// UndoInfo is the trimmed, IPC-safe summary of whether an app has an undo
// snapshot available — SnapshotEntry's internals never cross the boundary.
type UndoInfo struct {
	Available bool   `json:"available"`
	CreatedAt string `json:"createdAt"`
	FileCount int    `json:"fileCount"`

	// Restorable is how many entries would actually succeed if undo ran now,
	// established by a dry run that writes nothing. A count rather than a
	// boolean because UndoRestore is per-entry-independent: a single damaged
	// entry must not suppress an offer that would put the other nine back.
	// "This undo can never succeed" is Restorable == 0.
	Restorable int `json:"restorable"`

	// Stale marks a snapshot taken from a different vault than the one
	// currently configured. Not corruption — it replays a genuinely valid
	// earlier state of $HOME — but the offer's implied claim that it relates
	// to THIS vault's app of that name is false, so the UI labels it rather
	// than hiding it.
	Stale     bool   `json:"stale"`
	VaultPath string `json:"vaultPath"`
}

// UndoResult reports what UndoRestore actually did, file by file — the
// same shape as RestoreResult's Failed side, for the same
// per-entry-independent reason.
type UndoResult struct {
	Restored []string         `json:"restored"`
	Failed   []RestoreFailure `json:"failed"`
}

// resonanceStateDir is the per-machine state directory RESONANCE's own
// on-disk state (undo snapshots, the activity log) lives under. Go's
// stdlib has no os.UserStateDir() (unlike UserConfigDir / UserCacheDir),
// so this follows the same XDG fallback convention by hand. Not
// ~/.cache — a cache is safe to lose, and undo snapshots exist nowhere
// else once a restore has run.
func resonanceStateDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "resonance"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "resonance"), nil
}

// undoRootDir is where undo snapshots live, one subdirectory below
// resonanceStateDir() — kept separate so undo's directory-per-app layout
// can never collide with the activity log or any future state stored
// alongside it.
func undoRootDir() (string, error) {
	dir, err := resonanceStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "undo"), nil
}

// captureEntry records destPath's current state into pendingDir before
// RestoreApp is about to mutate it. A regular file's bytes are copied in
// full; an absent path or a symlink only needs its metadata captured. Any
// error here means the caller must not mutate destPath either —
// fail-closed, matching CORE.md's requirement that prior state is tucked
// aside before it's overwritten, not best-effort.
func captureEntry(pendingDir string, scope pathScope, relPath, destPath string) (SnapshotEntry, error) {
	system := scope == scopeSystem
	info, err := os.Lstat(destPath)
	if os.IsNotExist(err) {
		return SnapshotEntry{Path: relPath, Kind: "absent", System: system}, nil
	}
	if err != nil {
		return SnapshotEntry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(destPath)
		if err != nil {
			return SnapshotEntry{}, err
		}
		return SnapshotEntry{Path: relPath, Kind: "symlink", LinkTarget: target, System: system}, nil
	}
	if !info.Mode().IsRegular() {
		return SnapshotEntry{}, errors.New(relPath + " is not a regular file")
	}
	// Stored under the same reserved .system segment the vault uses, not at
	// the raw path: filepath.Join would swallow the leading slash of an
	// absolute entry, so /etc/alsa/alsa.conf and a home file at
	// ~/etc/alsa/alsa.conf would land on the identical byte in the snapshot
	// and one would overwrite the other's pre-restore state.
	//
	// Capturing is a plain read of the live file, which is why it needs no
	// rights even for /etc. Putting it back does.
	store := filepath.Join(pendingDir, filepath.FromSlash(vaultRelFor(scope, relPath)))
	if err := copyFile(destPath, store); err != nil {
		return SnapshotEntry{}, err
	}
	return SnapshotEntry{Path: relPath, Kind: "regular", System: system}, nil
}

// entryScope reads a snapshot entry's scope back. One function so the
// bool-to-scope mapping is written once rather than at each undo call site.
func entryScope(e SnapshotEntry) pathScope {
	if e.System {
		return scopeSystem
	}
	return scopeHome
}

// writeSnapshot is the one point where a snapshot becomes durable. Called
// only on the pending directory — see RestoreApp's stage-then-commit
// sequence for why the canonical directory is never written to directly.
func writeSnapshot(dir string, snap RestoreSnapshot) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, snapshotFileName), data, 0644)
}

// readSnapshot loads dir/snapshot.json. Any failure — missing, corrupt,
// unreadable — is reported the same way, as "no snapshot", matching
// GetMirrorRows' philosophy for locally-cached state: a bad cache means
// nothing is offered, not a crash.
func readSnapshot(dir string) (RestoreSnapshot, bool) {
	data, err := os.ReadFile(filepath.Join(dir, snapshotFileName))
	if err != nil {
		return RestoreSnapshot{}, false
	}
	var snap RestoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return RestoreSnapshot{}, false
	}
	return snap, true
}

// removeAnythingAt clears whatever's at path — regular file or symlink —
// so a fresh write can land cleanly. Unlike RestoreApp's removeSymlinkAt,
// this doesn't care what's currently there: undo may need to replace a
// regular file (what a normal restore left behind) with a symlink.
func removeAnythingAt(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// commitSnapshot finishes the stage-then-commit sequence RestoreApp begins
// by capturing entries into pendingDir: only once the new snapshot is
// fully written to pendingDir does the canonical directory get replaced.
// If writeSnapshot fails, pendingDir is left in place (abandoned, cleaned
// up opportunistically by the next RestoreApp call) and canonicalDir is
// left completely untouched — the crash-safety property this whole design
// exists for: a disk-full write here can never destroy a still-valid old
// snapshot.
//
// The old canonicalDir is moved aside rather than os.RemoveAll'd in place:
// RemoveAll walks and deletes the tree file by file, so a crash partway
// through can corrupt the old snapshot before the new one has replaced it.
// A rename is a single atomic step either way, so the old snapshot is
// never in a half-deleted state — worst case after a crash, a harmless
// ".stale" directory is left for the next commitSnapshot call to clean up.
//
// That cleanup is a recovery, not a blind delete: a crash landing between
// the two renames below leaves canonicalDir gone and staleDir holding the
// only surviving copy of the *previous* snapshot, with nothing in between
// GetUndoInfo/readSnapshot to find. The next commitSnapshot call restores
// staleDir to canonicalDir first, before doing anything else, so that
// snapshot is reinstated rather than silently deleted the moment this
// function's cleanup would otherwise have run again. The same recovery
// covers a failed final rename: if pendingDir can't be renamed onto
// canonicalDir, the old snapshot is moved back into place so a valid
// snapshot stays reachable instead of disappearing.
func commitSnapshot(pendingDir, canonicalDir string, snap RestoreSnapshot) error {
	if err := writeSnapshot(pendingDir, snap); err != nil {
		return err
	}
	staleDir := canonicalDir + ".stale"

	if _, err := os.Stat(canonicalDir); err != nil {
		if _, staleErr := os.Stat(staleDir); staleErr == nil {
			if err := os.Rename(staleDir, canonicalDir); err != nil {
				return err
			}
		}
	}

	if _, err := os.Stat(canonicalDir); err == nil {
		_ = os.RemoveAll(staleDir)
		if err := os.Rename(canonicalDir, staleDir); err != nil {
			return err
		}
	}
	if err := os.Rename(pendingDir, canonicalDir); err != nil {
		if _, statErr := os.Stat(staleDir); statErr == nil {
			_ = os.Rename(staleDir, canonicalDir)
		}
		return err
	}
	_ = os.RemoveAll(staleDir)
	return nil
}

// GetUndoInfo reports whether appName has a pending undo snapshot, for the
// restore-confirm overlay to check before falling back to its "nothing to
// restore" toast.
func (a *App) GetUndoInfo(appName string) (UndoInfo, error) {
	// appName arrives over IPC and is joined straight onto the undo root
	// below. The frontend only ever sources it from sanitized manifest rows,
	// but nothing about the IPC boundary enforces that.
	if err := validAppName(appName); err != nil {
		return UndoInfo{}, err
	}
	root, err := undoRootDir()
	if err != nil {
		return UndoInfo{}, err
	}
	snap, ok := readSnapshot(filepath.Join(root, appName))
	if !ok {
		return UndoInfo{}, nil
	}

	info := UndoInfo{
		Available: true,
		CreatedAt: snap.CreatedAt,
		FileCount: len(snap.Entries),
		VaultPath: snap.VaultPath,
	}

	// An empty VaultPath is a pre-v1.2.2 snapshot: unknown, not foreign, so
	// it keeps being offered exactly as it was before this field existed.
	if snap.VaultPath != "" {
		if current := a.GetSettings().VaultPath; current != "" {
			info.Stale = resolveDir(snap.VaultPath) != resolveDir(current)
		}
	}

	info.Restorable = countRestorableEntries(snap, filepath.Join(root, appName))
	return info, nil
}

// countRestorableEntries dry-runs a snapshot: how many of its entries would
// succeed if undo ran right now. It writes nothing and mirrors UndoRestore's
// own per-entry checks, so the two cannot disagree about what is possible.
//
// This exists because an undo whose backing bytes are gone was offered
// forever with no way to tell and no way to clear it — the user meets the
// same failing offer on every visit.
func countRestorableEntries(snap RestoreSnapshot, canonicalDir string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	n := 0
	for _, entry := range snap.Entries {
		scope := entryScope(entry)
		destPath := sourceAbs(home, scope, entry.Path)
		if gotScope, _, err := classifySource(destPath, home); err != nil || gotScope != scope {
			continue
		}
		switch entry.Kind {
		case "absent", "symlink":
			// Nothing on disk backs these — the entry itself carries
			// everything undo needs.
			n++
		case "regular":
			// This one has captured bytes sitting beside snapshot.json, and
			// they are what a partially-deleted state directory loses first.
			backing := filepath.Join(canonicalDir, filepath.FromSlash(vaultRelFor(scope, entry.Path)))
			if info, err := os.Lstat(backing); err == nil && info.Mode().IsRegular() {
				n++
			}
		}
		// An unrecognised Kind counts as unrestorable: UndoRestore fails it.
	}
	return n
}

// UndoRestore replays appName's snapshot back onto the live system,
// per-entry-independent like RestoreApp itself. Every path is
// re-validated through relativeUnder before any write — snapshot.json is
// on-disk state, the same trust boundary as manifest.json, never trusted
// blindly.
func (a *App) UndoRestore(appName string) (UndoResult, error) {
	result := UndoResult{Restored: []string{}, Failed: []RestoreFailure{}}

	if err := validAppName(appName); err != nil {
		return result, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return result, err
	}
	root, err := undoRootDir()
	if err != nil {
		return result, err
	}
	canonicalDir := filepath.Join(root, appName)
	snap, ok := readSnapshot(canonicalDir)
	if !ok {
		return result, errors.New("no undo snapshot available")
	}

	allSucceeded := true
	for _, entry := range snap.Entries {
		scope := entryScope(entry)
		destPath := sourceAbs(home, scope, entry.Path)
		// Re-validated through the classifier rather than a bare containment
		// test: snapshot.json is on-disk state and gets the same treatment as
		// manifest.json. Comparing the scope back is what stops a hand-edited
		// entry from claiming to be a home path and being written into /etc,
		// or the reverse.
		if gotScope, _, err := classifySource(destPath, home); err != nil || gotScope != scope {
			reason := "this entry's path is outside everywhere RESONANCE writes"
			if err != nil {
				reason = err.Error()
			}
			result.Failed = append(result.Failed, RestoreFailure{Path: entry.Path, Reason: reason})
			allSucceeded = false
			continue
		}

		var restoreErr error
		if scope == scopeSystem {
			// Undoing a system restore is as privileged as making one: every
			// branch below unlinks or creates a file in a folder that belongs
			// to root. It goes to the helper whole, for the same reason the
			// restore does.
			restoreErr = undoSystemEntry(entry, destPath, filepath.Join(canonicalDir, filepath.FromSlash(vaultRelFor(scope, entry.Path))))
		} else {
			switch entry.Kind {
			case "absent":
				restoreErr = removeAnythingAt(destPath)
			case "symlink":
				if err := removeAnythingAt(destPath); err != nil {
					restoreErr = err
				} else {
					restoreErr = os.Symlink(entry.LinkTarget, destPath)
				}
			case "regular":
				if err := removeAnythingAt(destPath); err != nil {
					restoreErr = err
				} else {
					restoreErr = copyFile(filepath.Join(canonicalDir, filepath.FromSlash(entry.Path)), destPath)
				}
			default:
				restoreErr = errors.New("unknown snapshot entry kind: " + entry.Kind)
			}
		}

		if restoreErr != nil {
			result.Failed = append(result.Failed, RestoreFailure{Path: entry.Path, Reason: restoreErr.Error()})
			allSucceeded = false
			continue
		}
		result.Restored = append(result.Restored, entry.Path)
	}

	if allSucceeded {
		_ = os.RemoveAll(canonicalDir)
	} else if len(result.Restored) > 0 {
		// Keep the snapshot, but only the entries that still need applying.
		// Leaving it whole means a retry re-applies everything that already
		// succeeded: an "absent" entry deletes a file the user has since
		// recreated, and a "regular" entry clobbers edits made after the
		// first undo. The retry is meant to be the safe move, so it has to
		// actually be one.
		pruneSnapshotEntries(canonicalDir, snap, result.Restored)
	}

	recordActivity("undo", appName, summarizeUndoActivity(result))
	return result, nil
}

// pruneSnapshotEntries rewrites a snapshot to hold only the entries that
// have not been applied yet.
//
// writeFileAtomic rather than writeSnapshot's plain WriteFile: a crash
// midway through this rewrite would leave snapshot.json truncated, and
// readSnapshot reports unparseable JSON as "no snapshot at all" — so a
// non-atomic write here could destroy an undo that was merely incomplete.
//
// Best-effort. Failing to shrink the snapshot leaves the pre-existing
// behaviour (a retry that redoes applied work), which is worse than the
// pruned version but better than reporting an undo that did happen as
// having failed.
func pruneSnapshotEntries(canonicalDir string, snap RestoreSnapshot, applied []string) {
	done := make(map[string]bool, len(applied))
	for _, p := range applied {
		done[p] = true
	}
	kept := make([]SnapshotEntry, 0, len(snap.Entries))
	for _, e := range snap.Entries {
		if !done[e.Path] {
			kept = append(kept, e)
		}
	}
	snap.Entries = kept

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(canonicalDir, filepath.Join(canonicalDir, snapshotFileName), data, 0644)
}

// SnapshotInfo is one pending undo snapshot as the management list sees it.
//
// Until v1.2.2 snapshots were invisible storage: nothing listed them, nothing
// reported their size, and nothing but `rm` could clear one. A partly-failed
// undo keeps its snapshot by design, so the same failing offer reappeared on
// every visit with no way out from inside the app.
type SnapshotInfo struct {
	App        string `json:"app"`
	CreatedAt  string `json:"createdAt"`
	FileCount  int    `json:"fileCount"`
	Restorable int    `json:"restorable"`
	Bytes      int64  `json:"bytes"`
	Stale      bool   `json:"stale"`
	VaultPath  string `json:"vaultPath"`

	// Orphaned marks a snapshot whose app is not in the current vault —
	// removed, renamed, or simply a different vault's app list. It stays
	// fully usable: UndoRestore never reads the vault, so the snapshot
	// describes a $HOME state that is still exactly as recoverable as it was.
	Orphaned bool `json:"orphaned"`
}

// ListUndoSnapshots reports every pending undo snapshot on this machine.
//
// Read-only, deliberately. A snapshot parked at <app>.stale is reported by
// reading through to it, but never reinstated here — commitSnapshot's
// recovery path is the only thing entitled to move a .stale directory back,
// and a listing call racing it would be a genuinely hard bug to find.
func (a *App) ListUndoSnapshots() ([]SnapshotInfo, error) {
	out := []SnapshotInfo{}

	root, err := undoRootDir()
	if err != nil {
		return out, err
	}
	dirEntries, err := os.ReadDir(root)
	if err != nil {
		// No undo directory yet is the normal state before the first
		// restore, not a failure worth surfacing.
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}

	// Collapse <app>, <app>.pending and <app>.stale onto one row per app.
	names := make([]string, 0, len(dirEntries))
	seen := make(map[string]bool, len(dirEntries))
	for _, e := range dirEntries {
		if !e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".pending"), ".stale")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)

	// Which apps the current vault knows about. A vault that can't be read
	// right now (drive unplugged) leaves this nil, and nothing is claimed to
	// be orphaned on the strength of a missing drive.
	var known map[string]bool
	currentVault := a.GetSettings().VaultPath
	if currentVault != "" {
		if m, err := loadManifest(currentVault); err == nil {
			known = make(map[string]bool, len(m.Apps))
			for _, app := range m.Apps {
				known[app.Name] = true
			}
		}
	}

	for _, name := range names {
		dir := filepath.Join(root, name)
		snap, ok := readSnapshot(dir)
		if !ok {
			dir = filepath.Join(root, name+".stale")
			if snap, ok = readSnapshot(dir); !ok {
				continue
			}
		}

		info := SnapshotInfo{
			App:        name,
			CreatedAt:  snap.CreatedAt,
			FileCount:  len(snap.Entries),
			Restorable: countRestorableEntries(snap, dir),
			Bytes:      dirSizeBytes(dir),
			VaultPath:  snap.VaultPath,
		}
		if snap.VaultPath != "" && currentVault != "" {
			info.Stale = resolveDir(snap.VaultPath) != resolveDir(currentVault)
		}
		if known != nil {
			info.Orphaned = !known[name]
		}
		out = append(out, info)
	}
	return out, nil
}

// DiscardUndoSnapshot deletes one app's undo snapshot for good.
//
// This is the only exit from a snapshot that can never be applied, so it
// clears all three of <app>, <app>.pending and <app>.stale together — unlike
// every other path here, which is careful to leave .stale alone. The user
// asked for this one explicitly.
func (a *App) DiscardUndoSnapshot(appName string) error {
	if err := validAppName(appName); err != nil {
		return err
	}
	root, err := undoRootDir()
	if err != nil {
		return err
	}
	for _, suffix := range []string{"", ".pending", ".stale"} {
		if err := os.RemoveAll(filepath.Join(root, appName+suffix)); err != nil {
			return err
		}
	}
	recordActivity("edit", appName, "Discarded the pending undo snapshot")
	return nil
}

// dirSizeBytes sums the regular files under dir. Best-effort: a snapshot
// whose size can't be totalled is still worth listing, so an unreadable
// subtree contributes what it can rather than failing the whole call.
func dirSizeBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// summarizeUndoActivity turns UndoResult's counts into a short
// human-readable description for the activity log, e.g. "3 restored, 1
// failed". Counts of zero are omitted entirely.
func summarizeUndoActivity(result UndoResult) string {
	var parts []string
	if n := len(result.Restored); n > 0 {
		parts = append(parts, fmt.Sprintf("%d restored", n))
	}
	if n := len(result.Failed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// renameUndoSnapshot moves an undo snapshot so it follows a renamed app.
//
// Snapshots are keyed by app name and nothing else. Without this the
// snapshot would stay behind under the old name: unreachable from the
// renamed app, and waiting to be inherited by whatever app next takes that
// name — at which point the restore overlay would offer to replay one app's
// pre-restore bytes over another app's live files.
func renameUndoSnapshot(oldName, newName string) {
	root, err := undoRootDir()
	if err != nil {
		return
	}
	oldDir := filepath.Join(root, oldName)
	if _, err := os.Lstat(oldDir); err != nil {
		return // nothing was ever captured for this app
	}
	newDir := filepath.Join(root, newName)
	// Anything already sitting under the new name belongs to a different app
	// instance, and adopting it would be the exact confusion this function
	// exists to prevent. Neither is it destroyed: it is a real record of a
	// real change to $HOME, and deleting one to tidy up a rename is not a
	// trade this app gets to make on the user's behalf.
	//
	// So on a collision the move is simply declined. Both snapshots survive,
	// ListUndoSnapshots shows them (the stranded one marked Orphaned, since
	// no app answers to the old name any more), and the user decides which
	// to keep.
	if _, err := os.Lstat(newDir); err == nil {
		return
	}
	_ = os.Rename(oldDir, newDir)
	_ = os.RemoveAll(filepath.Join(root, oldName+".pending"))
	// commitSnapshot parks a superseded snapshot at <app>.stale while it
	// swaps in the new one. A rename landing between those two steps would
	// otherwise strand the only surviving copy under the old name.
	_ = os.Rename(filepath.Join(root, oldName+".stale"), filepath.Join(root, newName+".stale"))
}
