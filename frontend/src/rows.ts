import { main } from "../wailsjs/go/models";
import { GetMirrorRows } from "../wailsjs/go/main/App";
import { formatDate, formatLastUpdated } from "./dates";
import { openUpdateConfirm } from "./update";
import { openRestoreConfirm } from "./restore";
import { refreshStatusbar } from "./statusbar";
import { showToast } from "./toast";
import { extractErrorMessage, formatSize } from "./util";
import { openDifferences } from "./differences";

// Single source of truth for the mirror's data. Expand/collapse is a pure
// client-side toggle against this cache — every file's drift state was
// already fetched in one GetMirrorRows() call, so expanding a row never
// costs a second round trip. refreshMirror() is the only thing that talks
// to Go; renderRows()/toggling only ever re-render from lastRows.
let lastRows: main.AppRow[] = [];
const expandedApps = new Set<string>();

export async function refreshMirror(): Promise<void> {
  try {
    const rows = await GetMirrorRows();
    renderRows(rows);
    await refreshStatusbar(rows);
  } catch (err) {
    // GetMirrorRows now reports a real error for an unreachable vault
    // (unplugged drive, corrupt manifest) instead of silently rendering it
    // the same as a genuinely empty one — surface it, don't just log it.
    console.error(err);
    showToast(extractErrorMessage(err));
  }
}

export function getDriftedApps(): main.AppRow[] {
  return lastRows.filter((r) => r.drifted);
}

// Worst-first. The app-level badge shows the most serious thing wrong with
// any of its files, so the order here is the editorial judgement about which
// conditions matter most: a backup that isn't there at all outranks a source
// that has merely changed, and "found but never backed up" is the mildest —
// it's a to-do, not a fault.
const STATE_SEVERITY = ["vaultMissing", "vaultDamaged", "missing", "drifted", "untracked"];

const STATE_LABEL: Record<string, string> = {
  vaultMissing: "Backup missing from the vault",
  vaultDamaged: "Backup doesn't match what was saved",
  missing: "Source file is missing",
  drifted: "Changed since backup",
  untracked: "Found in a tracked folder — not backed up yet",
};

function rowState(row: main.AppRow): string {
  for (const candidate of STATE_SEVERITY) {
    if (row.files.some((f) => f.state === candidate)) return candidate;
  }
  return "ok";
}

// The badge doubles as the way in to the per-app differences view. It
// already appears only when something is wrong and already carries an
// explanation, so making it activate adds no new chrome to the spine — and
// "you say something changed, show me what" is the obvious next question.
function driftBadge(state: string, onOpen?: () => void): HTMLElement | null {
  if (state === "ok" || !state) return null;

  const label = STATE_LABEL[state] ?? "Changed since backup";
  const el = document.createElement(onOpen ? "button" : "span");
  el.className = "drift-badge";
  el.title = onOpen ? `${label} — click to see what differs` : label;

  switch (state) {
    case "missing":
    case "vaultMissing":
    case "vaultDamaged":
      el.classList.add("drift-badge--missing");
      break;
    case "untracked":
      el.classList.add("drift-badge--untracked");
      break;
    default:
      el.classList.add("drift-badge--drifted");
  }

  if (onOpen) {
    const btn = el as HTMLButtonElement;
    btn.type = "button";
    btn.classList.add("drift-badge--button");
    btn.setAttribute("aria-label", `${label} — show differences`);
    btn.addEventListener("click", onOpen);
  }
  return el;
}

function toggleExpand(name: string): void {
  if (expandedApps.has(name)) {
    expandedApps.delete(name);
  } else {
    expandedApps.add(name);
  }
  renderRows(lastRows);
}

function buildAppRowCells(row: main.AppRow): HTMLDivElement[] {
  const isExpanded = expandedApps.has(row.name);

  const system = document.createElement("div");
  system.className = "mirror-row-cell mirror-row-system";
  system.dataset.appName = row.name;

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "mirror-row-toggle";
  if (isExpanded) toggle.classList.add("mirror-row-toggle--expanded");
  toggle.setAttribute("aria-expanded", String(isExpanded));
  toggle.setAttribute("aria-label", `${isExpanded ? "Collapse" : "Expand"} ${row.name}`);
  toggle.addEventListener("click", () => toggleExpand(row.name));

  const name = document.createElement("span");
  name.className = "app-row-name";
  name.textContent = row.name;

  const count = document.createElement("span");
  count.className = "app-row-count";
  const n = row.files.length;
  count.textContent = n === 1 ? "1 file" : `${n} files`;

  system.appendChild(toggle);
  system.appendChild(name);
  system.appendChild(count);

  const spine = document.createElement("div");
  spine.className = "mirror-row-cell mirror-row-spine";

  const restoreBtn = document.createElement("button");
  restoreBtn.type = "button";
  restoreBtn.className = "spine-restore-btn";
  restoreBtn.setAttribute("aria-label", `Restore ${row.name} from vault`);
  restoreBtn.title = "Restore from vault \u2014 copies the vault's files onto this system";
  const restoreIcon = document.createElement("span");
  restoreIcon.className = "icon-glyph";
  restoreIcon.setAttribute("aria-hidden", "true");
  // fa-long-arrow-left. Was fa-download, which is a DOWN arrow in a layout
  // whose whole grammar is left/right: SYSTEM on the left, VAULT on the
  // right, direction is meaning. Restore moves vault -> system, so the arrow
  // points left, at the pane it writes to.
  restoreIcon.textContent = "\uf177";
  restoreBtn.appendChild(restoreIcon);
  restoreBtn.addEventListener("click", () => openRestoreConfirm(row));
  spine.appendChild(restoreBtn);

  const badge = driftBadge(rowState(row), () => openDifferences(row));
  if (badge) spine.appendChild(badge);

  const updateBtn = document.createElement("button");
  updateBtn.type = "button";
  updateBtn.className = "spine-update-btn";
  updateBtn.setAttribute("aria-label", `Update ${row.name} from source`);
  updateBtn.title = "Update from source \u2014 copies this system's files into the vault";
  const updateIcon = document.createElement("span");
  updateIcon.className = "icon-glyph";
  updateIcon.setAttribute("aria-hidden", "true");
  // fa-long-arrow-right, replacing fa-upload for the same reason: Update
  // moves system -> vault, so the arrow points right.
  updateIcon.textContent = "\uf178";
  updateBtn.appendChild(updateIcon);
  updateBtn.addEventListener("click", () => openUpdateConfirm(row));
  spine.appendChild(updateBtn);

  const vault = document.createElement("div");
  vault.className = "mirror-row-cell mirror-row-vault";
  const vaultName = document.createElement("span");
  vaultName.className = "app-row-name";
  vaultName.textContent = row.name;
  vault.appendChild(vaultName);

  return [system, spine, vault];
}

function buildFileRowCells(row: main.AppRow): HTMLDivElement[] {
  const cells: HTMLDivElement[] = [];
  for (const f of row.files) {
    const source = document.createElement("div");
    source.className = "mirror-row-cell file-row-system";
    const path = document.createElement("span");
    path.className = "file-row-path";
    path.textContent = f.path;
    const sourceDate = document.createElement("span");
    sourceDate.className = "file-row-date";
    sourceDate.textContent = f.state === "missing" ? "source missing" : formatDate(f.sourceModified);
    source.appendChild(path);
    source.appendChild(sourceDate);

    const state = document.createElement("div");
    state.className = "mirror-row-cell file-row-state";
    const badge = driftBadge(f.state);
    if (badge) state.appendChild(badge);

    // The VAULT side used to be a bare date, which is what "the vault must
    // be more verbose" was about: expanding a row bought almost nothing, and
    // the size and state the app already knew were never shown. It now says
    // what the vault actually holds — and says so honestly when it holds
    // nothing.
    const vault = document.createElement("div");
    vault.className = "mirror-row-cell file-row-vault";

    const vaultState = document.createElement("span");
    vaultState.className = "file-row-vault-state";
    const vaultDate = document.createElement("span");
    vaultDate.className = "file-row-date";

    switch (f.state) {
      case "untracked":
        vaultState.textContent = "not backed up yet";
        vaultState.classList.add("file-row-vault-state--absent");
        break;
      case "vaultMissing":
        vaultState.textContent = "backup missing";
        vaultState.classList.add("file-row-vault-state--absent");
        vaultDate.textContent = formatLastUpdated(f.vaultModified);
        break;
      case "vaultDamaged":
        vaultState.textContent = "backup doesn't match";
        vaultState.classList.add("file-row-vault-state--absent");
        vaultDate.textContent = formatLastUpdated(f.vaultModified);
        break;
      default:
        vaultState.textContent = f.size > 0 ? formatSize(f.size) : "";
        vaultDate.textContent = formatLastUpdated(f.vaultModified);
    }

    if (vaultState.textContent) vault.appendChild(vaultState);
    if (vaultDate.textContent) vault.appendChild(vaultDate);

    cells.push(source, state, vault);
  }
  return cells;
}

function appendHint(root: HTMLElement): void {
  const hint = document.createElement("div");
  hint.className = "mirror-empty-hint";
  hint.textContent = "No apps yet.";
  root.appendChild(hint);
}

// The header row (SYSTEM/spine-controls/VAULT) is static markup in
// index.html — its first 3 children are never rebuilt, only everything
// appended after them.
export function renderRows(rows: main.AppRow[]): void {
  lastRows = rows;
  const root = document.getElementById("mirror-rows")!;
  while (root.children.length > 3) {
    root.removeChild(root.lastElementChild!);
  }

  if (rows.length === 0) {
    appendHint(root);
    return;
  }

  for (const row of rows) {
    for (const cell of buildAppRowCells(row)) root.appendChild(cell);
    if (expandedApps.has(row.name)) {
      for (const cell of buildFileRowCells(row)) root.appendChild(cell);
    }
  }
}
