export interface ThemePalette {
  id: string;
  name: string;
  isDark: boolean;
  vars: {
    '--bg-base': string;
    '--bg-sidebar': string;
    '--bg-surface': string;
    '--bg-elevated': string;
    '--bg-hover': string;
    '--border': string;
    '--border-strong': string;
    '--text-primary': string;
    '--text-secondary': string;
    '--text-muted': string;
    '--accent': string;
    '--accent-soft': string;
    '--accent-fg': string;
    '--success': string;
    '--warning': string;
    '--danger': string;
    '--diff-add-bg': string;
    '--diff-add-fg': string;
    '--diff-del-bg': string;
    '--diff-del-fg': string;
  };
}

export const BUILTIN_THEMES: ThemePalette[] = [
  {
    id: 'obsidian',
    name: 'Obsidian Dark',
    isDark: true,
    vars: {
      '--bg-base': '#0c0d10',
      '--bg-sidebar': '#111217',
      '--bg-surface': '#16181f',
      '--bg-elevated': '#1d2029',
      '--bg-hover': '#252936',
      '--border': '#262936',
      '--border-strong': '#363b4e',
      '--text-primary': '#eceef4',
      '--text-secondary': '#a6adbf',
      '--text-muted': '#6f778d',
      '--accent': '#6ea8ff',
      '--accent-soft': 'rgba(110, 168, 255, 0.14)',
      '--accent-fg': '#09111f',
      '--success': '#3dd68c',
      '--warning': '#f5b841',
      '--danger': '#f46767',
      '--diff-add-bg': 'rgba(46, 160, 67, 0.16)',
      '--diff-add-fg': '#6ee7a8',
      '--diff-del-bg': 'rgba(248, 81, 73, 0.16)',
      '--diff-del-fg': '#fca5a5',
    },
  },
  {
    id: 'midnight',
    name: 'Midnight Slate',
    isDark: true,
    vars: {
      '--bg-base': '#0b101b',
      '--bg-sidebar': '#0f1624',
      '--bg-surface': '#141d2f',
      '--bg-elevated': '#1b263d',
      '--bg-hover': '#23314d',
      '--border': '#22304b',
      '--border-strong': '#314468',
      '--text-primary': '#e8eefc',
      '--text-secondary': '#9bb0d4',
      '--text-muted': '#647aa3',
      '--accent': '#7c9cff',
      '--accent-soft': 'rgba(124, 156, 255, 0.16)',
      '--accent-fg': '#091124',
      '--success': '#34d399',
      '--warning': '#fbbf24',
      '--danger': '#fb7185',
      '--diff-add-bg': 'rgba(52, 211, 153, 0.16)',
      '--diff-add-fg': '#6ee7b7',
      '--diff-del-bg': 'rgba(251, 113, 133, 0.16)',
      '--diff-del-fg': '#fda4af',
    },
  },
  {
    id: 'nord',
    name: 'Nord Frost',
    isDark: true,
    vars: {
      '--bg-base': '#242933',
      '--bg-sidebar': '#292f3b',
      '--bg-surface': '#2e3440',
      '--bg-elevated': '#3b4252',
      '--bg-hover': '#434c5e',
      '--border': '#3f4758',
      '--border-strong': '#4c566a',
      '--text-primary': '#eceff4',
      '--text-secondary': '#d8dee9',
      '--text-muted': '#909bb0',
      '--accent': '#88c0d0',
      '--accent-soft': 'rgba(136, 192, 208, 0.16)',
      '--accent-fg': '#1a2029',
      '--success': '#a3be8c',
      '--warning': '#ebcb8b',
      '--danger': '#bf616a',
      '--diff-add-bg': 'rgba(163, 190, 140, 0.18)',
      '--diff-add-fg': '#b8d3a0',
      '--diff-del-bg': 'rgba(191, 97, 106, 0.18)',
      '--diff-del-fg': '#e08e96',
    },
  },
  {
    id: 'graphite',
    name: 'Graphite Mono',
    isDark: true,
    vars: {
      '--bg-base': '#0a0a0a',
      '--bg-sidebar': '#101010',
      '--bg-surface': '#151515',
      '--bg-elevated': '#1d1d1d',
      '--bg-hover': '#262626',
      '--border': '#252525',
      '--border-strong': '#3a3a3a',
      '--text-primary': '#f4f4f5',
      '--text-secondary': '#a1a1aa',
      '--text-muted': '#71717a',
      '--accent': '#e4e4e7',
      '--accent-soft': 'rgba(228, 228, 231, 0.12)',
      '--accent-fg': '#09090b',
      '--success': '#4ade80',
      '--warning': '#facc15',
      '--danger': '#f87171',
      '--diff-add-bg': 'rgba(74, 222, 128, 0.15)',
      '--diff-add-fg': '#86efac',
      '--diff-del-bg': 'rgba(248, 113, 113, 0.15)',
      '--diff-del-fg': '#fca5a5',
    },
  },
  {
    id: 'daylight',
    name: 'Daylight Light',
    isDark: false,
    vars: {
      '--bg-base': '#ffffff',
      '--bg-sidebar': '#f6f7f9',
      '--bg-surface': '#f9fafb',
      '--bg-elevated': '#ffffff',
      '--bg-hover': '#eceff4',
      '--border': '#e2e6ee',
      '--border-strong': '#cbd2df',
      '--text-primary': '#111827',
      '--text-secondary': '#4b5563',
      '--text-muted': '#6b7280',
      '--accent': '#2563eb',
      '--accent-soft': 'rgba(37, 99, 235, 0.10)',
      '--accent-fg': '#ffffff',
      '--success': '#15803d',
      '--warning': '#b45309',
      '--danger': '#dc2626',
      '--diff-add-bg': 'rgba(22, 163, 74, 0.12)',
      '--diff-add-fg': '#166534',
      '--diff-del-bg': 'rgba(220, 38, 38, 0.12)',
      '--diff-del-fg': '#991b1b',
    },
  },
  {
    id: 'paper',
    name: 'Paper Warm',
    isDark: false,
    vars: {
      '--bg-base': '#faf8f5',
      '--bg-sidebar': '#f2efe9',
      '--bg-surface': '#f6f3ed',
      '--bg-elevated': '#ffffff',
      '--bg-hover': '#eae5dc',
      '--border': '#e2dcd1',
      '--border-strong': '#cbc3b4',
      '--text-primary': '#1f1c18',
      '--text-secondary': '#575148',
      '--text-muted': '#7d7569',
      '--accent': '#b45309',
      '--accent-soft': 'rgba(180, 83, 9, 0.10)',
      '--accent-fg': '#ffffff',
      '--success': '#15803d',
      '--warning': '#a16207',
      '--danger': '#b91c1c',
      '--diff-add-bg': 'rgba(21, 128, 61, 0.12)',
      '--diff-add-fg': '#166534',
      '--diff-del-bg': 'rgba(185, 28, 28, 0.12)',
      '--diff-del-fg': '#991b1b',
    },
  },
];

export function sanitizeCustomCss(css: string): string {
  // Strip @import / external url() for safety, matching T3's sanitized Custom CSS
  return css.replace(/@import[^;]*;?/gi, '').replace(/url\s*\([^)]*\)/gi, 'none');
}

export function applyTheme(themeId: string, diffPalette: 'red-green' | 'blue-orange', customCss?: string): ThemePalette {
  const palette = BUILTIN_THEMES.find((t) => t.id === themeId) ?? BUILTIN_THEMES[0];
  const root = document.documentElement;
  for (const [k, v] of Object.entries(palette.vars)) {
    root.style.setProperty(k, v);
  }
  if (diffPalette === 'blue-orange') {
    root.style.setProperty('--diff-add-bg', palette.isDark ? 'rgba(56, 189, 248, 0.16)' : 'rgba(2, 132, 199, 0.12)');
    root.style.setProperty('--diff-add-fg', palette.isDark ? '#7dd3fc' : '#0369a1');
    root.style.setProperty('--diff-del-bg', palette.isDark ? 'rgba(251, 146, 60, 0.16)' : 'rgba(234, 88, 12, 0.12)');
    root.style.setProperty('--diff-del-fg', palette.isDark ? '#fdba74' : '#c2410c');
  }
  root.dataset.theme = palette.isDark ? 'dark' : 'light';
  window.agentcDesktop?.setTitleBarTheme(palette.isDark);

  let styleEl = document.getElementById('agentc-custom-css') as HTMLStyleElement | null;
  if (customCss && customCss.trim()) {
    const safeCss = sanitizeCustomCss(customCss);
    if (!styleEl) {
      styleEl = document.createElement('style');
      styleEl.id = 'agentc-custom-css';
      document.head.appendChild(styleEl);
    }
    styleEl.textContent = safeCss;
  } else if (styleEl) {
    styleEl.remove();
  }
  return palette;
}
