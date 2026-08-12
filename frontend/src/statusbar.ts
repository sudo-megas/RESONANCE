import { main } from "../wailsjs/go/models";
import { formatDateTime } from "./dates";

// Every value here is already sitting in GetMirrorRows()' return shape —
// zero new Go calls. The vault path itself is shown in full in the topbar
// (#7); this bar is stats-only so the two never duplicate the same string.
// "Last activity" is the most recent timestamp on either side of any
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

interface Segment {
  icon: string;
  text: string;
  warn?: boolean;
}

function buildSegment(seg: Segment): HTMLDivElement {
  const el = document.createElement("div");
  el.className = "statusbar-segment";
  if (seg.warn) el.classList.add("statusbar-segment--warn");

  const icon = document.createElement("span");
  icon.className = "statusbar-icon";
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = seg.icon;

  const text = document.createElement("span");
  text.className = "statusbar-text";
  text.textContent = seg.text;

  el.append(icon, text);
  return el;
}

export async function refreshStatusbar(rows: main.AppRow[]): Promise<void> {
  const el = document.getElementById("statusbar");
  if (!el) return;

  const total = rows.length;
  const drifted = rows.filter((r) => r.drifted).length;
  const latest = mostRecentActivity(rows);

  el.replaceChildren(
    buildSegment({
      icon: "",
      text: total === 1 ? "1 app tracked" : `${total} apps tracked`,
    }),
    buildSegment({
      icon: "",
      warn: drifted > 0,
      text: drifted === 0 ? "all in sync" : drifted === 1 ? "1 drifted" : `${drifted} drifted`,
    }),
    buildSegment({
      icon: "",
      text: latest ? `last activity ${formatDateTime(latest)}` : "no activity yet",
    }),
  );
}
