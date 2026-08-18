import { main } from "../wailsjs/go/models";
import {
  GetSettings,
  ChooseVaultPath,
  ProbeVaultPath,
  AdoptVaultPath,
  CopyVaultTo,
  MoveVaultTo,
  UseVaultPath,
  UseVaultPathWithAdmin,
  CheckVaultDir,
} from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { refreshMirror } from "./rows";
import { extractErrorMessage } from "./util";

export function refreshVaultPathDisplay(path: string): void {
  const el = document.getElementById("vault-path-display")!;
  el.textContent = path || "not set";
  el.title = path;
}

/**
 * Mandatory, non-dismissable first-launch prompt. Resolves once a vault
 * path has actually been chosen and saved. There is nothing to migrate
 * here — VaultPath was empty, so whatever's at the chosen folder (fresh,
 * or an existing vault carried over from another machine) is simply
 * adopted as-is once refreshMirror() runs afterward.
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
        // UseVaultPath rather than a bare SaveSettings: it creates the
        // folder if it isn't there, proves it can actually be written to,
        // and refuses the handful of places a vault must never live. Saving
        // the raw picker result was how a path that no later write could
        // succeed at got persisted in the first place.
        await UseVaultPath(path);
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

/**
 * Offered when the vault FOLDER can't be reached — an unplugged drive, a
 * deleted folder, a path that now belongs to someone else.
 *
 * Before this existed the user was simply stuck: Copy and Move both died
 * walking the missing source, adopting refused any folder without a
 * manifest, and Change Path had no "just use this one" branch. The saved
 * path could not be changed to anything, which is the state the v1.2.1
 * report was actually written from.
 *
 * Opened ONLY for a directory-level failure. A vault that is present but
 * whose manifest won't parse stays a dismissable message with the app fully
 * usable — see CheckVaultDir.
 */
export function openVaultRecovery(status: main.VaultDirStatus, dismissable: boolean): void {
  const content = document.createElement("div");
  content.className = "vault-prompt";

  if (dismissable) {
    const closeBtn = document.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "overlay-close";
    closeBtn.setAttribute("aria-label", "Close");
    closeBtn.textContent = "";
    closeBtn.addEventListener("click", () => closeOverlay());
    content.appendChild(closeBtn);
  }

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = "Your vault isn't there";
  content.appendChild(heading);

  const explain = document.createElement("p");
  explain.className = "vault-prompt-explain";
  explain.textContent = status.message || `RESONANCE can't reach ${status.path}.`;
  content.appendChild(explain);

  const note = document.createElement("p");
  note.className = "vault-decision-note";
  note.textContent =
    "Nothing has been changed or deleted. If the vault lives on a drive, connecting it and choosing Try Again is usually all it needs.";
  content.appendChild(note);

  const msg = document.createElement("p");
  msg.className = "vault-prompt-status";
  content.appendChild(msg);

  const actions = document.createElement("div");
  actions.className = "vault-decision";
  content.appendChild(actions);

  async function settle(): Promise<void> {
    const settings = await GetSettings();
    refreshVaultPathDisplay(settings.vaultPath);
    closeOverlay();
    await refreshMirror();
  }

  const retryBtn = document.createElement("button");
  retryBtn.type = "button";
  retryBtn.textContent = "Try Again";
  retryBtn.addEventListener("click", async () => {
    retryBtn.disabled = true;
    msg.textContent = "";
    const again = await CheckVaultDir();
    if (again.reachable) {
      await settle();
      return;
    }
    msg.textContent = again.message || "Still can't reach it.";
    retryBtn.disabled = false;
  });
  actions.appendChild(retryBtn);

  // Only creates the folder — it cannot bring back what was in it, and the
  // button's own explanation says so rather than letting "recreate" read as
  // "restore".
  const recreateBtn = document.createElement("button");
  recreateBtn.type = "button";
  recreateBtn.textContent = "Recreate the folder (empty)";
  recreateBtn.title =
    "Creates the folder again at the same path. It will be empty — the files that were in it are not recoverable from here.";
  recreateBtn.addEventListener("click", async () => {
    recreateBtn.disabled = true;
    msg.textContent = "";
    try {
      await UseVaultPath(status.path);
      await settle();
    } catch (err) {
      msg.textContent = extractErrorMessage(err);
      recreateBtn.disabled = false;
    }
  });
  actions.appendChild(recreateBtn);

  const chooseBtn = document.createElement("button");
  chooseBtn.type = "button";
  chooseBtn.textContent = "Choose Another Folder";
  chooseBtn.addEventListener("click", async () => {
    chooseBtn.disabled = true;
    msg.textContent = "";
    try {
      const picked = await ChooseVaultPath();
      if (!picked) {
        chooseBtn.disabled = false;
        return;
      }
      await UseVaultPath(picked);
      await settle();
    } catch (err) {
      msg.textContent = extractErrorMessage(err);
      chooseBtn.disabled = false;
    }
  });
  actions.appendChild(chooseBtn);

  openOverlay(content, { dismissable });
}

function buildDecisionArea(): HTMLDivElement {
  const area = document.createElement("div");
  area.className = "vault-decision";
  return area;
}

/**
 * Dismissable Change Path flow, available any time after the first launch.
 */
export async function openChangePath(): Promise<void> {
  // If the current vault has gone missing, that is the problem to solve
  // first — every branch below reads or copies from it.
  const dirStatus = await CheckVaultDir();
  if (dirStatus.set && !dirStatus.reachable) {
    openVaultRecovery(dirStatus, true);
    return;
  }

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

  // Action first, results below. The button used to sit underneath the area
  // its own result fills, so the overlay read backwards: you looked at an
  // empty space, then found the thing that populates it beneath. Choosing a
  // folder is the one thing this overlay is for, so it comes first, and
  // everything the choice produces appears under it in the order it happens.
  const chooseBtn = document.createElement("button");
  chooseBtn.type = "button";
  chooseBtn.className = "vault-prompt-choose-btn";
  chooseBtn.textContent = "Choose New Folder";
  content.appendChild(chooseBtn);

  const status = document.createElement("p");
  status.className = "vault-prompt-status";
  content.appendChild(status);

  let decisionArea = buildDecisionArea();
  content.appendChild(decisionArea);

  async function finish(): Promise<void> {
    const settings = await GetSettings();
    refreshVaultPathDisplay(settings.vaultPath);
    await refreshMirror();
    closeOverlay();
  }

  chooseBtn.addEventListener("click", async () => {
    chooseBtn.disabled = true;
    status.textContent = "";
    decisionArea.remove();
    decisionArea = buildDecisionArea();
    content.appendChild(decisionArea);

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

      // A folder that belongs to root is usable — reading it needs nothing,
      // and writes go through the helper — but it is worth saying before the
      // password dialog appears rather than after. Said once, here, so every
      // button below inherits the explanation instead of repeating it.
      if (probe.needsAdmin) {
        const note = document.createElement("p");
        note.className = "vault-decision-note";
        note.textContent =
          "This folder belongs to root. RESONANCE can use it, but it will ask for administrator rights once per session before writing anything there.";
        decisionArea.appendChild(note);
      }

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
            // Adopting a root-owned vault is the same decision plus the
            // rights to write in it; the frontend already knows this folder
            // holds a manifest, which is the only thing AdoptVaultPath checks
            // that the admin route does not.
            await (probe.needsAdmin ? UseVaultPathWithAdmin(newPath) : AdoptVaultPath(newPath));
            await finish();
          } catch (err) {
            status.textContent = extractErrorMessage(err);
            useBtn.disabled = false;
          }
        });
        decisionArea.appendChild(msg);
        decisionArea.appendChild(useBtn);
      } else {
        // No emptiness gate. A folder that already holds files used to be
        // refused outright with "choose an empty folder"; picking where your
        // own vault goes is your business, so it is offered like any other.
        const msg = document.createElement("p");
        msg.textContent = "Copy your vault here, or move it?";

        if (!probe.isEmpty) {
          const warn = document.createElement("p");
          warn.className = "vault-decision-note";
          const count =
            probe.entryCount === 1 ? "1 item" : `${probe.entryCount.toLocaleString()} items`;
          warn.textContent = `This folder already has ${count} in it. Copying here won't remove any of them, but files with the same names will be overwritten.`;
          decisionArea.appendChild(warn);
        }

        const copyBtn = document.createElement("button");
        copyBtn.type = "button";
        copyBtn.textContent = "Copy";
        const moveBtn = document.createElement("button");
        moveBtn.type = "button";
        moveBtn.textContent = "Move";
        // Move is the only operation that later deletes the folder it moved
        // away from, so it is the only one withheld when the destination
        // already holds things that were never part of the vault. Copy does
        // the same job here and destroys nothing.
        if (!probe.isEmpty) {
          moveBtn.disabled = true;
          moveBtn.title = "Move isn't offered into a folder that already has files — use Copy";
        }
        // Copy and Move both build the new vault by writing a whole tree into
        // it, and that is the one thing a root-owned destination doesn't get.
        // Pointing RESONANCE at it works, so that is what stays offered.
        if (probe.needsAdmin) {
          copyBtn.disabled = true;
          moveBtn.disabled = true;
          const why = "Copying a vault into a folder that belongs to root isn't offered — use \"Just use this folder\" instead";
          copyBtn.title = why;
          moveBtn.title = why;
        }

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
            moveArmed = false;
          }
        }

        copyBtn.addEventListener("click", () => runMigration(CopyVaultTo, copyBtn));

        // Move permanently deletes everything at the old vault path once
        // the copy is verified — unlike Copy, which never removes anything.
        // A first click only arms it and explains the consequence; the
        // actual migration only runs on the second, explicit confirmation.
        let moveArmed = false;
        moveBtn.addEventListener("click", () => {
          if (!moveArmed) {
            moveArmed = true;
            moveBtn.textContent = "Confirm Move (deletes old vault)";
            status.textContent = `This will copy your vault to the new location, then permanently delete everything at ${current.vaultPath}.`;
            return;
          }
          runMigration(MoveVaultTo, moveBtn);
        });

        // Pointing at a folder without migrating anything — the escape hatch
        // that existed only on first launch before this. Without it, a user
        // whose vault has gone missing has no way to start over: adopting
        // needs a manifest, and migrating needs a source vault to read.
        const freshBtn = document.createElement("button");
        freshBtn.type = "button";
        freshBtn.textContent = "Just use this folder";
        freshBtn.title = "Point RESONANCE here without copying or moving anything";
        freshBtn.addEventListener("click", async () => {
          freshBtn.disabled = true;
          try {
            await (probe.needsAdmin ? UseVaultPathWithAdmin(newPath) : UseVaultPath(newPath));
            await finish();
          } catch (err) {
            status.textContent = extractErrorMessage(err);
            freshBtn.disabled = false;
          }
        });

        decisionArea.insertBefore(msg, decisionArea.firstChild);
        decisionArea.appendChild(copyBtn);
        decisionArea.appendChild(moveBtn);
        decisionArea.appendChild(freshBtn);
      }

      chooseBtn.disabled = false;
    } catch (err) {
      status.textContent = extractErrorMessage(err);
      chooseBtn.disabled = false;
    }
  });

  openOverlay(content, { dismissable: true });
}
