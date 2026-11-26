import type { ToolCall } from '@agentc/protocol';

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

export interface InlineToolDiff {
  oldStr: string;
  newStr: string;
  path?: string;
  line?: number;
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

function stringifyToolValue(val: unknown): string {
  if (val == null) return '';
  if (typeof val === 'string') return val;
  try {
    return JSON.stringify(val, null, 2);
  } catch {
    return String(val);
  }
}

export function extractToolInlineDiffs(tool?: ToolCall | null): InlineToolDiff[] {
  if (!tool) return [];

  if (Array.isArray(tool.content)) {
    const contentDiffs: InlineToolDiff[] = [];
    for (const raw of tool.content) {
      if (!raw || typeof raw !== 'object') continue;
      const blk = raw as Record<string, unknown>;
      if (blk.type !== 'diff') continue;
      const oldStr =
        typeof blk.oldText === 'string'
          ? blk.oldText
          : typeof blk.old_string === 'string'
            ? blk.old_string
            : typeof blk.old === 'string'
              ? blk.old
              : '';
      const newStr =
        typeof blk.newText === 'string'
          ? blk.newText
          : typeof blk.new_string === 'string'
            ? blk.new_string
            : typeof blk.new === 'string'
              ? blk.new
              : '';
      const path =
        typeof blk.path === 'string'
          ? blk.path
          : typeof blk.file_path === 'string'
            ? blk.file_path
            : undefined;
      const line = typeof blk.line === 'number' ? blk.line : undefined;
      contentDiffs.push({ oldStr, newStr, path, line });
    }
    if (contentDiffs.length > 0) {
      return contentDiffs;
    }
  }

  const inp = tool.input as Record<string, unknown> | undefined;
  if (!inp || typeof inp !== 'object') return [];
  const oldStr =
    typeof inp.old_string === 'string'
      ? inp.old_string
      : typeof inp.oldText === 'string'
        ? inp.oldText
        : typeof inp.old === 'string'
          ? inp.old
          : null;
  const newStr =
    typeof inp.new_string === 'string'
      ? inp.new_string
      : typeof inp.newText === 'string'
        ? inp.newText
        : typeof inp.new === 'string'
          ? inp.new
          : typeof inp.content === 'string'
            ? inp.content
            : null;
  if (oldStr === null && newStr === null) return [];
  const path =
    typeof inp.file_path === 'string'
      ? inp.file_path
      : typeof inp.path === 'string'
        ? inp.path
        : undefined;
  return [{ oldStr: oldStr ?? '', newStr: newStr ?? '', path }];
}

export function extractToolOutputText(tool?: ToolCall | null): string {
  if (!tool) return '';
  if (tool.output != null) {
    return stringifyToolValue(tool.output);
  }
  if (tool.content == null) {
    return '';
  }
  if (Array.isArray(tool.content)) {
    const parts: string[] = [];
    for (const raw of tool.content) {
      if (raw == null) continue;
      if (typeof raw === 'string') {
        if (raw) parts.push(raw);
        continue;
      }
      if (typeof raw === 'object') {
        const blk = raw as Record<string, unknown>;
        if (blk.type === 'diff') continue;
        if (typeof blk.text === 'string') {
          if (blk.text) parts.push(blk.text);
          continue;
        }
        if (blk.content && typeof blk.content === 'object') {
          const inner = blk.content as Record<string, unknown>;
          if (typeof inner.text === 'string' && inner.text) {
            parts.push(inner.text);
            continue;
          }
        }
      }
      const s = stringifyToolValue(raw);
      if (s) parts.push(s);
    }
    return parts.join('\n');
  }
  return stringifyToolValue(tool.content);
}
