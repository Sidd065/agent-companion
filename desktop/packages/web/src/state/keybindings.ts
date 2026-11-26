export interface KeybindingDef {
  id: string;
  label: string;
  group: 'Navigation' | 'Threads' | 'Composer' | 'Panels' | 'Search & Views';
  defaultChord: string;
}

export const DEFAULT_KEYBINDINGS: KeybindingDef[] = [
  { id: 'commandPalette', label: 'Open Command Palette', group: 'Search & Views', defaultChord: 'mod+k' },
  { id: 'fileFinder', label: 'Quick Open File', group: 'Search & Views', defaultChord: 'mod+p' },
  { id: 'globalSearch', label: 'Search Threads & Messages', group: 'Search & Views', defaultChord: 'mod+shift+f' },
  { id: 'findInThread', label: 'Find in Thread', group: 'Search & Views', defaultChord: 'mod+f' },
  { id: 'openUsage', label: 'Open Usage Dashboard', group: 'Search & Views', defaultChord: 'mod+u' },
  { id: 'openSettings', label: 'Open Settings', group: 'Search & Views', defaultChord: 'mod+,' },
  { id: 'toggleSidebar', label: 'Toggle Sidebar', group: 'Navigation', defaultChord: 'mod+b' },
  { id: 'toggleRightPanel', label: 'Toggle Right Panel', group: 'Panels', defaultChord: 'mod+alt+b' },
  { id: 'toggleTerminal', label: 'Toggle Terminal Drawer', group: 'Panels', defaultChord: 'mod+j' },
  { id: 'toggleDiff', label: 'Open / Toggle Diff Panel', group: 'Panels', defaultChord: 'mod+d' },
  { id: 'reopenClosedTab', label: 'Reopen Closed Right-Panel Tab', group: 'Panels', defaultChord: 'mod+shift+t' },
  { id: 'newThread', label: 'New Thread', group: 'Threads', defaultChord: 'mod+n' },
  { id: 'settleThread', label: 'Settle / Unsettle Thread', group: 'Threads', defaultChord: 'mod+shift+s' },
  { id: 'pinThread', label: 'Pin / Unpin Thread', group: 'Threads', defaultChord: 'mod+shift+p' },
  { id: 'undoThreadAction', label: 'Undo Last Sidebar Action', group: 'Threads', defaultChord: 'mod+z' },
  { id: 'openModelPicker', label: 'Open Model Picker', group: 'Composer', defaultChord: 'mod+shift+m' },
  { id: 'openSideChat', label: 'Open Side Chat (/btw)', group: 'Panels', defaultChord: 'mod+shift+b' },
  { id: 'openSessionNotes', label: 'Open Session Notes', group: 'Panels', defaultChord: 'mod+shift+n' },
  { id: 'cycleTerminalPrev', label: 'Previous Terminal Tab', group: 'Panels', defaultChord: 'mod+shift+[' },
  { id: 'cycleTerminalNext', label: 'Next Terminal Tab', group: 'Panels', defaultChord: 'mod+shift+]' },
  { id: 'switchRecentThread', label: 'Switch to Previous Thread (MRU)', group: 'Navigation', defaultChord: 'mod+tab' },
  { id: 'jumpLastNotified', label: 'Jump to Last Notified Thread', group: 'Navigation', defaultChord: 'mod+shift+u' },
];

export function formatEventChord(e: KeyboardEvent): string {
  const parts: string[] = [];
  if (e.ctrlKey || e.metaKey) parts.push('mod');
  if (e.altKey) parts.push('alt');
  if (e.shiftKey) parts.push('shift');
  const k = e.key.toLowerCase();
  if (!['control', 'meta', 'alt', 'shift'].includes(k)) {
    const norm = k === '{' ? '[' : k === '}' ? ']' : k === ' ' ? 'space' : k;
    parts.push(norm);
  }
  return parts.join('+');
}

export function matchChord(e: KeyboardEvent, chord: string): boolean {
  if (!chord) return false;
  return formatEventChord(e) === chord.toLowerCase().trim();
}

export function displayChord(chord: string): string {
  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);
  return chord
    .split('+')
    .map((p) => {
      if (p === 'mod') return isMac ? '⌘' : 'Ctrl';
      if (p === 'shift') return 'Shift';
      if (p === 'alt') return isMac ? '⌥' : 'Alt';
      return p.length === 1 ? p.toUpperCase() : p;
    })
    .join('+');
}
