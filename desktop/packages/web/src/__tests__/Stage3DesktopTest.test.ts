import { describe, expect, it } from 'vitest';
import {
  allowsDesktopNotification,
  isSubagentWorking,
  matchesClientSearch,
  nextNoteSummary,
  parseGitHubAlertBlockquote,
  parseLocalFileLinkTarget,
  parseNoteChecklist,
  plainInlineTitle,
  splitArtifactMarkdownBlocks,
  splitCarouselSlides,
  toggleNoteChecklistLine,
} from '../utils/sessionHelpers.js';

describe('Stage 3 Desktop Helpers & Parity Features', () => {
  it('strips inline markdown backticks and emphasis from session titles', () => {
    expect(plainInlineTitle('Setup `git init` in this dir and **push**')).toBe(
      'Setup git init in this dir and push',
    );
    expect(plainInlineTitle('### Fix [app.ts](file:///tmp/app.ts)')).toBe('Fix app.ts');
  });

  it('supports quoted exact-phrase search and Turkish dotted-İ folding', () => {
    expect(matchesClientSearch('istanbul', 'İstanbul Cloudflare Setup')).toBe(true);
    expect(matchesClientSearch('"static tunnel"', 'Configure static tunnel on jdlf.cc')).toBe(true);
    expect(matchesClientSearch('"tunnel static"', 'Configure static tunnel on jdlf.cc')).toBe(false);
  });

  it('extracts path, parent dir, and line numbers from file://#L and :line links', () => {
    expect(parseLocalFileLinkTarget('file:///home/siddharth/duck_physics.py#L123-L145')).toEqual({
      path: '/home/siddharth/duck_physics.py',
      dirPath: '/home/siddharth',
      line: 123,
    });
    expect(parseLocalFileLinkTarget('/home/siddharth/app/Main.kt:42')).toEqual({
      path: '/home/siddharth/app/Main.kt',
      dirPath: '/home/siddharth/app',
      line: 42,
    });
    expect(parseLocalFileLinkTarget('https://jdlf.cc')).toBeNull();
  });

  it('keeps 4-backtick carousel blocks with nested 3-backtick code intact and parses GitHub alerts', () => {
    const md = [
      '> [!IMPORTANT]',
      '> **GPU Budget**: instance `62509883` destroyed.',
      '',
      '````carousel',
      '![blockout](/home/siddharth/blockout_01.mp4)',
      '<!-- slide -->',
      '```python',
      'def run():',
      '    print("hello")',
      '',
      'run()',
      '```',
      '````',
    ].join('\n');

    const blocks = splitArtifactMarkdownBlocks(md);
    expect(blocks).toHaveLength(2);
    expect(parseGitHubAlertBlockquote(blocks[0])).toEqual({
      kind: 'IMPORTANT',
      body: '**GPU Budget**: instance `62509883` destroyed.',
    });

    const inner = blocks[1].split('\n').slice(1, -1).join('\n');
    const slides = splitCarouselSlides(inner);
    expect(slides).toHaveLength(2);
    expect(slides[1]).toContain('```python');
  });

  it('filters desktop notifications by category with minor progress updates off by default', () => {
    const base = { notificationsEnabled: true };
    expect(allowsDesktopNotification('permission', base)).toBe(true);
    expect(allowsDesktopNotification('done', base)).toBe(true);
    expect(allowsDesktopNotification('error', base)).toBe(true);
    expect(allowsDesktopNotification('progress', base)).toBe(false);
    expect(
      allowsDesktopNotification('progress', { ...base, notifyOnProgress: true }),
    ).toBe(true);
    expect(
      allowsDesktopNotification('done', { ...base, notifyOnDone: false }),
    ).toBe(false);
  });

  it('manages session notes checklists and subagent working states accurately', () => {
    const note = '- [ ] Wire Cloudflare auth\n- [ ] Add notes tab';
    expect(nextNoteSummary(note)).toBe('Wire Cloudflare auth');
    const toggled = toggleNoteChecklistLine(note, 0);
    expect(parseNoteChecklist(toggled)[0].checked).toBe(true);
    expect(nextNoteSummary(toggled)).toBe('Add notes tab');

    const now = 1_700_000_000_000;
    expect(isSubagentWorking('DONE', true, now - 1000, now)).toBe(false);
    expect(isSubagentWorking('RUNNING', true, now - 1000, now)).toBe(true);
    expect(isSubagentWorking('ALIVE', false, now - 500_000, now)).toBe(false);
  });
});
