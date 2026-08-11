# RESONANCE — STEP3.md → v0.3.0 "The Baseline"

**Goal:** the vault learns to notice change. Every backed-up file gets a
checksum and a backup timestamp; the mirror gains real drift badges and
per-file date displays; "update from source (now)" becomes a working
action, confirmed with dates before it writes anything. The twin-list
rendering STEP2 used as a stand-in is replaced by the single-row-spanning-
both-panes architecture STEP2.md's own design note anticipated for this
step. No restore, no content-diff view, no machine info, no status bar yet
— those are STEP4 and STEP5. When this document's checklist is green,
commit locally as v0.3.0 — per CORE.md §8, nothing pushes or tags until
v1.0.0.

Location: `/home/megas/RESONANCE/build/docs/STEP3.md`
Approved by maker: sudo-megas, 2026-08-11

---

## 0. BEFORE STARTING

No new dependency this step. `crypto/sha256` and `encoding/hex` are
stdlib — unlike STEP2's `rymdport/portal`, which existed to fill a real
gap (no viable stdlib XDG portal picker), there's no gap here. `go.mod`/
`go.sum` are untouched.

**Gotcha, pre-warned (same shape as STEP2's):** every new Go method bound
in `drift.go` needs one `wails dev -tags webkit2_41` run before any
TypeScript importing it can be written — `GetMirrorRows`,
`UpdateFromSource`, and the `AppRow`/`FileRow`/`UpdateResult` types don't
exist in `frontend/wailsjs/` until that run happens. Write all the Go
first, run it once, then write TS against the real bindings.

**Dates gotcha:** Linux/ext4 exposes no reliable file *creation* time
through Go's stdlib `os.Stat` (ctime is inode-change time, not creation
time; birth time needs `statx` and isn't universally available). CORE.md
§4's "created/modified dates" becomes **modified-time only**, both source
and vault side. Flagging this here so it's approved once, not rediscovered
mid-implementation.

---

## 1. THE MANIFEST LEARNS A CHECKSUM

`ManifestFile` (`manifest.go`) gains three fields, additive to the
existing `path`:

```json
{ "path": ".bashrc", "size": 3421, "checksum": "3a7f...", "backedUpAt": "2026-08-11T14:02:00Z" }
```

- **`checksum`** — hex SHA-256 of the *vault-side* copy, computed at backup
  time. `manifest.json`'s top-level `"version"` stays `1` — these are
  additive optional fields, exactly as STEP2.md promised ("STEP3 adds
  checksum/date fields to `ManifestFile` without touching this shape").
- **`size`** — a cheap `os.Stat`-only short-circuit: a live-file size
  mismatch against the stored size proves drift without hashing anything.
  It cannot shortcut the *no-drift* case — same size doesn't prove same
  content, so a full hash read still happens whenever sizes match.
- **`backedUpAt`** — RFC3339 UTC, the vault-side copy's write time.

**Per-file, not per-app.** CORE.md §3's row spec is explicit — source
dates on the left, vault dates on the right, per expanded file row — and a
single per-app timestamp couldn't drive that display. It also can't
represent partial drift: if an app has 5 files and 1 changed, a per-app
checksum would either mask the other 4 as drifted too or mean nothing.

**Backward compatibility — lazy backfill, not permanent "no baseline."**
Every STEP2-created `manifest.json` has `ManifestFile{path}` only; on
first load under STEP3 code, `backfillChecksums(vaultPath, *Manifest)`
walks every entry with an empty `checksum`, hashes the **vault-side**
copy only (never the live source — this only reads bytes `AddApp` already
trusted and copied in STEP2), fills `size`/`checksum`/`backedUpAt` (mtime
of the vault-side file — `copyFile` never calls `os.Chtimes`, so that
mtime genuinely is the moment STEP2 wrote it, not a fabricated value), and
persists once if anything changed. A vault-side copy that's itself
missing or unreadable during backfill doesn't abort the whole pass — that
one file is left with an empty checksum and surfaces as `"drifted"` (no
valid baseline) rather than crashing the mirror. This is a one-time,
bounded cost (dotfile-scale vaults hash in well under a second) — the
alternative (permanently showing STEP2-era apps as "no data") is pure
busywork with no correctness benefit.

**`verifyTreesMatch` (`migration.go`) is upgraded to checksum, not just
size** — STEP2.md's own words: "checksums aren't part of STEP2's
manifest schema... that's STEP3's job." After the existing size check
(kept as a cheap first-pass short-circuit), both copies are hashed and
compared. This is a **distinct** use from manifest drift below — migration
verification compares two *current* filesystem trees during a vault
move/copy; drift compares a *current* source file against a *historical*
stored checksum. Both share one primitive:

```go
// checksum.go — the one sha256 primitive, shared by both call sites
func fileChecksum(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil { return "", err }
    defer f.Close()
    h := sha256.New()
    if _, err := io.Copy(h, f); err != nil { return "", err }
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

Streamed via `io.Copy`, not read-into-memory, so a large dotfile (a fat
`.bash_history`, a cache dump) doesn't blow up memory.

---

## 2. DRIFT — WHAT IT MEANS, WHEN IT'S COMPUTED

Drift is **three states, not a boolean** — a source file can also have
been deleted since backup, a distinct condition from "changed":

```go
// drift.go
type FileRow struct {
    Path           string `json:"path"`
    State          string `json:"state"` // "ok" | "drifted" | "missing"
    SourceModified string `json:"sourceModified"` // RFC3339 UTC, "" if source missing
    VaultModified  string `json:"vaultModified"`   // == ManifestFile.BackedUpAt
}
type AppRow struct {
    Name    string    `json:"name"`
    Files   []FileRow `json:"files"`
    Drifted bool      `json:"drifted"` // true if any file is drifted or missing
}
```

Dates travel the wire as UTC RFC3339 (a vault must be restorable by any
user on any machine — CORE.md §4 — and machine timezones differ) and are
converted to the *viewer's* local time only at display time in
TypeScript.

**Computed eagerly, at the same points the mirror already refreshes
today** — app startup, after a vault-path migration finishes, after
`AddApp` succeeds, after an update commits. Not lazy-per-expand, not a
manual "check for changes" button. This is forced by the spec, not a
close call: CORE.md's badges are an *ambient* signal — "glow on any row
where the two sides no longer match" — visible on the **collapsed** row,
before any expand action. A lazy-per-expand model can't produce that
collapsed-row badge without computing drift first, which means it isn't
actually lazy for the thing that matters. The size short-circuit from §1
keeps this cheap for the common case; for dotfile-scale vaults (small
text configs, not GB media) a full hash pass at these refresh points is
fast. An mtime+size skip-cache (skip re-hashing files unchanged since the
last computed result, à la git/rsync) is a real future optimization, not
built speculatively — see §7.

---

## 3. "UPDATE FROM SOURCE (NOW)"

**Granularity: per-app**, matching CORE.md §4's own framing of the
action ("only when the user provokes it (add or 'update from source
(now)')" — scoped like `AddApp`, at the app level, not per-file or purely
global):

```go
type UpdateResult struct {
    Updated []string `json:"updated"` // re-copied
    Skipped []string `json:"skipped"` // already identical, untouched
    Missing []string `json:"missing"` // source gone; vault copy left untouched, reported not failed
}
func (a *App) UpdateFromSource(name string) (UpdateResult, error)
```

Deliberately **not** all-or-nothing like `AddApp`'s validate-everything-
before-copying-anything. Each file here is an *independent*, already-
established manifest entry — one file's source having vanished since
backup must not block re-syncing the other files that are fine.

**Trigger placement — both, living in the spine.** CORE.md §3 already
names "→ Update" as spine content by design (unlike the "+" button, which
needed a one-off exception to enter the spine at all in STEP2). STEP3
realizes it two ways: a small update control inside each app row's spine
cell (precise, single-app), plus a global "→ Update all" pinned at the
top of the spine next to the "+" (bulk convenience). The bulk action is
implemented **client-side only** — a loop over `UpdateFromSource(name)`
for every `AppRow` with `drifted === true`, aggregating results into one
summary toast. No new Go method for "update everything."

**Confirmation — dates-only, no content diff.** CORE.md §4's Overwrite
rules say diff + dates must be shown before an overwrite commits, but
STEP2.md's own "Explicitly out of STEP2" list already places the "diff
overlay" itself under STEP4. STEP3 satisfies the spirit of the rule
without reopening STEP4's scope: clicking an update trigger (row or bulk)
opens a confirm overlay — reusing the existing `overlay.ts` grammar,
same as `addapp.ts` — listing every file about to be re-copied with its
source-modified and vault-last-updated dates side by side. Confirming
calls `UpdateFromSource`; cancelling is a silent no-op, nothing written.

---

## 4. THE MIRROR BECOMES ONE GRID

STEP2.md's own design note already named this as due now: "full single-
DOM-row-spanning-both-panes architecture (needed once accordion/drift
chrome exists) is deferred to STEP3." Building it now, not the twin-list
approximation — because expanded content is asymmetric (source date only
on the left; vault date + "last updated DD MM YYYY" on the right), any
twin-list "fix" would either require permanent per-row height-matching
between two independently-scrolled regions, or duplicate the drift badge
onto both flanking rows — in both cases more fragile than the real
rewrite, not safer.

`index.html`'s `#system-body`/`#vault-body` are removed, replaced by one
scrollable grid:

```html
<main class="mirror" id="mirror">
  <div class="mirror-rows" id="mirror-rows"></div>
</main>
```

`.mirror-rows { display: grid; grid-template-columns: 1fr 96px 1fr; }`.
Row zero is a real row in that same grid — the sticky header row — holding
the SYSTEM label, the persistent spine controls ("+" and "→ Update all"),
and the VAULT label. Every app row shares the identical 3-column
template: system-side content, a drift-badge cell (`background:
var(--spine)`, preserving the continuous spine look across stacked
per-row cells), vault-side content. Expanding a row inserts 3-column
sub-rows beneath it (source date | per-file state | vault date + "last
updated DD MM YYYY"); collapsing removes them.

**One shared data object, one row builder.** `GetMirrorRows()` returns
every app, every file, drift already computed, in one call.
`buildRow(row: main.AppRow)` builds **one** DOM row from **one** object —
there is no second, independently-built "other side" anymore, which
directly retires STEP2's flagged risk ("drift-aware divergence... is
STEP3's job" — now structurally impossible to get wrong, since there's
exactly one code path). Expand/collapse state is a small `Set<string>` of
expanded app names in `rows.ts`; because all per-file data was already
fetched in the one `GetMirrorRows()` call, expanding is a pure client-side
toggle — no extra round trip per expand.

The pane/spine background bands stay visually continuous down to the
bottom of the window regardless of row count, via a background gradient
on the outer `.mirror` container sized to the same column proportions —
decoupling "does the grid fill the viewport" from "do the pane colors
look right," so a vault with 2 apps looks exactly as finished as one with
50.

---

## 5. STATUS BAR — DEFERRED AGAIN, EXPLICITLY

STEP2.md's §8 suggested STEP3 as the moment once drift/date data exists —
but CORE.md §9's ladder table, the authoritative scope boundary, names
exactly three items for STEP3 and a status bar isn't one of them.
Decided explicitly, not silently: **deferred to STEP5.** Nothing is lost
by waiting — `GetMirrorRows()` already carries everything a status bar
would summarize, whenever it gets built.

---

## 6. DEFINITION OF DONE — v0.3.0 checklist

- [x] `ManifestFile` gains `size`, `checksum` (sha256 hex), `backedUpAt`
      (RFC3339 UTC); `manifestVersion` stays `1`
- [x] Opening a STEP2-era vault under STEP3 code silently backfills
      checksum/size/backedUpAt from the vault-side copy on first load,
      writes `manifest.json` once, shows no false drift for unchanged files
- [x] Backfill never reads live source files — only the vault's own bytes
- [x] A missing/unreadable vault-side copy during backfill doesn't abort
      the pass; that file is treated as `"drifted"`, not a crash
- [x] `fileChecksum` (`checksum.go`) is the single sha256 primitive shared
      by drift computation and `verifyTreesMatch` — no duplicate hashing
- [x] `verifyTreesMatch` checks size **and** checksum; Copy/Move migration
      ordering (copy → verify → switch settings → delete-old-if-move)
      unchanged
- [x] `GetMirrorRows` reports per-file state as `"ok"` / `"drifted"` /
      `"missing"`; a vanished source doesn't block the rest of the call
- [x] Drift badges render only on rows/files where state ≠ `"ok"` — steady
      state shows no badge, no visual noise at rest
- [x] Drift badge visibly glows using `var(--badge-drift)` — its first
      real consumer since being reserved in STEP1
- [x] Missing-source state renders visually distinct (`var(--danger)`)
      from generic drift
- [x] `#system-body`/`#vault-body` twin-list rendering is gone from the
      codebase, not hidden — replaced by the single `#mirror-rows` grid
- [x] Every row and every expanded file sub-row is built from one shared
      `AppRow`/`FileRow` object — no code path independently builds "the
      other side"
- [x] Persistent spine controls render as a sticky header row, staying
      visually continuous with the spine column as content scrolls beneath
- [x] Clicking a row's toggle expands per-file rows in place — source
      date left, vault date + "last updated DD MM YYYY" right, state in
      the middle — and collapses back cleanly
- [x] All dates render as literal zero-padded `"DD MM YYYY"` via one
      shared `formatDate` — no second date-formatting implementation
- [x] A per-app "→ Update" control lives in that row's spine cell; a
      global "→ Update all" lives in the sticky header row
- [x] Either trigger opens a dates-only confirm overlay (every file about
      to be re-copied, source date + vault date) before anything writes
- [x] Confirming calls `UpdateFromSource(name)` (bulk = client-side loop
      over the same call); result toast distinguishes updated / skipped /
      missing; a missing source never fails the whole update and its
      vault copy is left untouched
- [x] Cancelling the confirm overlay is a silent no-op — nothing written
- [x] `extractErrorMessage` exists in exactly one place (`util.ts`),
      imported everywhere needed — no third copy-paste
- [x] `ListApps` removed from `vault.go` and every frontend call site —
      `GetMirrorRows` is its sole successor
- [x] Every manifest-mutating and `SaveSettings(` call site re-checked for
      STEP2's RMW discipline (regression check)
- [x] All new CSS uses existing `var(--token)`s only, zero new hex
      literals; still exactly 3 CSS files
- [x] `wails build -tags webkit2_41` produces a working binary on the
      main rig
- [x] Status bar remains explicitly deferred to STEP5 — not built, not
      silently dropped
- [x] `version.ts` bumped to v0.3.0 with the real release date
- [x] README.md updated to reflect drift/dates/update-from-source
- [x] Committed locally as v0.3.0, no AI trailers anywhere — per CORE.md
      §8, still local-only through v0.9.x

## 7. EXPLICITLY OUT OF STEP3

Per-file update inside the expanded accordion (files re-sync only as
part of their app's full update-from-source; individually-scoped file
update is a later-step idea, not this one). Restore of any kind, `$HOME`
remap on restore, restore preview, content-diff overlay, machine-info
card (STEP4, unchanged from STEP2.md). Pre-restore snapshot/undo,
remaining JADEITE palettes, packaging (STEP5). Status bar (deferred again
to STEP5 per §5 above). An mtime+size skip-cache to avoid re-hashing
unchanged files between launches (an optimization worth revisiting only
if real-vault launch latency becomes a problem, not a correctness
requirement now). File *creation* dates (Linux/ext4 has no reliable
stdlib API for this — STEP3 uses modified-time only, both sides, per §0's
gotcha).

---

**When the checklist is green:** commit v0.3.0 locally, then STEP4.md
gets written — informed by whatever building drift and dates taught.

Copyright © sudo-megas
*Built with Reason and Passion.*
