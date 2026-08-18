import { main } from "../wailsjs/go/models";
import {
  GetSettings,
  GetAppComposition,
  GetUndoInfo,
  AddToApp,
  RemoveFromApp,
  RemoveApp,
  RenameApp,
  UntrackDir,
  PreviewUntrackDir,
  PickFiles,
  PickFolders,
} from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { showToast } from "./toast";
import { refreshMirror } from "./rows";
import { extractErrorMessage } from "./util";

/** Rows rendered at once in the "currently in the vault" list.
 *
 * Not a cap on what can be removed — capping the payload would make files
 * beyond the cap permanently unremovable, which is worse than the problem.
 * This caps only the DOM: an app tracking ~/.config can hold tens of
 * thousands of entries, and building that many rows with a button each
 * freezes the webview while the overlay is mid-transition. The filter box
 * reaches anything the cap hides. */
const MAX_RENDERED_ROWS = 300;

// Mirrors manifest.go's validAppName. The server call stays authoritative;
// this only avoids a round trip on input that is obviously wrong.
function clientValidAppName(name: string): string | null {
  const trimmed = name.trim();
  if (trimmed === "") return "App name can't be empty";
  if (trimmed === "." || trimmed === "..") return "That name isn't allowed";
  if (/[/\\]/.test(trimmed)) return "App name can't contain a slash";
  if (trimmed.startsWith(".")) return "App name can't start with a dot";
  if (trimmed.toLowerCase() === "manifest.json") return "That name is reserved";
  return null;
}

function coveredByDir(path: string, dir: string): boolean {
  return path === dir || path.startsWith(dir + "/");
}

/** Whether an absolute picked path denotes the same file as a $HOME-relative
 * manifest path.
 *
 * The frontend never learns $HOME — the picker hands back absolute paths and
 * stageAdd computes the relative form itself — so the two lists can only be
 * matched by suffix. That is exact in practice: every manifest path is
 * $HOME-relative, so the absolute form of it always ends with the separator
 * plus that path. */
function sameEntry(absPath: string, relPath: string): boolean {
  return absPath === relPath || absPath.endsWith("/" + relPath);
}

/**
 * The edit overlay for one app.
 *
 * Everything is staged: clicking ✕ moves a row into the "will be removed"
 * list and nothing reaches Go until Save. The removal half is the part that
 * needed care — the stated worry about a delete button is that it eats real
 * dotfiles, so each staged removal names both paths, the one being deleted
 * and the one being kept.
 */
export async function openEditApp(appName: string): Promise<void> {
  let comp: main.AppComposition;
  let vaultPath = "";
  try {
    const settings = await GetSettings();
    vaultPath = settings.vaultPath;
    comp = await GetAppComposition(appName);
  } catch (err) {
    showToast(extractErrorMessage(err));
    return;
  }

  let name = comp.name;
  const content = document.createElement("div");
  content.className = "addapp-form";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "\uf00d";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = `Edit ${name}`;
  content.appendChild(heading);

  // --- rename ------------------------------------------------------------

  const nameLabel = document.createElement("label");
  nameLabel.className = "addapp-label";
  nameLabel.textContent = "Name";
  nameLabel.htmlFor = "editapp-name";
  content.appendChild(nameLabel);

  const renameRow = document.createElement("div");
  renameRow.className = "editapp-rename-row";
  const nameInput = document.createElement("input");
  nameInput.type = "text";
  nameInput.id = "editapp-name";
  nameInput.className = "addapp-input";
  nameInput.value = name;
  const renameBtn = document.createElement("button");
  renameBtn.type = "button";
  renameBtn.className = "addapp-choose-files-btn";
  renameBtn.textContent = "Rename";
  renameBtn.disabled = true;
  renameRow.appendChild(nameInput);
  renameRow.appendChild(renameBtn);
  content.appendChild(renameRow);

  // --- current contents ---------------------------------------------------

  const currentLabel = document.createElement("p");
  currentLabel.className = "editapp-section-label";
  currentLabel.textContent = "Currently in the vault";
  content.appendChild(currentLabel);

  const filterInput = document.createElement("input");
  filterInput.type = "text";
  filterInput.className = "addapp-input editapp-filter";
  filterInput.placeholder = "Filter…";
  content.appendChild(filterInput);

  const countNote = document.createElement("p");
  countNote.className = "editapp-count-note";
  content.appendChild(countNote);

  const currentList = document.createElement("ul");
  currentList.className = "addapp-filelist";
  content.appendChild(currentList);

  // --- additions ----------------------------------------------------------

  const chooseRow = document.createElement("div");
  chooseRow.className = "addapp-choose-row";
  const chooseFilesBtn = document.createElement("button");
  chooseFilesBtn.type = "button";
  chooseFilesBtn.className = "addapp-choose-files-btn";
  chooseFilesBtn.textContent = "Choose files…";
  chooseRow.appendChild(chooseFilesBtn);
  const chooseFoldersBtn = document.createElement("button");
  chooseFoldersBtn.type = "button";
  chooseFoldersBtn.className = "addapp-choose-files-btn";
  chooseFoldersBtn.textContent = "Choose folders…";
  chooseRow.appendChild(chooseFoldersBtn);
  content.appendChild(chooseRow);

  const addList = document.createElement("ul");
  addList.className = "addapp-filelist";
  content.appendChild(addList);

  // --- staged removals ----------------------------------------------------

  const removeSection = document.createElement("div");
  removeSection.className = "editapp-remove-section";
  content.appendChild(removeSection);

  const errorEl = document.createElement("p");
  errorEl.className = "addapp-error";
  content.appendChild(errorEl);

  const actions = document.createElement("div");
  actions.className = "editapp-actions";
  const saveBtn = document.createElement("button");
  saveBtn.type = "button";
  saveBtn.className = "addapp-submit";
  saveBtn.textContent = "Save";
  const cancelBtn = document.createElement("button");
  cancelBtn.type = "button";
  cancelBtn.className = "editapp-cancel-btn";
  cancelBtn.textContent = "Cancel";
  cancelBtn.addEventListener("click", () => closeOverlay());
  actions.appendChild(saveBtn);
  actions.appendChild(cancelBtn);
  content.appendChild(actions);

  const removeAppBtn = document.createElement("button");
  removeAppBtn.type = "button";
  removeAppBtn.className = "editapp-remove-app-btn";
  removeAppBtn.textContent = "Remove app from vault";
  content.appendChild(removeAppBtn);

  // --- state --------------------------------------------------------------

  const pendingAdds: string[] = [];
  const pendingAddFolders = new Set<string>();
  const removeFiles = new Set<string>();
  const removeDirs = new Set<string>();
  let gateChecked = false;

  /** Staging a removal drops any pending add for the same file, and vice
   * versa. Without this the two lists can disagree and Save silently loses
   * the user's work: adds run first, re-adding a path already tracked
   * collapses to a no-op in stageAdd, and the removal then deletes it — so a
   * deliberate remove-then-re-add ends with the file simply gone. */
  function unstageAddsFor(relPath: string): void {
    for (let i = pendingAdds.length - 1; i >= 0; i--) {
      if (sameEntry(pendingAdds[i], relPath)) {
        pendingAddFolders.delete(pendingAdds[i]);
        pendingAdds.splice(i, 1);
      }
    }
  }

  function unstageRemovalsFor(absPath: string): void {
    for (const rel of [...removeFiles]) {
      if (sameEntry(absPath, rel)) removeFiles.delete(rel);
    }
    for (const rel of [...removeDirs]) {
      if (sameEntry(absPath, rel)) removeDirs.delete(rel);
    }
  }

  function stageFileRemoval(path: string): void {
    removeFiles.add(path);
    unstageAddsFor(path);
    renderAll();
  }

  function stageDirRemoval(path: string): void {
    removeDirs.add(path);
    unstageAddsFor(path);
    renderAll();
  }

  function coveringDir(path: string): string | null {
    for (const d of comp.dirs) {
      if (coveredByDir(path, d.path)) return d.path;
    }
    return null;
  }

  function renderCurrent(): void {
    currentList.innerHTML = "";
    const needle = filterInput.value.trim().toLowerCase();
    const matches = (s: string) => needle === "" || s.toLowerCase().includes(needle);

    let shown = 0;
    let total = 0;

    for (const d of comp.dirs) {
      total++;
      if (!matches(d.path) || shown >= MAX_RENDERED_ROWS) continue;
      shown++;

      const li = document.createElement("li");
      li.className = "addapp-file-row addapp-file-row--folder";
      if (removeDirs.has(d.path)) li.classList.add("editapp-row--staged");

      const label = document.createElement("span");
      label.className = "addapp-file-path";
      const n = d.fileCount;
      label.textContent = `${d.path}/ — ${n === 1 ? "1 backed-up file" : `${n.toLocaleString()} backed-up files`}`;
      li.appendChild(label);

      // Removing one file out of a tracked folder cannot work: the walk
      // would find it again on the next refresh and the next update would
      // copy it back. Untracking the folder is the way through, and it is
      // offered rather than explained away.
      const untrackBtn = document.createElement("button");
      untrackBtn.type = "button";
      untrackBtn.className = "editapp-untrack-btn";
      untrackBtn.textContent = "Untrack folder";
      let armed = false;
      untrackBtn.addEventListener("click", async () => {
        errorEl.textContent = "";
        if (!armed) {
          untrackBtn.disabled = true;
          try {
            const p = await PreviewUntrackDir(name, d.path);
            armed = true;
            untrackBtn.textContent = "Confirm untrack";
            errorEl.textContent =
              `${d.path}/ stops being watched as a folder. ` +
              `${p.keepsTracked === 1 ? "1 backed-up file stays" : `${p.keepsTracked.toLocaleString()} backed-up files stay`} tracked individually` +
              (p.stopsTracking > 0
                ? `, and ${p.stopsTracking === 1 ? "1 file that has never been backed up stops" : `${p.stopsTracking.toLocaleString()} files that have never been backed up stop`} being tracked. Update from source first if you want to keep ${p.stopsTracking === 1 ? "it" : "them"}.`
                : ". Nothing is copied or deleted.");
          } catch (err) {
            errorEl.textContent = extractErrorMessage(err);
          }
          untrackBtn.disabled = false;
          return;
        }
        untrackBtn.disabled = true;
        try {
          await UntrackDir(name, d.path);
          comp = await GetAppComposition(name);
          errorEl.textContent = "";
          renderAll();
          await refreshMirror();
        } catch (err) {
          errorEl.textContent = extractErrorMessage(err);
          untrackBtn.disabled = false;
        }
      });
      li.appendChild(untrackBtn);

      const removeBtn = document.createElement("button");
      removeBtn.type = "button";
      removeBtn.className = "addapp-file-remove";
      removeBtn.setAttribute("aria-label", `Remove the folder ${d.path} from the vault`);
      removeBtn.textContent = "\uf00d";
      removeBtn.addEventListener("click", () => stageDirRemoval(d.path));
      li.appendChild(removeBtn);

      currentList.appendChild(li);
    }

    for (const path of comp.files) {
      total++;
      if (!matches(path) || shown >= MAX_RENDERED_ROWS) continue;
      shown++;

      const li = document.createElement("li");
      li.className = "addapp-file-row";
      if (removeFiles.has(path)) li.classList.add("editapp-row--staged");

      const label = document.createElement("span");
      label.className = "addapp-file-path";
      label.textContent = path;
      li.appendChild(label);

      const covering = coveringDir(path);
      if (covering) {
        // Inside a tracked folder — its fate belongs to the folder above.
        const note = document.createElement("span");
        note.className = "editapp-covered-note";
        note.textContent = `in ${covering}/`;
        li.appendChild(note);
      } else {
        const removeBtn = document.createElement("button");
        removeBtn.type = "button";
        removeBtn.className = "addapp-file-remove";
        removeBtn.setAttribute("aria-label", `Remove ${path} from the vault`);
        removeBtn.textContent = "\uf00d";
        removeBtn.addEventListener("click", () => stageFileRemoval(path));
        li.appendChild(removeBtn);
      }

      currentList.appendChild(li);
    }

    if (total === 0) {
      const empty = document.createElement("li");
      empty.className = "editapp-empty-note";
      empty.textContent = "This app holds nothing. Add files, or remove the app.";
      currentList.appendChild(empty);
      countNote.textContent = "";
      return;
    }
    countNote.textContent =
      shown === total
        ? `${total.toLocaleString()} ${total === 1 ? "entry" : "entries"}`
        : `showing ${shown.toLocaleString()} of ${total.toLocaleString()} — type to filter`;
  }

  function renderAdds(): void {
    addList.innerHTML = "";
    for (const path of pendingAdds) {
      const li = document.createElement("li");
      li.className = "addapp-file-row";
      if (pendingAddFolders.has(path)) li.classList.add("addapp-file-row--folder");

      const label = document.createElement("span");
      label.className = "addapp-file-path";
      label.textContent = pendingAddFolders.has(path) ? `${path}/` : path;
      li.appendChild(label);

      const removeBtn = document.createElement("button");
      removeBtn.type = "button";
      removeBtn.className = "addapp-file-remove";
      removeBtn.setAttribute("aria-label", `Don't add ${path}`);
      removeBtn.textContent = "\uf00d";
      removeBtn.addEventListener("click", () => {
        const i = pendingAdds.indexOf(path);
        if (i !== -1) pendingAdds.splice(i, 1);
        pendingAddFolders.delete(path);
        renderAll();
      });
      li.appendChild(removeBtn);
      addList.appendChild(li);
    }
  }

  function renderRemovals(): void {
    removeSection.innerHTML = "";
    const staged = [...removeDirs, ...removeFiles];
    if (staged.length === 0) {
      gateChecked = false;
      return;
    }

    const label = document.createElement("p");
    label.className = "editapp-section-label";
    label.textContent = "Will be removed from the vault";
    removeSection.appendChild(label);

    const list = document.createElement("ul");
    list.className = "editapp-remove-list";
    for (const path of staged) {
      const li = document.createElement("li");
      li.className = "editapp-remove-row";

      const head = document.createElement("div");
      head.className = "editapp-remove-head";
      const p = document.createElement("span");
      p.className = "addapp-file-path";
      p.textContent = removeDirs.has(path) ? `${path}/` : path;
      head.appendChild(p);

      const unstage = document.createElement("button");
      unstage.type = "button";
      unstage.className = "addapp-file-remove";
      unstage.setAttribute("aria-label", `Keep ${path}`);
      unstage.textContent = "\uf00d";
      unstage.addEventListener("click", () => {
        removeFiles.delete(path);
        removeDirs.delete(path);
        renderAll();
      });
      head.appendChild(unstage);
      li.appendChild(head);

      // Naming both paths is the most convincing element of the whole
      // overlay: it shows, by name, the file that will NOT be touched.
      const pair = document.createElement("div");
      pair.className = "editapp-path-pair";
      const del = document.createElement("div");
      del.className = "editapp-path-delete";
      del.textContent = `deletes  ${vaultPath}/${name}/${path}`;
      const keep = document.createElement("div");
      keep.className = "editapp-path-keep";
      keep.textContent = `keeps    ~/${path}`;
      pair.appendChild(del);
      pair.appendChild(keep);
      li.appendChild(pair);

      if (removeDirs.has(path)) {
        const note = document.createElement("div");
        note.className = "editapp-remove-note";
        note.textContent = "Removing a tracked folder removes every backed-up file inside it.";
        li.appendChild(note);
      }

      list.appendChild(li);
    }
    removeSection.appendChild(list);

    const reassurance = document.createElement("p");
    reassurance.className = "editapp-reassurance";
    reassurance.textContent =
      "RESONANCE only ever deletes its own copy. Nothing in your home folder is changed.";
    removeSection.appendChild(reassurance);

    const gateLabel = document.createElement("label");
    gateLabel.className = "restore-confirm-gate";
    const gate = document.createElement("input");
    gate.type = "checkbox";
    gate.className = "restore-confirm-checkbox";
    gate.checked = gateChecked;
    gate.addEventListener("change", () => {
      gateChecked = gate.checked;
      updateSaveState();
    });
    const gateText = document.createElement("span");
    gateText.textContent =
      "I understand the vault copies will be deleted. My files in ~ are not touched.";
    gateLabel.appendChild(gate);
    gateLabel.appendChild(gateText);
    removeSection.appendChild(gateLabel);
  }

  function updateSaveState(): void {
    const hasRemovals = removeFiles.size > 0 || removeDirs.size > 0;
    const hasWork = hasRemovals || pendingAdds.length > 0;
    saveBtn.disabled = !hasWork || (hasRemovals && !gateChecked);
  }

  function renderAll(): void {
    renderCurrent();
    renderAdds();
    renderRemovals();
    updateSaveState();
  }

  filterInput.addEventListener("input", () => renderCurrent());

  async function pickInto(pick: () => Promise<string[]>, asFolders: boolean): Promise<void> {
    try {
      const picked = await pick();
      for (const p of picked) {
        unstageRemovalsFor(p);
        if (!pendingAdds.includes(p)) pendingAdds.push(p);
        if (asFolders) pendingAddFolders.add(p);
      }
      renderAll();
    } catch (err) {
      errorEl.textContent = extractErrorMessage(err);
    }
  }

  chooseFilesBtn.addEventListener("click", () => void pickInto(PickFiles, false));
  chooseFoldersBtn.addEventListener("click", () => void pickInto(PickFolders, true));

  nameInput.addEventListener("input", () => {
    renameBtn.disabled =
      nameInput.value.trim() === name || clientValidAppName(nameInput.value) !== null;
  });

  renameBtn.addEventListener("click", async () => {
    const wanted = nameInput.value.trim();
    const invalid = clientValidAppName(wanted);
    if (invalid) {
      errorEl.textContent = invalid;
      return;
    }
    renameBtn.disabled = true;
    errorEl.textContent = "";
    try {
      await RenameApp(name, wanted);
      name = wanted;
      heading.textContent = `Edit ${name}`;
      comp = await GetAppComposition(name);
      renderAll();
      await refreshMirror();
      showToast(`Renamed to ${name}`);
    } catch (err) {
      errorEl.textContent = extractErrorMessage(err);
      renameBtn.disabled = false;
    }
  });

  /** Re-read the truth from disk and drop every staged change.
   *
   * Used after a partial or failed save: what the user staged no longer
   * describes what is there, so showing it again would invite them to
   * repeat work that already happened. */
  async function resyncFromDisk(): Promise<void> {
    comp = await GetAppComposition(name);
    removeFiles.clear();
    removeDirs.clear();
    pendingAdds.length = 0;
    pendingAddFolders.clear();
    gateChecked = false;
    renderAll();
    await refreshMirror();
  }

  saveBtn.addEventListener("click", async () => {
    saveBtn.disabled = true;
    cancelBtn.disabled = true;
    errorEl.textContent = "";
    try {
      // Additions first, so a swap-shaped edit never leaves the app
      // momentarily holding nothing.
      if (pendingAdds.length > 0) {
        await AddToApp(name, pendingAdds);
      }
      let summary = "Saved";
      if (removeFiles.size > 0 || removeDirs.size > 0) {
        const result = await RemoveFromApp(name, [...removeFiles], [...removeDirs]);
        if (result.failed.length > 0) {
          errorEl.textContent = result.failed.map((f) => `${f.path}: ${f.reason}`).join("\n");
          await resyncFromDisk();
          cancelBtn.disabled = false;
          return;
        }
        const n = result.removedFiles.length + result.removedDirs.length;
        summary = n === 1 ? "1 item removed from the vault" : `${n} items removed from the vault`;
      }
      closeOverlay();
      showToast(summary);
      await refreshMirror();
    } catch (err) {
      // The call may have applied part of its work before throwing — the
      // same rule update.ts follows — so the mirror is refreshed either way.
      //
      // The specific trap worth naming: if the vault copies were deleted but
      // the manifest save failed, every removed file now reads as
      // "backup missing from the vault", and the app's own advice for that
      // state is to update from source — which would copy every one of them
      // straight back. Say so, rather than letting the mirror give advice
      // that undoes what the user just asked for.
      errorEl.textContent =
        extractErrorMessage(err) +
        "\n\nIf any of these now show as “backup missing from the vault”, remove them again " +
        "here rather than using Update from source — Update would copy them back.";
      try {
        await resyncFromDisk();
      } catch {
        // Already reporting a failure; a failed re-read must not replace it.
      }
      saveBtn.disabled = false;
      cancelBtn.disabled = false;
    }
  });

  // Removing the whole app is irreversible and has one consequence, so it
  // uses the arm-then-confirm shape the Move-vault button already uses
  // rather than the checkbox gate, which belongs to list-shaped choices.
  let removeArmed = false;
  removeAppBtn.addEventListener("click", async () => {
    if (!removeArmed) {
      removeArmed = true;
      const n = comp.files.length;
      removeAppBtn.textContent = `Confirm — deletes ${n === 1 ? "1 vault copy" : `${n.toLocaleString()} vault copies`}`;
      let msg = `This deletes the vault's whole folder for ${name}. Your files in ~ are not touched.`;
      try {
        const undo = await GetUndoInfo(name);
        if (undo.available) {
          msg += ` The pending undo snapshot for ${name} is kept — find it under Recent activity.`;
        }
      } catch {
        // Advisory only; failing it must not block the removal or leave the
        // button armed with no explanation.
      }
      errorEl.textContent = msg;
      return;
    }
    removeAppBtn.disabled = true;
    try {
      await RemoveApp(name);
      closeOverlay();
      showToast(`Removed ${name} from the vault`);
      await refreshMirror();
    } catch (err) {
      errorEl.textContent = extractErrorMessage(err);
      removeAppBtn.disabled = false;
      removeArmed = false;
      removeAppBtn.textContent = "Remove app from vault";
    }
  });

  renderAll();
  openOverlay(content, { dismissable: true });
}
