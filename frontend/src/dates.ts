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

// DD MM YYYY plus HH:MM — the status bar needs a time-of-day component that
// formatDate's day granularity can't give: a "last activity" moment is often
// same-day, where a bare date cannot distinguish 5 minutes ago from 20 hours
// ago. Still the same literal, non-locale-dependent convention as formatDate,
// just with minutes.
export function formatDateTime(iso: string): string {
  const datePart = formatDate(iso);
  if (datePart === "—") return datePart;
  const d = new Date(iso);
  const hh = String(d.getHours()).padStart(2, "0");
  const min = String(d.getMinutes()).padStart(2, "0");
  return `${datePart} ${hh}:${min}`;
}

// The differences view compares two moments that are routinely seconds apart:
// a file edited and then backed up, or edited again just after. HH:MM has
// exactly the failure mode there that DD MM YYYY has at day granularity, only
// rarer — two timestamps printing the same string while describing different
// moments — so that one view asks for the extra digits. Everywhere else keeps
// minutes, where seconds would be noise.
export function formatDateTimeExact(iso: string): string {
  const base = formatDateTime(iso);
  if (base === "—") return base;
  const d = new Date(iso);
  return `${base}:${String(d.getSeconds()).padStart(2, "0")}`;
}
