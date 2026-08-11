import { main } from "../wailsjs/go/models";

// Renders app rows into both pane bodies. STEP2 keeps rows simple — name
// and file count only. A file just backed up is by definition identical on
// both sides, so both panes render the same content for now; drift-aware
// divergence between SYSTEM and VAULT is STEP3's job.

function buildRow(app: main.ManifestApp): HTMLDivElement {
  const row = document.createElement("div");
  row.className = "app-row";
  row.dataset.appName = app.name;

  const name = document.createElement("span");
  name.className = "app-row-name";
  name.textContent = app.name;

  const count = document.createElement("span");
  count.className = "app-row-count";
  const n = app.files?.length ?? 0;
  count.textContent = n === 1 ? "1 file" : `${n} files`;

  row.appendChild(name);
  row.appendChild(count);
  return row;
}

export function renderRows(apps: main.ManifestApp[]): void {
  const systemBody = document.getElementById("system-body")!;
  const vaultBody = document.getElementById("vault-body")!;
  systemBody.innerHTML = "";
  vaultBody.innerHTML = "";

  if (apps.length === 0) {
    const systemHint = document.createElement("p");
    systemHint.className = "pane-hint";
    systemHint.textContent = "No apps yet.";
    const vaultHint = document.createElement("p");
    vaultHint.className = "pane-hint";
    vaultHint.textContent = "No apps yet.";
    systemBody.appendChild(systemHint);
    vaultBody.appendChild(vaultHint);
    return;
  }

  for (const app of apps) {
    systemBody.appendChild(buildRow(app));
    vaultBody.appendChild(buildRow(app));
  }
}
