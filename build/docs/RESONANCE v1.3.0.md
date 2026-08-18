RESONANCE:

***v1.3.0 — /etc, /usr, and the rights to put them back***

**#1** - *"can it operate dotfiles from /usr and such?" — asked at v1.2.1, answered no, and
queued as its own work: "so no admin rights work. we will make scope change after redesign.
so swap work queues. first redesign, then scope change." The redesign shipped as v1.2.2.
This is the scope change.*

**#2** - *"So this example shows that we must integrate admin operations somehow right?"*

---

## Amendment deployed by megas — v1.3.0

Three ratified decisions are reversed by this release.

**1. The administrative/polkit release is back, for a different reason than it was dropped.**
v1.2.2's amendment #1 said there is "no helper binary, no polkit policy, and no privileged
code in this release or any planned one". That was right when it was written and it is worth
recording why: the error that motivated the original polkit design was never a permission
failure at all — it was `ENOENT` on the *old* vault, and an elevation prompt attached to it
would have been a prompt for root to fix a problem root does not solve. Nothing about that
finding has changed.

What changed is that the investigation had not yet reached the real case. Backing up
`/etc/alsa/alsa.conf` needs nothing — the file is world-readable and your own vault is
yours to write. Putting it back cannot be done as you, by any design, because `/etc/alsa/`
belongs to root. A file you can back up and never restore is half a feature, which is the
same shape as the "half-done app" verdict that produced v1.2.2. So elevation returns,
scoped to the one operation that genuinely requires it.

**2. `/etc` and `/usr` are places the vault reads from.** CORE §1 said "dotfiles"; it now
says configuration, which includes the system's. Amended in CORE.

**3. The manifest stores absolute paths — for `/etc` and `/usr` only.** CORE §4 said
`$HOME`-relative, never absolute. The rule's *intent* survives intact and that is why the
amendment is narrow: §4 exists to answer the username caveat, so a vault written by
`megas@mainrig` restores for any user. `/etc` and `/usr` are identical on every machine and
have no per-user form. Storing them absolute is what keeps them portable, not what breaks
it. Amended in CORE.

Two things were settled by the maker during planning and are not amendments, only decisions:
`/run` was dropped from the allowed roots once its tmpfs half was explained, and root-owned
vaults — reserved for "someday" at v1.2.2 — came back into scope alongside the helper that
makes them possible.

---

## What shipped

### Two axes, which had been conflated across two conversations

They are independent, and saying so was most of the planning:

| | **source** | **vault** |
|---|---|---|
| the question | what may go *into* the vault | where the vault *lives* |
| before | `$HOME` only, refused in code | anywhere you can write |
| now | `$HOME` + `/etc` + `/usr` | anywhere, **including root-owned** |
| needs root for | putting a file back | writing into it — reading stays direct |

The sentence the maker saw quoted — *"no permission to use /etc/alsa/ — that folder belongs
to another user, and RESONANCE runs as you"* — was the **vault** axis, a refusal to put the
vault somewhere. Widening the source axis does not touch it. Asked which variant of the
vault axis was needed to operate `/etc` and `/usr`, the honest answer was **neither**: they
are backed up and restored the same way whether the vault sits on a USB stick, in `$HOME`,
or in `/opt`. The vault axis rides along because the helper the source axis requires happens
to serve it too.

### `homeRelative` was never only about home

The rename landed first and alone, with no behaviour change. The function is `filepath.Rel`
plus a `".."` test, and it had always answered "is X under Y" for two unrelated Ys: twelve
callers passed `$HOME` and were asking whether a file may be backed up at all; seven passed
a vault directory and were asking whether a path stays inside the vault. The name made the
second group read like the first, and widening scope is exactly the change that would have
widened vault containment along with it. Renaming to `relativeUnder` first is what makes the
diff after it readable as a security change rather than hidden inside a mechanical one.

### The manifest holds a path that has no `$HOME`

`SystemFiles` and `SystemDirs` are separate arrays of absolute paths — deliberately not a
`Path` field that may be either with a flag saying which. The flag shape is the one that
must not be used: an older RESONANCE knows nothing about roots, so it would read
`"/etc/alsa/alsa.conf"` as `$HOME`-relative, join it onto `$HOME`, and restore into
`~/etc/alsa/alsa.conf` — silently, to the wrong place. Separate arrays make an old binary
**blind instead of wrong**: `encoding/json` drops fields it does not know, so it sees an app
with fewer files. That is the same lossy downgrade `Dirs` already documents.

Version-gating was not available as a defence. `loadManifest` reads `Version` and has never
checked it, so bumping `manifestVersion` locks nothing out.

Vault layout gets one reserved segment. `/etc/alsa/alsa.conf` lands at
`vault/<app>/.system/etc/alsa/alsa.conf`, because `vault/<app>/etc/...` would collide with a
literal `$HOME/etc/...`. It costs exactly one refusal — a home file whose path begins
`.system/` — and no migration, since nothing has ever written that name.

### The source side is entirely unprivileged

Adding, previewing, drift, checksums and diff all work as you, because most of `/etc` is
world-readable. `expandTrackedDir` is contained **per root**: `/etc` is full of symlinks that
leave it (`resolv.conf` → `/run`, `os-release` → `/usr/lib`), so a walk anchored to "any
allowed root" would carry a `/usr` file into an app that tracks `/etc`. Folder walks skip
symlinks outright as they always have, so those files are simply not tracked by a folder; an
explicitly chosen file still copies its target's content, which is the Stow rationale and
stays.

Readability is proved during validation, not discovered by the copy, so an unreadable file
fails the whole add rather than half-adding it and unwinding. The orphan scan maps `.system`
back to `SystemFiles` — without that, every backed-up system file reads as unaccounted-for
and the delete surface offers to destroy all of them.

**No elevated reads, in this release or as a plan.** The files under `/etc` you cannot read
are `shadow`, `gshadow`, `sudoers`, ssh host keys and `ssl/private`. Those are secrets, and
the vault's whole point is that it lives on a stick you carry around.

### A second binary, so the first one never has to be root

`cmd/resonance-helper` is launched once per session through `pkexec` and spoken to over a
pipe until the app exits. One password prompt per session, not one per file: with a folder
of forty tracked files the alternative is forty prompts, which is not an app anyone would
use. `auth_admin_keep` holds the authorisation briefly afterwards — the "act as
administrator" behaviour that was asked for.

A long-lived root process is the cost of that, and the answer is a helper small enough to
read in one sitting:

- **It never reads a path.** Not one operation returns file contents, and none takes a path
  to read from. Bytes are read by RESONANCE, as you, from a vault you can already read, and
  sent over. A privileged process that cannot be asked to open a caller-named file for
  reading cannot be turned into a way to read `/etc/shadow`. Descoping elevated reads is
  what bought this, and it is worth more than the three operations it removed.
- **Six operations and nothing else:** write, mkdir, remove, removeAll, symlink, rename.
- **Paths are proved inside the helper, at root**, because a check made in a process that is
  not the one doing the write is a check that can be raced. It walks down from a resolved
  root one component at a time and refuses any symlink standing where a folder should be, so
  `/etc/alsa` replaced by a link to `/root` redirects nothing. Files land via a temp file and
  a `rename`, which replaces a symlink at the destination instead of following it.
- **The allowed roots are compiled in**, with no flag and no environment override. The vault
  root is fixed by the opening message of a session and a second attempt to set it is
  refused.
- **It shares no package with the app.** The validation is a deliberate copy of
  `classifySource`, and the protocol types are a deliberate copy of each other, so the
  privileged half can be audited on its own without following an import into code that does
  not run as root.
- `json.Decoder` rather than a line scanner: `bufio.Scanner` caps a token at 64KB and would
  have failed only for large files and only in the field.

Honest about the trust boundary: the helper's allowlist is defence against **bugs**, not
against a hostile user — anyone who can answer the polkit prompt is already an
administrator. What it must never be is a way to do something the user did not ask for.

**polkit authorises a path, not a binary.** A helper sitting in `build/bin/` is not
`/usr/lib/resonance/resonance-helper`, so a development build cannot elevate at all. That is
the design working, not a fault, and the app says so in plain words rather than failing at a
prompt that never appears.

### A vault that belongs to root becomes a vault you can use

Six wrappers in `vaultfs.go`, not a filesystem layer under the backend. A vault owned by
root but still readable — `root:root 755`, the ordinary shape — refuses writes and nothing
else, so reads, drift scans, checksums, diffs and manifest loads stay plain `os` calls.

- **Writability is decided once per vault, not per failed write.** Falling back to elevation
  whenever a write happened to return `EACCES` would mean a vault of yours containing one
  folder someone `sudo`'d into root ownership quietly starting to raise password prompts
  mid-backup. Needing rights is a property of the vault you adopted.
- **Every path is rebased onto the resolved vault root before it is sent.** The helper pins
  `EvalSymlinks(vaultRoot)` at the start of a session, while the saved vault path is whatever
  was picked — and a vault on a removable drive is reached through `/run/media`, which is a
  symlink on plenty of systems. Without the rebase, our own helper would refuse every
  request. There is a test for exactly this.
- **The refusal became an offer.** `ProbeVaultPath` reports `NeedsAdmin` so Change Path can
  ask while you are still choosing, and `UseVaultPathWithAdmin` accepts. It proves the vault
  by landing a probe file through the helper and removing it again, so a vault is accepted
  because a write really worked — not because a mode bit suggested it would.

Two things a root-owned vault cannot do, both refused in words rather than discovered. It
cannot be **created**: a folder under a root-owned parent is outside the helper's reach,
which is the vault itself, so an elevated vault is one you adopt. And it cannot be **moved
away**, because Move ends by deleting the vault folder, which is a write to that folder's
parent. Copy does the same job and needs nothing, since reading a root-owned vault is free.

A vault **inside** `/etc` or `/usr` is now refused outright, and not over ownership: it would
be walked as part of the folder it sits in and back itself up, growing on every backup. That
collision exists only because those roots became places RESONANCE reads from.

"No chown" stayed ratified and was not reopened in either direction.

### Change Path stops reading backwards

"Choose New Folder" moves directly under the heading. It used to sit beneath the area its own
result fills, so the overlay read bottom-up: an empty space, and underneath it the button
that populates it. This was scoped to this release by the maker himself rather than settled
twice, because the root-owned-vault offer lands in the same overlay — and it does, as one
sentence that every button below inherits instead of repeating.

The Add overlay counts system **files**, not just system folders: picking one file out of
`/etc` adds no folder at all and still cannot be put back without rights. The restore
confirmation says a password dialog is coming before it comes — a prompt appearing
unannounced mid-operation is the kind of thing people cancel out of reflex, which leaves the
restore half-done for no reason worth having. Row and folder labels needed nothing: a system
path is stored absolute, so it already renders as `/etc/alsa/alsa.conf` beside its
`~/`-relative neighbours.

### Packaging

Both packages now install the helper to `/usr/lib/resonance/resonance-helper` (root-owned,
0755 — `pkexec` refuses a helper that is not) and the policy to
`/usr/share/polkit-1/actions/xyz.namli.resonance.policy`. CI builds the helper explicitly,
because `wails build` knows nothing about a second main package, and ships both binaries in
one artifact. `depends` gains `polkit` on Arch; `Depends` gains `pkexec | policykit-1` on
Debian — the alternative is deliberate, since `policykit-1` is the old name and transitional
on current Debian/Ubuntu.

---

## Verified, and what could not be

`go vet`, `go test -count=1 ./...` (both packages), `gofmt`, `npx tsc --noEmit` and
`wails build -tags webkit2_41` are all clean. The helper's own tests drive the real binary
over a real pipe, unprivileged, through a seam `pkexec` would otherwise hold shut — one of
them caught a genuine asymmetry where the elevated write created the folders above a file
and the direct one did not, which would have made the same call succeed or fail depending on
who owns the vault.

**What no test on this machine can cover: the elevated paths themselves.** polkit authorises
`/usr/lib/resonance/resonance-helper`, so becoming root requires an installed package. The
round trip — back up `/etc/alsa/alsa.conf`, edit it as root, see the drift badge, diff it,
restore it through the prompt, undo the restore — has to be walked from a real install.

---

## Not in this release

- **Elevated reads.** `/etc/shadow` and friends are secrets and stay out of a vault you carry
  around.
- **A vault that is unreadable to you**, not merely unwritable. It would put the whole
  backend behind root and does nothing for `/etc` or `/usr`.
- **Creating a vault inside a root-owned folder**, and **Copy/Move into one.** Adopting an
  existing one works.
- **Moving a root-owned vault away.** Copy does the same job and destroys nothing.
- **`chown`ing an adopted root-owned vault** — ratified out, and not reopened.
- **`/run`**, dropped during planning.
- The v1.2.1 audit's remaining 19-gap / 11-dead-end list, still not walked line by line.
