import { main } from "../wailsjs/go/models";
import { GetRecentActivity } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { formatDateTime } from "./dates";
import { extractErrorMessage } from "./util";

// Kind -> icon codepoint, verified against the bundled Nerd Font's cmap in
// the UI-overhaul plan (fa-plus_circle, fa-upload, fa-download, fa-undo).
const KIND_ICON: Record<string, string> = {
  add: "",
  update: "",
  restore: "",
  undo: "",
};

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

  const list = document.createElement("ul");
  list.className = "activity-filelist";
  const loading = document.createElement("li");
  loading.className = "activity-empty";
  loading.textContent = "Loading…";
  list.appendChild(loading);
  content.appendChild(list);

  openOverlay(content, { dismissable: true });

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
