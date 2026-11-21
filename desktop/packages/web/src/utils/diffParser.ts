export interface ParsedDiffLine {
  type: 'ctx' | 'add' | 'del' | 'hunk';
  oldNo: number | null;
  newNo: number | null;
  text: string;
}

export interface SplitRow {
  left: ParsedDiffLine | null;
  right: ParsedDiffLine | null;
}

export function isTestFile(path: string): boolean {
  const lower = path.toLowerCase();
  return (
    /(?:^|\/)(?:__tests__|tests?|spec)\//.test(lower) ||
    /(?:_test\.go|\.test\.[cm]?[jt]sx?|\.spec\.[cm]?[jt]sx?|test\.kt|test\.py)$/.test(lower)
  );
}

export function parseUnifiedPatch(patch: string): ParsedDiffLine[] {
  if (!patch) return [];
  const rawLines = patch.split('\n');
  const out: ParsedDiffLine[] = [];
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;

  for (const line of rawLines) {
    if (line.startsWith('@@')) {
      const m = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (m) {
        oldLine = parseInt(m[1], 10);
        newLine = parseInt(m[2], 10);
      }
      inHunk = true;
      out.push({ type: 'hunk', oldNo: null, newNo: null, text: line });
      continue;
    }
    if (!inHunk) {
      continue;
    }
    if (line.startsWith('\\ No newline')) continue;
    if (line.startsWith('+')) {
      out.push({ type: 'add', oldNo: null, newNo: newLine++, text: line.slice(1) });
    } else if (line.startsWith('-')) {
      out.push({ type: 'del', oldNo: oldLine++, newNo: null, text: line.slice(1) });
    } else if (line.startsWith(' ')) {
      out.push({ type: 'ctx', oldNo: oldLine++, newNo: newLine++, text: line.slice(1) });
    } else if (line.length > 0) {
      out.push({ type: 'ctx', oldNo: oldLine++, newNo: newLine++, text: line });
    }
  }
  return out;
}

export function toSplitRows(lines: ParsedDiffLine[]): SplitRow[] {
  const rows: SplitRow[] = [];
  let i = 0;
  while (i < lines.length) {
    const ln = lines[i];
    if (ln.type === 'hunk' || ln.type === 'ctx') {
      rows.push({ left: ln, right: ln });
      i++;
      continue;
    }
    const dels: ParsedDiffLine[] = [];
    const adds: ParsedDiffLine[] = [];
    while (i < lines.length && lines[i].type === 'del') {
      dels.push(lines[i++]);
    }
    while (i < lines.length && lines[i].type === 'add') {
      adds.push(lines[i++]);
    }
    const maxLen = Math.max(dels.length, adds.length);
    for (let k = 0; k < maxLen; k++) {
      rows.push({ left: dels[k] ?? null, right: adds[k] ?? null });
    }
  }
  return rows;
}
