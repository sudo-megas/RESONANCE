# RESONANCE

**Dotfile syncing that is for "users" not developers.**

---

This is an early scaffold (v0.5.0) — the window, the mirror layout, the theme system, backup, drift detection, and restore now work: choose a vault, add an app's files, and RESONANCE copies them in byte-identical. The mirror shows checksum-based drift badges and per-file dates, "update from source (now)" re-syncs a drifted app after a dates-only confirmation, and "← restore" brings an app back from the vault after previewing what's new, what would be overwritten (with a real content diff), and which machine the vault was last written from. A restore can be undone with one button, all eleven JADEITE palettes ship, and a status bar shows the mirror's overall state at a glance. Full usage documentation arrives once the vault functionality ships with v1.0.0.

File dates shown throughout the app are modification time only, never creation time — ext4/Linux has no reliable, portable way to read a file's creation date, so RESONANCE doesn't pretend to.

## Licence

GPL-3.0-or-later. Full text in [LICENSE](LICENSE).

---

Copyright © sudo-megas · <https://github.com/sudo-megas/RESONANCE>

*Built with Reason and Passion.*
