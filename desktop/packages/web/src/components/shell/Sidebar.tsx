import React, { useMemo, useState } from 'react';
import {
  Archive,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  FolderGit2,
  FolderPlus,
  GitBranch,
  Loader2,
  Pin,
  Plus,
  RefreshCw,
  Search,
  Server,
  Trash2,
} from 'lucide-react';
import type { SessionSummary } from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';

type RowSession = SessionSummary & { hostId: string; hostName: string };

function statusBadge(s: SessionSummary): { label: string; tone: string } | null {
  if (s.pendingPermissions > 0 || s.attention === 'permission') {
    return { label: 'Approval', tone: 'bg-amber-500/20 text-amber-300 border-amber-500/40' };
  }
  if (s.attention === 'question') {
    return { label: 'Question', tone: 'bg-sky-500/20 text-sky-300 border-sky-500/40' };
  }
  if (s.attention === 'plan') {
    return { label: 'Plan', tone: 'bg-violet-500/20 text-violet-300 border-violet-500/40' };
  }
  if (s.state === 'running' || s.state === 'starting') {
    return { label: 'Working', tone: 'bg-blue-500/20 text-blue-300 border-blue-500/40' };
  }
  if (s.state === 'error') {
    return { label: 'Error', tone: 'bg-red-500/20 text-red-300 border-red-500/40' };
  }
  return null;
}

function agentShort(agent: string): string {
  switch (agent) {
    case 'claude':
      return 'CL';
    case 'agy':
      return 'AG';
    case 'codex':
      return 'CX';
    default:
      return agent.slice(0, 2).toUpperCase();
  }
}

export const Sidebar: React.FC = () => {
  const {
    hostList,
    allProjects,
    allSessions,
    activeHostId,
    setActiveHostId,
    activeSessionId,
    selectSession,
    openDraft,
    setAddProjectModalOpen,
    setPairModalOpen,
    settings,
    callHost,
    pushToast,
    reconnectHost,
    openSettingsTab,
    openRightTab,
  } = useApp();

  const [query, setQuery] = useState('');
  const [selectedHostFilter, setSelectedHostFilter] = useState<string>('all');
  const [selectedProjectFilter, setSelectedProjectFilter] = useState<string>('all');
  const [workingShelfOpen, setWorkingShelfOpen] = useState(true);
  const [settledOpen, setSettledOpen] = useState(false);
  const [collapsedProjectGroups, setCollapsedProjectGroups] = useState<Record<string, boolean>>({});

  const filteredSessions = useMemo(() => {
    const q = query.trim().toLowerCase();
    return allSessions.filter((s) => {
      if (s.archived) return false;
      if (selectedHostFilter !== 'all' && s.hostId !== selectedHostFilter) return false;
      if (selectedProjectFilter !== 'all' && `${s.hostId}:${s.projectId || ''}` !== selectedProjectFilter && s.projectId !== selectedProjectFilter) {
        return false;
      }
      if (q) {
        const hay = `${s.title} ${s.cwd} ${s.branch || ''} ${s.agent} ${s.hostName}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [allSessions, query, selectedHostFilter, selectedProjectFilter]);

  const { pinned, working, active, settled } = useMemo(() => {
    const pinnedList: RowSession[] = [];
    const workingList: RowSession[] = [];
    const activeList: RowSession[] = [];
    const settledList: RowSession[] = [];

    for (const s of filteredSessions) {
      if (s.pinned) {
        pinnedList.push(s);
      } else if (s.settled) {
        settledList.push(s);
      } else if (
        settings.showWorkingShelf &&
        (s.state === 'running' || s.state === 'starting') &&
        s.pendingPermissions === 0 &&
        !s.attention
      ) {
        workingList.push(s);
      } else {
        activeList.push(s);
      }
    }
    return { pinned: pinnedList, working: workingList, active: activeList, settled: settledList };
  }, [filteredSessions, settings.showWorkingShelf]);

  const groupedActive = useMemo(() => {
    if (!settings.groupByProject) return null;
    const groups = new Map<string, { key: string; label: string; rows: RowSession[] }>();
    for (const s of active) {
      const proj = allProjects.find((p) => p.hostId === s.hostId && p.id === s.projectId);
      const baseLabel = proj?.name || s.cwd.split('/').filter(Boolean).pop() || 'Other';
      const label = hostList.length > 1 ? `${baseLabel} · ${s.hostName}` : baseLabel;
      const key = `${s.hostId}:${proj?.id || baseLabel}`;
      const cur = groups.get(key) ?? { key, label, rows: [] };
      cur.rows.push(s);
      groups.set(key, cur);
    }
    return Array.from(groups.values());
  }, [settings.groupByProject, active, allProjects, hostList.length]);

  const handleSettle = async (s: RowSession, next: boolean) => {
    try {
      await callHost(s.hostId, 'session.settle', { id: s.id, settled: next });
      pushToast({
        text: next ? `Settled "${s.title}"` : `Moved "${s.title}" to Active`,
        level: 'info',
        undoLabel: 'Undo',
        onUndo: () => {
          void callHost(s.hostId, 'session.settle', { id: s.id, settled: !next });
        },
      });
    } catch (err) {
      pushToast({ text: (err as Error).message, level: 'error' });
    }
  };

  const handlePin = async (s: RowSession, next: boolean) => {
    try {
      await callHost(s.hostId, 'session.pin', { id: s.id, pinned: next });
      pushToast({
        text: next ? 'Pinned thread' : 'Unpinned thread',
        level: 'info',
        undoLabel: 'Undo',
        onUndo: () => {
          void callHost(s.hostId, 'session.pin', { id: s.id, pinned: !next });
        },
      });
    } catch (err) {
      pushToast({ text: (err as Error).message, level: 'error' });
    }
  };

  const handleArchive = async (s: RowSession) => {
    try {
      await callHost(s.hostId, 'session.archive', { id: s.id, archived: true });
      pushToast({
        text: `Archived "${s.title}"`,
        level: 'info',
        undoLabel: 'Undo',
        onUndo: () => {
          void callHost(s.hostId, 'session.archive', { id: s.id, archived: false });
        },
      });
    } catch (err) {
      pushToast({ text: (err as Error).message, level: 'error' });
    }
  };

  const handleDelete = async (s: RowSession) => {
    if (!confirm(`Permanently delete thread "${s.title}"?`)) return;
    try {
      await callHost(s.hostId, 'session.delete', { id: s.id });
      pushToast({ text: 'Thread deleted', level: 'info' });
    } catch (err) {
      pushToast({ text: (err as Error).message, level: 'error' });
    }
  };

  const renderRow = (s: RowSession) => {
    const isActive = s.hostId === activeHostId && s.id === activeSessionId;
    const badge = statusBadge(s);
    const proj = allProjects.find((p) => p.hostId === s.hostId && p.id === s.projectId);
    return (
      <div
        key={`${s.hostId}:${s.id}`}
        data-testid={`thread-row-${s.id}`}
        onClick={() => selectSession(s.hostId, s.id)}
        className={`group relative mx-2 mb-1 px-2.5 py-2 rounded-lg cursor-pointer border transition-colors ${
          isActive
            ? 'bg-[var(--bg-elevated)] border-[var(--border-strong)]'
            : 'bg-transparent border-transparent hover:bg-[var(--bg-hover)]/60'
        }`}
      >
        <div className="flex items-center justify-between gap-1.5 mb-0.5">
          <div className="flex items-center gap-1.5 min-w-0">
            <span className="shrink-0 text-[10px] font-mono font-semibold px-1 py-0.2 rounded bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-secondary)]">
              {agentShort(s.agent)}
            </span>
            <span className="text-[11px] text-[var(--text-muted)] truncate">
              {proj?.name || s.cwd.split('/').filter(Boolean).pop() || s.hostName}
            </span>
            {hostList.length > 1 && (
              <span className="text-[10px] text-[var(--text-muted)] px-1 rounded bg-[var(--bg-base)]">
                {s.hostName}
              </span>
            )}
          </div>
          {badge && (
            <span className={`shrink-0 text-[10px] font-medium px-1.5 py-0.5 rounded border ${badge.tone} flex items-center gap-1`}>
              {badge.label === 'Working' && <Loader2 className="w-2.5 h-2.5 animate-spin" />}
              {badge.label}
            </span>
          )}
        </div>

        <div className="text-[13px] font-medium text-[var(--text-primary)] truncate pr-10">
          {s.title || 'Untitled session'}
        </div>

        <div className="flex items-center gap-2 mt-0.5 text-[11px] text-[var(--text-muted)]">
          {s.branch && (
            <span className="inline-flex items-center gap-0.5 truncate max-w-[130px]">
              <GitBranch className="w-3 h-3 shrink-0" />
              {s.branch}
            </span>
          )}
          {s.lastCheckpoint && (s.lastCheckpoint.totalAdd > 0 || s.lastCheckpoint.totalDel > 0) && (
            <span className="font-mono text-[10px]">
              <span className="text-[var(--success)]">+{s.lastCheckpoint.totalAdd}</span>{' '}
              <span className="text-[var(--danger)]">-{s.lastCheckpoint.totalDel}</span>
            </span>
          )}
        </div>

        {/* Hover quick actions */}
        <div
          className="hidden group-hover:flex items-center gap-0.5 absolute right-2 bottom-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded-md p-0.5 shadow"
          onClick={(e) => e.stopPropagation()}
        >
          <button
            type="button"
            title={s.settled ? 'Move to Active' : 'Settle'}
            onClick={() => void handleSettle(s, !s.settled)}
            className="p-1 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] cursor-pointer"
          >
            <CheckCircle2 className="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            title={s.pinned ? 'Unpin' : 'Pin'}
            onClick={() => void handlePin(s, !s.pinned)}
            className={`p-1 rounded hover:bg-[var(--bg-hover)] cursor-pointer ${s.pinned ? 'text-[var(--accent)]' : 'text-[var(--text-secondary)]'}`}
          >
            <Pin className="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            title="Archive"
            onClick={() => void handleArchive(s)}
            className="p-1 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] cursor-pointer"
          >
            <Archive className="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            title="Delete"
            onClick={() => void handleDelete(s)}
            className="p-1 rounded hover:bg-[var(--bg-hover)] text-[var(--danger)] cursor-pointer"
          >
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
    );
  };

  const selectedProjEntry =
    selectedProjectFilter !== 'all'
      ? allProjects.find((p) => `${p.hostId}:${p.id}` === selectedProjectFilter || p.id === selectedProjectFilter)
      : undefined;

  return (
    <aside
      data-testid="sidebar"
      className="w-[276px] shrink-0 h-full flex flex-col border-r border-[var(--border)] bg-[var(--bg-sidebar)] select-none"
    >
      {/* Top actions: New Thread + Add Project */}
      <div className="p-2.5 space-y-2 border-b border-[var(--border)]">
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            data-testid="new-thread-btn"
            onClick={() =>
              openDraft({
                hostId: selectedProjEntry?.hostId || (selectedHostFilter !== 'all' ? selectedHostFilter : activeHostId),
                projectId: selectedProjEntry?.id,
                cwd: selectedProjEntry?.path,
              })
            }
            className="flex-1 flex items-center justify-center gap-1.5 py-1.5 px-2.5 rounded-lg bg-[var(--accent)] text-[var(--accent-fg)] font-medium text-xs hover:opacity-90 cursor-pointer"
          >
            <Plus className="w-3.5 h-3.5" />
            New thread
          </button>
          <button
            type="button"
            data-testid="add-project-btn"
            onClick={() => setAddProjectModalOpen(true)}
            title="Add project"
            className="p-1.5 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            <FolderPlus className="w-4 h-4" />
          </button>
        </div>

        {/* Search threads */}
        <div className="relative">
          <Search className="w-3.5 h-3.5 absolute left-2.5 top-2 text-[var(--text-muted)]" />
          <input
            type="text"
            data-testid="sidebar-search-input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter threads…"
            className="w-full pl-8 pr-2.5 py-1.5 text-xs rounded-md bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:border-[var(--accent)]"
          />
        </div>

        {/* Multi-host selector chips */}
        {hostList.length > 0 && (
          <div className="flex items-center gap-1 overflow-x-auto pb-0.5">
            {hostList.length > 1 && (
              <button
                type="button"
                onClick={() => {
                  setSelectedHostFilter('all');
                  setSelectedProjectFilter('all');
                }}
                className={`shrink-0 px-2 py-0.5 rounded text-[11px] cursor-pointer ${
                  selectedHostFilter === 'all'
                    ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                    : 'text-[var(--text-muted)] hover:bg-[var(--bg-hover)]'
                }`}
              >
                All ({hostList.length})
              </button>
            )}
            {hostList.map((h) => (
              <button
                key={h.creds.id}
                type="button"
                data-testid={`host-chip-${h.creds.id}`}
                onClick={() => {
                  setSelectedHostFilter(selectedHostFilter === h.creds.id ? 'all' : h.creds.id);
                  setSelectedProjectFilter('all');
                  setActiveHostId(h.creds.id);
                }}
                className={`shrink-0 flex items-center gap-1 px-2 py-0.5 rounded text-[11px] cursor-pointer ${
                  selectedHostFilter === h.creds.id
                    ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                    : 'bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                }`}
              >
                <span
                  className={`w-1.5 h-1.5 rounded-full ${
                    h.state === 'connected'
                      ? 'bg-[var(--success)]'
                      : h.state === 'reconnecting' || h.state === 'connecting'
                        ? 'bg-[var(--warning)]'
                        : 'bg-[var(--danger)]'
                  }`}
                />
                {h.creds.name}
                {(h.state === 'error' || h.state === 'reconnecting') && (
                  <RefreshCw
                    className="w-2.5 h-2.5 ml-0.5 opacity-80 hover:opacity-100"
                    onClick={(e) => {
                      e.stopPropagation();
                      reconnectHost(h.creds.id);
                    }}
                  />
                )}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Projects rail */}
      {allProjects.length > 0 && (
        <div className="px-2 py-2 border-b border-[var(--border)] max-h-[168px] overflow-y-auto">
          <div className="flex items-center justify-between px-1.5 mb-1">
            <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
              Projects
            </span>
            {selectedProjectFilter !== 'all' && (
              <button
                type="button"
                onClick={() => setSelectedProjectFilter('all')}
                className="text-[10px] text-[var(--accent)] hover:underline cursor-pointer"
              >
                Clear
              </button>
            )}
          </div>
          {allProjects.map((p) => {
            const filterKey = `${p.hostId}:${p.id}`;
            const active = selectedProjectFilter === filterKey || selectedProjectFilter === p.id;
            return (
              <div
                key={filterKey}
                data-testid={`project-item-${p.name}`}
                onClick={() => {
                  setSelectedProjectFilter(active ? 'all' : filterKey);
                  if (!active && p.hostId !== activeHostId) {
                    setActiveHostId(p.hostId);
                  }
                }}
                className={`group flex items-center justify-between gap-1 px-2 py-1 rounded-md text-xs cursor-pointer ${
                  active ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                }`}
              >
                <div className="flex items-center gap-1.5 min-w-0 flex-1">
                  <FolderGit2 className="w-3.5 h-3.5 shrink-0 opacity-75" />
                  <span className="truncate">{p.name}</span>
                  {hostList.length > 1 && (
                    <span className="shrink-0 rounded bg-[var(--bg-base)] px-1 py-0.2 text-[9.5px] text-[var(--text-muted)]">
                      {p.hostName}
                    </span>
                  )}
                </div>
                <button
                  type="button"
                  title={`New thread in ${p.name}`}
                  onClick={(e) => {
                    e.stopPropagation();
                    openDraft({ hostId: p.hostId, projectId: p.id, cwd: p.path });
                  }}
                  className="opacity-0 group-hover:opacity-100 p-0.5 rounded hover:bg-[var(--bg-elevated)] cursor-pointer shrink-0"
                >
                  <Plus className="w-3 h-3" />
                </button>
              </div>
            );
          })}
        </div>
      )}

      {/* Threads inbox: Pinned -> Working shelf -> Active -> Settled */}
      <div className="flex-1 overflow-y-auto py-2">
        {hostList.length === 0 ? (
          <div className="px-4 py-8 text-center">
            <Server className="w-8 h-8 mx-auto mb-2 text-[var(--text-muted)]" />
            <div className="text-sm font-medium mb-1">No hosts paired yet</div>
            <p className="text-xs text-[var(--text-muted)] mb-3">
              Connect to your local agentc daemon or paste a link from <code className="font-mono">agentc pair</code>.
            </p>
            <button
              type="button"
              data-testid="sidebar-pair-cta"
              onClick={() => setPairModalOpen(true)}
              className="px-3 py-1.5 rounded-md bg-[var(--accent)] text-[var(--accent-fg)] text-xs font-medium cursor-pointer"
            >
              Pair a host
            </button>
          </div>
        ) : (
          <>
            {pinned.length > 0 && (
              <div className="mb-2">
                <div className="px-3 mb-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                  Pinned ({pinned.length})
                </div>
                {pinned.map(renderRow)}
              </div>
            )}

            {working.length > 0 && (
              <div className="mb-2">
                <button
                  type="button"
                  onClick={() => setWorkingShelfOpen((v) => !v)}
                  className="w-full flex items-center gap-1 px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--accent)] hover:bg-[var(--bg-hover)]/40 cursor-pointer"
                >
                  {workingShelfOpen ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                  Working ({working.length})
                </button>
                {workingShelfOpen && working.map(renderRow)}
              </div>
            )}

            <div className="mb-2">
              <div className="px-3 mb-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                Active ({active.length})
              </div>
              {active.length === 0 ? (
                <div className="px-4 py-3 text-xs text-[var(--text-muted)]">No active threads</div>
              ) : groupedActive ? (
                <div className="space-y-1.5">
                  {groupedActive.map((grp) => {
                    const isCollapsed = Boolean(collapsedProjectGroups[grp.key]);
                    return (
                      <div key={grp.key}>
                        <button
                          type="button"
                          onClick={() =>
                            setCollapsedProjectGroups((prev) => ({ ...prev, [grp.key]: !isCollapsed }))
                          }
                          className="w-full flex items-center gap-1 px-3 py-0.5 text-[10.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                        >
                          {isCollapsed ? (
                            <ChevronRight className="w-3 h-3 shrink-0" />
                          ) : (
                            <ChevronDown className="w-3 h-3 shrink-0" />
                          )}
                          <FolderGit2 className="w-3 h-3 shrink-0 text-[var(--accent)]" />
                          <span className="truncate">{grp.label}</span>
                          <span className="text-[10px] text-[var(--text-muted)]">({grp.rows.length})</span>
                        </button>
                        {!isCollapsed && grp.rows.map(renderRow)}
                      </div>
                    );
                  })}
                </div>
              ) : (
                active.map(renderRow)
              )}
            </div>

            {settled.length > 0 && (
              <div className="mt-2 pt-2 border-t border-[var(--border)]">
                <button
                  type="button"
                  data-testid="settled-toggle-btn"
                  onClick={() => setSettledOpen((v) => !v)}
                  className="w-full flex items-center gap-1 px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)] hover:text-[var(--text-secondary)] cursor-pointer"
                >
                  {settledOpen ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                  Settled ({settled.length})
                </button>
                {settledOpen && settled.map(renderRow)}
              </div>
            )}
          </>
        )}
      </div>

      {/* Bottom quick-bar matching T3 */}
      <div className="px-2.5 py-2 border-t border-[var(--border)] flex items-center justify-between text-xs text-[var(--text-secondary)]">
        <button
          type="button"
          onClick={() => openRightTab({ id: 'git', kind: 'git', title: 'Source Control' })}
          className="flex items-center gap-1.5 px-2 py-1 rounded hover:bg-[var(--bg-hover)] cursor-pointer"
          title="Source Control"
        >
          <GitBranch className="w-3.5 h-3.5" />
          <span>Git</span>
        </button>
        <button
          type="button"
          data-testid="sidebar-health-btn"
          onClick={() => openSettingsTab('health')}
          className="flex items-center gap-1.5 px-2 py-1 rounded hover:bg-[var(--bg-hover)] cursor-pointer"
          title="Host Health & Diagnostics"
        >
          <Server className="w-3.5 h-3.5" />
          <span>Health</span>
        </button>
        <button
          type="button"
          onClick={() => openSettingsTab('archive')}
          className="flex items-center gap-1.5 px-2 py-1 rounded hover:bg-[var(--bg-hover)] cursor-pointer"
          title="Archived Threads"
        >
          <Archive className="w-3.5 h-3.5" />
          <span>Archive</span>
        </button>
      </div>
    </aside>
  );
};
