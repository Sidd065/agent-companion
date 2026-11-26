import { describe, expect, it } from 'vitest';
import type { ToolCall } from '@agentc/protocol';
import {
  extractToolInlineDiffs,
  extractToolOutputText,
  isTestFile,
  parseUnifiedPatch,
  toSplitRows,
} from './diffParser.js';

describe('diffParser', () => {
  it('parses unified patches with accurate line numbers', () => {
    const patch = [
      'diff --git a/src/app.ts b/src/app.ts',
      '--- a/src/app.ts',
      '+++ b/src/app.ts',
      '@@ -10,4 +10,5 @@ function demo() {',
      ' const a = 1;',
      '-const b = 2;',
      '+const b = 20;',
      '+const c = 30;',
      ' return a + b;',
    ].join('\n');

    const lines = parseUnifiedPatch(patch);
    expect(lines).toHaveLength(6);
    expect(lines[0]).toMatchObject({ type: 'hunk' });
    expect(lines[1]).toEqual({ type: 'ctx', oldNo: 10, newNo: 10, text: 'const a = 1;' });
    expect(lines[2]).toEqual({ type: 'del', oldNo: 11, newNo: null, text: 'const b = 2;' });
    expect(lines[3]).toEqual({ type: 'add', oldNo: null, newNo: 11, text: 'const b = 20;' });
    expect(lines[4]).toEqual({ type: 'add', oldNo: null, newNo: 12, text: 'const c = 30;' });
    expect(lines[5]).toEqual({ type: 'ctx', oldNo: 12, newNo: 13, text: 'return a + b;' });

    const split = toSplitRows(lines);
    // hunk, ctx, paired del+add, extra add, ctx
    expect(split).toHaveLength(5);
    expect(split[2].left?.text).toBe('const b = 2;');
    expect(split[2].right?.text).toBe('const b = 20;');
    expect(split[3].left).toBeNull();
    expect(split[3].right?.text).toBe('const c = 30;');
  });

  it('detects test files across Go, TS, JS, Python, Kotlin', () => {
    expect(isTestFile('daemon/internal/gitops/gitops_test.go')).toBe(true);
    expect(isTestFile('src/components/App.test.tsx')).toBe(true);
    expect(isTestFile('e2e/desktop.spec.ts')).toBe(true);
    expect(isTestFile('src/__tests__/helper.ts')).toBe(true);
    expect(isTestFile('src/components/App.tsx')).toBe(false);
    expect(isTestFile('daemon/internal/gitops/gitops.go')).toBe(false);
  });

  it('extracts multi-file tool.content diff blocks and suppresses raw diff JSON in output', () => {
    const codexMultiEdit: ToolCall = {
      callId: 'fc-1',
      name: 'Edit',
      kind: 'edit',
      status: 'completed',
      input: { path: 'src/a.ts', oldText: 'old-a', newText: 'new-a' },
      content: [
        { type: 'diff', path: 'src/a.ts', oldText: 'old-a', newText: 'new-a', line: 3 },
        { type: 'diff', path: 'src/b.ts', oldText: '', newText: 'brand-new-b', line: 1 },
      ],
    };

    const diffs = extractToolInlineDiffs(codexMultiEdit);
    expect(diffs).toEqual([
      { path: 'src/a.ts', oldStr: 'old-a', newStr: 'new-a', line: 3 },
      { path: 'src/b.ts', oldStr: '', newStr: 'brand-new-b', line: 1 },
    ]);
    // Must NOT dump raw [{"type":"diff",...}] JSON into Output
    expect(extractToolOutputText(codexMultiEdit)).toBe('');
  });

  it('falls back to tool.input for single-file Edit and preserves non-diff text output', () => {
    const singleEdit: ToolCall = {
      callId: 'ed-1',
      name: 'Edit',
      kind: 'edit',
      status: 'completed',
      input: { file_path: 'README.md', old_string: 'v1', new_string: 'v2' },
      content: [
        { type: 'diff', path: 'README.md', oldText: 'v1', newText: 'v2' },
        { type: 'text', text: 'Applied patch cleanly' },
      ],
    };
    expect(extractToolInlineDiffs({ ...singleEdit, content: undefined })).toEqual([
      { path: 'README.md', oldStr: 'v1', newStr: 'v2' },
    ]);
    expect(extractToolOutputText(singleEdit)).toBe('Applied patch cleanly');
    expect(
      extractToolOutputText({
        callId: 'sh-1',
        name: 'Bash',
        kind: 'execute',
        status: 'completed',
        output: 'BUILD PASS\n',
      }),
    ).toBe('BUILD PASS\n');
  });
});
