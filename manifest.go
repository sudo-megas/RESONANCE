package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const manifestVersion = 1

// Manifest is the vault's own self-description, stored at
// <vault>/manifest.json. It travels with the vault, not with any one
// machine's config — a vault must be restorable by any user on any machine.
type Manifest struct {
	Version int           `json:"version"`
	Apps    []ManifestApp `json:"apps"`

	// MachineInfo describes whichever machine most recently wrote into this
	// vault (AddApp or UpdateFromSource). Additive — absent (zero-valued) on
	// any manifest.json written before STEP4, and unlike checksums this
	// can't be backfilled: it's a fact about a past event, not a property of
	// bytes the vault still has.
	MachineInfo MachineInfo `json:"machineInfo,omitempty"`
}

type ManifestApp struct {
	Name  string         `json:"name"`
	Files []ManifestFile `json:"files"`

	// Dirs are folders whose current contents belong to this app, stored
	// $HOME-relative and slash-separated exactly like ManifestFile.Path.
	// Additive, on the same footing as Size/Checksum/BackedUpAt above:
	// absent from every manifest.json written before v1.2.1, and
	// manifestVersion deliberately stays 1 because an entry no older binary
	// understands is not a format change.
	//
	// Downgrading is lossy, and silently so: encoding/json drops fields it
	// doesn't know about on unmarshal, so an older RESONANCE that loads and
	// then saves this manifest strips every tracked folder. Nothing is
	// corrupted and no backed-up file is lost — the materialised Files
	// entries survive untouched — but the folders stop being tracked and
	// nothing tells the user. SystemFiles and SystemDirs below inherit that
	// behaviour exactly.
	Dirs []string `json:"dirs,omitempty"`

	// SystemFiles and SystemDirs hold ABSOLUTE paths under /etc or /usr,
	// added in v1.3.0. Everything above is $HOME-relative; these are not, and
	// the separation is the point rather than an inconvenience.
	//
	// The obvious alternative — one Files array whose Path may be absolute,
	// plus a field saying which — is the one shape that must not be used. An
	// older RESONANCE knows nothing about roots, so it would read
	// "/etc/alsa/alsa.conf" as $HOME-relative, join it onto $HOME, and
	// restore into ~/etc/alsa/alsa.conf. Silently writing a file to the wrong
	// place is a worse failure than not knowing about it at all. Separate
	// arrays make an old binary blind instead of wrong: it drops these
	// fields on unmarshal and sees an app with fewer files, the same lossy
	// downgrade Dirs already documents.
	SystemFiles []ManifestFile `json:"systemFiles,omitempty"`
	SystemDirs  []string       `json:"systemDirs,omitempty"`
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

// describePathProblem turns a filesystem error about a vault path into one
// sentence a person can act on. The old blanket "vault not found — is the
// drive connected?" actively lied whenever the real cause was permission or
// a read-only remount — and an external drive remounted read-only after an
// unclean unmount is the likeliest EROFS case on the hardware this app is
// built for. Naming the real cause is the whole point and the whole payoff:
// an act-as-administrator prompt was planned to hang off this signal, and was
// then dropped once it turned out the reported failure was never a permission
// error at all. The honest sentence is what remains, and it is what the user
// actually needed.
func describePathProblem(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("vault not found at %s — is the drive connected?", path)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("no permission to use %s — that folder belongs to another user, and RESONANCE runs as you", path)
	case errors.Is(err, syscall.EROFS):
		return fmt.Errorf("%s is on a read-only filesystem — RESONANCE can't write there", path)
	case errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("%s is a file, not a folder", path)
	default:
		return fmt.Errorf("can't use %s: %w", path, err)
	}
}

// requireVaultDir proves a directory is there without creating anything —
// used where the caller needs an existing vault (migration's source), as
// opposed to accepting a new one.
func requireVaultDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return describePathProblem(path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is a file, not a folder", path)
	}
	return nil
}

// ensureVaultDir creates the vault folder if it isn't there — but only one
// level, and only when its parent already exists.
//
// os.MkdirAll would be the obvious call and is the wrong one. The saved
// vault path in the bug this fixes is /run/media/megas/DOTFILES/TEST: with
// the drive unplugged, MkdirAll would happily invent that entire chain on
// the root filesystem, shadowing the mount point so the real drive can
// never mount there again, and silently writing every future backup to a
// directory that vanishes the moment the drive is plugged back in.
// Refusing when the parent is missing turns that disaster into a sentence
// the user can act on. When the drive IS connected and only the folder was
// deleted — the actual reported case — the one-level Mkdir is exactly what
// unsticks them.
func ensureVaultDir(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is a file, not a folder", path)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return describePathProblem(path, err)
	}

	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return fmt.Errorf(
			"%s isn't there, and neither is the folder that should contain it (%s) — if your vault lives on a drive, connect it and try again",
			path, parent)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		return describePathProblem(path, err)
	}
	return nil
}

// ensureVaultWritable proves RESONANCE can actually write into dir, using
// the very same os.CreateTemp call writeFileAtomic uses for real writes —
// a permission or read-only-mount check built from mode bits or ACLs could
// disagree with the write that follows, and this one cannot. The .tmp-*
// prefix is deliberate: a crash mid-probe leaves a file indistinguishable
// from an interrupted atomic write, so it introduces no new kind of litter.
func ensureVaultWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return describePathProblem(dir, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// loadManifest distinguishes "the vault directory itself doesn't exist"
// (a real error — unmounted drive, stale saved path) from "the directory
// exists but has no manifest.json yet" (a fresh, valid, empty vault).
func loadManifest(vaultPath string) (Manifest, error) {
	if _, err := os.Stat(vaultPath); err != nil {
		return Manifest{}, describePathProblem(vaultPath, err)
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
	m.Apps = sanitizeManifestApps(m.Apps)
	return m, nil
}

// sanitizeManifestApps drops any app entry whose Name wouldn't pass
// validAppName, and collapses duplicate names (case-insensitive, matching
// AddApp's own uniqueness check) to their first occurrence. This is the one
// place that guarantee is established for every app a manifest could ever
// contain, no matter where the manifest.json came from — a manifest loaded
// from disk is untrusted input (a crafted or foreign manifest.json, e.g. one
// reachable via AdoptVaultPath), and every call site downstream builds a
// vault-side path via filepath.Join(vaultPath, app.Name, ...) trusting
// app.Name to be a single, safe path segment. Without this, a name like
// "../../../etc" would make that join escape the vault entirely, and a
// duplicate name would make two different app entries silently alias the
// same on-disk files.
func sanitizeManifestApps(apps []ManifestApp) []ManifestApp {
	seen := make(map[string]bool, len(apps))
	out := make([]ManifestApp, 0, len(apps))
	for _, app := range apps {
		if err := validAppName(app.Name); err != nil {
			continue
		}
		key := strings.ToLower(app.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		app.Dirs = sanitizeTrackedDirs(app.Dirs)
		out = append(out, app)
	}
	return out
}

// sanitizeTrackedDirs drops any tracked-folder entry that isn't a plain
// relative path staying inside $HOME, and collapses duplicates. Same
// reasoning as sanitizeManifestApps above: manifest.json is untrusted input,
// and every consumer of Dirs joins it onto $HOME and then walks it.
//
// This is a necessary check, not a sufficient one, and the distinction
// matters enough to state here: it is purely lexical, so it stops "../../etc"
// but cannot stop ".wine/dosdevices/z:/etc", where an intermediate component
// is a symlink to /. Nothing string-based can. The walk itself must resolve
// symlinks before trusting a path — see expandTrackedDir in drift.go.
func sanitizeTrackedDirs(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(d)))
		if clean == "" || clean == "." || clean == ".." {
			continue
		}
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "../") {
			continue
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// saveManifest never creates the vault directory itself — by the time this
// is called, loadManifest has already proven the directory exists.
// manifestMu serializes every read-modify-write of manifest.json, on the
// same reasoning that gave activity.json its activityMu (activity.go): the
// load-modify-save sequences below have no other coordination, and Wails
// dispatches bound calls concurrently. Two writers that both load the same
// manifest, each apply their own change, and each save would leave only the
// second change — with the first writer's already-copied bytes stranded in
// the vault as orphans nothing but ScanVaultOrphans can see.
//
// v1.2.2 is what makes this reachable in practice rather than in theory. The
// update-confirm overlay is dismissable, so a user can Escape out of an
// in-flight update of a large app, open the edit overlay, and save an edit
// while the update is still walking its file list.
//
// Every writer takes this around its whole load-modify-save. None of them
// calls another, so a plain mutex cannot deadlock here.
var manifestMu sync.Mutex

func saveManifest(vaultPath string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(vaultPath, manifestPath(vaultPath), data, 0644)
}

// writeFileAtomic writes data to path via a temp file in dir followed by a
// rename, so a crash or kill mid-write can never leave path holding a
// truncated or partial file — rename(2) is a single atomic step, unlike
// os.WriteFile's open(O_TRUNC)-then-write, which has a real window where a
// kill leaves path empty or half-written. The previous contents (or its
// absence) stay intact right up until the fully-written replacement lands.
func writeFileAtomic(dir, path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// relativeUnder converts an absolute path into one relative to base,
// rejecting anything that isn't actually under base.
//
// The name matters, and the old one was actively misleading. This is pure
// string work — filepath.Rel plus a ".." test, resolving nothing — and it
// answers "is X under Y" for two unrelated Ys. Twelve callers pass $HOME and
// are asking a scope question: may this file be backed up at all? Seven pass
// a vault directory and are asking a containment question: does this path
// stay inside the vault? It was called homeRelative until v1.3.0, which made
// the second group read like the first — and widening scope to /etc and /usr
// is precisely the change that would have widened vault containment too if
// the misnomer had survived into it.
func relativeUnder(absPath, base string) (string, error) {
	rel, err := filepath.Rel(base, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("outside your home folder")
	}
	return rel, nil
}

// systemRoots are the absolute roots outside $HOME that a backed-up file may
// live under, added in v1.3.0. Fixed rather than user-configurable: the ask
// was /etc and /usr specifically, and every additional root is one more place
// a mistaken click can reach.
//
// /run was considered and dropped. Its useful half — /run/media, where drives
// mount — is a vault location, which already works and is a different axis
// entirely; its other half is a tmpfs rebuilt from scratch at every boot, so
// anything backed up from it reads as missing on the next start, forever.
var systemRoots = []string{"/etc", "/usr"}

// systemVaultSegment is the reserved first path segment under which system
// files live inside an app's vault folder:
//
//	vault/<app>/.system/etc/alsa/alsa.conf
//
// It exists because /etc/alsa/alsa.conf and $HOME/etc/alsa/alsa.conf would
// otherwise want the same slot. Reserving a segment leaves every existing
// vault valid and needing no migration — nothing has ever written .system —
// at the cost of exactly one refusal: a home file whose own path starts with
// .system/ can't be tracked. validAppName's reserved "manifest.json" is the
// precedent for spending a name that way.
const systemVaultSegment = ".system"

type pathScope int

const (
	scopeHome pathScope = iota
	scopeSystem
)

// classifySource decides which allowed root an absolute source path belongs
// to, and returns the string the manifest stores for it.
//
// It returns a scope rather than normalising both cases into one string,
// because the two genuinely store different things. A home file is stored
// $HOME-relative so that a vault written by megas@mainrig restores for any
// user on any machine — CORE §4, and the entire answer to the username
// caveat. A system file has no per-user form to be relative to:
// /etc/alsa/alsa.conf is that same path on every machine, so storing it
// absolute is what keeps it portable rather than what breaks it.
func classifySource(absPath, home string) (pathScope, string, error) {
	if rel, err := relativeUnder(absPath, home); err == nil {
		relSlash := filepath.ToSlash(rel)
		if relSlash == systemVaultSegment || strings.HasPrefix(relSlash, systemVaultSegment+"/") {
			return 0, "", fmt.Errorf(
				"%s can't be tracked — RESONANCE stores system files under %s inside the vault, so that name is reserved",
				absPath, systemVaultSegment)
		}
		return scopeHome, relSlash, nil
	}
	for _, root := range systemRoots {
		if _, err := relativeUnder(absPath, root); err == nil {
			return scopeSystem, filepath.ToSlash(filepath.Clean(absPath)), nil
		}
	}
	return 0, "", outsideAllowedRoots(absPath)
}

// outsideAllowedRoots is the single sentence spoken wherever a path is
// refused for being outside every root RESONANCE reads from. One function so
// the three roots are never listed inconsistently in two different messages.
func outsideAllowedRoots(absPath string) error {
	return fmt.Errorf(
		"%s is outside your home folder, /etc and /usr — those are the places RESONANCE backs up",
		absPath)
}

// sourceAbs turns a stored manifest path back into the live path it names.
// This is the inverse of classifySource's second return value, and the reason
// $HOME is still resolved at read time rather than baked in: a home entry
// names whoever is running the app now, which is the whole portability model.
// A system entry is already absolute and names the same file everywhere.
func sourceAbs(home string, scope pathScope, stored string) string {
	if scope == scopeSystem {
		return filepath.FromSlash(stored)
	}
	return filepath.Join(home, filepath.FromSlash(stored))
}

// systemRootOf reports which system root an absolute path sits under.
//
// It exists so containment can be re-proved against the root that actually
// applies. Every guard in this program that resolves symlinks compares the
// result against a root; for home files that root is $HOME, and for system
// files it has to be /etc or /usr specifically. Comparing a resolved /etc
// path against $HOME would fail every time, and comparing it against "any
// allowed root" would let a symlink walk a file from /usr into /etc.
func systemRootOf(absPath string) (string, bool) {
	for _, root := range systemRoots {
		if _, err := relativeUnder(absPath, root); err == nil {
			return root, true
		}
	}
	return "", false
}

// sourceScopeRoot returns the resolved root a stored path must stay inside
// once symlinks are followed, paired with sourceAbs above.
func sourceScopeRoot(home string, scope pathScope, stored string) (string, error) {
	if scope != scopeSystem {
		return resolveDir(home), nil
	}
	root, ok := systemRootOf(filepath.FromSlash(stored))
	if !ok {
		return "", outsideAllowedRoots(stored)
	}
	return resolveDir(root), nil
}

// vaultRelFor is where a stored path's copy lives inside the app's vault
// folder. Home files keep sitting at their own relative path, exactly as
// every vault written before v1.3.0 has them; system files go under the
// reserved segment.
func vaultRelFor(scope pathScope, stored string) string {
	if scope == scopeSystem {
		return systemVaultRel(stored)
	}
	return stored
}

// systemVaultRel maps an absolute system path to its slash-separated place
// inside an app's vault folder. /etc/alsa/alsa.conf becomes
// .system/etc/alsa/alsa.conf.
func systemVaultRel(absPath string) string {
	clean := filepath.Clean(absPath)
	trimmed := strings.TrimPrefix(clean, string(filepath.Separator))
	return filepath.ToSlash(filepath.Join(systemVaultSegment, trimmed))
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

// copyFileAtomic copies src to dst the same way copyFile does, but lands the
// bytes via a temp file in dst's directory followed by a rename rather than
// truncating dst in place. Two properties fall out of that: a crash
// mid-copy can never leave dst holding truncated/partial bytes (the old
// content, if any, is untouched until the very last step), and if dst is
// currently a symlink, the rename replaces the symlink itself rather than
// following it to write through to whatever it points at — rename(2) never
// dereferences its destination path. Used wherever dst could have been
// planted by whoever last had write access to it (an adopted or synced
// vault, not necessarily this machine's own session), rather than by this
// process moments earlier.
func copyFileAtomic(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(src + " is not a regular file")
	}
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(dstDir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmpPath, dst)
}

// refuseSymlink errors if a symlink sits at path, without following it —
// Lstat reports the symlink itself, unlike Stat. Used before reading file
// content from a path that could have been planted by whoever last had
// write access to it: opening straight through such a symlink would
// silently disclose whatever it points at, including files far outside the
// vault entirely. A path with nothing there yet, or a real file, passes
// through unchanged — the Stat/Open that follows reports whatever error is
// actually appropriate.
func refuseSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New(path + " is a symlink — refusing to read through it")
	}
	return nil
}

// refuseSymlinkedParents refuses if any INTERMEDIATE component of rel below
// base is a symlink. refuseSymlink guards the leaf; this guards the
// directories leading to it.
//
// It exists for deletion specifically, and it is deliberately stricter than
// vaultDirEscapes (drift.go), which asks only whether a path resolves outside
// the vault. That question is the right one for reading and writing a
// tracked file, but not for unlinking one: a symlink planted at
// <vault>/<app>/.config pointing at <vault>/otherapp resolves *inside* the
// vault and so passes vaultDirEscapes cleanly, while os.Remove of the
// manifest-listed ".config/init.lua" would delete another app's backup. One
// pointing at $HOME/.config would delete the user's live config — the single
// thing removal must never do. unlink(2) declines to follow a symlink only at
// the FINAL component; every directory above it is resolved normally, and
// filepath.Join with relativeUnder is purely lexical, so neither notices.
//
// The leaf is deliberately NOT checked: os.Remove on a symlink unlinks the
// link itself without dereferencing it, which is exactly what should happen
// to a hostile planting — it gets cleaned out of the vault and whatever it
// pointed at is untouched.
//
// A missing intermediate is not an error. There is then nothing to delete,
// and os.Remove reports that far more precisely than a guess here could.
func refuseSymlinkedParents(base, rel string) error {
	// The app directory itself is an intermediate too, and it is the one that
	// matters most: for a single-segment rel like ".bashrc" the loop below has
	// no intermediates to walk at all, so without this check the function
	// would return nil having checked nothing. A vault carrying
	// <vault>/evil -> $HOME plus a manifest entry naming ".bashrc" would then
	// unlink the user's live ~/.bashrc, which is the exact deletion this
	// whole function exists to refuse.
	//
	// Note this stops at base and never inspects the vault root above it: a
	// user whose configured vault path is itself a symlink has an ordinary,
	// valid setup, and removal must keep working for them.
	if info, err := os.Lstat(base); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New(base + " is a symlink — refusing to delete through it")
	}
	return refuseSymlinkedIntermediates(base, rel)
}

// refuseSymlinkedIntermediates is refuseSymlinkedParents without the check on
// base itself. It exists for the one caller whose base IS the user-configured
// vault root — orphan removal, whose paths are vault-relative rather than
// app-relative. There, base being a symlink is the ordinary valid setup the
// comment above describes, while every directory inside the vault is
// plantable and must still be refused.
func refuseSymlinkedIntermediates(base, rel string) error {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	cur := base
	for _, seg := range parts[:max(len(parts)-1, 0)] {
		if seg == "" {
			continue
		}
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New(cur + " is a symlink — refusing to delete through it")
		}
	}
	return nil
}
