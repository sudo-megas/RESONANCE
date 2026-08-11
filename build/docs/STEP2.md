# RESONANCE — STEP2.md → v0.2.0 "The Vault"

**Goal:** the vault becomes real. A guided, mandatory, migration-aware path chooser; a JSON manifest; an add-app flow that performs RESONANCE's first actual byte-identical backup; and the mirror's chrome finally looks the same whether it holds 0 apps or 50. No drift, no dates, no restore yet — those are STEP3 and STEP4. When this document's checklist is green, commit locally as v0.2.0 — per CORE.md §8, nothing pushes or tags until v1.0.0.

Location: `/home/megas/RESONANCE/build/docs/STEP2.md`
Approved by maker: sudo-megas, 2026-08-11

---

## 0. PREREQUISITES — main rig (CachyOS) and laptop (Arch)

```bash
go get github.com/rymdport/portal@latest
go mod tidy
```

No new system packages are required for the dependency itself — `rymdport/portal` is pure Go, talking to D-Bus via the already-indirect `github.com/godbus/dbus/v5`.

**Gotcha, pre-warned:** the XDG Desktop Portal file-chooser call needs a running portal backend that implements `org.freedesktop.impl.portal.FileChooser` — `xdg-desktop-portal` itself plus something like `xdg-desktop-portal-gtk` registered for your session. Niri ships neither; this is a session-level install, not something RESONANCE brings with it. Quick sanity check before writing any Go: if Flatpak apps on this machine already show a native-looking file picker, the portal is already working and nothing new needs installing. If not, that's a one-time environment fix, not a RESONANCE bug.

Fonts: download the current `CascadiaCode.zip` from `ryanoasis/nerd-fonts` GitHub releases. It contains both **CaskaydiaCove Nerd Font** (ligature variant) and **CaskaydiaMono Nerd Font** (no-ligature variant) as `.ttf` files. Confirm the exact filenames inside the archive at download time (weight naming varies slightly by release); Regular + Bold of each is enough for STEP2. Place them at `frontend/src/assets/fonts/`.

---

## 1. THE VAULT PATH — GUIDED, MANDATORY, MIGRATION-AWARE

Replaces STEP1's inert `vault-path` span entirely.

**Settings extended** (`settings.go`):

```go
type Settings struct {
    Theme     string `json:"theme"`
    VaultPath string `json:"vaultPath"`
}
```

`GetSettings()` defaults `VaultPath` to `""` when unset — an empty string is the signal "no vault chosen yet" everywhere in this document. `SaveSettings` itself stays a dumb full-overwrite write (that's correct Go: predictable, no hidden merge logic). The RMW discipline lives on the **caller** side: every frontend call to `SaveSettings` must start from a fresh `GetSettings()` and spread over it, never construct a partial object. **One existing violation to fix as part of this STEP:** `theme-picker.ts`'s `selectTheme` currently calls `SaveSettings({ theme: id })` — a bare partial object. The day `VaultPath` exists, that line silently wipes it on every theme switch. Fix:

```ts
async function selectTheme(id: string): Promise<void> {
  applyTheme(id);
  closeOverlay();
  try {
    const current = await GetSettings();
    await SaveSettings({ ...current, theme: id });
  } catch (err) {
    console.error(err);
  }
}
```

Grep the frontend for every `SaveSettings(` call site before calling this DoD item done — there must be exactly the ones this document introduces, and every one of them RMW.

**First launch (no `VaultPath` saved):** RESONANCE does not passively wait for the maker to notice a control. On startup, if `GetSettings().vaultPath === ""`, an overlay opens automatically — reusing the existing overlay component, not a new modal mechanism — prompting for a vault folder. This overlay is **non-dismissable**: Escape and backdrop-click do nothing until a path is actually chosen. That requires one small, still domain-free addition to `overlay.ts`:

```ts
export interface OverlayOptions {
  onClose?: () => void;
  dismissable?: boolean; // default true; false disables Escape and backdrop-click
}
```

`openOverlay` stores this flag; `handleKeydown` and the scrim's click handler both check it before calling `closeOverlay()`. This keeps `overlay.ts` a pure UI primitive (it still doesn't know what a "vault" is) while making a genuinely mandatory step possible without inventing a second overlay mechanism.

The window itself is never blocked from rendering — chrome (top bar, SYSTEM/spine/VAULT panes) paints immediately per §4 below; the vault prompt is just the first overlay that happens to auto-open on top of it. If the maker cancels the portal's own folder dialog, the prompt stays open with a small inline line — *"No folder chosen yet — RESONANCE needs one to continue."* — and lets them try again. Nothing else in the app is blocked by this except the add-app flow (see §5).

**Once chosen, it's persisted.** `VaultPath` in `settings.json`, survives relaunch.

**Change Path.** The top bar's vault control is reframed — not generic "vault path" language, literally the button reads **"Change Path"**, always present, always clickable, first-launch or the thousandth:

```html
<div class="vault-control">
  <span class="vault-path-display" id="vault-path-display" title=""></span>
  <button class="text-btn" id="vault-path-btn" type="button">Change Path</button>
</div>
```

`vault-path-display` shows the live path, truncated with CSS ellipsis, full path in `title=` on hover, rendered in CaskaydiaMono (§6). The button label itself never changes — "Change Path" is correct both for first-time setup and every subsequent change, so there's no dynamic-label state to get wrong.

**The critical new behavior — copy/move/adopt.** Picking a *different* folder than the currently-saved one no longer just silently re-reads whatever's there — apps would appear to vanish, which is close to the worst failure mode for backup software to leave unhandled. Every folder picked via Change Path is probed first:

```go
type VaultProbe struct {
    HasManifest bool `json:"hasManifest"`
    IsEmpty     bool `json:"isEmpty"`
    AppCount    int  `json:"appCount"`
}

func (a *App) ProbeVaultPath(path string) (VaultProbe, error)
```

Three branches, decided in the frontend from the probe result:

1. **Target already has its own `manifest.json`** (a real scenario per CORE.md §4 — "another HDD, a USB drive, anywhere" implies pointing at an *existing* vault, not only fresh folders). Offer **"Use this vault"** — `AdoptVaultPath(newPath)` just points `Settings.VaultPath` at it. Zero files move. The old vault is left completely untouched at its old location.
2. **Target is empty, no manifest.** Offer **Copy** or **Move** of the current vault into it. See migration mechanics below.
3. **Target is neither empty nor a vault** (random non-empty folder — someone else's files). **Refused**, with a clear inline message: *"This folder isn't empty and isn't a RESONANCE vault. Choose an empty folder, or one that already has a RESONANCE vault."* Nothing is touched. This is the deliberate, justified choice over a merge strategy: merging two independently-evolved manifests (potential app-name collisions, no checksums yet in STEP2 to arbitrate conflicts) is real complexity for a scenario the maker can trivially avoid by picking a different folder — refusing outright is simpler, safer, and honest about what STEP2 can and can't reconcile.

**Copy mechanics** (`migration.go`, new file):

```go
func (a *App) CopyVaultTo(newPath string) error { return a.migrateVault(newPath, false) }
func (a *App) MoveVaultTo(newPath string) error { return a.migrateVault(newPath, true) }

func (a *App) migrateVault(newPath string, remove bool) error {
    settings := a.GetSettings()
    oldPath := settings.VaultPath
    if oldPath == "" { return errors.New("no current vault to migrate") }
    if newPath == oldPath { return errors.New("new path is the same as the current vault path") }

    probe, err := a.ProbeVaultPath(newPath)
    if err != nil { return err }
    if probe.HasManifest { return errors.New("target already contains a vault — use Adopt instead") }
    if !probe.IsEmpty    { return errors.New("target folder is not empty — choose an empty folder") }

    // 1. Copy everything first. Old vault untouched throughout.
    if err := copyTree(oldPath, newPath); err != nil {
        return fmt.Errorf("copy to new vault failed, old vault untouched: %w", err)
    }
    // 2. Verify before trusting the copy enough to switch or delete anything.
    if err := verifyTreesMatch(oldPath, newPath); err != nil {
        return fmt.Errorf("copy verification failed, old vault untouched, new path left for inspection: %w", err)
    }
    // 3. Only now does Settings point at the new path.
    settings.VaultPath = newPath
    if err := a.SaveSettings(settings); err != nil { return err }
    // 4. Move only: delete old contents, now that new is confirmed good and active.
    if remove {
        if err := os.RemoveAll(oldPath); err != nil {
            return fmt.Errorf("vault switched to new path, but could not remove old vault at %s: %w", oldPath, err)
        }
    }
    return nil
}
```

Never delete-then-copy. The ordering is always **copy → verify → switch settings → (move only) delete old**, so a failure at any point leaves the maker's data either fully in the old location or fully verified in the new one — never neither. Verification (`verifyTreesMatch`) re-walks both trees and compares file presence + size; it deliberately does **not** checksum, since checksums aren't part of STEP2's manifest schema (that's STEP3's job) — size-match is the right-sized check for this step, not a shortcut past a real one.

---

## 2. JSON MANIFEST & VAULT STORAGE LAYOUT (`manifest.go`, new file)

- `<vault>/manifest.json`: `{ "version": 1, "apps": [ { "name": "bash", "files": [ { "path": ".bashrc" } ] } ] }`. Forward-shaped: STEP3 adds checksum/date fields to `ManifestFile` without touching this shape.
- Vault storage layout: `<vault>/<app-name>/<$HOME-relative-path>` — the same relative string is both the vault storage path today and the restore destination in STEP4.
- `loadManifest(vaultPath string) (Manifest, error)` distinguishes "vault directory itself doesn't exist" (a real error — unmounted drive, bad saved path) from "directory exists, no `manifest.json` yet" (a fresh, valid, empty vault — returns `Manifest{Version: 1, Apps: []ManifestApp{}}`, not an error).
- `saveManifest`, `homeRelative`, `validAppName`, `copyFile` — pure data/IO, no picker or migration logic in this file.
- `validAppName` rejects empty names, path separators, and — case-insensitively — the reserved name `manifest.json`, which would otherwise collide with `<vault>/manifest.json` itself if an app were ever named that.

---

## 3. FILE & FOLDER PICKERS — XDG DESKTOP PORTAL (`vault.go`, new file)

Wails v2.14.0's Linux dialogs call plain `gtk_file_chooser_dialog_new()` directly (confirmed against its C source) — never the portal, regardless of what's installed. On the maker's actual desktop (Niri + Noctalia, no GTK/Qt dependency, no bundled portal) that dialog can render inconsistently or not match the session's chosen file-manager integration at all. Forking Wails to swap in `GtkFileChooserNative` was considered and rejected — it would create an ongoing personal maintenance burden the maker is deliberately avoiding. Hand-rolling the raw `org.freedesktop.portal.FileChooser` D-Bus protocol (async `Response` signal, handle-token race) was also considered and rejected for the same reason. Both alternatives were real options; the maker chose the third: a real, maintained dependency.

**`github.com/rymdport/portal/filechooser`** — Apache-2.0 (GPL-3.0-or-later compatible), used by the Fyne toolkit since v2.5.0:

```go
type OpenFileOptions struct {
    HandleToken   string
    AcceptLabel   string
    NotModal      bool
    Multiple      bool
    Directory     bool   // folder-select mode
    Filters       []*Filter
    CurrentFilter *Filter
    Choices       []*ComboBox
    CurrentFolder string
}
func OpenFile(parentWindow, title string, options *OpenFileOptions) ([]string, error)
```

```go
package main

import (
    "fmt"
    "net/url"

    "github.com/rymdport/portal/filechooser"
)

func uriToPath(uri string) (string, error) {
    u, err := url.Parse(uri)
    if err != nil { return "", err }
    if u.Scheme != "file" { return "", fmt.Errorf("unsupported URI scheme: %s", u.Scheme) }
    return u.Path, nil // url.Parse already percent-decodes
}

// ChooseVaultPath opens the portal folder picker. Returns "" (not an error)
// if the user cancels — on cancel the library's internal readURIFromResponse
// returns (nil, nil), so an empty result and a nil error is what
// "cancelled" looks like here, not a distinguishable error.
func (a *App) ChooseVaultPath() (string, error) {
    uris, err := filechooser.OpenFile("", "Choose vault folder", &filechooser.OpenFileOptions{
        Directory: true,
    })
    if err != nil { return "", err }
    if len(uris) == 0 { return "", nil }
    return uriToPath(uris[0])
}

// PickFiles opens the portal multi-select file picker. Returns nil (not an
// error) if cancelled.
func (a *App) PickFiles() ([]string, error) {
    uris, err := filechooser.OpenFile("", "Choose files to back up", &filechooser.OpenFileOptions{
        Multiple: true,
    })
    if err != nil { return nil, err }
    paths := make([]string, 0, len(uris))
    for _, u := range uris {
        if p, err := uriToPath(u); err == nil {
            paths = append(paths, p)
        }
    }
    return paths, nil
}
```

**Gotcha, pre-warned:** `OpenFile`'s first argument is a parent-window handle string; Wails gives no portable way to obtain one on Linux. Pass `""` (unparented). The dialog will work but may not be strictly modal to the RESONANCE window — acceptable for STEP2, revisit only if it's genuinely annoying in daily use.

**`AddApp` and `ListApps`** live in `vault.go` too, since they're the orchestration layer sitting directly on top of the pickers and `manifest.go`. `AddApp` validates every picked path (exists, is a regular file, resolves to a `$HOME`-relative path) *before* copying any of them — a validation failure on file 3 of 5 must not leave files 1–2 already copied onto disk with no manifest entry pointing at them.

This is RESONANCE's first working backup, exactly per CORE.md §4: files are copied byte-identical into the vault, only when provoked, never touching the originals.

---

## 4. NO EMPTY STATE, EVER — THE SPINE GETS A "＋"

The mirror's chrome — top bar, SYSTEM/spine/VAULT panes — looks and behaves identically at 0 apps and at 50. STEP1's `#empty-state` block (the centered logo + "＋ Add your first app" button) is **deleted**, not hidden — along with its CSS (`.empty-state`, `.add-app-btn` in `layout.css`) and its listener in `main.ts` (the `add-app-btn` → `showToast("Coming in v0.2.0")` line). None of it survives into STEP2.

The only persistent way to add an app is a small "＋" pinned near the top of the spine, present from first launch onward — never gated on "at least one app already exists":

```html
<div class="spine" id="spine">
  <button class="spine-add-btn" id="add-app-btn" type="button" aria-label="Add app">＋</button>
</div>
```

(`aria-hidden="true"` is removed from `.spine` — it can't stay on a container holding a real interactive control.)

**Gotcha, pre-warned — flagged maker-confirmed deviation:** CORE.md §3 calls the mirror layout "decided, not open," reserved for future → Update / ← Restore direction semantics. Putting a "＋" there touches that reserved territory. The maker was asked directly, offered two alternatives (a footer row spanning both panes, or a topbar icon button), and explicitly chose the spine. This is the one confirmed exception to §3's framing — see CORE.md's own amendment note.

**Zero apps, zero drama.** If the vault genuinely has no apps yet, the panes are just empty — no distinct "mode," no special screen. A small muted hint is acceptable inside each pane body (`--text-dim`, small type, sits exactly where the first row would render — not centered, not dominant, doesn't change pane structure or padding):

```html
<p class="pane-hint">No apps yet.</p>
```

`renderRows(apps: ManifestApp[])` (`rows.ts`, new file) renders this hint when `apps.length === 0` and otherwise renders one simple row per app into **both** `#system-body` and `#vault-body` — same content on each side for STEP2, since a file that was just backed up is by definition identical on both sides; drift-aware divergence is STEP3's job. Rows are name + file-count only, no accordion:

```html
<div class="app-row"><span class="app-row-name">bash</span><span class="app-row-count">2 files</span></div>
```

*Design note:* CORE.md §3 describes "every app is a horizontal row spanning both panes" — full single-DOM-row-spanning-both-panes architecture (needed once accordion/drift chrome exists) is deferred to STEP3. STEP2 renders twin lists with matched CSS row height as a visual approximation, which is enough for name+count chrome and avoids building STEP3's data-synchronization machinery a step early.

---

## 5. THE ADD-APP OVERLAY (`addapp.ts`, new file)

Reuses the overlay grammar (dismissable, this one — not the mandatory vault prompt). Flow: name input (client-side validated against the same rule as `validAppName`, server is still authoritative) → "Choose files…" button calling `PickFiles()` → chosen files listed (deduplicated by absolute path — repeatedly clicking "Choose files" and re-picking the same file doesn't create a duplicate entry), each removable → "Add" button, disabled until name is valid and ≥1 file is chosen. On submit, call `AddApp(name, absPaths)`; on success, close, `showToast("Added <name>")`, re-fetch `ListApps()` and re-render rows; on error (duplicate name, reserved name, anything `AddApp` rejects), show the error inline and keep the overlay open.

If the spine "＋" is clicked before any vault path exists, it does **not** open this overlay — it re-opens the vault-path prompt from §1 instead, since `AddApp` requires a vault path.

---

## 6. TYPOGRAPHY — BUNDLED NERD FONTS

Both CaskaydiaCove and CaskaydiaMono are monospace; this is a deliberate whole-app monospace aesthetic, not a body/code split. The split that *is* meaningful: **CaskaydiaCove** (ligatures) for general UI chrome — labels, headings, buttons, the app name; **CaskaydiaMono** (no ligatures) specifically for raw technical strings — the vault path display, the file-path list in the add-app overlay — where ligature substitution on a literal filesystem path would be actively misleading. Declared as tokens, matching the existing color-token discipline:

```css
--font-ui: "CaskaydiaCove Nerd Font", monospace;
--font-mono: "CaskaydiaMono Nerd Font", monospace;
```

**No fourth CSS file.** `@font-face` rules and the two tokens above go directly into `theme.css` (the file that already owns `html, body { font-family: ... }`), keeping the CSS file count at exactly three. `html, body`'s `font-family` switches from the system stack to `var(--font-ui)`; `.vault-path-display` and the add-app file list get `font-family: var(--font-mono)` explicitly.

```css
@font-face {
  font-family: "CaskaydiaCove Nerd Font";
  src: url("../assets/fonts/CaskaydiaCoveNerdFont-Regular.ttf") format("truetype");
  font-weight: 400;
  font-display: swap;
}
@font-face {
  font-family: "CaskaydiaMono Nerd Font";
  src: url("../assets/fonts/CaskaydiaMonoNerdFont-Regular.ttf") format("truetype");
  font-weight: 400;
  font-display: swap;
}
```

Files live at `frontend/src/assets/fonts/*.ttf`. Vite (the Wails vanilla-ts template's bundler) resolves `url()` references in CSS at build time and copies the referenced assets into `frontend/dist`; `main.go`'s existing `//go:embed all:frontend/dist` already embeds everything under `dist` into the Go binary — the fonts ride along automatically, no new `//go:embed` directive needed. Self-hosted, zero network, exactly per CORE.md §5. Nerd Font files are large; per CORE.md §6, final binary size is explicitly not a problem — one-line acknowledgment, not a blocker.

---

## 7. ICONOGRAPHY — NERD FONT GLYPHS

Nerd Fonts patch Font Awesome glyphs into the font's own Private-Use-Area range (roughly `U+ED00`–`U+F2FF`). Since CaskaydiaCove/Mono are already bundled for typography, RESONANCE's four current placeholder glyphs — `◐` (theme), `?` (About), `＋` (add-app/spine), `✕` (overlay close) — should be swapped for the equivalent Font Awesome PUA glyphs from the same font, rather than shipping a second icon bundle. The CSS-drawn ring logo mark is unaffected — it stays pure CSS.

**Not fabricated as fact:** exact PUA codepoints for these specific icons are not asserted here. **Verify at implementation time** via `nerdfonts.com/cheat-sheet` (searchable by name) against the actual bundled font file, then hardcode the confirmed `\uXXXX` values as plain string literals where the code currently uses `◐`/`?`/`＋`/`✕` — no architecture change, just a character swap. `aria-label`s on every button stay exactly as they are; PUA glyphs mean nothing to a screen reader. If any one of the four turns out missing from the patch set, keep that one icon's current plain-Unicode character rather than adding a second font bundle for a single glyph.

Do not adopt a full icon-webfont CSS-class system for four textContent swaps — that's more machinery than this step's icon count needs.

---

## 8. STATUS BAR — DEFERRED

Explicitly not decided in STEP2. Deferred to STEP3, once drift/date data exists to put in it. No design work happens now.

---

## 9. DEFINITION OF DONE — v0.2.0 checklist

- [x] `go get github.com/rymdport/portal` added, `go mod tidy` clean, `wails dev -tags webkit2_41` still runs on the main rig
- [x] CaskaydiaCove + CaskaydiaMono `.ttf` bundled at `frontend/src/assets/fonts/`, loaded via `@font-face` in `theme.css`, zero network requests at runtime
- [x] Mirror chrome (top bar, SYSTEM/spine/VAULT panes) is structurally identical at 0 apps and at many apps — `#empty-state` and its CSS/listener are gone from the codebase, not hidden
- [x] Spine "＋" visible and clickable from first launch, before any vault path is chosen
- [x] First launch with no saved vault path auto-opens the mandatory vault-path overlay; Escape/backdrop-click do not dismiss it; it closes only once a valid path is chosen
- [x] Chosen vault path persists in `settings.json` as `vaultPath`, survives relaunch
- [x] "Change Path" always available in the top bar, shows current path in CaskaydiaMono
- [x] Change Path → empty, non-vault folder → offers Copy/Move; Copy leaves old vault fully intact and independently usable; Move deletes old vault contents only after the new path is verified
- [x] Change Path → folder with its own `manifest.json` → offers Adopt, zero files move, old vault untouched
- [x] Change Path → non-empty, non-vault folder → refused with a clear message, nothing touched
- [x] Cancelling any portal picker (folder or files) is a silent no-op everywhere — no error toast, no crash
- [x] Add-app: pick files, name the app, files land byte-identical at `<vault>/<app-name>/<$HOME-relative-path>`, `manifest.json` updates, new row appears in both panes without relaunch — RESONANCE's first working backup
- [x] Apps/rows survive relaunch, loaded from `manifest.json`
- [x] Reserved name `manifest.json` and duplicate app names both rejected with clear errors, no crash
- [x] Every `SaveSettings(` call site in the frontend confirmed RMW (including the `theme-picker.ts` fix)
- [x] Theme switching still persists correctly (regression check)
- [x] All new CSS uses existing `var(--token)`s only; still exactly 3 CSS files
- [x] `wails build -tags webkit2_41` produces a working binary on the main rig
- [x] Committed locally, no AI trailers anywhere — per CORE.md §8's updated release policy, STEP2 stays **local only**: no push, no tag, no GitHub release until v1.0.0, when a GitHub Actions pipeline takes over the release step

## 10. EXPLICITLY OUT OF STEP2

Status bar of any kind (deferred to STEP3, once drift/date data exists). Drift badges, checksums, "update from source (now)", date displays (STEP3). Restore of any kind, `$HOME` remap on restore, restore preview, diff overlay, machine-info card (STEP4). Pre-restore snapshot/undo, remaining JADEITE palettes, packaging (STEP5). Accordion/per-file expand rows (STEP3, once there's drift/date data worth expanding into). A supplementary non-Nerd-Font icon bundle (only if implementation-time glyph verification finds real gaps). Checksum-based migration verification (STEP2 uses size-match; full checksums arrive naturally with STEP3's manifest fields).

---

**When the checklist is green:** commit v0.2.0 locally, then STEP3.md gets written — informed by whatever building the vault taught.

Copyright © sudo-megas
*Built with Reason and Passion.*
