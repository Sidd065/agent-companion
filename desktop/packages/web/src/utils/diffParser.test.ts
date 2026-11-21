import { describe, expect, it } from 'vitest';
import { isTestFile, parseUnifiedPatch, toSplitRows } from './diffParser.js';

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
});
