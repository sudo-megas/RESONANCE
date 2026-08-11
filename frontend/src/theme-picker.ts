import { GetSettings, SaveSettings } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";

export interface ThemeDef {
  id: string;
  name: string;
}

export const THEMES: ThemeDef[] = [
  { id: "default-dark", name: "Default Dark" },
  { id: "ubuntu-aubergine", name: "Ubuntu Aubergine Canonical" },
];

const DEFAULT_THEME_ID = "default-dark";

export function applyTheme(id: string): void {
  document.documentElement.setAttribute("data-theme", id);
}

export async function loadPersistedTheme(): Promise<void> {
  try {
    const settings = await GetSettings();
    applyTheme(settings.theme || DEFAULT_THEME_ID);
  } catch (err) {
    console.error(err);
    applyTheme(DEFAULT_THEME_ID);
  }
}

async function selectTheme(id: string): Promise<void> {
  applyTheme(id);
  closeOverlay();
  try {
    await SaveSettings({ theme: id });
  } catch (err) {
    console.error(err);
  }
}

export function openThemePicker(): void {
  const content = document.createElement("div");

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "✕";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const heading = document.createElement("h2");
  heading.className = "overlay-heading";
  heading.textContent = "Theme";
  content.appendChild(heading);

  const grid = document.createElement("div");
  grid.className = "theme-grid";
  const current = document.documentElement.getAttribute("data-theme");

  for (const theme of THEMES) {
    const card = document.createElement("button");
    card.type = "button";
    card.className = "theme-card";
    if (theme.id === current) card.classList.add("theme-card--active");

    const swatch = document.createElement("span");
    swatch.className = "theme-card-swatch";
    swatch.setAttribute("data-theme", theme.id);

    const label = document.createElement("span");
    label.className = "theme-card-label";
    label.textContent = theme.name;

    card.appendChild(swatch);
    card.appendChild(label);
    card.addEventListener("click", () => selectTheme(theme.id));
    grid.appendChild(card);
  }
  content.appendChild(grid);

  openOverlay(content);
}
