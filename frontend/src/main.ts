import "./styles/theme.css";
import "./styles/layout.css";
import "./styles/overlay.css";

import { loadPersistedTheme, openThemePicker } from "./theme-picker";
import { openAbout } from "./about";
import { showToast } from "./toast";

async function init(): Promise<void> {
  await loadPersistedTheme();

  document.getElementById("theme-btn")!.addEventListener("click", () => openThemePicker());
  document.getElementById("about-btn")!.addEventListener("click", () => openAbout());
  document.getElementById("add-app-btn")!.addEventListener("click", () => showToast("Coming in v0.2.0"));
}

init();
