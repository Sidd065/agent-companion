import { describe, expect, it } from 'vitest';
import { BUILTIN_THEMES, sanitizeCustomCss } from './themes.js';

describe('themes', () => {
  it('provides 6 built-in light and dark themes', () => {
    expect(BUILTIN_THEMES.length).toBeGreaterThanOrEqual(6);
    expect(BUILTIN_THEMES.some((t) => t.isDark)).toBe(true);
    expect(BUILTIN_THEMES.some((t) => !t.isDark)).toBe(true);
  });

  it('sanitizes dangerous custom CSS directives', () => {
    const dirty = '@import url("https://evil.example/x.css"); :root { --accent: #ff0000; background: url(http://evil); }';
    const clean = sanitizeCustomCss(dirty);
    expect(clean).not.toContain('@import');
    expect(clean).not.toContain('evil.example');
    expect(clean).toContain('--accent: #ff0000');
  });
});
