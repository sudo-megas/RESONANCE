// A small transient bottom-corner notice — deliberately not the overlay
// grammar. It doesn't dim the mirror or need a secondary view, so reusing
// the overlay component here would misuse it.

let hideTimer: number | undefined;

export function showToast(message: string, durationMs = 2400): void {
  const root = document.getElementById("toast-root")!;
  root.innerHTML = "";

  const el = document.createElement("div");
  el.className = "toast";
  el.textContent = message;
  root.appendChild(el);
  requestAnimationFrame(() => el.classList.add("toast-visible"));

  clearTimeout(hideTimer);
  hideTimer = window.setTimeout(() => {
    el.classList.remove("toast-visible");
    setTimeout(() => el.remove(), 200);
  }, durationMs);
}
