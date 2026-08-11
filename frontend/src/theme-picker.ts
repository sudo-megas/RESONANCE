import { GetSettings, SaveSettings } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";

export interface ThemeDef {
  id: string;
  name: string;
}

export const THEMES: ThemeDef[] = [
  { id: "default-dark", name: "Default Dark" },
  { id: "ubuntu-aubergine", name: "Ubuntu Aubergine Canonical" },
  { id: "default-light", name: "Default Light" },
  { id: "noctalia", name: "Noctalia" },
  { id: "catppuccin-latte", name: "Catppuccin Latte" },
  { id: "catppuccin-frappe", name: "Catppuccin Frappé" },
  { id: "catppuccin-macchiato", name: "Catppuccin Macchiato" },
  { id: "catppuccin-mocha", name: "Catppuccin Mocha" },
  { id: "rose-pine-dawn", name: "Rosé Pine Dawn" },
  { id: "nord", name: "Nord" },
  { id: "kanagawa-lotus", name: "Kanagawa Lotus" },
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
    const current = await GetSettings();
    await SaveSettings({ ...current, theme: id });
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
  closeBtn.textContent = "\uf00d";
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
