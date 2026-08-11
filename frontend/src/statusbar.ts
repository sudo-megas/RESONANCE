import { main } from "../wailsjs/go/models";
import { GetSettings } from "../wailsjs/go/main/App";
import { formatDateTime } from "./dates";

// Every value here is already sitting in GetMirrorRows()' return shape (plus
// the vault path from GetSettings, already fetched elsewhere) — zero new Go
// calls. "Last activity" is the most recent timestamp on either side of any
// tracked file: VaultModified only moves on a backup (AddApp/UpdateFromSource
// stamp it, RestoreApp never touches manifest.json), SourceModified moves on
// any live write, restores included (copyFile never calls os.Chtimes, so the
// moment a restore writes a file is genuinely its new mtime). Taking the max
// of both is the closest "backup or restore, whichever was more recent"
// reading obtainable from data that's already there.
function mostRecentActivity(rows: main.AppRow[]): string | null {
  let latest: string | null = null;
  for (const row of rows) {
    for (const f of row.files) {
      for (const ts of [f.sourceModified, f.vaultModified]) {
        if (ts && (!latest || ts > latest)) latest = ts;
      }
    }
  }
  return latest;
}

export async function refreshStatusbar(rows: main.AppRow[]): Promise<void> {
  const el = document.getElementById("statusbar");
  if (!el) return;

  const settings = await GetSettings();
  const total = rows.length;
  const drifted = rows.filter((r) => r.drifted).length;
  const latest = mostRecentActivity(rows);

  const parts: string[] = [
    total === 1 ? "1 app tracked" : `${total} apps tracked`,
    drifted === 0 ? "all in sync" : drifted === 1 ? "1 drifted" : `${drifted} drifted`,
    settings.vaultPath || "no vault set",
    latest ? `last activity ${formatDateTime(latest)}` : "no activity yet",
  ];

  el.textContent = parts.join("  •  ");
}
