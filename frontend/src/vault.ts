import {
  GetSettings,
  SaveSettings,
  ChooseVaultPath,
  ProbeVaultPath,
  AdoptVaultPath,
  CopyVaultTo,
  MoveVaultTo,
  ListApps,
} from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { renderRows } from "./rows";

export function refreshVaultPathDisplay(path: string): void {
  const el = document.getElementById("vault-path-display")!;
  el.textContent = path || "not set";
  el.title = path;
}

async function refreshRows(): Promise<void> {
  try {
    const apps = await ListApps();
    renderRows(apps);
  } catch (err) {
    console.error(err);
  }
}

function extractErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * Mandatory, non-dismissable first-launch prompt. Resolves once a vault
 * path has actually been chosen and saved. There is nothing to migrate
 * here — VaultPath was empty, so whatever's at the chosen folder (fresh,
 * or an existing vault carried over from another machine) is simply
 * adopted as-is once ListApps/renderRows run afterward.
 */
export function openVaultPrompt(): Promise<void> {
  return new Promise((resolve) => {
    const content = document.createElement("div");
    content.className = "vault-prompt";

    const heading = document.createElement("h2");
    heading.className = "overlay-heading";
    heading.textContent = "Choose a vault location";
    content.appendChild(heading);

    const explain = document.createElement("p");
    explain.className = "vault-prompt-explain";
    explain.textContent =
      "This is where RESONANCE keeps your backed-up files. Any folder works — another drive, a USB stick, anywhere.";
    content.appendChild(explain);

    const status = document.createElement("p");
    status.className = "vault-prompt-status";
    content.appendChild(status);

    const chooseBtn = document.createElement("button");
    chooseBtn.type = "button";
    chooseBtn.className = "vault-prompt-choose-btn";
    chooseBtn.textContent = "Choose Folder";
    content.appendChild(chooseBtn);

    chooseBtn.addEventListener("click", async () => {
      chooseBtn.disabled = true;
      status.textContent = "";
      try {
        const path = await ChooseVaultPath();
        if (!path) {
          status.textContent = "No folder chosen yet — RESONANCE needs one to continue.";
          chooseBtn.disabled = false;
          return;
        }
        const current = await GetSettings();
        await SaveSettings({ ...current, vaultPath: path });
        refreshVaultPathDisplay(path);
        closeOverlay();
        resolve();
      } catch (err) {
        status.textContent = extractErrorMessage(err);
        chooseBtn.disabled = false;
      }
    });

    openOverlay(content, { dismissable: false });
  });
}

function buildDecisionArea(): HTMLDivElement {
  const area = document.createElement("div");
  area.className = "vault-decision";
  return area;
}

/**
 * Dismissable Change Path flow, available any time after the first launch.
 */
export function openChangePath(): void {
  const content = document.createElement("div");
  content.className = "vault-prompt";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "\uf00d";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = "Change Path";
  content.appendChild(heading);

  const status = document.createElement("p");
  status.className = "vault-prompt-status";
  content.appendChild(status);

  let decisionArea = buildDecisionArea();
  content.appendChild(decisionArea);

  const chooseBtn = document.createElement("button");
  chooseBtn.type = "button";
  chooseBtn.className = "vault-prompt-choose-btn";
  chooseBtn.textContent = "Choose New Folder";
  content.appendChild(chooseBtn);

  async function finish(): Promise<void> {
    const settings = await GetSettings();
    refreshVaultPathDisplay(settings.vaultPath);
    await refreshRows();
    closeOverlay();
  }

  chooseBtn.addEventListener("click", async () => {
    chooseBtn.disabled = true;
    status.textContent = "";
    decisionArea.remove();
    decisionArea = buildDecisionArea();
    content.insertBefore(decisionArea, chooseBtn);

    try {
      const newPath = await ChooseVaultPath();
      if (!newPath) {
        chooseBtn.disabled = false;
        return;
      }

      const current = await GetSettings();
      if (newPath === current.vaultPath) {
        status.textContent = "That's already your current vault.";
        chooseBtn.disabled = false;
        return;
      }

      const probe = await ProbeVaultPath(newPath);

      if (probe.hasManifest) {
        const count = probe.appCount === 1 ? "1 app" : `${probe.appCount} apps`;
        const msg = document.createElement("p");
        msg.textContent = `This folder is already a RESONANCE vault (${count}).`;
        const useBtn = document.createElement("button");
        useBtn.type = "button";
        useBtn.textContent = "Use this vault";
        useBtn.addEventListener("click", async () => {
          useBtn.disabled = true;
          try {
            await AdoptVaultPath(newPath);
            await finish();
          } catch (err) {
            status.textContent = extractErrorMessage(err);
            useBtn.disabled = false;
          }
        });
        decisionArea.appendChild(msg);
        decisionArea.appendChild(useBtn);
      } else if (probe.isEmpty) {
        const msg = document.createElement("p");
        msg.textContent = "Copy your vault here, or move it?";
        const copyBtn = document.createElement("button");
        copyBtn.type = "button";
        copyBtn.textContent = "Copy";
        const moveBtn = document.createElement("button");
        moveBtn.type = "button";
        moveBtn.textContent = "Move";

        async function runMigration(fn: (path: string) => Promise<void>, btn: HTMLButtonElement): Promise<void> {
          copyBtn.disabled = true;
          moveBtn.disabled = true;
          btn.textContent = "Working…";
          try {
            await fn(newPath);
            await finish();
          } catch (err) {
            status.textContent = extractErrorMessage(err);
            copyBtn.disabled = false;
            moveBtn.disabled = false;
            copyBtn.textContent = "Copy";
            moveBtn.textContent = "Move";
          }
        }

        copyBtn.addEventListener("click", () => runMigration(CopyVaultTo, copyBtn));
        moveBtn.addEventListener("click", () => runMigration(MoveVaultTo, moveBtn));

        decisionArea.appendChild(msg);
        decisionArea.appendChild(copyBtn);
        decisionArea.appendChild(moveBtn);
      } else {
        status.textContent =
          "This folder isn't empty and isn't a RESONANCE vault. Choose an empty folder, or one that already has a RESONANCE vault.";
      }

      chooseBtn.disabled = false;
    } catch (err) {
      status.textContent = extractErrorMessage(err);
      chooseBtn.disabled = false;
    }
  });

  openOverlay(content, { dismissable: true });
}
