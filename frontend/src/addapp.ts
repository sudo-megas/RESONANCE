import { GetSettings, AddApp, PickFiles } from "../wailsjs/go/main/App";
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

  const chooseFilesBtn = document.createElement("button");
  chooseFilesBtn.type = "button";
  chooseFilesBtn.className = "addapp-choose-files-btn";
  chooseFilesBtn.textContent = "Choose files…";
  content.appendChild(chooseFilesBtn);

  const fileList = document.createElement("ul");
  fileList.className = "addapp-filelist";
  content.appendChild(fileList);

  const errorEl = document.createElement("p");
  errorEl.className = "addapp-error";
  content.appendChild(errorEl);

  const submitBtn = document.createElement("button");
  submitBtn.type = "button";
  submitBtn.className = "addapp-submit";
  submitBtn.textContent = "Add";
  content.appendChild(submitBtn);

  const paths: string[] = [];

  function renderFileList(): void {
    fileList.innerHTML = "";
    for (const path of paths) {
      const li = document.createElement("li");
      li.className = "addapp-file-row";

      const label = document.createElement("span");
      label.className = "addapp-file-path";
      label.textContent = path;

      const removeBtn = document.createElement("button");
      removeBtn.type = "button";
      removeBtn.className = "addapp-file-remove";
      removeBtn.setAttribute("aria-label", `Remove ${path}`);
      removeBtn.textContent = "\uf00d";
      removeBtn.addEventListener("click", () => {
        const idx = paths.indexOf(path);
        if (idx !== -1) paths.splice(idx, 1);
        renderFileList();
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

  chooseFilesBtn.addEventListener("click", async () => {
    try {
      const picked = await PickFiles();
      for (const p of picked) {
        if (!paths.includes(p)) paths.push(p);
      }
      renderFileList();
    } catch (err) {
      errorEl.textContent = extractErrorMessage(err);
    }
  });

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
