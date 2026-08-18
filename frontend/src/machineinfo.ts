import { main } from "../wailsjs/go/models";
import { GetMachineInfo, GetSettings } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { extractErrorMessage } from "./util";

export function buildMachineInfoCard(info: main.MachineInfo): HTMLElement {
  const card = document.createElement("div");
  card.className = "machine-info-card";

  if (!info.kernel && !info.os && !info.hostname && !info.username) {
    const unknown = document.createElement("p");
    unknown.className = "machine-info-unknown";
    unknown.textContent = "Machine info unknown — this vault predates STEP4.";
    card.appendChild(unknown);
    return card;
  }

  const fields: Array<[string, string]> = [
    ["Kernel", info.kernel || "—"],
    ["OS", info.os || "—"],
    ["Hostname", info.hostname || "—"],
    ["User", info.username || "—"],
  ];
  for (const [label, value] of fields) {
    const row = document.createElement("div");
    row.className = "machine-info-row";
    const l = document.createElement("span");
    l.className = "machine-info-label";
    l.textContent = label;
    const v = document.createElement("span");
    v.className = "machine-info-value";
    v.textContent = value;
    row.appendChild(l);
    row.appendChild(v);
    card.appendChild(row);
  }
  return card;
}

/**
 * The machine-info card as a surface of its own.
 *
 * It was previously reachable only from inside the restore preview, which
 * returns early whenever nothing is restorable — so a vault that is
 * perfectly in sync, the normal state, had no way at all to show who wrote
 * it. Since the card describes the vault as a whole, it belongs to the
 * chrome that labels the vault.
 */
export async function openMachineInfo(): Promise<void> {
  const content = document.createElement("div");
  content.className = "machine-info-panel";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "\uf00d";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = "This vault";
  content.appendChild(heading);

  const note = document.createElement("p");
  note.className = "machine-info-note";
  note.textContent = "The machine that last backed anything up into this vault.";
  content.appendChild(note);

  const body = document.createElement("div");
  content.appendChild(body);

  openOverlay(content, { dismissable: true });

  try {
    const [info, settings] = await Promise.all([GetMachineInfo(), GetSettings()]);
    body.appendChild(buildMachineInfoCard(info));

    const pathRow = document.createElement("div");
    pathRow.className = "machine-info-row";
    const l = document.createElement("span");
    l.className = "machine-info-label";
    l.textContent = "Vault";
    const v = document.createElement("span");
    v.className = "machine-info-value";
    v.textContent = settings.vaultPath || "\u2014";
    pathRow.appendChild(l);
    pathRow.appendChild(v);
    body.querySelector(".machine-info-card")?.appendChild(pathRow);
  } catch (err) {
    const error = document.createElement("p");
    error.className = "machine-info-unknown";
    error.textContent = extractErrorMessage(err);
    body.appendChild(error);
  }
}
