# RESONANCE — STEP5.md → v0.5.0, the last STEP before v1.0.0

**Goal:** close out the feature set. A restore can be undone. Every
JADEITE palette ships, not just the two RESONANCE started with. The
status bar finally has data to show. And for the first time, RESONANCE
can be packaged and installed like a real Linux app — `.deb` and Arch,
built by a CI pipeline that's dry-run-verified before it's ever asked to
do it for real. This document's checklist is green as of local
verification below; committed locally as v0.5.0 — the v1.0.0 cut itself
is a separate, later, maker-initiated step, not part of this one.

Location: `/home/megas/RESONANCE/build/docs/STEP5.md`
Approved by maker: sudo-megas, 2026-08-12

---

## 0. BEFORE STARTING

No new Go dependency this step — stdlib only (`os`, `path/filepath`,
`encoding/json`), reusing `copyFile`/`homeRelative`/`fileDriftRow`
exactly as prior STEPs did. Palette additions are two-file changes
(`theme.css` + `theme-picker.ts`), per `theme.css`'s own header
invariant. Packaging is the first STEP where CI tooling extends past
Go+Node — an Arch container for `makepkg` — though nothing new ships
inside the app itself.

**`wails generate module` gotcha, same shape as every prior STEP's:**
`GetUndoInfo`/`UndoRestore` needed one `-tags webkit2_41` regen before
TypeScript could import them.

**A CORE.md §3 question surfaced mid-implementation.** §3 mandates a
literal, non-locale-dependent `DD MM YYYY` date format for the mirror's
own per-file rows. The undo overlay and the new status bar both needed
to show a timestamp too, and a bare calendar date can't distinguish "5
minutes ago" from "20 hours ago" for same-day events — exactly the case
an undo snapshot usually is. Asked the maker directly rather than
guessing; the answer was a third option neither the plan nor I had
proposed: keep the literal, non-relative convention `formatDate` already
established, just extend it with a time-of-day component
(`dates.ts:formatDateTime`, `DD MM YYYY HH:MM`). Both new surfaces use
it — one consistent date convention across the whole app, no relative-
time phrasing anywhere.

**A real PKGBUILD bug, caught by actually running `makepkg` rather than
trusting the design.** The first draft referenced the pre-built binary
and icons via relative paths (`../bin/resonance`, `../icons/32.png`)
inside PKGBUILD's `source=()` array, on the assumption makepkg would
resolve them like any other local file. It doesn't — makepkg's local-
source resolution only looks up bare basenames inside the PKGBUILD's own
directory, never arbitrary relative paths. `makepkg` failed immediately
with "resonance was not found in the build directory." Fixed by dropping
`source=()`/`sha256sums=()` entirely and reading everything directly via
`${startdir}/../...` inside `package()` — the standard pattern for
binary-repackaging PKGBUILDs. Verified by re-running `makepkg` after the
fix: clean build, and the resulting binary — extracted straight from the
`.pkg.tar.zst`, no `pacman -U` install — launched for real (WebKit
initialized, process stayed alive) rather than just passing a structural
check.

**Icons were untracked, not committed.** The maker placed nine staged
PNG sizes at `build/icons/` during planning, but they'd never been
`git add`ed — invisible to this worktree and to CI until committed
alongside this STEP's other changes. Copied in from the main checkout
and committed here; see §4.

**This is the last STEP.** When this checklist is green and reviewed,
`master` gets pushed once, deliberately, to dry-run the release pipeline
(§4) — an approved, logged, one-time exception to "local until v1.0.0."
The *real* v1.0.0 push + tag + release is a separate action the maker
triggers later, possibly a different day — not something this STEP's
own completion does automatically.

---

## 1. PRE-RESTORE SNAPSHOT + UNDO

Before `RestoreApp` overwrites or creates a live file, its current state
is captured so the whole restore can be undone with one button.

```go
// snapshot.go
type SnapshotEntry struct {
    Path       string `json:"path"`
    Kind       string `json:"kind"` // "absent" | "regular" | "symlink"
    LinkTarget string `json:"linkTarget,omitempty"`
}
type RestoreSnapshot struct {
    App       string          `json:"app"`
    CreatedAt string          `json:"createdAt"`
    Entries   []SnapshotEntry `json:"entries"`
}
type UndoInfo struct {
    Available bool   `json:"available"`
    CreatedAt string `json:"createdAt"`
    FileCount int    `json:"fileCount"`
}
type UndoResult struct {
    Restored []string         `json:"restored"`
    Failed   []RestoreFailure `json:"failed"`
}

func undoRootDir() (string, error)   // $XDG_STATE_HOME/resonance/undo,
                                      // fallback ~/.local/state/resonance/undo
func captureEntry(pendingDir, relPath, destPath string) (SnapshotEntry, error)
func writeSnapshot(dir string, snap RestoreSnapshot) error
func readSnapshot(dir string) (RestoreSnapshot, bool)
func commitSnapshot(pendingDir, canonicalDir string, snap RestoreSnapshot) error

func (a *App) GetUndoInfo(appName string) (UndoInfo, error)
func (a *App) UndoRestore(appName string) (UndoResult, error)
```

Storage lives entirely outside the vault, at `undoRootDir()/<appName>/` —
an OS-standard per-machine state directory, not `~/.cache` (a cache is
"safe to lose"; these bytes exist nowhere else) and not inside the vault
(undo describes a mutation to *this machine's* `$HOME`, not a portable
property of the vault). `manifest.json` is never touched by any of this.

**The crash-safety design.** The obvious version — clear the previous
snapshot, then write the new one in place as you go — has a real failure
mode: a disk-full write tearing both the live-file copy *and* the final
snapshot write leaves zero valid undo state, in exactly the scenario undo
exists to protect against. Fixed with stage-then-atomic-commit:
`captureEntry` writes into a **pending** directory
(`undoRootDir()/<app>.pending/`), never the canonical one, for the entire
restore loop. Only after the loop, and only if at least one file was
captured, does `commitSnapshot` write `snapshot.json` into the pending
directory and — *only once that succeeds* — `os.RemoveAll` the old
canonical directory and `os.Rename` the pending directory into its
place. A fully no-op restore (every file already `"ok"`) never calls
`commitSnapshot` at all, which is what makes "keep 1 per app" retention
work without a separate prune step. If `writeSnapshot` itself fails, the
pending directory is simply abandoned — cleaned up opportunistically at
the start of the next `RestoreApp` call — and the old snapshot survives
completely untouched.

Verified with a real regression test (`TestCommitSnapshot_
OldSnapshotSurvivesWriteFailure`): sabotage the pending write so it
fails, confirm the pre-existing snapshot is still readable and unchanged
afterward. Eleven other tests in `snapshot_test.go` cover the three
capture kinds, fail-closed behavior when undo storage itself is
unavailable, a full no-op restore leaving an existing snapshot untouched,
`UndoRestore`'s round trip across all three kinds (including a real
symlink), and `UndoRestore` re-validating every path through
`homeRelative` before any write — a hand-crafted hostile snapshot entry
(`"../../etc/hostile"`) is rejected and lands in `Failed` without
touching the legitimate entry alongside it.

**A subtlety beyond the plan's own wording, found while implementing.**
"Fail-closed" only fully protects the file if the snapshot entry is
recorded the moment capture succeeds — *before* the subsequent
`removeSymlinkAt`/`copyFile` mutation is attempted, not after it
succeeds. If capture succeeds but the mutation partway fails (symlink
removed, then the copy itself errors), the live file has already changed
even though the overall restore reports that file as `Failed` — so its
prior state still has to be in the snapshot. `RestoreApp` records the
entry right after capture, unconditionally; over-including a file whose
mutation never actually happened is harmless (undo just no-ops on it),
but under-including one that did change would be a real bug.

**Retention: keep 1 per app** (maker-confirmed) — exactly what the
commit step above produces, no separate prune logic exists to get wrong.
**Fail-closed on capture failure** (maker-confirmed): if a file can't be
snapshotted, it isn't restored either — it lands in `RestoreResult.
Failed`, restorable later, matching CORE.md §4's "tucked aside
automatically" as a precondition rather than best-effort.

**UI.** `openRestoreConfirm`'s `!row.drifted` early-return now calls
`GetUndoInfo(row.name)` first. If available, the overlay still opens, in
a reduced mode — no New/Overwrite list, just "Nothing to restore. Undo
restore from `DD MM YYYY HH:MM` (N files)?" (see §0's date-format note)
plus the same checkbox-gate + commit-button pattern restore itself uses,
wired to `UndoRestore`. If undo isn't available either, the toast fires
exactly as before. Undo overwrites live files too, so it earns the same
"this touches your system" friction restore already has — never a bare
one-click.

---

## 2. THE FULL JADEITE PALETTE SET

JADEITE's real palette source (maker-provided, read directly rather than
guessed): a mounted drive at
`DESKAPPS/JADEITE/src/shared/theme/palettes/` — TypeScript, not CSS,
defining an 18-token `PaletteTokens` contract across ten palettes (six
dark, four light).

RESONANCE already shipped `default-dark` (a direct port) and its own
exclusive `ubuntu-aubergine`. **The remaining nine** now ship too:
`default-light`, `noctalia`, `catppuccin-latte`, `catppuccin-frappe`,
`catppuccin-macchiato`, `catppuccin-mocha`, `rose-pine-dawn`, `nord`,
`kanagawa-lotus` — eleven palettes total.

**Token-mapping formula**, verified byte-for-byte against the already-
shipped `default-dark` block and confirmed to hold for `default-light`
too (the light-mode risk the planning critique flagged: `--overlay-
scrim`'s darkening effect needs `surfaceSunken` relatively darker than
`surface`, which JADEITE's elevation model guarantees in every mode):

| RESONANCE token | JADEITE token |
|---|---|
| `--bg` | `surface` |
| `--pane` | `surfaceRaised` |
| `--spine`, `--overlay-panel` | `surfaceOverlay` |
| `--text` | `text` |
| `--text-dim` | `textMuted` |
| `--accent` | `accent` |
| `--accent-2` | `info` |
| `--danger` | `danger` |
| `--badge-drift` | `warning` |
| `--border` | `border` |
| `--border-strong` | `borderStrong` |
| `--focus-ring` | `focusRing` |
| `--overlay-scrim` | `rgba(`surfaceSunken as rgb`, 0.72)` |
| `--diff-add` | `success` |
| `--diff-add-bg` | `rgba(`success as rgb`, 0.14)` |
| `--diff-remove-bg` | `rgba(`danger as rgb`, 0.14)` |

Four JADEITE tokens (`textOnAccent`, `textSubtle`, `accentHover`,
`selection`) have no RESONANCE equivalent and are dropped, as
`default-dark` already did. `default-dark` and `ubuntu-aubergine` are
**not** retroactively changed — out of scope, no reported problem with
either. All nine new `[data-theme="..."]` blocks live in `theme.css`
only; `theme-picker.ts`'s `THEMES` array gains nine entries in JADEITE's
own presentation order and display names, diacritics included
("Catppuccin Frappé", "Rosé Pine Dawn"). No other file changed —
confirmed by spot-checking `layout.css`/`overlay.css` for hardcoded
colors that might assume a dark background: the only non-token color in
either file is a conventional black `box-shadow`, which reads correctly
on light palettes too.

---

## 3. POLISH

**Status bar** ships this STEP, reading entirely from data already
returned by `GetMirrorRows()` plus the vault path from the already-
existing `GetSettings()` — zero new Go calls. Content: total apps
tracked, count currently drifted, active vault path, and "last activity"
— the most recent timestamp across every file's `SourceModified` or
`VaultModified`. `VaultModified` only moves on a backup (`AddApp`/
`UpdateFromSource` stamp it; `RestoreApp` never touches `manifest.json`);
`SourceModified` moves on any live write, restores included (`copyFile`
never calls `os.Chtimes`, so a restore's write genuinely becomes the new
mtime). Taking the max of both is the closest "backup or restore,
whichever was more recent" reading obtainable without a new backend
call. Same `DD MM YYYY HH:MM` format as the undo overlay (§0).

**Everything else on the STEP1–4 deferred-items backlog stays out of
STEP5**, decided explicitly rather than silently dropped: bulk "Restore
All" (STEP4's own "if ever" hedge stands, and it would need its own
bulk-undo counterpart to a currently per-app `UndoRestore`); per-file
restore/update granularity, either direction; the mtime+size skip-cache.
The supplementary non-Nerd-Font icon bundle is resolved, not deferred —
all four in-app icons already use real PUA codepoints with no fallback
needed. The creation-dates limitation (CORE.md §4 literally asks for
"both files' created/modified dates"; ext4/Linux has no reliable stdlib
creation-time API, so STEP3 permanently uses modified-time on both
sides) gets one explicit README line — a permanent, already-shipped
limitation doesn't need new in-app UI to announce itself.

---

## 4. PACKAGING + THE v1.0.0 RELEASE PIPELINE

Deliverables per CORE.md §7: `.deb` and Arch (`.pkg.tar.zst`) only — no
Windows, AppImage, Flatpak, Snap, or AUR publishing (AUR needs ongoing
maintenance nothing should depend on after the maker leaves the
industry).

**Mechanism (maker-chosen over the smaller `nfpm` alternative):
container-based, per-distro tooling**, four jobs in
`.github/workflows/release.yml`:
- `build` — `ubuntu-24.04`, native. `wails build -tags webkit2_41
  -platform linux/amd64` (its `frontend:install` hook is now `npm ci`,
  not `npm install` — `wails.json` changed project-wide, not just in
  CI, since a lockfile is present either way) produces the binary,
  uploaded as an artifact.
- `package-deb` — `ubuntu-24.04`, native (already Debian-family, no
  container needed). Downloads the binary, assembles a `DEBIAN/control`
  + FHS tree by hand, `dpkg-deb --build` produces the `.deb`.
- `package-arch` — `container: archlinux:base-devel`. Downloads the
  binary, stages it alongside the checked-in `PKGBUILD`, `makepkg`
  (as a throwaway non-root `builder` user — makepkg refuses to run as
  root, and nothing here needs `--syncdeps`/sudo since it's packaging
  an already-built binary, not compiling) produces the `.pkg.tar.zst`.
- `release` — depends on both packaging jobs. On a real tag push, `gh
  release create` uploads both artifacts, `--prerelease` unless the tag
  is exactly `v1.0.0` (maker-specified — v1.0.0 is the only tag this
  project ever publishes as a normal, full release). On
  `workflow_dispatch`, this entire job is skipped via `if:
  github.event_name == 'push'` — `build` and both packaging jobs still
  run and prove themselves on real GitHub infrastructure, but nothing
  ever reaches GitHub Releases.

**Pinning.** Every third-party Action pinned to a full commit SHA,
resolved by hand against each action's real tag history at write time
(`git ls-remote --tags`), never written from memory or left as a
floating major-version tag: `actions/checkout` v7.0.1, `actions/setup-
go` v7.0.0, `actions/setup-node` v7.0.0, `actions/upload-artifact`
v7.0.1, `actions/download-artifact` v8.0.1. Go and the Wails CLI pinned
to `go.mod`'s exact versions (1.25.0 / v2.14.0). The one unavoidable
unpinned surface: the `apt-get install` call for `libgtk-3-dev`/
`libwebkit2gtk-4.1-dev` has no SHA-pin equivalent.

**Version derivation.** `control`'s `Version:` and `PKGBUILD`'s
`pkgver=` are checked in with a placeholder matching this STEP's own
release (0.5.0); each packaging job overwrites it via `sed` from
`${GITHUB_REF_NAME#v}` on a real tag push, or `0.0.0-dev` on a
`workflow_dispatch` dry-run — the checked-in files never need editing
for a future patch release.

**Local verification, maker-directed.** Real `makepkg` was run against
the finished `PKGBUILD` on the maker's own Arch machine (see §0's bug
writeup) — clean build, and the resulting binary, extracted straight
from the archive, launched for real. The `.deb` side's file-tree
staging (paths, `sed` version substitution) was verified by hand the
same way; the maker explicitly decided **the actual `.deb` build itself,
and every package either format ever ships, must come from GitHub
Actions CI, never a local `dpkg-deb`/`makepkg` run** — this machine
lacks `dpkg-deb` and there's no container runtime for a Debian
environment, and more importantly, CI-only production keeps every
shipped artifact machine-agnostic and reproducible rather than tied to
whatever happens to be on one contributor's box. Recorded as a standing
policy, not a one-off for this STEP.

**Icons.** `build/icons/`'s nine maker-placed PNG sizes (32–4096px) were
untracked until this STEP (see §0) — now committed. Sizes 32 through 512
are wired into both packages' `hicolor` icon theme install; 1024–4096
stay staged but unconsumed, not an error, just unneeded by standard
Linux icon-theme installs.

A real gap surfaced later, caught only by actually asking "is the icon
showing up" and auditing every surface it should appear on: the packaged
`hicolor` install above was correct, but the icon had never been wired
into the *app itself*. `build/appicon.png` was still Wails' stock "W"
placeholder — never swapped for the real logo despite `build/icons/`
landing — and `main.go` never set `options.App.Linux.Icon` at all, so
the running window had no icon (titlebar/taskbar showed nothing custom,
even on a packaged install run outside its window-manager's desktop-file
resolution). The in-app "about" mark and topbar mark were the CSS-drawn
placeholder rings `layout.css` had shipped since STEP1, whose own comment
said they were standing in "until [the PNG] lands at build/icons/" — it
landed, but nobody came back to do the swap. Fixed: `build/appicon.png`
replaced with the real 1024px logo (Wails' own convention slot);
`main.go` now embeds `build/icons/128.png` and sets
`Linux: &linux.Options{Icon: icon, ProgramName: "resonance"}`; both
`.logo-mark` (topbar) and `.logo-mark--about` now render the same real
128px PNG via `background: url(...) center/contain`, replacing the ring
placeholder entirely.

One non-obvious sizing bug found while fixing this, worth recording so
it isn't rediscovered the hard way: feeding `Linux.Icon` the full 1024px
master (matching the Mac convention, by direct analogy) *silently*
produces a window with no `_NET_WM_ICON` at all — confirmed via `xprop`,
comparing the 1024px and 128px builds side by side. GTK still emits a
legacy `WM_HINTS` icon pixmap either way, so the failure isn't visible
without checking the modern property directly; a window manager reading
only `_NET_WM_ICON` (as most do today) would show no icon and nothing
in the build would say so. 128px, matching the size already used for the
in-app mark, works correctly and is what's shipped.

Also recorded plainly rather than glossed over: `Linux.Icon` only
reaches windows running under X11 or XWayland — confirmed by testing
under `GDK_BACKEND=x11`, the only way to make this webkit2gtk app
visible to X11 tooling at all on this machine's native-Wayland KDE
Plasma session. Native Wayland has no per-window icon protocol; a
compositor resolves an app's icon from its `app_id` through the
installed `.desktop` file to the `hicolor` theme, which is exactly the
install path already verified above. So the packaged, installed app was
already getting a correct icon on Wayland before this fix — this fix's
real effect is the *unpackaged* app (dev runs, a bare extracted binary)
and any X11/XWayland session, which previously had no icon at all.

**Maintainer identity**, corrected during implementation: the plan
assumed the session's own account email; the project's real git identity
(every prior commit, `git config user.email`) is
`sudo-megas@users.noreply.github.com`, used consistently in `wails.
json`'s `author` block, `control`'s `Maintainer:`, and `PKGBUILD`'s
maintainer comment — also more appropriate for public package metadata
than a personal address.

**Explicit stop line.** This STEP's packaging/CI work ends at "the
pipeline exists, is reviewed, and is verified as far as it safely can be
without touching shared infrastructure." It does not push `master`, tag
v1.0.0, or fire the real release — those are separate, later,
maker-initiated actions using the pipeline this STEP built.

---

## 5. DEFINITION OF DONE — v0.5.0 checklist

- [x] `SnapshotEntry`/`RestoreSnapshot` capture regular files, symlinks
      (by target, not content), and absent-destination cases correctly
- [x] Snapshot capture writes into a pending directory; the canonical
      snapshot is only replaced after `writeSnapshot` fully succeeds —
      regression test confirms the old snapshot survives a forced
      write failure
- [x] A fully no-op restore never touches an existing snapshot
- [x] Snapshot-capture failure is fail-closed: that file is not
      restored, lands in `Failed`
- [x] `UndoRestore` re-validates every path via `homeRelative` before
      any write; never trusts `snapshot.json` blindly
- [x] `UndoRestore` is per-file-independent; full success clears the
      snapshot, partial failure leaves it in place
- [x] `GetUndoInfo`/`UndoRestore` never touch `manifest.json`
- [x] `openRestoreConfirm`'s early-return checks `GetUndoInfo` before
      falling back to the "nothing to restore" toast
- [x] Undo reuses the checkbox-gate pattern; no bare one-click undo
- [x] Undo storage lives at `$XDG_STATE_HOME/resonance/undo` (fallback
      `~/.local/state/resonance/undo`), never inside the vault
- [x] All nine new palette blocks match the verified mapping formula —
      cross-checked against `default-dark`/`default-light`
- [x] `theme.css`'s "no other CSS file needs to change" invariant
      holds — `layout.css`/`overlay.css` spot-checked, only non-token
      color is a theme-agnostic drop shadow
- [x] `theme-picker.ts`'s `THEMES` array carries all eleven palettes;
      `openThemePicker()`/`.theme-grid` needed no structural change
      (already array-driven and `auto-fit`) — **visual confirmation
      that all eleven render and preview correctly, and that switching
      persists across a relaunch, needs the maker's own eyes**; no
      GUI-automation tool was available this session to click through
      it directly, only `wails dev`'s clean compile/console output
- [x] Status bar renders from existing `GetMirrorRows()`/`GetSettings()`
      data, zero new Go calls — **same visual-confirmation caveat**
- [x] `.desktop` file, Debian `control`, `PKGBUILD` all exist and are
      correct (Maintainer field filled, description present)
- [x] `build/icons/`'s 32–512px sizes wired into both packages;
      1024–4096 intentionally left unconsumed
- [x] The real logo is wired into the app itself, not just the packages:
      `build/appicon.png` replaced (was Wails' stock placeholder),
      `main.go` sets `Linux.Icon`/`ProgramName`, topbar/about marks
      render the real PNG in place of the STEP1 CSS-ring placeholder —
      confirmed via `xprop`'s `_NET_WM_ICON` and a window screenshot
      under `GDK_BACKEND=x11`; the about-panel's click-through visual
      still needs the maker's own eyes (no working input-synthesis path
      this session — see the theme-picker caveat above)
- [x] Local `makepkg` build succeeds; the resulting package's binary
      installs (extracted) and **launches for real**, not just passes
      structural checks — `.deb` build itself deliberately deferred to
      CI per the maker's standing policy (§4)
- [x] CI workflow: every third-party Action pinned to a full commit
      SHA (resolved live via `git ls-remote`, not memorized); Go/Wails
      CLI pinned to `go.mod`'s exact versions; `npm ci` not
      `npm install`
- [x] `release` job's tag logic is reviewable in isolation:
      `--prerelease` for every tag except exactly `v1.0.0`
- [x] `workflow_dispatch` dry-run succeeds on real GitHub Actions
      infrastructure — run for real once the maker began the actual
      release process (see the addendum below); `build`, `package-deb`,
      and `package-arch` all pass and `release` correctly no-ops
- [x] No push, tag, or `gh release create` for the real v1.0.0 happens
      as part of this STEP's work
- [x] README.md gets one explicit line on the creation-dates limitation
- [x] `version.ts` bumped to v0.5.0 with the real date
- [x] Committed locally, no AI trailers — the one approved early push
      (§4) is `master` only, for the dry-run; nothing else pushes or
      tags

## 6.5. ADDENDUM — the real release, 2026-08-12

Everything above was written and checked off before any of this STEP's
work reached `master`. The maker started the actual release process the
same day: `master` fast-forwarded to this STEP's work, the
`workflow_dispatch` dry run finally run for real, and `v0.2.0` through
`v0.5.0` tagged and published (the three pre-STEP5 versions manually, as
plain notes-only pre-releases — their commits predate this pipeline
entirely, so tagging them triggers no CI run at all, silently; `v0.5.0`
for real, through the pipeline itself). Recorded here rather than only
in commit history because two of the three bugs the dry run caught are
exactly the kind of thing worth knowing about *before* reading this
pipeline's YAML and assuming it was already proven correct:

1. **Arch's `pkgver` may not contain hyphens.** `workflow_dispatch`'s
   fallback version string was `0.0.0-dev` — `makepkg` rejected it
   outright. Real tag pushes were never affected (`0.5.0`-style values
   have no hyphen), but every dry run failed here until fixed.
2. **`makepkg`'s dependency check needs `--nodeps`.** The build
   container's pacman database is never synced, so `webkit2gtk-4.1`/
   `gtk3` can't resolve — even though this PKGBUILD only repackages an
   already-built binary and needs neither library present to do it.
   This one *did* affect real releases too, not just the dry run; it
   passed on the maker's own machine only because those libraries
   happen to already be installed there.
3. **The `release` job had no `actions/checkout` step at all.**
   `gh release create --generate-notes` needs a git repo to work from;
   without one it fails with `fatal: not a git repository`. This
   couldn't be caught by any dry run — `release` is unconditionally
   skipped under `workflow_dispatch` by design — so it only surfaced on
   the first real tag push. `v0.5.0`'s tag was deleted and recreated
   once fixed, since the first attempt published nothing.

All three are fixed and merged; `v0.5.0`'s real release
(https://github.com/sudo-megas/RESONANCE/releases/tag/v0.5.0) is the
first artifact this pipeline ever actually produced. The real `v1.0.0`
cut — the one action this STEP's own stop line explicitly deferred — is
the maker-initiated action this addendum's date belongs to.

---

## 6. EXPLICITLY OUT OF STEP5

Bulk "Restore All" (permanently deferred, not revisited — see §3).
Per-file restore/update granularity, either direction. The mtime+size
skip-cache. Redo (undoing an undo). AUR publishing or any packaging
target beyond `.deb`/Arch. New in-app UI for the creation-dates
limitation (README only). And — stated once more because it's the one
easiest to get wrong by momentum — **the real v1.0.0 push, tag, and
release themselves.** That is a separate action, triggered by the maker,
whenever they're ready, using the pipeline this STEP built.

---

Copyright © sudo-megas
*Built with Reason and Passion.*
