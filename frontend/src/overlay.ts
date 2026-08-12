// The one reusable overlay component every secondary view is built on:
// theme picker and About today, diff/preview/machine-info/add-app in later
// STEPs. It owns zero domain knowledge of what's inside it — callers hand it
// a content element and get a dismiss handle back.

interface ActiveOverlay {
  scrim: HTMLDivElement;
  onClose?: () => void;
  dismissable: boolean;
}

let active: ActiveOverlay | null = null;

export interface OverlayOptions {
  onClose?: () => void;
  /** Default true. Set false to disable Escape and backdrop-click — the
   * overlay can then only be closed by calling closeOverlay() explicitly
   * from within its own content (e.g. once a required choice is made). */
  dismissable?: boolean;
}

export interface OverlayHandle {
  close: () => void;
}

function handleKeydown(e: KeyboardEvent): void {
  if (e.key === "Escape" && active?.dismissable) closeOverlay();
}

export function openOverlay(content: HTMLElement, opts?: OverlayOptions): OverlayHandle {
  // A non-dismissable overlay (e.g. the mandatory first-launch vault
  // prompt) can only be closed by its own content calling closeOverlay()
  // explicitly — it must not be silently torn down just because something
  // else tried to open over it (e.g. a stray Tab+Enter reaching another
  // button before a choice is made).
  if (active && !active.dismissable) {
    return { close: () => {} };
  }

  closeOverlay();

  const root = document.getElementById("overlay-root")!;
  const scrim = document.createElement("div");
  scrim.className = "overlay-scrim";

  const panel = document.createElement("div");
  panel.className = "overlay-panel";
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-modal", "true");
  panel.appendChild(content);

  scrim.appendChild(panel);
  scrim.addEventListener("click", (e) => {
    if (e.target === scrim && active?.dismissable) closeOverlay();
  });
  root.appendChild(scrim);
  document.addEventListener("keydown", handleKeydown);

  // Force a reflow so the transition actually animates from the initial state.
  requestAnimationFrame(() => scrim.classList.add("overlay-open"));

  active = { scrim, onClose: opts?.onClose, dismissable: opts?.dismissable ?? true };

  return { close: closeOverlay };
}

export function closeOverlay(): void {
  if (!active) return;
  const { scrim, onClose } = active;
  active = null;
  document.removeEventListener("keydown", handleKeydown);
  scrim.classList.remove("overlay-open");
  onClose?.();
  setTimeout(() => scrim.remove(), 220);
}
