import { GetLicenseText } from "../wailsjs/go/main/App";
import { openOverlay, closeOverlay } from "./overlay";
import { APP_VERSION, RELEASE_DATE } from "./version";

const ABOUT_FIELDS: [string, string][] = [
  ["Version", APP_VERSION],
  ["Date", RELEASE_DATE],
  ["Maker", "sudo-megas"],
  ["Licence", "GPL-3.0-or-later"],
  ["Source", "github.com/sudo-megas/RESONANCE"],
];

export function openAbout(): void {
  const content = document.createElement("div");
  content.className = "about-panel";

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "overlay-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "\uf00d";
  closeBtn.addEventListener("click", () => closeOverlay());
  content.appendChild(closeBtn);

  const mark = document.createElement("span");
  mark.className = "logo-mark logo-mark--about";
  mark.setAttribute("aria-hidden", "true");
  content.appendChild(mark);

  const name = document.createElement("h2");
  name.className = "about-name";
  name.textContent = "RESONANCE";
  content.appendChild(name);

  const tagline = document.createElement("p");
  tagline.className = "about-tagline";
  tagline.textContent = "A tiny, fully local dotfiles backup & restore tool.";
  content.appendChild(tagline);

  const grid = document.createElement("dl");
  grid.className = "about-grid";
  for (const [label, value] of ABOUT_FIELDS) {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = value;
    if (label === "Source") dd.classList.add("about-source");
    grid.appendChild(dt);
    grid.appendChild(dd);
  }
  content.appendChild(grid);

  const privacyNote = document.createElement("p");
  privacyNote.className = "about-privacy-note";
  privacyNote.textContent =
    "This app makes no network requests. No telemetry, no analytics, no crash reporting, no update checks.";
  content.appendChild(privacyNote);

  content.appendChild(document.createElement("hr")).className = "about-separator";

  const licenseHeading = document.createElement("h3");
  licenseHeading.className = "about-license-heading";
  licenseHeading.textContent = "Licence, in full";
  content.appendChild(licenseHeading);

  const licenseBox = document.createElement("pre");
  licenseBox.className = "about-license-text";
  licenseBox.textContent = "Loading…";
  content.appendChild(licenseBox);

  const footer = document.createElement("p");
  footer.className = "about-footer";
  footer.textContent = "Copyright © sudo-megas · Built with Reason and Passion.";
  content.appendChild(footer);

  openOverlay(content);

  GetLicenseText()
    .then((text) => {
      licenseBox.textContent = text;
    })
    .catch((err) => {
      licenseBox.textContent = "Could not load licence text.";
      console.error(err);
    });
}
