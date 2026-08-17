import { GetSettings, AddApp, PickFiles, PickFolders, PreviewPaths } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { showToast } from "./toast";
import { refreshMirror } from "./rows";
import { openVaultPrompt } from "./vault";
import { extractErrorMessage } from "./util";

// Mirrors manifest.go's validAppName — the server call is still
// authoritative, this just avoids a round trip for obviously-invalid input.
function clientValidAppName(name: string): string | null {
  const trimmed = name.trim();
  if (trimmed === "") return "App name can't be empty";
  if (trimmed === "." || trimmed === "..") return "That name isn't allowed";
  if (/[/\\]/.test(trimmed)) return "App name can't contain a slash";
  if (trimmed.startsWith(".")) return "App name can't start with a dot";
  if (trimmed.toLowerCase() === "manifest.json") return "That name is reserved";
  return null;
}

export async function openAddApp(): Promise<void> {
  const settings = await GetSettings();
  if (!settings.vaultPath) {
    await openVaultPrompt();
    return openAddApp();
  }

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
  heading.textContent = "Add app";
  content.appendChild(heading);

  const nameLabel = document.createElement("label");
  nameLabel.className = "addapp-label";
  nameLabel.textContent = "Name";
  nameLabel.htmlFor = "addapp-name";
  content.appendChild(nameLabel);

  const nameInput = document.createElement("input");
  nameInput.type = "text";
  nameInput.id = "addapp-name";
  nameInput.className = "addapp-input";
  nameInput.placeholder = "bash";
  content.appendChild(nameInput);

  // Two buttons rather than one, because the XDG desktop portal's directory
  // option is a mode switch and not a filter: a dialog either returns files
  // or returns folders, and no single dialog can return both. They feed the
  // same list, so the mix happens here instead.
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

  const fileList = document.createElement("ul");
  fileList.className = "addapp-filelist";
  content.appendChild(fileList);

  // Choosing a folder is the user's business, so nothing here blocks — but
  // "~/.config" can mean tens of thousands of files, and being told the size
  // of the commitment beforehand is not the same as discovering it after.
  const previewEl = document.createElement("p");
  previewEl.className = "addapp-preview";
  content.appendChild(previewEl);

  const errorEl = document.createElement("p");
  errorEl.className = "addapp-error";
  content.appendChild(errorEl);

  const submitBtn = document.createElement("button");
  submitBtn.type = "button";
  submitBtn.className = "addapp-submit";
  submitBtn.textContent = "Add";
  content.appendChild(submitBtn);

  const paths: string[] = [];
  const folders = new Set<string>();

  async function refreshPreview(): Promise<void> {
    if (paths.length === 0) {
      previewEl.textContent = "";
      return;
    }
    try {
      const p = await PreviewPaths(paths);
      if (p.folderCount === 0) {
        previewEl.textContent = "";
        return;
      }
      const files = p.fileCount === 1 ? "1 file" : `${p.fileCount.toLocaleString()} files`;
      const dirs = p.folderCount === 1 ? "1 folder" : `${p.folderCount} folders`;
      previewEl.textContent = `${files} from ${dirs} — the folders stay tracked, so anything added to them later is picked up too.`;
    } catch {
      previewEl.textContent = "";
    }
  }

  function renderFileList(): void {
    fileList.innerHTML = "";
    for (const path of paths) {
      const li = document.createElement("li");
      li.className = "addapp-file-row";

      const label = document.createElement("span");
      label.className = "addapp-file-path";
      label.textContent = folders.has(path) ? `${path}/` : path;
      if (folders.has(path)) li.classList.add("addapp-file-row--folder");

      const removeBtn = document.createElement("button");
      removeBtn.type = "button";
      removeBtn.className = "addapp-file-remove";
      removeBtn.setAttribute("aria-label", `Remove ${path}`);
      removeBtn.textContent = "\uf00d";
      removeBtn.addEventListener("click", () => {
        const idx = paths.indexOf(path);
        if (idx !== -1) paths.splice(idx, 1);
        folders.delete(path);
        renderFileList();
        void refreshPreview();
      });

      li.appendChild(label);
      li.appendChild(removeBtn);
      fileList.appendChild(li);
    }
    updateSubmitState();
  }

  function updateSubmitState(): void {
    const nameError = clientValidAppName(nameInput.value);
    submitBtn.disabled = nameError !== null || paths.length === 0;
  }

  nameInput.addEventListener("input", updateSubmitState);

  async function pickInto(pick: () => Promise<string[]>, asFolders: boolean): Promise<void> {
    try {
      const picked = await pick();
      for (const p of picked) {
        if (!paths.includes(p)) paths.push(p);
        if (asFolders) folders.add(p);
      }
      renderFileList();
      await refreshPreview();
    } catch (err) {
      errorEl.textContent = extractErrorMessage(err);
    }
  }

  chooseFilesBtn.addEventListener("click", () => void pickInto(PickFiles, false));
  chooseFoldersBtn.addEventListener("click", () => void pickInto(PickFolders, true));

  submitBtn.addEventListener("click", async () => {
    const nameError = clientValidAppName(nameInput.value);
    if (nameError) {
      errorEl.textContent = nameError;
      return;
    }
    submitBtn.disabled = true;
    errorEl.textContent = "";
    try {
      await AddApp(nameInput.value.trim(), paths);
      closeOverlay();
      showToast(`Added ${nameInput.value.trim()}`);
      await refreshMirror();
    } catch (err) {
      errorEl.textContent = extractErrorMessage(err);
      submitBtn.disabled = false;
    }
  });

  updateSubmitState();
  openOverlay(content, { dismissable: true });
}
