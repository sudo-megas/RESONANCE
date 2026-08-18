RESONANCE:

***v1.2.2 — the app can finally forget something***

**#1** - *The app is append-only. `AddApp` is the only method in the whole backend that
ever writes a manifest entry, and nothing anywhere removes one. Add a file by mistake and
it is in the vault forever. "as far as i tested you built a half-done app :))"*

---

## Amendment deployed by megas — v1.2.2

Three ratified decisions are reversed by this release.

**1. The administrative/polkit release is dropped entirely.**
v1.2.1's closing note reserved v1.2.2 for "administrative/polkit vault paths", and a full
pkexec design was ratified after it. There is no helper binary, no polkit policy, and no
privileged code in this release or any planned one. Two findings killed it, and both are
worth recording because the premise looked solid right up until it was checked:

- The error that motivated the whole thing — `copy to new vault failed, old vault
  untouched: lstat /run/media/megas/DOTFILES/TEST: no such file or directory` — was never
  a permission failure. It is `ENOENT` on the *old* vault: `copyTree` is a `WalkDir` whose
  root gets `lstat`ed first. v1.2.1 fixed its real cause. An elevation prompt attached to
  it would have been a prompt for root to fix a problem root does not solve.
- The drive it was meant for has no permission wall. `/run/media/megas/DOTFILES` is ext4,
  `drwxr-xr-x megas megas`, and `mkdir` there succeeds as the user.

It was also established that an "administrative mkdir" alone is useless — a root-created
folder the user cannot write to is worse than no folder — and that adopting a *populated*
root-owned vault needs a recursive `chown`, which is a different and much larger feature
than the one being asked for.

**2. The `/usr` and `/etc` question is a scope change, not a permissions one.**
The app cannot back up anything outside `$HOME`, and root would not change that.
`homeRelative` (manifest.go) refuses it at 15 call sites covering every route in and out
of the vault. That is a code refusal by design, and lifting it is queued as its own piece
of work *after* this release — deliberately, because it changes what the manifest means,
not merely who may write it.

**3. Removing an app no longer discards its undo snapshot.**
The first cut of this release cleared the snapshot on `RemoveApp`, to stop a later app
reusing the name from inheriting it. That defence now lives where it belongs: a snapshot
records the vault its restore pulled from, and a mismatch is flagged rather than acted on.
With that in place the discard was doing nothing but harm. `UndoRestore` never reads the
vault, so a snapshot stays exactly as valid once its app leaves — and it is the only thing
in the program that can revert a *prior* change to `$HOME`. Deleting it inside an
operation whose headline promise is "nothing in your home folder changes" was the one
genuinely destructive thing that operation did. A rename onto an occupied name now
declines the move for the same reason, instead of overwriting.

---

## What shipped

**Editing, in the two units `AddApp` already accepts.** Add files or folders to an
existing app, remove a file, remove a tracked folder, untrack a folder, rename an app,
remove an app outright. `AddApp` was split into `stageAdd`/`commitAdd` so adding to a new
app and adding to an existing one share one classifier and cannot drift apart.

Two invariants carry the whole feature. `RemoveFromApp` never computes `$HOME` — no
`os.UserHomeDir`, nowhere — and that absence *is* the "it never touches your real file"
guarantee, which is why there is a test asserting the live bytes are unchanged. And the
vault copy is deleted *before* the manifest entry is dropped, never the reverse: an entry
whose copy is gone reports itself as `vaultMissing` and a retry is self-healing, while an
entry dropped while its copy survives is unreferenced garbage nothing can ever find.

`RemoveApp` is not optional alongside per-file removal. Name uniqueness is enforced
case-insensitively, so an app emptied to zero files would otherwise hold its name forever
— `bash` could never be created again.

**Two ways the vault could reach outside itself, both found by review of this release's
own code.**

- `refuseSymlinkedParents` walked the components *below* the app directory, so for a
  single-segment entry like `.bashrc` there was nothing to walk and it checked nothing at
  all. A vault carrying `<vault>/evil -> $HOME` and a manifest entry naming `.bashrc`
  would have had the user's live `~/.bashrc` unlinked by a removal. `unlink(2)` declines
  to follow a symlink only at the *final* component. The walk deliberately stops at the
  app directory: a vault path that is itself a symlink is an ordinary setup, and there is
  a test proving removal still works for it.
- `commitAdd` was the last write path without v1.2.1's `vaultDirEscapes` guard.

The deletion guard is deliberately stricter than `vaultDirEscapes`, which asks only
whether a path resolves *outside* the vault. That is the right question for reading and
writing — a cross-app write is repairable by the victim app's own Update — but not for
unlinking: `<vault>/appA/.config -> <vault>/appB` resolves *inside* the vault, passes
cleanly, and deletes another app's only backup. Deletes are not idempotent-repairable.

**Removing a file inside a tracked folder.** While a folder is tracked, the walk
rediscovers a removed file on the next refresh and the next update copies it back — the
deletion silently undoes itself. The folder is untracked instead, which is offered rather
than explained away, and refused in the backend too so the loop is unreachable even from
a hand-written IPC call. An `Excludes []string` filter was rejected: a permanent,
invisible negative model whose only visible effect is an absence, and the ignore-file
pattern that always grows. That is the Stow-family complexity CORE §1 names as the reason
this program exists.

`UntrackDir` drops the `Dirs` entry and nothing else. An earlier cut walked the folder and
added an entry for every file it found, on the reasoning that an empty checksum already
means "needs backing up" — reasoning that was one release out of date. v1.2.1 made
`fileDriftRow` check the vault side *before* the checksum branch, so those entries reported
`vaultMissing` instead: files went from the mildest badge in the app to its most severe,
and restore began reporting each as a failure, because the user clicked a conversion
advertised as changing nothing. A backup tool claiming it lost a file it never held is the
same lie v1.2.1 was written to kill, pointed the other way. `PreviewUntrackDir` states in
advance what stops being tracked.

**Undo snapshots stop being invisible storage.** Nothing listed them, nothing reported
their size, and nothing but `rm` could clear one — while a partly-failed undo keeps its
snapshot by design, so the same failing offer returned on every visit. `ListUndoSnapshots`
and `DiscardUndoSnapshot` are the way out, surfaced in Recent Activity because a snapshot
is the residue of a logged restore and that overlay is always reachable.

Two dead ends behind it. A snapshot now records the vault its restore pulled from, so undo
can tell whether it still relates to the vault on screen; absent means *unknown*, not
foreign, so every snapshot written before this release keeps being offered exactly as it
was. And `GetUndoInfo` dry-runs the entries to report how many would actually succeed, so
an undo whose captured bytes are gone is recognised rather than offered forever. Both are
counted rather than flagged, because undo is per-entry-independent and one damaged entry
must not suppress an offer that would put the other nine back.

`UndoRestore` also prunes the entries it applied when the rest fail. Retrying was meant to
be the safe move, but replaying an applied "absent" entry deletes a file the user
recreated in between, and an applied "regular" entry clobbers edits made after the first
undo. The rewrite is atomic, because a torn `snapshot.json` reads as no snapshot at all.

**The machine-info card is reachable.** CORE §3 lists it as an overlay surface in its own
right, but it was only ever built inside the restore preview — which returns early
whenever nothing is restorable. A vault in sync, the normal state, had no way to show who
wrote it. It now opens from the VAULT pane header, from the same builder, so the two can
never disagree.

**`manifest.json` gets the mutex `activity.json` already had.** Wails dispatches bound
calls concurrently, and this release adds five manifest writers to what was an
unsynchronised read-modify-write. The reachable case is not theoretical: the update-confirm
overlay is dismissable, so a user can Escape out of an in-flight update and save an edit
while it is still running, and the update's own save at the end would silently revert the
edit — leaving the copied bytes in the vault as orphans.

**Smaller.** A rename records itself as an edit rather than a deletion in the activity log.
A removal refused for sitting inside a tracked folder names every folder covering it, not
just the first, so untracking one no longer leads straight to the same refusal naming the
next. `RenameApp` unwinds its folder move when the manifest save fails. The three snapshot
entry points that take an app name over IPC and join it onto a path now validate it. The
edit overlay renders at most 300 rows behind a filter — the payload is never capped, since
that would make files beyond the cap permanently unremovable.

**Not in this release.** Deleting vault orphans (`ScanVaultOrphans` still ships as
detection only), `GetDiffPair`'s leaf-only symlink check on the vault side, and the
`/etc`/`/usr` scope change.

## Icons

Every codepoint was measured against the bundled `CaskaydiaCoveNerdFont-Regular.ttf`
(cmap fmt 4 + 12, `hmtx`, `loca`, `glyf`) using layout.css's own centering formula,
`translateX = −(inkCenter − boxCenter) / unitsPerEm`. The method reproduces `U+F177` at
−0.1685em — the arrow shipped in v1.2.1 — which is what validates it.

| Use | Codepoint | Measured | Cost |
|---|---|---|---|
| vault row edit button | `U+F0AE` fa-tasks | −0.1685em | none — joins the existing selector list |
| VAULT header info button | `U+F05A` fa-info-circle | −0.1685em | none — same list |
| activity `remove` | `U+F056` fa-minus-circle | −0.1685em | none — `.activity-icon` hardcodes it |
| activity `edit` | `U+F0AE` fa-tasks | −0.1685em | none — same |
| per-row remove | `U+F00D` fa-xmark | −0.0000em | none — already used, correctly untransformed |

`fa-pencil` (`U+F040`) measures −0.1655em and would have needed its own rule. `fa-trash`
(`U+F1F8`) was deliberately not proposed: a trash can is precisely the icon a user reads
as "this destroys my real file".
