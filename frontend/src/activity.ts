import { main } from "../wailsjs/go/models";
import {
  GetRecentActivity,
  ListUndoSnapshots,
  DiscardUndoSnapshot,
  UndoRestore,
} from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { formatDateTime } from "./dates";
import { extractErrorMessage, formatSize } from "./util";
import { showToast } from "./toast";
import { refreshMirror } from "./rows";

// Kind -> icon codepoint, verified against the bundled Nerd Font's cmap in
// the UI-overhaul plan (fa-plus_circle, fa-upload, fa-download, fa-undo).
const KIND_ICON: Record<string, string> = {
  add: "",
  update: "",
  restore: "",
  undo: "",
  // fa-minus-circle, the exact counterpart of add's fa-plus-circle. Both
  // measure -0.1685em, which is what .activity-icon already hardcodes, so
  // neither costs a centering rule.
  remove: "",
  // fa-tasks, the same glyph as the vault row's edit button — a rename or
  // an untrack in the log points back at the surface that performed it.
  edit: "",
};


/**
 * One pending undo snapshot, with the two things that can be done to it.
 *
 * This section is the only place a snapshot is reachable once its app has
 * left the vault — and it is what makes keeping the snapshot through an app
 * removal coherent rather than merely stubborn. It lives in Recent Activity
 * because a snapshot is the residue of a logged restore, both are per-machine
 * state under the same state directory, and this overlay is always reachable
 * from the topbar whatever the mirror happens to be showing.
 */
function buildSnapshotRow(s: main.SnapshotInfo, reload: () => void): HTMLLIElement {
  const li = document.createElement("li");
  li.className = "snapshot-row";

  const head = document.createElement("div");
  head.className = "snapshot-head";
  const name = document.createElement("span");
  name.className = "snapshot-app";
  name.textContent = s.app;
  const when = document.createElement("span");
  when.className = "activity-date";
  when.textContent = formatDateTime(s.createdAt);
  head.appendChild(name);
  head.appendChild(when);
  li.appendChild(head);

  const meta = document.createElement("div");
  meta.className = "snapshot-meta";
  const files =
    s.restorable === s.fileCount
      ? `${s.fileCount} ${s.fileCount === 1 ? "file" : "files"}`
      : `${s.fileCount} ${s.fileCount === 1 ? "file" : "files"} (${s.restorable} restorable)`;
  meta.textContent = `${files} \u00b7 ${formatSize(s.bytes)}`;
  li.appendChild(meta);

  // Say plainly why an offer might not mean what it looks like, rather than
  // hiding it — the snapshot is real and the user may still want it.
  const notes: string[] = [];
  if (s.stale) notes.push(`Taken under a different vault (${s.vaultPath})`);
  if (s.orphaned) notes.push("This app is no longer in the vault");
  if (s.restorable === 0) notes.push("None of its files can be put back any more");
  if (notes.length > 0) {
    const note = document.createElement("div");
    note.className = "snapshot-note";
    note.textContent = notes.join(" \u00b7 ");
    li.appendChild(note);
  }

  const actions = document.createElement("div");
  actions.className = "snapshot-actions";

  const undoBtn = document.createElement("button");
  undoBtn.type = "button";
  undoBtn.className = "snapshot-undo-btn";
  undoBtn.textContent = "Undo";
  undoBtn.disabled = s.restorable === 0;
  let undoArmed = false;
  undoBtn.addEventListener("click", async () => {
    // Undo writes to live $HOME files, so it gets the same arm-then-confirm
    // friction the vault-side destructive buttons get. UndoRestore never
    // reads the vault, which is why this works for an orphaned snapshot too.
    if (!undoArmed) {
      undoArmed = true;
      undoBtn.textContent = `Confirm \u2014 puts ${s.restorable} ${s.restorable === 1 ? "file" : "files"} back in ~`;
      return;
    }
    undoBtn.disabled = true;
    try {
      const result = await UndoRestore(s.app);
      showToast(
        result.failed.length > 0
          ? `${s.app}: ${result.restored.length} put back, ${result.failed.length} failed`
          : `${s.app}: ${result.restored.length} ${result.restored.length === 1 ? "file" : "files"} put back`,
      );
      await refreshMirror();
    } catch (err) {
      showToast(extractErrorMessage(err));
    }
    reload();
  });
  actions.appendChild(undoBtn);

  const discardBtn = document.createElement("button");
  discardBtn.type = "button";
  discardBtn.className = "snapshot-discard-btn";
  discardBtn.textContent = "Discard";
  let discardArmed = false;
  discardBtn.addEventListener("click", async () => {
    if (!discardArmed) {
      discardArmed = true;
      discardBtn.textContent = "Confirm \u2014 this undo is gone for good";
      return;
    }
    discardBtn.disabled = true;
    try {
      await DiscardUndoSnapshot(s.app);
    } catch (err) {
      showToast(extractErrorMessage(err));
    }
    reload();
  });
  actions.appendChild(discardBtn);

  li.appendChild(actions);
  return li;
}

function buildRow(entry: main.ActivityEntry): HTMLLIElement {
  const li = document.createElement("li");
  li.className = "activity-row";

  const icon = document.createElement("span");
  icon.className = "activity-icon";
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = KIND_ICON[entry.kind] ?? "";

  const summary = document.createElement("span");
  summary.className = "activity-summary";
  summary.textContent = entry.summary;

  const date = document.createElement("span");
  date.className = "activity-date";
  date.textContent = formatDateTime(entry.timestamp);

  li.appendChild(icon);
  li.appendChild(summary);
  li.appendChild(date);
  return li;
}

function renderEntries(list: HTMLUListElement, entries: main.ActivityEntry[]): void {
  if (entries.length === 0) {
    const empty = document.createElement("li");
    empty.className = "activity-empty";
    empty.textContent = "No activity yet.";
    list.replaceChildren(empty);
    return;
  }
  // Newest-first already — the backend returns entries in that order.
  list.replaceChildren(...entries.map(buildRow));
}

export function openRecentActivity(): void {
  const content = document.createElement("div");
  content.className = "activity-panel";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = "Recent Activity";
  content.appendChild(heading);

  // Snapshots first: they are the only thing in this overlay that is
  // pending rather than finished, and the only thing with actions on it.
  const snapshotSection = document.createElement("div");
  snapshotSection.className = "snapshot-section";
  content.appendChild(snapshotSection);

  const loadSnapshots = (): void => {
    ListUndoSnapshots()
      .then((snaps) => {
        snapshotSection.replaceChildren();
        if (snaps.length === 0) return;

        const label = document.createElement("h3");
        label.className = "snapshot-section-label";
        label.textContent = snaps.length === 1 ? "Pending undo snapshot" : "Pending undo snapshots";
        snapshotSection.appendChild(label);

        const snapList = document.createElement("ul");
        snapList.className = "snapshot-list";
        for (const s of snaps) {
          snapList.appendChild(buildSnapshotRow(s, loadSnapshots));
        }
        snapshotSection.appendChild(snapList);
      })
      .catch(() => {
        // A snapshot listing that cannot be read must not take the activity
        // log down with it — the log is what this overlay is named for.
        snapshotSection.replaceChildren();
      });
  };

  const logLabel = document.createElement("h3");
  logLabel.className = "snapshot-section-label";
  logLabel.textContent = "Recent";
  content.appendChild(logLabel);

  const list = document.createElement("ul");
  list.className = "activity-filelist";
  const loading = document.createElement("li");
  loading.className = "activity-empty";
  loading.textContent = "Loading…";
  list.appendChild(loading);
  content.appendChild(list);

  openOverlay(content, { dismissable: true });
  loadSnapshots();

  GetRecentActivity()
    .then((entries) => {
      renderEntries(list, entries);
    })
    .catch((err) => {
      const error = document.createElement("li");
      error.className = "activity-empty";
      error.textContent = extractErrorMessage(err);
      list.replaceChildren(error);
    });
}
