// Turns a rejected promise from a Go-bound call (a Go error, or occasionally
// a raw string) into user-facing text. Shared by every overlay that talks to
// the backend — previously duplicated verbatim in vault.ts and addapp.ts.
export function extractErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

// Formats a byte count for the diff view's "too large to diff" fallback —
// KB is the only unit dotfile-scale files ever need; MB would be reachable
// only past the 1 MiB cap itself, so this never has to round to it.
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${Math.round(bytes / 1024)} KB`;
}
