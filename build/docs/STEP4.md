# RESONANCE — STEP4.md → v0.4.0 "The Return"

**Goal:** the vault learns to give back. Every app can be restored from
the vault onto the live system — previewed first (what's new, what gets
overwritten, what's already identical), with a real content diff on
anything that would be overwritten, and a machine-info card showing what
system the vault was last written from. `$HOME` remap falls out of
machinery STEP1/STEP2 already built — nothing new there. No bulk restore,
no pre-restore snapshot/undo, no status bar yet — those stay STEP4-minus
and STEP5, respectively. When this document's checklist is green, commit
locally as v0.4.0 — per CORE.md §8, nothing pushes or tags until v1.0.0.

Location: `/home/megas/RESONANCE/build/docs/STEP4.md`
Approved by maker: sudo-megas, 2026-08-11

---

## 0. BEFORE STARTING

No new dependency this step either. Machine info is captured with pure
stdlib: `syscall.Utsname` (kernel), a plain read of `/etc/os-release`
(distro name), `os.Hostname()`, `os/user.Current()` (username).

**`Utsname` gotcha:** `syscall.Utsname`'s byte-array fields are typed
`[65]int8` on some architectures and `[65]uint8` on others — a small
generic helper (`utsnameToString[T int8 | uint8]`) turns either into a Go
`string` so this compiles portably.

**Machine info cannot be backfilled.** Unlike STEP3's checksums (which
could be computed retroactively from bytes the vault already had),
machine info describes *the machine that performed the backup* — that
fact was never captured before this STEP and cannot be reconstructed
after it. A vault last touched by STEP2/STEP3 code simply has no machine
info until the next `AddApp` or `UpdateFromSource` call stamps one. This
is expected, not a bug — the machine-info card renders an "unknown" state
for it, nothing crashes.

**Restore is the first STEP to write onto the live system.** Every prior
STEP only ever wrote into the vault (or, for migration, vault-to-vault).
This is *why* §5's two safety guards exist — no earlier STEP needed them,
because nothing earlier could put a byte outside a path the user
themselves chose as the vault destination.

**`copyFile` already creates parent directories.** `copyFile` (from
STEP2's `manifest.go`) calls `os.MkdirAll(filepath.Dir(dst), 0755)`
internally before writing — restoring into an app folder that no longer
exists on the live system needed zero extra code for this; it was already
handled by the primitive restore reuses.

---

## 1. MACHINE INFO

```go
// machineinfo.go
type MachineInfo struct {
    Kernel   string `json:"kernel"`   // e.g. "Linux 6.9.3-zen1"
    OS       string `json:"os"`       // /etc/os-release PRETTY_NAME
    Hostname string `json:"hostname"`
    Username string `json:"username"`
}
func captureMachineInfo() MachineInfo { ... } // best-effort; any single
    // field that fails to resolve is left as "" rather than aborting
    // the whole capture — a card with 3 known fields beats no card.
func (a *App) GetMachineInfo() (MachineInfo, error)
```

Stored as `Manifest.MachineInfo`, additive to the existing schema exactly
as STEP3's checksum fields were — `manifest.json`'s `"version"` stays
`1`. **Stamped by both `AddApp` and `UpdateFromSource`** — both are
"write into the vault" actions; either one re-establishes "this is the
machine this vault currently reflects." Last-write-wins if a vault is
shared across two machines; no merge logic — a vault has one current
machine-info snapshot, not a history.

`GetMachineInfo()` is a small standalone call, not folded into
`GetMirrorRows()` — it reads one manifest field, is needed once per
restore-preview open, and keeping it separate avoids reshaping
`GetMirrorRows()`'s already-shipped, already-consumed return type.

---

## 2. RESTORE PREVIEW — NO NEW COMPARISON LOGIC

The insight that makes this STEP smaller than it looks: **restore's
three states are the exact same three states `GetMirrorRows()` already
computes**, just read in the opposite direction.

`fileDriftRow` compares the live (`$HOME`) file against the vault's
stored checksum. That is *precisely* the comparison restore also needs —
restore only cares whether the live file matches the vault, not which
direction the last write happened to go. So:

| `FileRow.State` (as STEP3 built it) | Restore-preview meaning |
|---|---|
| `"missing"` (live file absent) | **New** — nothing exists at the destination yet; restore creates it |
| `"drifted"` (live file differs) | **Overwrite** — restoring replaces the live file's content with the vault's |
| `"ok"` (live file matches) | **Skip** — identical, restoring would be a no-op |

This relabeling happens entirely in TypeScript (`restore.ts`'s
`classify()`), from the same `AppRow` already sitting in `rows.ts`'s
`lastRows` — opening the restore-preview overlay for a row makes **zero**
additional Go calls beyond `GetMachineInfo()`. No new Go type, no new
comparison function.

The itemized preview list shows only **New** and **Overwrite** rows —
the ones an action will actually happen to — with a one-line summary
count for Skipped ("N unchanged"). This matches the drift badge's own
established philosophy: ambient signal, no noise for what's already fine.

The restore trigger is gated the same way the update trigger already is:
if `!row.drifted`, clicking "← Restore" shows a toast ("already up to
date — nothing to restore") instead of opening the overlay, same guard
`openUpdateConfirm` already uses.

---

## 3. THE DIFF VIEW

```go
// restore.go
type FileContents struct {
    Text     string `json:"text"`
    Binary   bool   `json:"binary"`
    TooLarge bool   `json:"tooLarge"`
    Missing  bool   `json:"missing"`
    Size     int64  `json:"size"` // populated whenever the file exists,
                                  // even if TooLarge or Binary — lets the
                                  // "too large to diff" fallback show a
                                  // real byte count instead of just "no".
}
type DiffPair struct {
    Live  FileContents `json:"live"`
    Vault FileContents `json:"vault"`
}
func (a *App) GetDiffPair(appName, relPath string) (DiffPair, error)
```

Server-side gating before any file content crosses the Wails IPC
boundary: a 1 MiB size cap and a 20,000-line cap (`TooLarge: true`,
content omitted past either), and binary detection (NUL byte or invalid
UTF-8 anywhere in the content → `Binary: true`, content omitted).
`GetDiffPair` re-validates both `appName` (checked against the manifest's
own app list) and the resolved path (via `homeRelative`, same guard §5
describes) before reading anything — it reads real files from an
app-name/path pair supplied across the IPC boundary, the same trust
boundary restore itself has to defend.

Only **Overwrite**-state rows in the preview are expandable to an inline
diff — New rows have nothing on the live side to diff against (nothing
there yet), Skip rows are identical by definition. Expanding an
Overwrite row lazily calls `GetDiffPair` on first expand only (not
prefetched for the whole app) and renders the result with a hand-rolled
line diff in `diff.ts` — no new dependency, matching this project's
established zero-dependency-where-avoidable posture. Common prefix/suffix
lines are trimmed first (collapsing a typical single-line dotfile edit to
almost nothing), then the remaining middle section runs through a
longest-common-subsequence table to produce the minimal add/remove
script. Binary or too-large pairs render a plain fallback line ("binary
file — no diff shown" / "file too large to diff (N KB)") instead of
attempting to diff.

---

## 4. RESTORE

```go
// restore.go
type RestoreFailure struct {
    Path   string `json:"path"`
    Reason string `json:"reason"`
}
type RestoreResult struct {
    New         []string         `json:"new"`
    Overwritten []string         `json:"overwritten"`
    Skipped     []string         `json:"skipped"`
    Failed      []RestoreFailure `json:"failed"`
}
func (a *App) RestoreApp(name string) (RestoreResult, error)
```

Per-file-independent, matching `UpdateFromSource`'s safety philosophy —
**not** validate-everything-first like `AddApp`. One file's problem
(missing vault copy, permission error, path escape) shouldn't block
restoring the rest of the app's files, and there's no shared manifest
mutation here that a partial failure could leave inconsistent (restore
doesn't touch `manifest.json` at all).

`RestoreApp` recomputes each file's state itself via `fileDriftRow`
rather than trusting anything the client's preview fetched earlier —
same reasoning `UpdateFromSource` already follows: the two calls (open
preview, click commit) aren't atomic, so the commit re-checks rather than
acting on a possibly-stale snapshot.

Per file: `"ok"` → append to `Skipped`, no write. `"missing"` or
`"drifted"` → resolve the destination path via `homeRelative` (§5), apply
the symlink guard (§5), then copy vault→home via the existing `copyFile`
primitive reversed (which creates any missing parent directory itself —
see §0). Append to `New` or `Overwritten` accordingly. Any per-file error
→ `RestoreFailure` with a plain-English reason, loop continues.

**`$HOME` remap: nothing new to build.** Restore resolves the live
destination the same way every prior STEP already resolves `$HOME` —
`os.UserHomeDir()` at call time, never a stored absolute path. Restoring
a vault built on one machine onto a different machine (different
username, different home directory) already works, for free, because
nothing in the manifest ever stored an absolute path to begin with.

---

## 5. TWO SAFETY GUARDS RESTORE NEEDS THAT NO EARLIER STEP DID

Both apply because this is the first STEP writing onto the live system —
see §0.

**Path-escape re-validation.** `RestoreApp` resolves every destination
through the existing `homeRelative` primitive before writing, exactly
like `AddApp` already does when *reading* source paths. This is
defense-in-depth, always-on, no maker-facing toggle: a manifest is
trusted data written by this app, but an adopted or hand-edited
`manifest.json` could in principle carry an escaping path (`"../../etc/
passwd"`), and restore is the one operation that would act on it as a
*write* destination. Verified directly: a test that hand-corrupts a
manifest entry to `"../outside.txt"` confirms the file lands in `Failed`
and nothing is written outside home.

**Symlink-at-destination: replace with a real file.** Before writing any
file, `os.Lstat` the destination path (not `os.Stat` — `Lstat` doesn't
follow symlinks, so it sees the symlink itself, including a *broken*
symlink whose target no longer exists). If it's a symlink, `os.Remove`
it first, then write via the normal `copyFile` path. **Never** a raw
`os.Create`/write directly at a path that might be a symlink — that
would silently write through it into whatever it points at, possibly
outside `$HOME` entirely. This check runs unconditionally for every file
restore touches, not only ones the preview already classified as
"Overwrite" — a broken symlink at a "New" destination still needs the
same guard, since `os.Stat`-based drift detection reports a broken
symlink's target as absent (looks like "missing") even though something
*is* there at that path. Verified live: a Stow-style symlinked `.bashrc`
pointing at unrelated content is replaced with a real file matching the
vault, and the old symlink's target file is left byte-for-byte untouched
— proof this was never a write-through.

This policy exists to support users migrating off GNU Stow (RESONANCE's
named predecessor), where live dotfiles are commonly symlinks into a
separate dotfiles repo — restore should be able to materialize real files
where those links stand.

---

## 6. UI FLOW

A new "←" restore button joins each app row's spine cell, ordered
`[← Restore] [drift badge] [→ Update]` — read left-to-right, each arrow
points in the direction its action writes (restore: vault→home,
leftward; update: home→vault, rightward), matching the SYSTEM-left/
VAULT-right layout already established.

Clicking "← Restore" (with `row.drifted`; otherwise a toast, see §2)
opens a restore-preview overlay (`restore.ts`, reusing `overlay.ts`
unchanged — no new overlay mechanism needed):

- a **machine-info card** at the top, from `GetMachineInfo()` — what
  system this vault currently reflects (kernel / OS / hostname /
  username), or an "unknown" state per §0's backfill note
- a **summary line** — counts of new / overwrite / unchanged
- the **itemized list** — New and Overwrite rows only (§2); Overwrite
  rows are expandable to their lazily-fetched diff (§3)
- a **checkbox gate** — "I understand this will overwrite files on this
  system," unchecked by default; the Restore button stays disabled until
  checked, unconditionally — every restore, regardless of whether the
  preview contains any Overwrite-state files, per the maker's decision
- **Restore** / **Cancel** buttons — Cancel is a silent no-op, same
  contract every overlay in this codebase already honors

Confirming calls `RestoreApp(name)`, closes the overlay, calls
`refreshMirror()` (the drift badges update immediately — a file just
restored to match the vault now reads `"ok"`), and shows a result toast
(new / overwritten / failed counts, mirroring `summarizeResults`'s
existing shape in `update.ts`, adapted for restore's fields).

No bulk "Restore All" this STEP, per the maker's decision — restore
stays per-app only while there's no undo yet (STEP5). No changes to
`update.ts` or its dates-only confirm, also per the maker's decision —
the content-diff overlay stays restore-exclusive.

---

## 7. DEFINITION OF DONE — v0.4.0 checklist

- [x] `MachineInfo{Kernel,OS,Hostname,Username}` captured via stdlib
      only; any single field failing to resolve doesn't abort capture
- [x] `Manifest` gains `MachineInfo`, additive; `manifestVersion` stays `1`
- [x] `AddApp` and `UpdateFromSource` both stamp `MachineInfo` after a
      successful write; `GetMachineInfo()` returns it
- [x] A pre-STEP4 vault with no machine info renders an "unknown" card,
      not an error
- [x] Restore-preview classification is a pure TS relabel of the existing
      `AppRow`/`FileRow.State` already in `lastRows` — no new Go call
      beyond `GetMachineInfo()` to open the preview
- [x] "← Restore" is gated the same way "→ Update" is: `!row.drifted`
      shows a toast, no overlay
- [x] Preview lists New and Overwrite rows; Skipped is a count only
- [x] `GetDiffPair` enforces the 1 MiB and 20,000-line caps and binary
      detection server-side; no oversized/binary content ever crosses IPC
- [x] Only Overwrite rows are diff-expandable; the diff fetches lazily on
      first expand, once per file, not prefetched
- [x] `diff.ts`'s line diff renders added/removed/unchanged lines using
      new theme tokens (both existing palettes), zero new hex literals
      outside `theme.css`
- [x] `RestoreApp` recomputes each file's state itself; never trusts a
      client-supplied preview snapshot
- [x] Every restore destination path is re-validated via `homeRelative`
      before any write
- [x] Every restore destination is `Lstat`'d before writing; a symlink
      (including a broken one) is `os.Remove`d first, then written via
      `copyFile` — never a raw write-through
- [x] Parent directories are created automatically (`copyFile`'s existing
      `MkdirAll`) if the live app folder doesn't exist
- [x] `RestoreResult`'s four slice fields are always initialized to `[]`,
      never left nil (STEP3's null-marshal bug, guarded against by
      construction — regression test asserted no `"null"` in the marshaled
      JSON)
- [x] One file's restore failure doesn't block the rest of the app's
      files; failures land in `Failed` with a plain-English reason
- [x] Restoring onto a different machine (different `$HOME`) works with
      no manifest changes needed — the manifest never stores an absolute
      path, only `os.UserHomeDir()`-relative ones
- [x] Checkbox gate disables Restore until checked, unconditionally,
      every restore
- [x] Cancelling the restore-preview overlay is a silent no-op — nothing
      written, nothing stamped
- [x] No bulk "Restore All" control exists anywhere in the UI
- [x] `update.ts` is unchanged — no diff view, no new gating added there
- [x] All new CSS uses `var(--token)`s; new diff-line tokens added to
      `theme.css` only, both palettes; still exactly 3 CSS files
- [x] `wails build -tags webkit2_41` produces a working binary
- [x] `version.ts` bumped to v0.4.0 with the real release date
- [x] README.md updated to mention restore/diff/machine-info
- [x] Committed locally as v0.4.0, no AI trailers anywhere — per CORE.md
      §8, still local-only through v0.9.x

## 8. EXPLICITLY OUT OF STEP4

Bulk "Restore All" — deferred pending STEP5's pre-restore snapshot/undo,
which changes the risk calculus for an all-at-once write. Content-diff
overlay retrofit onto "update from source" — STEP3's dates-only confirm
stays as maker-approved, not reopened. Pre-restore snapshot/undo itself,
remaining JADEITE palettes, packaging, status bar (all STEP5, unchanged
from STEP2/STEP3's own deferrals). Per-file restore selection inside an
app (restore stays app-granular, matching `UpdateFromSource`'s own
granularity — a later-step idea, not this one, if ever). A standalone
machine-info viewer outside the restore-preview overlay (the card only
appears in context, where it's actionable). An mtime+size skip-cache to
avoid re-hashing unchanged files (STEP3's own carried-over deferral,
still not a correctness requirement). Backfilling machine info for
pre-STEP4 vaults (impossible by construction — §0).

---

**When the checklist is green:** commit v0.4.0 locally, then STEP5.md
gets written — the last STEP before v1.0.0.

Copyright © sudo-megas
*Built with Reason and Passion.*
