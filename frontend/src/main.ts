import "./styles/theme.css";
import "./styles/layout.css";
import "./styles/overlay.css";

import { GetSettings } from "../wailsjs/go/main/App";
import { loadPersistedTheme, openThemePicker } from "./theme-picker";
import { openAbout } from "./about";
import { openVaultPrompt, openChangePath, refreshVaultPathDisplay } from "./vault";
import { openAddApp } from "./addapp";
import { refreshMirror, getDriftedApps } from "./rows";
import { openUpdateAllConfirm } from "./update";

async function init(): Promise<void> {
  await loadPersistedTheme();

  document.getElementById("theme-btn")!.addEventListener("click", () => openThemePicker());
  document.getElementById("about-btn")!.addEventListener("click", () => openAbout());
  document.getElementById("vault-path-btn")!.addEventListener("click", () => openChangePath());
  document.getElementById("add-app-btn")!.addEventListener("click", () => openAddApp());
  document.getElementById("update-all-btn")!.addEventListener("click", () => openUpdateAllConfirm(getDriftedApps()));

  const settings = await GetSettings();
  refreshVaultPathDisplay(settings.vaultPath);

  if (!settings.vaultPath) {
    await openVaultPrompt();
  }

  await refreshMirror();
}

init();
