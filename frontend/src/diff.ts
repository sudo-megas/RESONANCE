// A hand-rolled line diff — no dependency, matching this project's
// zero-dependency-where-avoidable posture. Common prefix/suffix lines are
// trimmed first: for a typical single-line dotfile edit this collapses the
// expensive part of the comparison down to almost nothing, leaving only
// the genuinely different middle section to run through the LCS table.

export interface DiffLine {
  type: "equal" | "add" | "remove";
  text: string;
}

function splitLines(text: string): string[] {
  if (text === "") return [];
  return text.split("\n");
}

export function diffLines(liveText: string, vaultText: string): DiffLine[] {
  const a = splitLines(liveText);
  const b = splitLines(vaultText);

  let start = 0;
  const maxStart = Math.min(a.length, b.length);
  while (start < maxStart && a[start] === b[start]) start++;

  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }

  const result: DiffLine[] = [];
  for (let i = 0; i < start; i++) result.push({ type: "equal", text: a[i] });
  result.push(...lcsDiff(a.slice(start, endA), b.slice(start, endB)));
  for (let i = endA; i < a.length; i++) result.push({ type: "equal", text: a[i] });
  return result;
}

// Classic LCS dynamic-programming diff over the (already prefix/suffix-
// trimmed) middle section only — the shortest edit script between the two
// remaining line arrays.
function lcsDiff(a: string[], b: string[]): DiffLine[] {
  const n = a.length;
  const m = b.length;
  if (n === 0) return b.map((text) => ({ type: "add" as const, text }));
  if (m === 0) return a.map((text) => ({ type: "remove" as const, text }));

  // dp[i][j] = length of the LCS of a[i:] and b[j:]
  const dp: Uint32Array[] = new Array(n + 1);
  for (let i = 0; i <= n; i++) dp[i] = new Uint32Array(m + 1);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }

  const result: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      result.push({ type: "equal", text: a[i] });
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      result.push({ type: "remove", text: a[i] });
      i++;
    } else {
      result.push({ type: "add", text: b[j] });
      j++;
    }
  }
  while (i < n) {
    result.push({ type: "remove", text: a[i] });
    i++;
  }
  while (j < m) {
    result.push({ type: "add", text: b[j] });
    j++;
  }
  return result;
}

// renderDiff builds the visual diff — live (home) vs. vault content, both
// already loaded via GetDiffPair. + marks a line only the vault has, -
// marks a line only the live file has (i.e. what "Restore" would remove).
export function renderDiff(liveText: string, vaultText: string): HTMLElement {
  const container = document.createElement("div");
  container.className = "diff-view";

  for (const line of diffLines(liveText, vaultText)) {
    const row = document.createElement("div");
    row.className = `diff-line diff-line--${line.type}`;

    const marker = document.createElement("span");
    marker.className = "diff-line-marker";
    marker.textContent = line.type === "add" ? "+" : line.type === "remove" ? "-" : " ";

    const text = document.createElement("span");
    text.className = "diff-line-text";
    text.textContent = line.text;

    row.appendChild(marker);
    row.appendChild(text);
    container.appendChild(row);
  }

  return container;
}
