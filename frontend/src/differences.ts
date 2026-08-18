import { main } from "../wailsjs/go/models";
import { GetDiffPair } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { renderDiff } from "./diff";
import { formatDateTime } from "./dates";
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

// One side of the comparison. Label, timestamp, size, in the same order on
// both sides so the eye compares down a column instead of hunting across a
// sentence.
function buildSide(
  kind: "source" | "vault",
  label: string,
  when: string,
  size: number,
  absent: string,
): HTMLElement {
  const side = document.createElement("div");
  side.className = `differences-side differences-side--${kind}`;

  const name = document.createElement("span");
  name.className = "differences-side-label";
  name.textContent = label;
  side.appendChild(name);

  if (absent) {
    const gone = document.createElement("span");
    gone.className = "differences-side-absent";
    gone.textContent = absent;
    side.appendChild(gone);
    return side;
  }

  const time = document.createElement("span");
  time.className = "differences-side-time";
  // formatDateTime, not formatDate. dates.ts already explains why day
  // granularity is not enough when two moments fall on the same day, and that
  // is the ordinary case here: a file is usually edited the same day it was
  // backed up. At day granularity both sides printed "18 08 2026", and the
  // view whose whole job is "which of these is newer" answered nothing.
  time.textContent = formatDateTime(when);
  side.appendChild(time);

  const bytes = document.createElement("span");
  bytes.className = "differences-side-size";
  bytes.textContent = size > 0 ? formatSize(size) : "—";
  side.appendChild(bytes);

  return side;
}

// The comparison block: what the live file is, against what the vault holds.
//
// Left is the live file and right is the vault, the same direction the mirror
// itself uses, so this overlay does not teach a second grammar. It says SOURCE
// where the pane says SYSTEM: the panes name two places on your machine, while
// this names two versions of one file, and "source" is already the word the
// update button uses.
function buildCompare(file: main.FileRow): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "differences-compare";

  const sourceAbsent = file.state === "missing" ? "file is gone" : "";
  let vaultAbsent = "";
  if (file.state === "untracked") {
    vaultAbsent = "not backed up yet";
  } else if (file.state === "vaultMissing") {
    vaultAbsent = "copy missing";
  } else if (file.state === "vaultDamaged") {
    // Deliberately not the recorded date and size. Those describe the backup
    // that was taken; the file sitting there now is not that file, and
    // printing its supposed size beside a live one would be this view stating
    // something it knows to be untrue.
    vaultAbsent = "doesn't match what was recorded";
  }

  const source = buildSide(
    "source",
    "SOURCE — live",
    file.sourceModified,
    file.sourceSize,
    sourceAbsent,
  );
  const vault = buildSide(
    "vault",
    "VAULT — backup",
    file.vaultModified,
    file.size,
    vaultAbsent,
  );
  wrap.appendChild(source);
  wrap.appendChild(vault);

  // Which one is newer, said outright rather than left to be worked out by
  // comparing two strings.
  //
  // Only for a drifted file. An in-sync file's backup is taken after the file
  // was last written, so the vault's timestamp is the later one on almost
  // every healthy row — marking it "newer" there would suggest the vault holds
  // something the source does not, which is exactly backwards.
  if (file.state === "drifted") {
    const a = new Date(file.sourceModified).getTime();
    const b = new Date(file.vaultModified).getTime();
    if (Number.isFinite(a) && Number.isFinite(b) && a !== b) {
      const newer = a > b ? source : vault;
      newer.classList.add("differences-side--newer");
      const marker = document.createElement("span");
      marker.className = "differences-newer";
      marker.textContent = "newer";
      newer.appendChild(marker);
    }
  }

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

  li.appendChild(header);
  li.appendChild(buildCompare(file));

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
      : `${changed.length} of ${row.files.length} ${
          row.files.length === 1 ? "file" : "files"
        } ${changed.length === 1 ? "differs" : "differ"}.`;
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
