export interface SessionNoteRecord {
  text: string;
  updatedAt: number;
}

export interface NoteChecklistItem {
  lineIndex: number;
  checked: boolean;
  text: string;
}

const INLINE_CODE_RE = /`+([^`]+)`+/g;
const INLINE_LINK_RE = /\[([^\]]+)\]\([^)]*\)/g;
const INLINE_EMPH_RE = /(?<![\w\\])(\*\*|__|~~|\*|_)(?=\S)(.+?)(?<=\S)\1(?!\w)/g;

/** Strips raw Markdown marks (`code`, **bold**, links, # headings) for single-line UI titles. */
export function plainInlineTitle(raw?: string | null): string {
  if (!raw) return '';
  const firstLine =
    raw
      .split(/\r?\n/)
      .map((l) => l.trim())
      .find((l) => l.length > 0) ?? '';
  if (!firstLine) return '';
  let t = firstLine.replace(/^#+\s*/, '');
  t = t.replace(INLINE_CODE_RE, '$1').replace(INLINE_LINK_RE, '$1');
  for (let i = 0; i < 2; i++) {
    t = t.replace(INLINE_EMPH_RE, '$2');
  }
  return t.replace(/\s+/g, ' ').trim();
}

/** Case-folds text with Turkish dotted/dotless İ/ı normalization so length & ASCII 'i' searches match cleanly. */
export function foldSearchText(s: string): string {
  let out = '';
  for (const ch of s) {
    if (ch === '\u0130' || ch === '\u0131' || ch === 'I' || ch === 'i') {
      out += 'i';
    } else {
      out += ch.toLowerCase();
    }
  }
  return out;
}

/** Splits a search query into unquoted words and `"exact phrase"` terms (AND semantics). */
export function parseSearchTerms(raw: string): string[] {
  const q = raw.trim();
  if (!q) return [];
  const out: string[] = [];
  let i = 0;
  while (i < q.length) {
    while (i < q.length && /\s/.test(q[i])) i++;
    if (i >= q.length) break;
    if (q[i] === '"') {
      const start = i + 1;
      const end = q.indexOf('"', start);
      if (end >= start) {
        const phrase = foldSearchText(q.slice(start, end).trim());
        if (phrase) out.push(phrase);
        i = end + 1;
        continue;
      }
      const rest = foldSearchText(q.slice(start).trim());
      if (rest) out.push(rest);
      break;
    }
    const start = i;
    while (i < q.length && !/\s/.test(q[i]) && q[i] !== '"') i++;
    const tok = foldSearchText(q.slice(start, i));
    if (tok) out.push(tok);
  }
  return out;
}

export function matchesClientSearch(
  query: string,
  ...fields: Array<string | undefined | null>
): boolean {
  const terms = parseSearchTerms(query);
  if (terms.length === 0) return true;
  const hay = foldSearchText(fields.filter(Boolean).join('\n'));
  return terms.every((t) => hay.includes(t));
}

export interface LocalFileLinkTarget {
  path: string;
  dirPath: string;
  line: number;
}

export function parseLocalFileLinkTarget(rawHref: string): LocalFileLinkTarget | null {
  const href = (rawHref || '').trim();
  if (!href.startsWith('file://') && !href.startsWith('/')) return null;

  const hashIdx = href.indexOf('#');
  const frag = hashIdx >= 0 ? href.slice(hashIdx + 1) : '';
  const withoutHash = hashIdx >= 0 ? href.slice(0, hashIdx) : href;

  let line = 0;
  const fragMatch = frag.match(/^L?(\d+)(?:-L?\d+)?$/i);
  if (fragMatch) {
    line = Number(fragMatch[1]) || 0;
  }

  let rawPath = withoutHash.startsWith('file://')
    ? decodeURIComponent(withoutHash.slice('file://'.length))
    : decodeURIComponent(withoutHash);

  if (line === 0) {
    const colonMatch = rawPath.match(/^(\/.+?):(\d+)(?::\d+)?$/);
    if (colonMatch) {
      rawPath = colonMatch[1];
      line = Number(colonMatch[2]) || 0;
    }
  }

  if (!rawPath.startsWith('/')) return null;
  const slashIdx = rawPath.lastIndexOf('/');
  const dirPath = slashIdx > 0 ? rawPath.slice(0, slashIdx) : '/';
  return { path: rawPath, dirPath, line: Math.max(0, line) };
}

export interface FenceOpen {
  char: '`' | '~';
  length: number;
  lang: string;
}

export function parseFenceOpen(trimmedStart: string): FenceOpen | null {
  if (trimmedStart.length < 3) return null;
  const ch = trimmedStart[0];
  if (ch !== '`' && ch !== '~') return null;
  let n = 0;
  while (n < trimmedStart.length && trimmedStart[n] === ch) n++;
  if (n < 3) return null;
  const rest = trimmedStart.slice(n).trim();
  if (ch === '`' && rest.includes('`')) return null;
  const lang = (rest.split(/\s+/)[0] || '').toLowerCase();
  return { char: ch, length: n, lang };
}

export function isFenceClose(trimmedStart: string, open: FenceOpen): boolean {
  if (trimmedStart.length < open.length) return false;
  let n = 0;
  while (n < trimmedStart.length && trimmedStart[n] === open.char) n++;
  if (n < open.length) return false;
  return /^[ \t]*$/.test(trimmedStart.slice(n));
}

/**
 * Splits markdown into top-level reviewable blocks without breaking fenced code blocks
 * (including 4-backtick ````carousel```` blocks containing inner 3-backtick fences), tables, or blockquotes.
 */
export function splitArtifactMarkdownBlocks(src: string): string[] {
  const lines = (src || '').replace(/\r\n/g, '\n').split('\n');
  const out: string[] = [];
  const paraBuf: string[] = [];
  const flushPara = () => {
    const joined = paraBuf.join('\n').trim();
    if (joined) out.push(joined);
    paraBuf.length = 0;
  };

  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const t = line.trimStart();
    if (!t) {
      flushPara();
      i++;
      continue;
    }
    const open = parseFenceOpen(t);
    if (open) {
      flushPara();
      let j = i + 1;
      while (j < lines.length && !isFenceClose(lines[j].trimStart(), open)) j++;
      out.push(lines.slice(i, Math.min(lines.length, j + 1)).join('\n'));
      i = j + 1;
      continue;
    }
    if (t.startsWith('>')) {
      flushPara();
      let j = i;
      while (j + 1 < lines.length && lines[j + 1].trimStart().startsWith('>')) j++;
      out.push(lines.slice(i, j + 1).join('\n'));
      i = j + 1;
      continue;
    }
    if (t.startsWith('|')) {
      flushPara();
      let j = i;
      while (j + 1 < lines.length && lines[j + 1].trimStart().startsWith('|')) j++;
      out.push(lines.slice(i, j + 1).join('\n'));
      i = j + 1;
      continue;
    }
    if (t.startsWith('#')) {
      flushPara();
      out.push(line);
      i++;
      continue;
    }
    paraBuf.push(line);
    i++;
  }
  flushPara();
  return out;
}

/** Splits ````carousel```` content on `<!-- slide -->` outside nested code fences. */
export function splitCarouselSlides(body: string): string[] {
  const lines = (body || '').replace(/\r\n/g, '\n').split('\n');
  const slides: string[] = [];
  const cur: string[] = [];
  let innerFence: FenceOpen | null = null;
  for (const line of lines) {
    const t = line.trimStart();
    if (innerFence) {
      cur.push(line);
      if (isFenceClose(t, innerFence)) innerFence = null;
      continue;
    }
    const open = parseFenceOpen(t);
    if (open) {
      innerFence = open;
      cur.push(line);
      continue;
    }
    if (/^\s*<!--\s*slide\s*-->\s*$/i.test(line)) {
      const s = cur.join('\n').trim();
      if (s) slides.push(s);
      cur.length = 0;
    } else {
      cur.push(line);
    }
  }
  const tail = cur.join('\n').trim();
  if (tail) slides.push(tail);
  return slides;
}

export function parseGitHubAlertBlockquote(
  rawQuote: string,
): { kind: 'NOTE' | 'TIP' | 'IMPORTANT' | 'WARNING' | 'CAUTION'; body: string } | null {
  const lines = (rawQuote || '').replace(/\r\n/g, '\n').split('\n');
  if (lines.length === 0) return null;
  const stripped: string[] = [];
  for (const l of lines) {
    const t = l.trimStart();
    if (!t.startsWith('>')) return null;
    const rest = t.slice(1);
    stripped.push(rest.startsWith(' ') ? rest.slice(1) : rest);
  }
  const m = (stripped[0] || '').trim().match(/^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*(.*)$/i);
  if (!m) return null;
  const kind = m[1].toUpperCase() as 'NOTE' | 'TIP' | 'IMPORTANT' | 'WARNING' | 'CAUTION';
  const bodyLines = [...(m[2]?.trim() ? [m[2].trim()] : []), ...stripped.slice(1)];
  return { kind, body: bodyLines.join('\n').trim() };
}

export function allowsDesktopNotification(
  kind: string | undefined,
  settings: {
    notificationsEnabled: boolean;
    notifyOnInput?: boolean;
    notifyOnDone?: boolean;
    notifyOnHalted?: boolean;
    notifyOnProgress?: boolean;
  },
): boolean {
  if (!settings.notificationsEnabled) return false;
  const k = (kind || '').trim().toLowerCase();
  if (k === 'test') return true;
  if (k === 'permission' || k === 'question' || k === 'plan' || k === 'term_blocked' || k === 'input') {
    return settings.notifyOnInput !== false;
  }
  if (k === 'error' || k === 'term_failed' || k === 'halted' || k === 'failed') {
    return settings.notifyOnHalted !== false;
  }
  if (k === 'progress') {
    return Boolean(settings.notifyOnProgress);
  }
  return settings.notifyOnDone !== false;
}

const CHECKLIST_RE = /^(\s*[-*+]\s+\[)([ xX])(\]\s+)(.*)$/;

export function parseNoteChecklist(text: string): NoteChecklistItem[] {
  if (!text.trim()) return [];
  const items: NoteChecklistItem[] = [];
  text
    .replace(/\r\n/g, '\n')
    .split('\n')
    .forEach((line, idx) => {
      const m = line.match(CHECKLIST_RE);
      if (m) {
        items.push({
          lineIndex: idx,
          checked: m[2].toLowerCase() === 'x',
          text: m[4].trim(),
        });
      }
    });
  return items;
}

export function toggleNoteChecklistLine(text: string, lineIndex: number): string {
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  if (lineIndex < 0 || lineIndex >= lines.length) return text;
  const m = lines[lineIndex].match(CHECKLIST_RE);
  if (!m) return text;
  const nextMark = m[2].toLowerCase() === 'x' ? ' ' : 'x';
  lines[lineIndex] = `${m[1]}${nextMark}${m[3]}${m[4]}`;
  return lines.join('\n');
}

export function nextNoteSummary(text?: string | null): string {
  if (!text || !text.trim()) return '';
  const list = parseNoteChecklist(text);
  const unchecked = list.find((i) => !i.checked && i.text);
  if (unchecked) return unchecked.text;
  return (
    text
      .split(/\r?\n/)
      .map((l) => l.replace(/^[#>*\-\s]+/, '').trim())
      .find((l) => l.length > 0) ?? ''
  );
}

export function isSubagentWorking(
  state?: string | null,
  sessionRunning = true,
  updatedMs = 0,
  nowMs = Date.now(),
): boolean {
  const st = (state || '').trim().toUpperCase();
  if (st === 'RUNNING' || st === 'WORKING') return true;
  if (st === 'DONE' || st === 'COMPLETED' || st === 'STOPPED' || st === 'KILLED' || st === 'FAILED') {
    return false;
  }
  if (st === 'ALIVE') {
    return sessionRunning && updatedMs > 0 && nowMs - updatedMs >= 0 && nowMs - updatedMs <= 90_000;
  }
  return false;
}
