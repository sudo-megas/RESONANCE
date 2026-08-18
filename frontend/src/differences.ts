import { main } from "../wailsjs/go/models";
import { GetDiffPair } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { renderDiff } from "./diff";
import { formatDate, formatLastUpdated } from "./dates";
import { extractErrorMessage, formatSize } from "./util";

// The per-app differences view: "you say something changed — show me what."
// Opened from the drift badge, which already appears only when something is
// wrong, so this adds no new chrome to the spine.
//
// It deliberately answers a different question from the restore preview.
// Restore asks "what would change on my system if I pulled the vault back?"
// This asks "what has happened since I backed up?" — so it lists every file,
// not just the actionable ones, and it reads the diff in the opposite
// direction (see buildDiffContent below).

const STATE_TEXT: Record<string, string> = {
  ok: "in sync",
  drifted: "changed since backup",
  missing: "source file is gone",
  vaultMissing: "backup missing from the vault",
  vaultDamaged: "backup doesn't match what was saved",
  untracked: "never backed up",
};

function note(text: string): HTMLElement {
  const p = document.createElement("p");
  p.className = "diff-fallback-note";
  p.textContent = text;
  return p;
}

function buildDiffContent(pair: main.DiffPair): HTMLElement {
  if (pair.live.binary || pair.vault.binary) {
    return note("Binary file — no diff shown.");
  }
  if (pair.live.tooLarge || pair.vault.tooLarge) {
    const bytes = Math.max(pair.live.size, pair.vault.size);
    return note(`File too large to diff (${formatSize(bytes)}).`);
  }
  if (pair.vault.missing) {
    return note("No copy in the vault yet — nothing to compare against.");
  }
  if (pair.live.missing) {
    return note("The file is gone from this system — only the vault's copy survives.");
  }

  // Argument order is inverted relative to restore.ts on purpose, and it
  // matters. There, renderDiff(live, vault) makes "+" mean a line the vault
  // would add to your system, because the vault is the incoming side. Here
  // the vault is the OLD side and the system is current, so
  // renderDiff(vault, live) makes "-" a line lost since the backup and "+" a
  // line added since. Inheriting restore's order would teach the reader the
  // exact opposite of the truth, which is why both columns are labelled too.
  const wrap = document.createElement("div");
  wrap.className = "differences-diff-wrap";

  const legend = document.createElement("div");
  legend.className = "differences-legend";
  const minus = document.createElement("span");
  minus.className = "differences-legend-item differences-legend-item--vault";
  minus.textContent = "− in the vault's copy";
  const plus = document.createElement("span");
  plus.className = "differences-legend-item differences-legend-item--system";
  plus.textContent = "+ on this system now";
  legend.appendChild(minus);
  legend.appendChild(plus);

  wrap.appendChild(legend);
  wrap.appendChild(renderDiff(pair.vault.text, pair.live.text));
  return wrap;
}

function buildRow(appName: string, file: main.FileRow): HTMLLIElement {
  const li = document.createElement("li");
  li.className = `differences-row differences-row--${file.state}`;

  const header = document.createElement("div");
  header.className = "differences-row-header";

  const path = document.createElement("span");
  path.className = "differences-path";
  path.textContent = file.path;

  const state = document.createElement("span");
  state.className = "differences-state";
  state.textContent = STATE_TEXT[file.state] ?? file.state;

  header.appendChild(path);
  header.appendChild(state);

  const meta = document.createElement("div");
  meta.className = "differences-meta";
  const sys = document.createElement("span");
  sys.textContent =
    file.state === "missing" ? "system: gone" : `system: ${formatDate(file.sourceModified)}`;
  const vault = document.createElement("span");
  if (file.state === "untracked") {
    vault.textContent = "vault: not backed up yet";
  } else if (file.state === "vaultMissing") {
    vault.textContent = "vault: copy missing";
  } else {
    const size = file.size > 0 ? `, ${formatSize(file.size)}` : "";
    vault.textContent = `vault: ${formatLastUpdated(file.vaultModified)}${size}`;
  }
  meta.appendChild(sys);
  meta.appendChild(vault);

  li.appendChild(header);
  li.appendChild(meta);

  // Only a file with both sides present has anything to diff.
  if (file.state !== "drifted") return li;

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "differences-row-toggle";
  toggle.setAttribute("aria-expanded", "false");
  toggle.setAttribute("aria-label", `Show what changed in ${file.path}`);
  header.prepend(toggle);

  const container = document.createElement("div");
  container.className = "differences-diff";
  container.hidden = true;
  li.appendChild(container);

  // Loaded on first expand and never again — one GetDiffPair per file the
  // user actually opens, never a prefetch of the whole app.
  let loaded = false;
  toggle.addEventListener("click", async () => {
    const expanded = toggle.getAttribute("aria-expanded") === "true";
    if (expanded) {
      toggle.setAttribute("aria-expanded", "false");
      toggle.classList.remove("differences-row-toggle--expanded");
      container.hidden = true;
      return;
    }
    toggle.setAttribute("aria-expanded", "true");
    toggle.classList.add("differences-row-toggle--expanded");
    container.hidden = false;

    if (!loaded) {
      loaded = true;
      container.textContent = "Loading…";
      try {
        const pair = await GetDiffPair(appName, file.path);
        container.textContent = "";
        container.appendChild(buildDiffContent(pair));
      } catch (err) {
        container.textContent = extractErrorMessage(err);
      }
    }
  });

  return li;
}

export function openDifferences(row: main.AppRow): void {
  const content = document.createElement("div");
  content.className = "differences";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = `${row.name} — what differs`;
  content.appendChild(heading);

  const changed = row.files.filter((f) => f.state !== "ok");
  const summary = document.createElement("p");
  summary.className = "differences-summary";
  summary.textContent =
    changed.length === 0
      ? "Everything matches the vault."
      : `${changed.length} of ${row.files.length} ${row.files.length === 1 ? "file" : "files"} differ.`;
  content.appendChild(summary);

  const list = document.createElement("ul");
  list.className = "differences-list";
  // Differing files first — the reason the overlay was opened shouldn't have
  // to be hunted for in a long list of healthy ones.
  for (const f of [...changed, ...row.files.filter((f) => f.state === "ok")]) {
    list.appendChild(buildRow(row.name, f));
  }
  content.appendChild(list);

  openOverlay(content, { dismissable: true });
}
