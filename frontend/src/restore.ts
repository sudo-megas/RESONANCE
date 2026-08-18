import { main } from "../wailsjs/go/models";
import { GetMachineInfo, GetDiffPair, RestoreApp, GetUndoInfo, UndoRestore } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { showToast } from "./toast";
import { extractErrorMessage, formatSize } from "./util";
import { formatDateTime } from "./dates";
import { refreshMirror } from "./rows";
import { renderDiff } from "./diff";
import { buildMachineInfoCard } from "./machineinfo";

// Restore reuses GetMirrorRows' three drift states directly, just read in
// the opposite direction: "missing" (nothing live) becomes what restore
// would create ("new"), "drifted" (live differs from vault) becomes what
// restore would overwrite, and "ok" is skipped entirely — identical, no
// action. This relabeling is the entire restore-preview data source; it
// costs zero additional Go calls beyond GetMachineInfo().
type RestoreAction = "new" | "overwrite";

function classify(state: string): RestoreAction | null {
  if (state === "missing") return "new";
  if (state === "drifted") return "overwrite";
  return null;
}

function summaryLine(newCount: number, overwriteCount: number, skipCount: number): string {
  const parts: string[] = [];
  if (newCount > 0) parts.push(newCount === 1 ? "1 new" : `${newCount} new`);
  if (overwriteCount > 0) parts.push(overwriteCount === 1 ? "1 overwrite" : `${overwriteCount} overwrite`);
  if (skipCount > 0) parts.push(skipCount === 1 ? "1 unchanged" : `${skipCount} unchanged`);
  return parts.length === 0 ? "Nothing to restore" : parts.join(", ");
}

function fallbackNote(text: string): HTMLElement {
  const p = document.createElement("p");
  p.className = "diff-fallback-note";
  p.textContent = text;
  return p;
}

function buildDiffContent(pair: main.DiffPair): HTMLElement {
  if (pair.live.binary || pair.vault.binary) {
    return fallbackNote("Binary file — no diff shown.");
  }
  if (pair.live.tooLarge || pair.vault.tooLarge) {
    const bytes = Math.max(pair.live.size, pair.vault.size);
    return fallbackNote(`File too large to diff (${formatSize(bytes)}).`);
  }
  if (pair.live.missing) {
    return fallbackNote("Live file no longer exists — nothing to compare.");
  }
  if (pair.vault.missing) {
    return fallbackNote("Vault copy no longer exists — nothing to compare.");
  }
  return renderDiff(pair.live.text, pair.vault.text);
}

// Shown instead of a toast when any file failed. Uses the same overlay
// grammar as every other surface rather than inventing a dialog: a toast is
// one line that vanishes in a couple of seconds, which cannot carry a
// per-file reason and is the wrong medium for "something went wrong while
// writing to your home directory".
function openRestoreReport(appName: string, result: main.RestoreResult): void {
  const content = document.createElement("div");
  content.className = "restore-report";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "\uf00d";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = `${appName} — restore finished with problems`;
  content.appendChild(heading);

  const summary = document.createElement("p");
  summary.className = "restore-report-summary";
  summary.textContent = summarizeResult(result);
  content.appendChild(summary);

  const list = document.createElement("ul");
  list.className = "restore-report-list";
  for (const failure of result.failed) {
    const li = document.createElement("li");
    li.className = "restore-report-row";

    const path = document.createElement("span");
    path.className = "restore-report-path";
    path.textContent = failure.path;

    const reason = document.createElement("span");
    reason.className = "restore-report-reason";
    reason.textContent = failure.reason;

    li.appendChild(path);
    li.appendChild(reason);
    list.appendChild(li);
  }
  content.appendChild(list);

  // Everything not listed above did land, and saying so plainly matters:
  // "restore failed" and "restore partly succeeded" call for different next
  // moves from the user.
  if (result.new.length > 0 || result.overwritten.length > 0) {
    const partial = document.createElement("p");
    partial.className = "restore-report-note";
    partial.textContent =
      "The files not listed here were restored successfully. Undo is available from this app's row.";
    content.appendChild(partial);
  }

  openOverlay(content, { dismissable: true });
}

function buildPreviewRow(appName: string, file: main.FileRow, action: RestoreAction): HTMLLIElement {
  const li = document.createElement("li");
  li.className = `restore-preview-row restore-preview-row--${action}`;

  const header = document.createElement("div");
  header.className = "restore-preview-row-header";

  const label = document.createElement("span");
  label.className = `restore-preview-label restore-preview-label--${action}`;
  label.textContent = action === "new" ? "New" : "Overwrite";

  const path = document.createElement("span");
  path.className = "restore-preview-path";
  path.textContent = file.path;

  header.appendChild(label);
  header.appendChild(path);

  if (action !== "overwrite") {
    li.appendChild(header);
    return li;
  }

  // Only Overwrite rows have a live file to diff against — New rows have
  // nothing on the live side yet, Skip rows aren't itemized at all.
  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "restore-preview-row-toggle";
  toggle.setAttribute("aria-expanded", "false");
  toggle.setAttribute("aria-label", `Show diff for ${file.path}`);
  header.prepend(toggle);

  const diffContainer = document.createElement("div");
  diffContainer.className = "restore-preview-diff";
  diffContainer.hidden = true;

  let loaded = false;
  toggle.addEventListener("click", async () => {
    const expanded = toggle.getAttribute("aria-expanded") === "true";
    if (expanded) {
      toggle.setAttribute("aria-expanded", "false");
      toggle.classList.remove("restore-preview-row-toggle--expanded");
      diffContainer.hidden = true;
      return;
    }
    toggle.setAttribute("aria-expanded", "true");
    toggle.classList.add("restore-preview-row-toggle--expanded");
    diffContainer.hidden = false;

    if (!loaded) {
      loaded = true;
      diffContainer.textContent = "Loading diff…";
      try {
        const pair = await GetDiffPair(appName, file.path);
        diffContainer.textContent = "";
        diffContainer.appendChild(buildDiffContent(pair));
      } catch (err) {
        diffContainer.textContent = extractErrorMessage(err);
      }
    }
  });

  li.appendChild(header);
  li.appendChild(diffContainer);
  return li;
}

function summarizeResult(result: main.RestoreResult): string {
  const parts: string[] = [];
  const n = result.new.length;
  const o = result.overwritten.length;
  const f = result.failed.length;
  if (n > 0) parts.push(n === 1 ? "1 file created" : `${n} files created`);
  if (o > 0) parts.push(o === 1 ? "1 file overwritten" : `${o} files overwritten`);
  if (f > 0) parts.push(f === 1 ? "1 failed" : `${f} failed`);
  return parts.length === 0 ? "Already up to date" : parts.join(", ");
}

function summarizeUndoResult(result: main.UndoResult): string {
  const parts: string[] = [];
  const r = result.restored.length;
  const f = result.failed.length;
  if (r > 0) parts.push(r === 1 ? "1 file restored" : `${r} files restored`);
  if (f > 0) parts.push(f === 1 ? "1 failed" : `${f} failed`);
  return parts.length === 0 ? "Nothing restored" : parts.join(", ");
}

// The reduced-mode overlay reachable once a row is back in sync but still
// has an undo snapshot on hand — same checkbox-gate + commit pattern as a
// real restore, since undo overwrites live files too and deserves the same
// "this touches your system" friction. No New/Overwrite list: there's
// nothing to preview, only a prior state to put back.
function openUndoConfirm(row: main.AppRow, info: main.UndoInfo): void {
  const content = document.createElement("div");
  content.className = "restore-confirm restore-confirm--undo";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = `Undo restore for ${row.name}`;
  content.appendChild(heading);

  const summary = document.createElement("p");
  summary.className = "restore-preview-summary";
  const fileWord = info.fileCount === 1 ? "file" : "files";
  summary.textContent = `Nothing to restore. Undo restore from ${formatDateTime(info.createdAt)} (${info.fileCount} ${fileWord})?`;
  content.appendChild(summary);

  const gateLabel = document.createElement("label");
  gateLabel.className = "restore-confirm-gate";
  const gateCheckbox = document.createElement("input");
  gateCheckbox.type = "checkbox";
  gateCheckbox.className = "restore-confirm-checkbox";
  const gateText = document.createElement("span");
  gateText.textContent = "I understand this will overwrite files on this system.";
  gateLabel.appendChild(gateCheckbox);
  gateLabel.appendChild(gateText);
  content.appendChild(gateLabel);

  const status = document.createElement("p");
  status.className = "restore-confirm-status";
  content.appendChild(status);

  const commitBtn = document.createElement("button");
  commitBtn.type = "button";
  commitBtn.className = "restore-confirm-commit-btn";
  commitBtn.textContent = "Undo Restore";
  commitBtn.disabled = true;
  content.appendChild(commitBtn);

  const cancelBtn = document.createElement("button");
  cancelBtn.type = "button";
  cancelBtn.className = "restore-confirm-cancel-btn";
  cancelBtn.textContent = "Cancel";
  cancelBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(cancelBtn);

  gateCheckbox.addEventListener("change", () => {
    commitBtn.disabled = !gateCheckbox.checked;
  });

  commitBtn.addEventListener("click", async () => {
    commitBtn.disabled = true;
    cancelBtn.disabled = true;
    gateCheckbox.disabled = true;
    status.textContent = "";
    try {
      const result = await UndoRestore(row.name);
      closeOverlay();
      await refreshMirror();
      showToast(summarizeUndoResult(result));
    } catch (err) {
      status.textContent = extractErrorMessage(err);
      commitBtn.disabled = !gateCheckbox.checked;
      cancelBtn.disabled = false;
      gateCheckbox.disabled = false;
    }
  });

  openOverlay(content, { dismissable: true });
}

// restoreConfirmEpoch guards every await inside openRestoreConfirm against a
// cross-row race: if the user closes this overlay and opens a restore
// preview for a different row before an in-flight GetUndoInfo/GetMachineInfo
// call resolves, that stale call must become a no-op instead of clobbering
// whatever overlay is now actually open. Each call captures the epoch at
// entry and re-checks it after every await; a later call bumping the epoch
// is what marks an earlier one as superseded.
let restoreConfirmEpoch = 0;

export async function openRestoreConfirm(row: main.AppRow): Promise<void> {
  const epoch = ++restoreConfirmEpoch;

  let undoInfo: main.UndoInfo | null = null;
  try {
    undoInfo = await GetUndoInfo(row.name);
  } catch {
    undoInfo = null;
  }
  if (epoch !== restoreConfirmEpoch) return;

  const newFiles = row.files.filter((f) => classify(f.state) === "new");
  const overwriteFiles = row.files.filter((f) => classify(f.state) === "overwrite");

  // Gate on what is actually restorable, not on row.drifted. Since v1.2.1
  // an app can be drifted for reasons restore cannot act on — a file found
  // in a tracked folder that was never backed up, or a vault copy that has
  // gone missing. Both are repaired by Update (system -> vault), the
  // opposite direction. Gating on drifted alone would open a restore
  // preview listing nothing, because classify() correctly declines to offer
  // an action for either.
  if (newFiles.length === 0 && overwriteFiles.length === 0) {
    // Only fall straight through to undo when the undo is one this vault can
    // actually stand behind. A snapshot taken under a different vault
    // replays a genuinely valid earlier $HOME state, but not one that has
    // anything to do with the app the user just clicked; and a snapshot
    // whose captured bytes are gone cannot put anything back at all.
    // Neither is hidden — both stay reachable and labelled below and in
    // Recent activity — but neither gets to be the automatic answer.
    if (undoInfo?.available && !undoInfo.stale && undoInfo.restorable > 0) {
      openUndoConfirm(row, undoInfo);
      return;
    }
    if (undoInfo?.available) {
      showToast(
        undoInfo.restorable === 0
          ? `${row.name}'s pending undo can no longer be applied — clear it under Recent activity`
          : `${row.name}'s pending undo was taken under a different vault — see Recent activity`,
      );
      return;
    }
    const vaultBroken = row.files.some((f) => f.state === "vaultMissing" || f.state === "vaultDamaged");
    const untracked = row.files.some((f) => f.state === "untracked");
    if (vaultBroken) {
      showToast(`${row.name} has no vault copy to restore — update from source first`);
    } else if (untracked) {
      showToast(`${row.name} has files that were never backed up — update from source first`);
    } else {
      showToast(`${row.name} is already up to date — nothing to restore`);
    }
    return;
  }

  const skipCount = row.files.length - newFiles.length - overwriteFiles.length;

  const content = document.createElement("div");
  content.className = "restore-confirm";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "\uf00d";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = `Restore ${row.name} from vault`;
  content.appendChild(heading);

  const machineCard = document.createElement("div");
  machineCard.className = "machine-info-card-slot";
  machineCard.textContent = "Loading machine info…";
  content.appendChild(machineCard);

  const summary = document.createElement("p");
  summary.className = "restore-preview-summary";
  summary.textContent = summaryLine(newFiles.length, overwriteFiles.length, skipCount);
  content.appendChild(summary);

  // A crashed or partially-failed restore leaves the row drifted — exactly
  // the state where undo used to become unreachable, since it was only ever
  // offered when a row was fully in sync. Surfacing it here too means undo
  // stays reachable in the one case it matters most.
  if (undoInfo?.available) {
    const undoLink = document.createElement("button");
    undoLink.type = "button";
    undoLink.className = "restore-preview-undo-link";
    if (undoInfo.restorable === 0) {
      // Offering it again would be the dead end: it cannot succeed now and
      // it never will, so the only useful action is to be rid of it.
      undoLink.textContent = "This undo can no longer be applied \u2014 clear it under Recent activity";
      undoLink.disabled = true;
    } else {
      // Say what it actually is. A snapshot from another vault, or one that
      // can only put some of its files back, is still worth offering — but
      // not while implying it is a clean undo of this vault's app.
      const when = formatDateTime(undoInfo.createdAt);
      const partial =
        undoInfo.restorable < undoInfo.fileCount
          ? ` \u2014 ${undoInfo.restorable} of ${undoInfo.fileCount} files can be put back`
          : "";
      const provenance = undoInfo.stale ? ` \u2014 taken under ${undoInfo.vaultPath}` : "";
      undoLink.textContent = `Undo last restore instead (${when})${partial}${provenance}`;
      undoLink.addEventListener("click", () => openUndoConfirm(row, undoInfo!));
    }
    content.appendChild(undoLink);
  }

  const list = document.createElement("ul");
  list.className = "restore-preview-list";
  for (const f of newFiles) list.appendChild(buildPreviewRow(row.name, f, "new"));
  for (const f of overwriteFiles) list.appendChild(buildPreviewRow(row.name, f, "overwrite"));
  content.appendChild(list);

  const gateLabel = document.createElement("label");
  gateLabel.className = "restore-confirm-gate";
  const gateCheckbox = document.createElement("input");
  gateCheckbox.type = "checkbox";
  gateCheckbox.className = "restore-confirm-checkbox";
  const gateText = document.createElement("span");
  gateText.textContent = "I understand this will overwrite files on this system.";
  gateLabel.appendChild(gateCheckbox);
  gateLabel.appendChild(gateText);
  content.appendChild(gateLabel);

  const status = document.createElement("p");
  status.className = "restore-confirm-status";
  content.appendChild(status);

  const commitBtn = document.createElement("button");
  commitBtn.type = "button";
  commitBtn.className = "restore-confirm-commit-btn";
  commitBtn.textContent = "Restore";
  commitBtn.disabled = true;
  content.appendChild(commitBtn);

  const cancelBtn = document.createElement("button");
  cancelBtn.type = "button";
  cancelBtn.className = "restore-confirm-cancel-btn";
  cancelBtn.textContent = "Cancel";
  cancelBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(cancelBtn);

  gateCheckbox.addEventListener("change", () => {
    commitBtn.disabled = !gateCheckbox.checked;
  });

  commitBtn.addEventListener("click", async () => {
    commitBtn.disabled = true;
    cancelBtn.disabled = true;
    gateCheckbox.disabled = true;
    status.textContent = "";
    try {
      const result = await RestoreApp(row.name);
      closeOverlay();
      await refreshMirror();
      // A bare "1 failed" toast used to be the whole report, with the
      // per-file reason the backend had already computed thrown away — at
      // the exact moment the user most needs it, because files on their
      // system have just been partially overwritten.
      if (result.failed.length > 0) {
        openRestoreReport(row.name, result);
      } else {
        showToast(summarizeResult(result));
      }
    } catch (err) {
      status.textContent = extractErrorMessage(err);
      commitBtn.disabled = !gateCheckbox.checked;
      cancelBtn.disabled = false;
      gateCheckbox.disabled = false;
    }
  });

  openOverlay(content, { dismissable: true });

  try {
    const info = await GetMachineInfo();
    if (epoch !== restoreConfirmEpoch) return;
    machineCard.replaceWith(buildMachineInfoCard(info));
  } catch (err) {
    if (epoch !== restoreConfirmEpoch) return;
    machineCard.textContent = extractErrorMessage(err);
  }
}
