RESONANCE:

***NEED FIXES TO v1.2.1***
**#1** - Move vault: app must have administrative "mkdir" function otherwise errors: "copy to new vault failed, old vault untouched: lstat /run/media/megas/DOTFILES/TEST: no such file or directory"
**#2** - *Error "This folder isn't empty and isn't a RESONANCE vault. Choose an empty folder, or one that already has a RESONANCE vault." the app must ignore whether selected directory is empty or not. Thats user's responsibility to select proper path.*
**#3** - *Vault must be more verbose. Has to show diffs if input app expanded.*
**#4** - *Middle pane buttons "Download" and "Upload" is ambiguous. instead the icons "system to vault (right arrow), vault to system (left arrow)"*
**#5!**(CRITICAL) - *The "Add app" >> "Choose Files" must be reworked. if a directory selected, The directory and sub-files must be added too.*
**#5.2**(CRITICAL) - *If multi-selection includes directories. it must also contain the directory and its contents.*

---

## Amendment deployed by megas — v1.2.1

Three earlier ratified decisions are reversed by this release. Recorded here because
nothing else in the repo would say so, and the prior STEP documents still assert the
opposite.

**1. STEP4 §6/§8 — the content-diff overlay is no longer restore-exclusive.**
STEP4 froze it as such ("update.ts is unchanged — no diff view, no new gating added
there"). Item #3 above lifts that, but **only for the mirror**: the drift badge now
opens a per-app differences view. `update.ts`'s dates-only confirm stays frozen exactly
as STEP3 approved it — no diff was added there.

The mirror's diff reads in the opposite direction from restore's, deliberately. Restore
asks "what would change on my system if I pulled the vault back", so `+` is a line the
vault would add. The mirror asks "what has happened since I backed up", so `−` is a line
lost since the backup and `+` a line added since. Both columns are labelled, because the
same symbols meaning opposite things in two places is otherwise a trap.

**2. STEP2 §1 — the "target folder must be empty" refusal is dropped.**
Per item #2: choosing where your own vault lives is the user's business. One narrowing
is kept, and it is not a re-litigation of that ruling: **Move** is still refused into a
folder that already contains files. Copy never deletes anything and is allowed anywhere;
Move ends by deleting the folder it moved away from, so permitting it into a folder full
of unrelated files sets up a later Move to destroy them.

That emptiness rule turned out to be the only thing standing between `Move` and the
user's data. With it gone, `$HOME` itself passed every remaining check — and the folder
picker's default landing directory *is* `$HOME`. Pointing the vault there and later
moving it away would have run `os.RemoveAll` on the home directory. `$HOME`, its
ancestors, and RESONANCE's own state and config directories are now refused outright, on
the symlink-resolved path.

**3. `ManifestApp.Dirs` is additive; `manifestVersion` stays 1.**
Same footing as `Size`/`Checksum`/`BackedUpAt` before it: absent from every manifest
written before v1.2.1, and a field no older binary understands is not a format change.

Downgrading is lossy and silent, which is worth stating plainly: `encoding/json` drops
unknown fields on unmarshal, so an older RESONANCE that loads and then saves a v1.2.1
manifest strips every tracked folder from it. Nothing is corrupted and no backed-up file
is lost — the materialised `files` entries survive — but the folders stop being tracked
and nothing tells the user.

---

### Also fixed in v1.2.1, beyond the list above

Found by a lifecycle audit of the whole app and approved for this release:

- **The vault pane described the manifest, not the vault.** A backup deleted by hand or
  lost to a failing drive left its row rendering "in sync" with a valid date, until a
  restore was attempted — possibly on another machine, after the source was gone. The
  vault side is now checked on every refresh (existence, type, size — one `Lstat`, no
  hashing). `vaultMissing` and `vaultDamaged` are kept distinct from `missing`: a gone
  backup is repaired by Update, a gone source cannot be.
- **Orphan vault files.** `AddApp` validated everything before copying anything, but that
  covered validation failures, not an I/O failure partway through the copy loop. The
  leftovers were referenced by nothing, removable by nothing inside the app, and copied
  faithfully by every later Copy/Move. `ScanVaultOrphans` reports them; `AddApp` now
  unwinds exactly the files it wrote. Removing them belongs to v1.2.3's delete surface.
- **Restore failures said only "1 failed".** The per-file reason was computed, shipped
  across the IPC boundary, and thrown away — at the moment the user most needs it,
  because their files had just been partially overwritten. It is now shown.

**Not in this release.** Administrative/polkit vault paths (v1.2.2) and app editing —
add files, remove files, delete, rename, untrack folders (v1.2.3). v1.2.1's only
contribution to the first is that a permission failure now reports itself as one, so the
elevation prompt has something honest to attach to.
