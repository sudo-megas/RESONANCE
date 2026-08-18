# RESONANCE — CORE.md

**The project constitution. Everything in this document is ratified by the maker. Nothing here may be changed, reinterpreted, or "improved" without his explicit permission.**

Document version 1.1 — 2026-08-11 (amended by STEP2, 2026-08-11)
Location: `/home/megas/RESONANCE/build/docs/CORE.md`

---

## 1. IDENTITY

**Name:** RESONANCE — chosen over "Echo". The symbolism: one config file resonating to another, at full strength, byte-identical. Resonance = mirroring, and the app's entire interface is built on that idea.

**What it is:** a tiny, fully local configuration backup & restore GUI for Linux — your dotfiles, and the system configuration under `/etc` and `/usr`. Three functions and only three functions: *(Widens the original "dotfiles" wording. Backing a system file up needs no rights at all; putting one back asks for administrator rights, which is the only privileged operation in the program. Added v1.3.0, 2026-08-18. Amendment deployed by megas, 2026-08-18.)*

1. **Backup** — copy chosen config files into a vault.
2. **Store** — keep them, show them, keep them fresh.
3. **Restore** — copy them back out to their relative places, on this machine or another.

**Why it exists:** GNU Stow is astronomically complicated for a non-developer. Its CLI is a second obstacle. Its repo-based workflow feels cloud-ish. RESONANCE is the opposite on all three counts: GUI, simple, local.

**Small does not mean primitive.** The app is intentionally tiny in scope and intentionally modern in appearance.

**Family:** RESONANCE is a member of the sudo-megas app family (JADEITE, INDIUM, PARACHRON, SAAT, ...). Family conventions for README, About page, and release discipline apply.

**Maker:** sudo-megas. All authorship is his.

---

## 2. STACK

| Layer | Choice | Notes |
|---|---|---|
| Backend language | **Go** | All file operations, manifest logic, machine info. |
| GUI framework | **Wails v2** | Single window. System webview (webkit2gtk). |
| Frontend | HTML / CSS / TypeScript | Vanilla-TS Wails template — no frontend framework. |
| Storage | **JSON** | Manifest and settings. No database, ever. "New language, new environment." |
| Licence | **GPL-3.0-or-later** | Deliberate: the first family app not on GPL-3.0-only. |
| Typography | **CaskaydiaCove Nerd Font** (chrome/labels/headings) + **CaskaydiaMono Nerd Font** (paths, technical strings) | Self-hosted `.ttf`, `frontend/src/assets/fonts/`, `@font-face` in `theme.css`. Zero network — see §5. Added STEP2. |
| Iconography | **Nerd Font glyphs** (Font Awesome patch set, Private-Use-Area codepoints) from the same bundled fonts | No second icon asset. Exact glyph codepoints confirmed at implementation time. Added STEP2. |

*Amendment deployed by megas, 2026-08-11.*

Considered and dropped: C#/Avalonia (dropped by the maker), Tauri (runner-up), Qt/QML, egui, C, Ruby, Lua.

---

## 3. THE MIRROR LAYOUT (decided, not open)

No sidebar. JADEITE and INDIUM share ~80% of a layout; RESONANCE deliberately does not.

- **Two panes facing each other.** Left = **SYSTEM** (live files on disk). Right = **VAULT** (stored copies).
- **A central spine** between them carries all action. Direction is meaning: **→ Update** flows system-to-vault (backup), **← Restore** flows vault-to-system.
- **Every app is a horizontal row** spanning both panes. Rows expand accordion-style into per-file rows: source dates on the left, vault dates + "last updated DD MM YYYY" on the right, drift state in the middle.
- **Drift badges** glow on any row where the two sides no longer match (checksum-based).
- **Single window.** All secondary views are overlays that dim the mirror and expand from the center outward — like a wave. One overlay grammar serves: diff view, restore preview, machine-info card, add-app flow, theme picker, About.
- **Top bar:** app logo left (family convention), vault path selector, theme button, About button.
- **No empty state, ever.** The mirror's chrome — top bar, SYSTEM/spine/VAULT panes — looks and behaves identically whether 0 or 500 apps exist. No centered logo, no "add your first app" call-to-action, anywhere. *(Amends the original "Empty state" line — STEP2, 2026-08-11. Amendment deployed by megas, 2026-08-11.)*
- **The spine carries a persistent "＋".** Pinned near the top, visible from first launch onward, opens the add-app overlay. This is the one confirmed exception to this section's "decided, not open" header — offered a footer-row and a topbar-icon alternative, the maker chose the spine. *(Added STEP2, 2026-08-11. Amendment deployed by megas, 2026-08-11.)*

---

## 4. FEATURE SPEC (scope of v1.0.0)

- **Per-APP organization.** The main window lists APP entries (e.g. `bash`). Each app holds as many files as the user wants (`~/.bashrc`, `~/.bash_history`, ...). Never a flat file dump.
- **The vault.** Path is user-choosable — any path: another HDD, a USB drive, anywhere. The vault **never modifies original files**. It copies byte-identical into its own directory in a relative layout, only when the user provokes it (add or "update from source (now)").
- **Relative paths.** The manifest stores `$HOME`-relative paths, never absolute `/home/<name>/...`. At restore time `$HOME` resolves against the *current* machine's user. This is the designed answer to the username caveat: backed up by `megas@mainrig`, restorable by any user on any machine. **Files under `/etc` and `/usr` are stored absolute, in their own manifest arrays.** The rule's intent survives intact: it exists to solve the username caveat, and `/etc` and `/usr` are the same on every machine and have no per-user form — storing them absolute is what keeps them portable, not what breaks it. They are separate arrays rather than a flag on `Path` so that an older RESONANCE, which knows nothing about roots, is blind to them instead of joining `/etc/alsa/alsa.conf` onto `$HOME` and restoring to the wrong place. *(Added v1.3.0, 2026-08-18. Amendment deployed by megas, 2026-08-18.)*
- **Restore** is the copy reversed onto the destination machine.
- **Overwrite rules.** Backup overwrites the vault from source. Restore overwrites the destination. Before either happens, the user is shown the file diff **and both files' created/modified dates**. The final decision is always the user's.
- **Pre-restore snapshot.** Before a restore overwrites anything, the current files are tucked aside automatically. One "Undo restore" button brings them back.
- **Drift badges.** A checksum stored at backup time; the UI shows "changed since backup" wherever the live file has drifted.
- **Restore preview.** Before committing: how many files will be overwritten, how many are new, how many are identical and skipped.
- **Machine info.** The vault records the backupper machine: kernel, OS, hostname, username. The restorer sees it before restoring.
- **Themes.** JADEITE's theme set, default **"Default Dark"**, plus one newcomer on top of all of them: **"Ubuntu Aubergine Canonical"** — which JADEITE does not have.
- **About** follows the family convention: maker, version, release date, source address, full licence. Addresses are selectable text but **not clickable** — the app opens no browser and follows no link, by design.
- **App logo** is present in the main window like the other family apps.

---

## 5. BANS

- **No AI trailers** in commits, tags, or anywhere else. Ever.
- **No developer-style README explanations.** The README is a simple user README, family style.
- **No network. None.** No cloud save, no update check, no telemetry, no analytics, no crash reporting. The app never opens a URL.

## 6. EXPLICITLY NOT A PROBLEM

- **Dependency count** — dependencies are welcome as long as each one is useful somewhere and doing real work.
- **Final binary size** — irrelevant. However many MB it is, it is.

## 7. HAVE-TOs

- Simple user README, exactly in the family style.
- Commits are pushed **only** from the sudo-megas GitHub account.
- **No system locale detection.** English only. No second language.
- Theme roster as specified in §4.
- Packaging: **Debian + Arch** (`.deb` + `.pkg.tar.zst`/`.pacman`). The rest is never gonna happen — no Windows, no AppImage, no Flatpak, no Snap.
- **Claude / claude-code improvisations are realized only after asking the maker and receiving permission.** This binds every Claude, in chat and in the terminal, equally.
- App icon is supplied by the maker at `/home/megas/RESONANCE/build/icons/`.

---

## 8. VERSIONING & RELEASES

- One STEP document = one minor version: STEP1 → **v0.1.0**, STEP2 → **v0.2.0**, and so on.
- Unexpected fix releases patch the third digit: vX.X.1, vX.X.2, ...
- **Local until v1.0.0.** STEP1/v0.1.0 was pushed and manually published as a GitHub pre-release before this rule was set. From STEP2 onward, every STEP is committed locally only — no push, no tag, no GitHub release — all the way through v0.9.x. *(Amendment deployed by megas, 2026-08-11.)*
- **At v1.0.0, release moves to CI.** A GitHub Actions pipeline handles build + release at that point, replacing the manual `gh release create` process used for v0.1.0. *(Amendment deployed by megas, 2026-08-11.)*

## 9. THE STEP LADDER (approved 2026-08-11)

| Step | Release | Contents |
|---|---|---|
| STEP1 | v0.1.0 | Wails scaffold, mirror-layout shell, theme system (Default Dark + Ubuntu Aubergine Canonical), logo in window, About overlay |
| STEP2 | v0.2.0 | Vault path chooser, JSON manifest, add app + files, first working backup |
| STEP3 | v0.3.0 | "Update from source (now)", drift badges, date displays |
| STEP4 | v0.4.0 | Restore with `$HOME` remap, restore preview, diff overlay, machine-info card |
| STEP5 | v0.5.0 → **v1.0.0** | Pre-restore snapshot + undo, full JADEITE palette set, polish, packaging (.deb + Arch) |

**STEP docs are written just-in-time** — one per step, the next written only when the previous has shipped, informed by what building it taught. Every STEP doc requires the maker's approval before work starts. All STEP docs live in `/home/megas/RESONANCE/build/docs/`.

---

Copyright © sudo-megas
*Built with Reason and Passion.*
