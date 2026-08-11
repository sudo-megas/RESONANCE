// CORE.md §3 specifies date displays as literal "DD MM YYYY" — not ISO, not
// locale-dependent. Wire values are UTC RFC3339 (a vault must be restorable
// on any machine, so storage stays timezone-neutral); formatting converts to
// the viewer's own local time only here, at display time.

export function formatDate(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  const dd = String(d.getDate()).padStart(2, "0");
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const yyyy = d.getFullYear();
  return `${dd} ${mm} ${yyyy}`;
}

export function formatLastUpdated(iso: string): string {
  const formatted = formatDate(iso);
  return formatted === "—" ? formatted : `last updated ${formatted}`;
}
