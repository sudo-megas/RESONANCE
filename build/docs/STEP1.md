# RESONANCE — STEP1.md → v0.1.0 "The Shell"

**Goal:** a running RESONANCE window that already *looks* like RESONANCE — mirror layout skeleton, working theme system (Default Dark default + Ubuntu Aubergine Canonical), logo in the window, complete About overlay, theme choice persisting across restarts. **No vault logic yet.** When this document's checklist is green, tag v0.1.0 and publish it as a GitHub pre-release.

Location: `/home/megas/RESONANCE/build/docs/STEP1.md`
Approved by maker: sudo-megas, 2026-08-11

---

## 0. PREREQUISITES — main rig (CachyOS) and laptop (Arch)

Same commands on both machines:

```bash
sudo pacman -S --needed go nodejs npm gtk3 webkit2gtk-4.1 base-devel pkgconf
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

**Gotcha, pre-warned:** `go install` drops the binary into `~/go/bin`, which is *not* on PATH by default. Add it once:

```bash
# bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.bashrc && source ~/.bashrc
# fish
fish_add_path ~/go/bin
```

**Gotcha, pre-warned:** Arch ships WebKitGTK as `webkit2gtk-4.1`, but Wails v2 looks for the older 4.0 API by default. This is not a failure — it's a known Arch/Wails quirk with a one-flag fix: every `wails dev` and `wails build` in this project carries `-tags webkit2_41`. The commands in this document already include it.

Then verify:

```bash
wails doctor
```

Everything under "Dependencies" should be green (ignore optional packagers like nsis — Windows is never gonna happen).

**A note so it doesn't look like a violation later:** the *toolchain* (`pacman`, `go install`, `npm`) uses the network to fetch build dependencies. The BAN in CORE.md §5 is about the shipped app: RESONANCE itself performs zero network activity at runtime. Building offline-capable software still requires downloading a compiler.

---

## 1. SCAFFOLD

```bash
cd ~/dev        # or wherever the repo will live
wails init -n resonance -t vanilla-ts
cd resonance
```

Binary and module name are lowercase `resonance`; the displayed app title is RESONANCE.

Immediately create the family structure:

```bash
mkdir -p build/docs build/icons
# CORE.md and STEP1.md go into build/docs/
```

**Gotcha, pre-warned:** Wails has its own opinion about `build/` — it expects the app icon at `build/appicon.png` for packaging. Our rule (CORE.md §7) is that the maker supplies icons at `build/icons/`. Both are true: the maker's PNGs live in `build/icons/`, and the chosen one is *copied* to `build/appicon.png`. Until the maker provides the real icon, the Wails placeholder stays and nothing blocks.

First run:

```bash
wails dev -tags webkit2_41
```

A window opens with the template page. The first run also downloads the frontend's npm packages — expect a minute.

**Gotcha, pre-warned (Niri):** Niri will tile the window like everything else; the size set in `main.go` is a request, not a guarantee. This is normal. Set a sane minimum (`MinWidth: 900, MinHeight: 600`) so the mirror never collapses into uselessness in a narrow tile.

---

## 2. WINDOW CONFIGURATION (`main.go`)

- Title: `RESONANCE`
- Width × Height: 1280 × 800, MinWidth 900, MinHeight 600
- `BackgroundColour` matched to the Default Dark background (kills the white flash on startup)
- Everything else stays stock. Normal window frame — no frameless games.

---

## 3. THE MIRROR SHELL (frontend)

STEP1 builds the *stage*, not the play. Structure:

```
┌──────────────────────────────────────────────────┐
│ [logo] RESONANCE      [vault path ▫] [theme] [?] │  ← top bar
├────────────────────┬────┬────────────────────────┤
│                    │    │                        │
│      SYSTEM        │spine│        VAULT          │
│    (left pane)     │    │     (right pane)       │
│                    │    │                        │
└────────────────────┴────┴────────────────────────┘
```

- **Top bar:** logo placeholder left (swapped for the real PNG when it lands in `build/icons/`), app name, then right-aligned: vault path selector (inert placeholder this step), theme button, About button.
- **Panes:** two equal columns labeled SYSTEM and VAULT, with the central spine between them. In STEP1 both panes are empty.
- **Empty state:** centered logo + "＋ Add your first app". The button exists but is inert — clicking it shows a small toast: *"Coming in v0.2.0"*. Honest, and it proves the toast component early.
- **CSS architecture for themes:** every color in the app is a CSS custom property on `:root` (`--bg`, `--pane`, `--spine`, `--text`, `--text-dim`, `--accent`, `--accent-2`, `--danger`, `--badge-drift`, ...). A theme is nothing but a `[data-theme="..."]` block overriding those variables. Adding the remaining JADEITE palettes in STEP5 must require **zero** structural CSS changes — that is the acceptance test of this architecture.

### Themes shipped in STEP1

1. **Default Dark** — the default. Taken from JADEITE's default dark palette so the family resemblance is immediate.
2. **Ubuntu Aubergine Canonical** — the newcomer JADEITE doesn't have. Canonical's palette: Aubergine `#772953`, Light Aubergine `#77216F`, Mid Aubergine `#5E2750`, Dark Aubergine `#2C001E`, Ubuntu Orange `#E95420` as accent, Warm Grey `#AEA79F` for dim text.

Theme picker = the first real use of the **overlay grammar**: the mirror dims, a panel expands from the center (CSS `transform: scale()` + opacity, ~200ms, ripple feel), theme cards inside, Esc or backdrop-click closes. Every future overlay (diff, preview, machine info, add-app, About) reuses this exact component.

---

## 4. THE ONLY BACKEND LOGIC IN STEP1: SETTINGS

Purpose: learn the Wails Go↔TS bridge on something small before the vault exists.

- Settings file: `os.UserConfigDir()` + `/resonance/settings.json` → in practice `~/.config/resonance/settings.json`.
- Content for now: `{ "theme": "default-dark" }`.
- Go side exposes two bound methods: `GetSettings()` and `SaveSettings(s)`. Write with `0644`, create the directory with `os.MkdirAll` on first save.
- Frontend calls them like async functions via the generated bindings (`wailsjs/go/main/App`).
- Acceptance: switch theme → quit → relaunch → theme survived.

---

## 5. ABOUT OVERLAY (family convention, complete in STEP1)

Uses the same overlay component. Contents, exactly per family rules:

- App logo + RESONANCE
- Maker: sudo-megas
- Version: v0.1.0 · Release date
- Source address — **selectable text, not clickable.** No `<a>` tag exists anywhere in this app. `user-select: text` on the address lines.
- Licence: GPL-3.0-or-later, full licence text scrollable inside the overlay (ship `LICENSE` in the repo root, read it into the overlay at build time).
- No telemetry, no analytics, no crash reporting, no update check — and the About page says so, plainly.

---

## 6. DEFINITION OF DONE — v0.1.0 checklist

- [x] `wails dev -tags webkit2_41` runs on the main rig
- [x] Window opens in Default Dark, no white flash, sane minimum size
- [x] Mirror skeleton visible: top bar, SYSTEM | spine | VAULT, empty state with inert add button + toast
- [x] Theme overlay works, both themes apply live
- [x] Theme choice persists across restart via `~/.config/resonance/settings.json`
- [x] About overlay complete per §5
- [x] `wails build -tags webkit2_41` produces a working binary on the main rig (laptop still needs its own check)
- [ ] Repo pushed from the sudo-megas account, no AI trailers anywhere
- [ ] Tagged `v0.1.0`, published as **pre-release** on GitHub

## 7. EXPLICITLY OUT OF STEP1

Vault logic of any kind, manifest, backup/restore/update, drift, diffs, machine info, the remaining JADEITE palettes, packaging (.deb/.pacman). Those belong to STEP2–STEP5 and will not sneak in early.

---

**When the checklist is green:** ship v0.1.0, then STEP2.md gets written — informed by whatever STEP1 taught us.

Copyright © sudo-megas
*Built with Reason and Passion.*
