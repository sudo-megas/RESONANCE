// Turns a rejected promise from a Go-bound call (a Go error, or occasionally
// a raw string) into user-facing text. Shared by every overlay that talks to
// the backend — previously duplicated verbatim in vault.ts and addapp.ts.
export function extractErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
