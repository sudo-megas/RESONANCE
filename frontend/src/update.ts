import { main } from "../wailsjs/go/models";
import { UpdateFromSource } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { showToast } from "./toast";
import { formatDate, formatLastUpdated } from "./dates";
import { extractErrorMessage } from "./util";
import { refreshMirror } from "./rows";

// "Update from source (now)" satisfies CORE.md §4's Overwrite-rules bullet
// (dates shown before an overwrite commits) without reopening the full
// content-diff overlay STEP2.md already deferred to STEP4 — this lists
// every file about to be re-copied with its source and vault dates,
// nothing more.

interface ConfirmEntry {
  appName: string;
  file: main.FileRow;
}

function collectEntries(rows: main.AppRow[]): ConfirmEntry[] {
  const entries: ConfirmEntry[] = [];
  for (const row of rows) {
    for (const f of row.files) {
      if (f.state !== "ok") entries.push({ appName: row.name, file: f });
    }
  }
  return entries;
}

function buildFileList(entries: ConfirmEntry[], showAppName: boolean): HTMLUListElement {
  const list = document.createElement("ul");
  list.className = "update-confirm-filelist";
  for (const { appName, file } of entries) {
    const li = document.createElement("li");
    li.className = "update-confirm-file-row";

    const path = document.createElement("span");
    path.className = "update-confirm-path";
    path.textContent = showAppName ? `${appName}/${file.path}` : file.path;

    const dates = document.createElement("span");
    dates.className = "update-confirm-date";
    dates.textContent =
      file.state === "missing"
        ? "source missing — will be skipped"
        : `${formatDate(file.sourceModified)} → ${formatLastUpdated(file.vaultModified)}`;

    li.appendChild(path);
    li.appendChild(dates);
    list.appendChild(li);
  }
  return list;
}

function summarizeResults(results: main.UpdateResult[]): string {
  let updated = 0;
  let missing = 0;
  for (const r of results) {
    updated += r.updated.length;
    missing += r.missing.length;
  }
  const parts: string[] = [];
  if (updated > 0) parts.push(updated === 1 ? "1 file updated" : `${updated} files updated`);
  if (missing > 0) parts.push(missing === 1 ? "1 source missing" : `${missing} sources missing`);
  return parts.length === 0 ? "Already up to date" : parts.join(", ");
}

function openConfirm(heading: string, entries: ConfirmEntry[], showAppName: boolean, commit: () => Promise<main.UpdateResult[]>): void {
  const content = document.createElement("div");
  content.className = "update-confirm";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "\uf00d";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading2 = document.createElement("h2");
  heading2.className = "overlay-heading";
  heading2.textContent = heading;
  content.appendChild(heading2);

  content.appendChild(buildFileList(entries, showAppName));

  const status = document.createElement("p");
  status.className = "update-confirm-status";
  content.appendChild(status);

  const commitBtn = document.createElement("button");
  commitBtn.type = "button";
  commitBtn.className = "update-confirm-commit-btn";
  commitBtn.textContent = "Update";
  content.appendChild(commitBtn);

  const cancelBtn = document.createElement("button");
  cancelBtn.type = "button";
  cancelBtn.className = "update-confirm-cancel-btn";
  cancelBtn.textContent = "Cancel";
  cancelBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(cancelBtn);

  commitBtn.addEventListener("click", async () => {
    commitBtn.disabled = true;
    cancelBtn.disabled = true;
    status.textContent = "";
    try {
      const results = await commit();
      closeOverlay();
      await refreshMirror();
      showToast(summarizeResults(results));
    } catch (err) {
      status.textContent = extractErrorMessage(err);
      commitBtn.disabled = false;
      cancelBtn.disabled = false;
    }
  });

  openOverlay(content, { dismissable: true });
}

export function openUpdateConfirm(row: main.AppRow): void {
  if (!row.drifted) {
    showToast(`${row.name} is already up to date`);
    return;
  }
  const entries = collectEntries([row]);
  openConfirm(`Update ${row.name} from source`, entries, false, async () => [await UpdateFromSource(row.name)]);
}

export function openUpdateAllConfirm(rows: main.AppRow[]): void {
  const drifted = rows.filter((r) => r.drifted);
  if (drifted.length === 0) {
    showToast("Everything is already up to date");
    return;
  }
  const entries = collectEntries(drifted);
  const label = drifted.length === 1 ? "1 app" : `${drifted.length} apps`;
  openConfirm(`Update ${label} from source`, entries, true, async () => {
    const results: main.UpdateResult[] = [];
    for (const row of drifted) {
      results.push(await UpdateFromSource(row.name));
    }
    return results;
  });
}
