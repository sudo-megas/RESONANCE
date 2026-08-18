import { main } from "../wailsjs/go/models";
import {
  GetMachineInfo,
  GetSettings,
  ScanVaultOrphans,
  RemoveVaultOrphans,
} from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { extractErrorMessage, formatSize } from "./util";
import { showToast } from "./toast";

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

/**
 * The list is shortened at this many rows; the deletion never is. Capping the
 * payload instead would make files beyond the cap permanently undeletable,
 * which is the bug this whole release exists to stop shipping.
 */
const MAX_RENDERED_ORPHANS = 300;

/**
 * Vault files that no manifest entry accounts for.
 *
 * This lives inside "This vault" rather than getting a surface of its own
 * because an orphan has no app to belong to — it is a property of the vault,
 * which is exactly what this overlay already describes. It is also the only
 * button in the program that deletes a file nothing references, so it says
 * plainly where the files came from before offering to remove them: a user
 * who cannot tell why a file is listed cannot judge whether to agree.
 */
function buildOrphanSection(): HTMLElement {
  const section = document.createElement("div");
  section.className = "orphan-section";

  const render = (): void => {
    ScanVaultOrphans()
      .then((report) => {
        section.replaceChildren();
        if (report.files.length === 0) return;

        const label = document.createElement("h3");
        label.className = "snapshot-section-label";
        label.textContent = "Unaccounted-for files";
        section.appendChild(label);

        const note = document.createElement("p");
        note.className = "orphan-note";
        note.textContent =
          "These sit in the vault, but nothing in it says which app they belong to. They are left behind by a copy that was interrupted, or by an older version that had no way to remove anything. Deleting them frees the space and changes no backup.";
        section.appendChild(note);

        const list = document.createElement("ul");
        list.className = "orphan-list";
        for (const f of report.files.slice(0, MAX_RENDERED_ORPHANS)) {
          const li = document.createElement("li");
          li.className = "orphan-row";
          li.textContent = f;
          list.appendChild(li);
        }
        section.appendChild(list);

        if (report.files.length > MAX_RENDERED_ORPHANS) {
          const more = document.createElement("p");
          more.className = "orphan-note";
          more.textContent = `\u2026 and ${report.files.length - MAX_RENDERED_ORPHANS} more. All ${report.files.length} are covered by the button below \u2014 the list is shortened, the action is not.`;
          section.appendChild(more);
        }

        const noun = report.files.length === 1 ? "file" : "files";
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "orphan-delete-btn";
        btn.textContent = `Delete ${report.files.length} unaccounted-for ${noun} (${formatSize(report.bytes)})`;
        let armed = false;
        btn.addEventListener("click", async () => {
          // Arm-then-confirm, as Move and Remove app already use: one button,
          // one irreversible consequence. The confirm wording names the vault
          // and names the home folder, because "delete" with no object is the
          // reading this program can least afford.
          if (!armed) {
            armed = true;
            btn.textContent = `Confirm \u2014 deletes ${report.files.length} ${noun} from the vault, nothing in ~`;
            return;
          }
          btn.disabled = true;
          try {
            const result = await RemoveVaultOrphans(report.files);
            showToast(
              result.failed.length > 0
                ? `${result.removedFiles.length} deleted, ${result.failed.length} left alone`
                : `${result.removedFiles.length} unaccounted-for ${result.removedFiles.length === 1 ? "file" : "files"} deleted`,
            );
          } catch (err) {
            showToast(extractErrorMessage(err));
          }
          // Always re-scan, success or failure: the backend re-derives the set
          // for itself and may legitimately have refused some of the list, so
          // what is on screen has to come back from disk rather than from what
          // the click assumed.
          render();
        });
        section.appendChild(btn);
      })
      .catch(() => {
        // A failed scan must not take the machine card down with it.
        section.replaceChildren();
      });
  };

  render();
  return section;
}

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
  content.appendChild(buildOrphanSection());

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
