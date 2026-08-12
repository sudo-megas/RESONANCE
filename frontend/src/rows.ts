import { main } from "../wailsjs/go/models";
import { GetMirrorRows } from "../wailsjs/go/main/App";
import { formatDate, formatLastUpdated } from "./dates";
import { openUpdateConfirm } from "./update";
import { openRestoreConfirm } from "./restore";
import { refreshStatusbar } from "./statusbar";
import { showToast } from "./toast";
import { extractErrorMessage } from "./util";

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

function rowState(row: main.AppRow): "ok" | "drifted" | "missing" {
  let state: "ok" | "drifted" | "missing" = "ok";
  for (const f of row.files) {
    if (f.state === "missing") return "missing";
    if (f.state === "drifted") state = "drifted";
  }
  return state;
}

function driftBadge(state: string): HTMLSpanElement | null {
  if (state === "ok") return null;
  const badge = document.createElement("span");
  badge.className = "drift-badge";
  if (state === "missing") {
    badge.classList.add("drift-badge--missing");
    badge.title = "Source file is missing";
  } else {
    badge.classList.add("drift-badge--drifted");
    badge.title = "Changed since backup";
  }
  return badge;
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
  const restoreIcon = document.createElement("span");
  restoreIcon.className = "icon-glyph";
  restoreIcon.setAttribute("aria-hidden", "true");
  restoreIcon.textContent = "\uf019";
  restoreBtn.appendChild(restoreIcon);
  restoreBtn.addEventListener("click", () => openRestoreConfirm(row));
  spine.appendChild(restoreBtn);

  const badge = driftBadge(rowState(row));
  if (badge) spine.appendChild(badge);

  const updateBtn = document.createElement("button");
  updateBtn.type = "button";
  updateBtn.className = "spine-update-btn";
  updateBtn.setAttribute("aria-label", `Update ${row.name} from source`);
  const updateIcon = document.createElement("span");
  updateIcon.className = "icon-glyph";
  updateIcon.setAttribute("aria-hidden", "true");
  updateIcon.textContent = "\uf093";
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

    const vault = document.createElement("div");
    vault.className = "mirror-row-cell file-row-vault";
    const vaultDate = document.createElement("span");
    vaultDate.className = "file-row-date";
    vaultDate.textContent = formatLastUpdated(f.vaultModified);
    vault.appendChild(vaultDate);

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
