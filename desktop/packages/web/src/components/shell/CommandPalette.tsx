import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  Archive,
  CheckCircle2,
  Clock,
  Cpu,
  FileDiff,
  FolderGit2,
  FolderPlus,
  GitBranch,
  GitFork,
  MessageSquare,
  Palette,
  Pin,
  Plus,
  Search,
  Server,
  Settings,
  StickyNote,
  TerminalSquare,
} from 'lucide-react';
import type { SearchHit, SessionSummary, TerminalInfo } from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';
import { BUILTIN_THEMES } from '../../state/themes.js';
import { matchesClientSearch, plainInlineTitle } from '../../utils/sessionHelpers.js';

interface PaletteAction {
  id: string;
  label: string;
  detail?: string;
  group: string;
  icon: React.ReactNode;
  run: () => void;
}

type TaggedSearchHit = SearchHit & { hostId: string; hostName: string };

export const CommandPalette: React.FC = () => {
  const {
    commandPaletteOpen,
    setCommandPaletteOpen,
    hostList,
    activeHost,
    activeSession,
    allSessions,
    allProjects,
    selectSession,
    openDraft,
    openRightTab,
    setTerminalDrawerOpen,
    setAddProjectModalOpen,
    setPairModalOpen,
    openSettingsTab,
    setViewMode,
    updateSettings,
    upsertHostTerminal,
    refreshHostTerminals,
    pushToast,
    callHost,
  } = useApp();

  const [query, setQuery] = useState('');
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [remoteHits, setRemoteHits] = useState<TaggedSearchHit[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const searchSeqRef = useRef(0);

  useEffect(() => {
    if (commandPaletteOpen) {
      setQuery('');
      setSelectedIdx(0);
      setRemoteHits([]);
      setTimeout(() => inputRef.current?.focus(), 10);
    }
  }, [commandPaletteOpen]);

  // Remote full-text search across ALL connected hosts when typing >= 2 chars
  useEffect(() => {
    const qTrim = query.trim();
    const connectedHosts = hostList.filter((h) => h.state === 'connected');
    if (!commandPaletteOpen || connectedHosts.length === 0 || qTrim.length < 2) {
      setRemoteHits([]);
      return;
    }
    const seq = ++searchSeqRef.current;
    const timer = setTimeout(() => {
      void Promise.allSettled(
        connectedHosts.map(async (h) => {
          const hits = await callHost<SearchHit[]>(h.creds.id, 'search.query', { query: qTrim, limit: 10 });
          return (Array.isArray(hits) ? hits : []).map((hit) => ({
            ...hit,
            hostId: h.creds.id,
            hostName: h.creds.name,
          }));
        }),
      ).then((results) => {
        if (seq !== searchSeqRef.current) return;
        const merged: TaggedSearchHit[] = [];
        for (const r of results) {
          if (r.status === 'fulfilled') merged.push(...r.value);
        }
        setRemoteHits(merged);
      });
    }, 180);
    return () => clearTimeout(timer);
  }, [commandPaletteOpen, hostList, query, callHost]);

  const actions = useMemo<PaletteAction[]>(() => {
    const close = () => setCommandPaletteOpen(false);
    const list: PaletteAction[] = [];

    if (activeHost && activeSession) {
      const hId = activeHost.creds.id;
      const sId = activeSession.id;
      const title = plainInlineTitle(activeSession.title) || 'Current thread';
      list.push(
        {
          id: 'cur-notes',
          label: 'Open Session Notes',
          detail: title,
          group: 'Current Thread',
          icon: <StickyNote className="w-4 h-4 text-[var(--accent)]" />,
          run: () => {
            openRightTab({ id: 'notes', kind: 'notes', title: 'Notes' });
            close();
          },
        },
        {
          id: 'cur-settle',
          label: activeSession.settled ? 'Move Current Thread to Active' : 'Settle Current Thread',
          detail: title,
          group: 'Current Thread',
          icon: <CheckCircle2 className="w-4 h-4" />,
          run: () => {
            void callHost(hId, 'session.settle', { id: sId, settled: !activeSession.settled });
            close();
          },
        },
        {
          id: 'cur-pin',
          label: activeSession.pinned ? 'Unpin Current Thread' : 'Pin Current Thread',
          detail: title,
          group: 'Current Thread',
          icon: <Pin className="w-4 h-4" />,
          run: () => {
            void callHost(hId, 'session.pin', { id: sId, pinned: !activeSession.pinned });
            close();
          },
        },
        {
          id: 'cur-snooze-1h',
          label: 'Snooze Current Thread for 1 Hour',
          detail: title,
          group: 'Current Thread',
          icon: <Clock className="w-4 h-4" />,
          run: () => {
            void callHost(hId, 'session.snooze', { id: sId, until: Date.now() + 3600_000 });
            pushToast({ text: 'Snoozed thread for 1 hour', level: 'info' });
            close();
          },
        },
        {
          id: 'cur-fork',
          label: 'Fork Current Thread',
          detail: title,
          group: 'Current Thread',
          icon: <GitFork className="w-4 h-4" />,
          run: () => {
            void callHost<{ session?: SessionSummary }>(hId, 'session.fork', { id: sId }).then((res) => {
              if (res?.session) selectSession(hId, res.session.id);
            });
            close();
          },
        },
        {
          id: 'cur-compact',
          label: 'Compact Current Thread Context',
          detail: title,
          group: 'Current Thread',
          icon: <Activity className="w-4 h-4" />,
          run: () => {
            void callHost(hId, 'session.compact', { id: sId });
            pushToast({ text: 'Requested context compaction', level: 'info' });
            close();
          },
        },
        {
          id: 'cur-terminal',
          label: 'Continue Current Thread in Terminal',
          detail: title,
          group: 'Current Thread',
          icon: <TerminalSquare className="w-4 h-4" />,
          run: () => {
            void callHost<TerminalInfo>(hId, 'session.openInTerminal', { id: sId, cols: 100, rows: 28 }).then((term) => {
              if (term?.id) {
                upsertHostTerminal(hId, term);
                void refreshHostTerminals(hId);
                openRightTab({
                  id: `term-${term.id}`,
                  kind: 'terminal',
                  title: term.name || `CLI: ${activeSession.agent}`,
                  termId: term.id,
                  hostId: hId,
                });
              }
            });
            close();
          },
        },
        {
          id: 'cur-archive',
          label: activeSession.archived ? 'Restore Current Thread' : 'Archive Current Thread',
          detail: title,
          group: 'Current Thread',
          icon: <Archive className="w-4 h-4" />,
          run: () => {
            void callHost(hId, 'session.archive', { id: sId, archived: !activeSession.archived });
            close();
          },
        },
      );
    }

    list.push(
      { id: 'new-thread', label: 'New Thread', group: 'Actions', icon: <Plus className="w-4 h-4" />, run: () => { openDraft(); close(); } },
      { id: 'add-project', label: 'Add Project (Folder / Init / Clone)', group: 'Actions', icon: <FolderPlus className="w-4 h-4" />, run: () => { setAddProjectModalOpen(true); close(); } },
      { id: 'pair-host', label: 'Pair / Manage Hosts', group: 'Actions', icon: <Server className="w-4 h-4" />, run: () => { setPairModalOpen(true); close(); } },
      { id: 'codex-openrouter', label: 'Configure Codex & OpenRouter', group: 'Actions', icon: <Cpu className="w-4 h-4" />, run: () => { openSettingsTab('providers'); close(); } },
      { id: 'open-diff', label: 'Open Diff Panel', group: 'Panels', icon: <FileDiff className="w-4 h-4" />, run: () => { openRightTab({ id: 'diff', kind: 'diff', title: 'Diff', diffScope: 'uncommitted' }); close(); } },
      { id: 'open-files', label: 'Open Files Explorer', group: 'Panels', icon: <FolderGit2 className="w-4 h-4" />, run: () => { openRightTab({ id: 'files', kind: 'files', title: 'Files' }); close(); } },
      { id: 'open-notes', label: 'Open Session Notes', group: 'Panels', icon: <StickyNote className="w-4 h-4" />, run: () => { openRightTab({ id: 'notes', kind: 'notes', title: 'Notes' }); close(); } },
      { id: 'open-side', label: 'Open Side Chat (/btw)', group: 'Panels', icon: <MessageSquare className="w-4 h-4" />, run: () => { openRightTab({ id: 'side', kind: 'side', title: 'Side Chat' }); close(); } },
      { id: 'open-git', label: 'Open Source Control & Worktrees', group: 'Panels', icon: <GitBranch className="w-4 h-4" />, run: () => { openRightTab({ id: 'git', kind: 'git', title: 'Source Control' }); close(); } },
      { id: 'open-pr', label: 'Open Pull Requests & Issues', group: 'Panels', icon: <GitBranch className="w-4 h-4" />, run: () => { openRightTab({ id: 'pr', kind: 'pr', title: 'Pull Requests' }); close(); } },
      { id: 'toggle-term', label: 'Toggle Terminal Drawer', group: 'Panels', icon: <TerminalSquare className="w-4 h-4" />, run: () => { setTerminalDrawerOpen((v) => !v); close(); } },
      { id: 'open-usage', label: 'Usage & Cost Dashboard', group: 'Views', icon: <Activity className="w-4 h-4" />, run: () => { setViewMode('usage'); close(); } },
      { id: 'open-schedules', label: 'Scheduled Tasks', group: 'Views', icon: <Clock className="w-4 h-4" />, run: () => { setViewMode('schedules'); close(); } },
      { id: 'open-settings', label: 'Settings', group: 'Views', icon: <Settings className="w-4 h-4" />, run: () => { openSettingsTab('general'); close(); } },
    );

    for (const th of BUILTIN_THEMES) {
      list.push({
        id: `theme-${th.id}`,
        label: `Theme: ${th.name}`,
        group: 'Appearance',
        icon: <Palette className="w-4 h-4" />,
        run: () => { updateSettings({ themeId: th.id }); close(); },
      });
    }

    for (const p of allProjects) {
      list.push({
        id: `proj-${p.hostId}-${p.id}`,
        label: `New thread in ${p.name}`,
        detail: `${p.hostName} · ${p.path}`,
        group: 'Projects',
        icon: <FolderGit2 className="w-4 h-4" />,
        run: () => { openDraft({ hostId: p.hostId, projectId: p.id, cwd: p.path }); close(); },
      });
    }

    for (const s of allSessions.slice(0, 40)) {
      list.push({
        id: `sess-${s.hostId}-${s.id}`,
        label: plainInlineTitle(s.title) || 'Untitled session',
        detail: `${s.hostName} · ${s.agent}${s.branch ? ' · ' + s.branch : ''}`,
        group: 'Threads',
        icon: <MessageSquare className="w-4 h-4" />,
        run: () => { selectSession(s.hostId, s.id); close(); },
      });
    }

    const q = query.trim();
    const filtered = q
      ? list.filter((a) => matchesClientSearch(q, a.label, a.detail, a.group))
      : list.slice(0, 28);

    // Append message search hits across all connected daemons
    for (const hit of remoteHits) {
      if (hit.kind === 'message' && hit.sessionId) {
        const prefix = hostList.length > 1 ? `${hit.hostName} · ` : '';
        filtered.push({
          id: `hit-${hit.hostId}-${hit.sessionId}-${hit.updatedAt}`,
          label: plainInlineTitle(hit.title) || hit.sessionId,
          detail: `${prefix}${hit.snippet || ''}`,
          group: 'Message Matches',
          icon: <Search className="w-4 h-4 text-[var(--accent)]" />,
          run: () => { selectSession(hit.hostId, hit.sessionId!); close(); },
        });
      }
    }
    return filtered;
  }, [
    activeHost,
    activeSession,
    allProjects,
    allSessions,
    callHost,
    hostList.length,
    openDraft,
    openRightTab,
    openSettingsTab,
    pushToast,
    query,
    refreshHostTerminals,
    remoteHits,
    selectSession,
    setAddProjectModalOpen,
    setCommandPaletteOpen,
    setPairModalOpen,
    setTerminalDrawerOpen,
    setViewMode,
    updateSettings,
    upsertHostTerminal,
  ]);

  if (!commandPaletteOpen) return null;

  return (
    <div
      data-testid="command-palette"
      className="fixed inset-0 z-50 flex items-start justify-center pt-[12vh] bg-black/55 backdrop-blur-xs p-4"
      onClick={() => setCommandPaletteOpen(false)}
    >
      <div
        className="w-full max-w-xl rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] shadow-2xl overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 px-4 py-3 border-b border-[var(--border)]">
          <Search className="w-4 h-4 text-[var(--text-muted)]" />
          <input
            ref={inputRef}
            data-testid="command-palette-input"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelectedIdx(0);
            }}
            onKeyDown={(e) => {
              if (e.key === 'Escape') setCommandPaletteOpen(false);
              else if (e.key === 'ArrowDown') {
                e.preventDefault();
                setSelectedIdx((i) => Math.min(actions.length - 1, i + 1));
              } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                setSelectedIdx((i) => Math.max(0, i - 1));
              } else if (e.key === 'Enter' && actions[selectedIdx]) {
                e.preventDefault();
                actions[selectedIdx].run();
              }
            }}
            placeholder="Jump to thread, project, panel, theme, or search messages…"
            className="flex-1 bg-transparent text-sm text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none"
          />
          <kbd className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--bg-elevated)] text-[var(--text-muted)]">ESC</kbd>
        </div>

        <div className="max-h-[52vh] overflow-y-auto py-1.5">
          {actions.length === 0 ? (
            <div className="px-4 py-6 text-center text-xs text-[var(--text-muted)]">No matching commands or threads</div>
          ) : (
            actions.map((a, idx) => (
              <button
                key={a.id}
                type="button"
                onMouseEnter={() => setSelectedIdx(idx)}
                onClick={() => a.run()}
                className={`w-full flex items-center justify-between gap-3 px-4 py-2 text-left text-xs cursor-pointer ${
                  idx === selectedIdx ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                }`}
              >
                <div className="flex items-center gap-2.5 min-w-0">
                  <span className="text-[var(--text-muted)] shrink-0">{a.icon}</span>
                  <div className="min-w-0">
                    <div className="font-medium text-[var(--text-primary)] truncate">{a.label}</div>
                    {a.detail && <div className="text-[11px] text-[var(--text-muted)] truncate">{a.detail}</div>}
                  </div>
                </div>
                <span className="shrink-0 text-[10px] uppercase tracking-wider text-[var(--text-muted)]">{a.group}</span>
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  );
};
