import { describe, expect, it } from 'vitest';
import { DEFAULT_KEYBINDINGS, displayChord, formatEventChord, matchChord } from './keybindings.js';

describe('keybindings', () => {
  it('formats and matches keyboard chords accurately', () => {
    const fakeEvent = {
      ctrlKey: true,
      metaKey: false,
      altKey: false,
      shiftKey: true,
      key: 'M',
    } as KeyboardEvent;

    expect(formatEventChord(fakeEvent)).toBe('mod+shift+m');
    expect(matchChord(fakeEvent, 'mod+shift+m')).toBe(true);
    expect(matchChord(fakeEvent, 'mod+m')).toBe(false);
    expect(displayChord('mod+shift+m')).toContain('Shift+M');
  });

  it('has unique default keybinding ids and chords', () => {
    const ids = new Set(DEFAULT_KEYBINDINGS.map((k) => k.id));
    const chords = new Set(DEFAULT_KEYBINDINGS.map((k) => k.defaultChord));
    expect(ids.size).toBe(DEFAULT_KEYBINDINGS.length);
    expect(chords.size).toBe(DEFAULT_KEYBINDINGS.length);
  });
});
