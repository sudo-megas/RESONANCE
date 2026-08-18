RESONANCE:

***v1.4.0 — two directions, and nothing else***

**#1** - *"i want to wipe "undo" function completely out from the app."*

**#2** - *"just 2 ways. backup from system to vault and restore from vault to system. any
other than these 2 is complicating things that i dont need"*

---

## Amendment deployed by megas — v1.4.0

One ratified decision is reversed by this release.

**CORE §4's pre-restore snapshot is removed.** The line read: *"Before a restore overwrites
anything, the current files are tucked aside automatically. One 'Undo restore' button brings
them back."* It shipped in STEP5, went out with v1.0.0, and survived every release since.

It is removed because the app it belongs to is not the app the maker wants. §1 has always
listed three functions and only three — backup, store, restore — and undo was never one of
them. It arrived as a safety net for the third, and a safety net is a fourth thing to learn,
a fourth thing to explain, and a fourth thing that can be wrong. The maker's own framing is
the whole argument: two ways, system into the vault and vault onto the system, and anything
past those two is complication he does not need.

**What the removal costs, stated plainly rather than glossed.** A restore overwrites live
configuration, and since v1.3.0 it can overwrite configuration under `/etc` and `/usr`. With
no snapshot behind it, a restore of the wrong app, or of a vault copy that turned out to be
older than the reader thought, cannot be walked back from inside RESONANCE. That is a real
loss and it was named before the work started rather than discovered afterwards.

What answers it is that the reconsidering now happens entirely *before* the write. The
restore preview says how many files are new, how many are overwritten and how many are
identical and skipped; the diff shows the actual content of both sides; and as of this
release the differences view shows both timestamps to the second with the newer of the two
marked. A decision made with that in front of you is a better safeguard than a button that
makes the decision reversible afterwards — and it is the safeguard CORE §4 already required.

---

## What shipped

### The deletion

`snapshot.go` and `snapshot_test.go` are gone: 1,474 lines. With them go four methods that
crossed the IPC boundary — `GetUndoInfo`, `UndoRestore`, `ListUndoSnapshots` and
`DiscardUndoSnapshot` — and `undoSystemEntry`, the privileged three-way dispatch v1.3.0 added
so that undoing a restore into `/etc` could work at all.

`resonanceStateDir` moved out to `state.go` rather than dying with the file it happened to
live in. It was never undo's: the activity log is built on it, and so are the skip-lists in
`drift.go`, `vault.go` and `migration.go`. The shared test fixtures moved to
`fixtures_test.go` for the same reason — seven test files use them and none of them are about
undo.

### What restore looks like now

`RestoreApp` loses its whole staging half. There is no pending undo directory, no capture
step, no commit-before-mutate ordering, and no branch handling the case where the snapshot
could not be written. The two-pass shape is kept — collect every file, then write every file
— because it still means a bad path anywhere in an app is found before anything is written
rather than half way through.

The ← button stops being two buttons wearing one coat. It restores. It does not sometimes
restore and sometimes undo depending on whether the row is drifted and whether a snapshot
happens to exist, which was a rule the interface never had room to explain.

### What the activity log keeps

Everything. The log is for verbosity, not for undo's functioning, so entries recorded before
this release stay exactly as they were — including entries of kind `undo`, which are records
of things that genuinely happened. `KIND_ICON` keeps its `fa-undo` glyph for them: nothing
writes new entries of that kind, but an old entry whose glyph had been deleted would render
as a blank space where every sibling has an icon.

What the log loses is the *pending snapshot section* — the card with its Undo and Discard
buttons, which was machinery rather than record.

### Old snapshots on disk

`clearRemovedUndoState` deletes `~/.local/state/resonance/undo/` on startup. Those
directories hold the captured bytes of files from past restores; nothing reads them now, and
nobody could reasonably be expected to know what they were or that they were safe to delete
by hand. `activity.json` sits beside them and is untouched.

It runs on every start rather than once behind a marker file. `RemoveAll` on a path that is
not there costs a failed stat and returns nil, so a marker would buy nothing except a second
piece of state to keep consistent — and it would be wrong the moment someone restored an
older copy of that directory from a backup. Best-effort: a state directory that cannot be
cleaned is not a reason to refuse to start.

### Comments that had become false

A removal this size leaves prose behind that describes machinery that is gone, and prose that
lies is worse than no prose. `migration.go` still refuses a vault at `~/.local/state`, but for
the reason that is now true — Move would delete the activity log, not the undo history — and
its user-visible sentence says so. `activity.go` stopped describing itself by reference to
`readSnapshot` and `snapshot.go`. `editapp.go`'s rename doc stopped promising to move a
snapshot. `dates.ts` stopped citing an overlay that no longer exists. The helper's own comment
about symlinks stopped calling them snapshot entries.

---

## Verified, and what could not be

`gofmt`, `go vet`, `go test -count=1 ./...` across both packages, `npx tsc --noEmit`, and
`wails build -tags webkit2_41` are all clean. The generated Wails bindings were checked
directly: `App.d.ts` and `models.ts` contain no `Undo` or `Snapshot` symbol at all, which is
the real proof the API surface shrank rather than merely stopped being called.

The Nerd Font glyphs in `KIND_ICON` are Private-Use-Area literals, and they were verified by
codepoint before and after the edit — `undo` is still `U+F0E2`. Retyping that block would have
silently emptied it, which has happened in this project before.

The startup cleanup was verified by running it, since no test in this repo starts the GUI and
the hook runs only under Wails. The maker's own machine had an `undo/` directory holding
snapshots from the v1.3.0 restore testing; after one launch of this build it was gone and
`activity.json` sat beside it untouched, at the mtime of the last restore.

---

## Not in this release

- The README package-size badges, still carrying v1.2.2's figures. Reserved to the maker.
- Any replacement safety mechanism. There is deliberately none: a confirmation that undoes
  itself is the thing being removed, and adding a smaller version of it would be the same
  mistake at a lower volume.
- The v1.2.1 audit's remaining gap list, still not walked line by line.
