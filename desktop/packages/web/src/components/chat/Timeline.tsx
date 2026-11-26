import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertCircle,
  AlertTriangle,
  ArrowDown,
  Brain,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Copy,
  FileDiff,
  FileEdit,
  FileSearch,
  GitFork,
  Loader2,
  Quote,
  RotateCcw,
  ShieldCheck,
  Terminal,
  Wrench,
  XCircle,
} from 'lucide-react';
import type {
  SessionDetail,
  SessionSummary,
  ToolCall,
  TranscriptItem,
  TurnCheckpoint,
} from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';
import { extractToolInlineDiffs, extractToolOutputText } from '../../utils/diffParser.js';
import { MarkdownRenderer } from '../markdown/MarkdownRenderer.js';

function formatDuration(ms: number): string {
  const sec = Math.max(0, Math.round(ms / 1000));
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return s > 0 ? `${m}m ${s}s` : `${m}m`;
}

/**
 * Isolated live timer component so 1s ticks never re-render the rest of the timeline.
 */
const WorkingTimer: React.FC<{ startMs: number }> = React.memo(({ startMs }) => {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const elapsed = Math.max(0, now - startMs);
  return (
    <div
      data-testid="working-timer"
      className="inline-flex items-center gap-2 rounded-full border border-[var(--accent)]/40 bg-[var(--accent-soft)] px-3 py-1 text-xs font-medium text-[var(--accent)]"
    >
      <Loader2 className="w-3.5 h-3.5 animate-spin" />
      <span>Working for {formatDuration(elapsed)}</span>
    </div>
  );
});

function stringifyVal(val: unknown): string {
  if (val == null) return '';
  if (typeof val === 'string') return val;
  try {
    return JSON.stringify(val, null, 2);
  } catch {
    return String(val);
  }
}

function extractCommand(tool: ToolCall): string | null {
  const inp = tool.input as Record<string, unknown> | undefined;
  if (inp && typeof inp === 'object' && typeof inp.command === 'string') return inp.command;
  if (typeof tool.title === 'string' && tool.title.startsWith('$ ')) return tool.title.slice(2);
  return null;
}

const ToolRow: React.FC<{ item: TranscriptItem; onCite: (label: string, content: string) => void }> = React.memo(
  ({ item, onCite }) => {
    const tool = item.tool;
    const [open, setOpen] = useState(false);
    if (!tool) return null;

    const kind = (tool.kind || tool.name || '').toLowerCase();
    const isErr = tool.status === 'failed' || tool.status === 'error';
    const isRunning = tool.status === 'in_progress' || tool.status === 'pending' || tool.status === 'running';
    const isExec = kind.includes('exec') || kind.includes('bash') || kind.includes('shell') || kind.includes('cmd');
    const isEdit = kind.includes('edit') || kind.includes('write') || kind.includes('patch');
    const isRead = kind.includes('read') || kind.includes('search') || kind.includes('glob') || kind.includes('grep');

    const cmd = isExec ? extractCommand(tool) : null;
    const inlineDiffs = isEdit ? extractToolInlineDiffs(tool) : [];
    const outStr = extractToolOutputText(tool);
    const inpStr = stringifyVal(tool.input);
    const durMs = tool.startTs && tool.endTs ? Math.max(0, tool.endTs - tool.startTs) : 0;

    const Icon = isExec ? Terminal : isEdit ? FileEdit : isRead ? FileSearch : Wrench;

    return (
      <div
        data-testid="tool-row"
        className={`rounded-lg border text-xs transition ${
          isErr
            ? 'border-[var(--danger)]/40 bg-[var(--danger)]/5'
            : 'border-[var(--border)] bg-[var(--bg-surface)]/80'
        }`}
      >
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left hover:bg-[var(--bg-hover)]/60 rounded-lg cursor-pointer"
        >
          {open ? (
            <ChevronDown className="w-3 h-3 shrink-0 text-[var(--text-muted)]" />
          ) : (
            <ChevronRight className="w-3 h-3 shrink-0 text-[var(--text-muted)]" />
          )}
          <Icon className="w-3.5 h-3.5 shrink-0 text-[var(--accent)]" />
          <span className="font-medium text-[var(--text-primary)] shrink-0">
            {tool.name || tool.kind || 'Tool'}
          </span>
          <span className="font-mono text-[11px] text-[var(--text-secondary)] truncate flex-1">
            {cmd ? `$ ${cmd}` : tool.title || inlineDiffs[0]?.path || ''}
          </span>
          {durMs > 0 && <span className="text-[10px] font-mono text-[var(--text-muted)]">{formatDuration(durMs)}</span>}
          {isRunning ? (
            <Loader2 className="w-3.5 h-3.5 shrink-0 animate-spin text-[var(--accent)]" />
          ) : isErr ? (
            <XCircle className="w-3.5 h-3.5 shrink-0 text-[var(--danger)]" />
          ) : (
            <CheckCircle2 className="w-3.5 h-3.5 shrink-0 text-[var(--success)]" />
          )}
        </button>

        {open && (
          <div className="border-t border-[var(--border)] px-3 py-2 space-y-2">
            {inlineDiffs.length > 0 ? (
              <div className="space-y-1.5">
                {inlineDiffs.map((df, dIdx) => (
                  <div
                    key={`${df.path || 'diff'}-${dIdx}`}
                    data-testid="tool-inline-diff"
                    className="rounded-md border border-[var(--border)] bg-[var(--bg-base)] overflow-x-auto font-mono text-[11.5px]"
                  >
                    {df.path && (
                      <div className="border-b border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[10.5px] text-[var(--text-secondary)]">
                        {df.path}
                      </div>
                    )}
                    {df.oldStr &&
                      df.oldStr.split('\n').map((ln, idx) => (
                        <div key={`d-${idx}`} className="px-2 py-0.5 bg-[var(--diff-del-bg)] text-[var(--diff-del-fg)] whitespace-pre">
                          - {ln}
                        </div>
                      ))}
                    {df.newStr &&
                      df.newStr.split('\n').map((ln, idx) => (
                        <div key={`a-${idx}`} className="px-2 py-0.5 bg-[var(--diff-add-bg)] text-[var(--diff-add-fg)] whitespace-pre">
                          + {ln}
                        </div>
                      ))}
                  </div>
                ))}
              </div>
            ) : (
              inpStr && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-[var(--text-muted)] mb-1">Input</div>
                  <pre className="max-h-40 overflow-auto rounded-md border border-[var(--border)] bg-[var(--bg-base)] p-2 font-mono text-[11px] text-[var(--text-secondary)] whitespace-pre-wrap">
                    {inpStr.slice(0, 4000)}
                  </pre>
                </div>
              )
            )}

            {outStr && (
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-[10px] uppercase tracking-wider text-[var(--text-muted)]">Output</span>
                  <button
                    type="button"
                    onClick={() => onCite(`${tool.name || 'Tool'} output`, `\`\`\`\n${outStr.slice(0, 1500)}\n\`\`\``)}
                    className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--accent)] hover:bg-[var(--accent-soft)] cursor-pointer"
                  >
                    <Quote className="w-3 h-3" />
                    Cite
                  </button>
                </div>
                <pre className="max-h-56 overflow-auto rounded-md border border-[var(--border)] bg-[var(--bg-base)] p-2 font-mono text-[11px] text-[var(--text-secondary)] whitespace-pre-wrap">
                  {outStr.slice(0, 6000)}
                </pre>
              </div>
            )}
          </div>
        )}
      </div>
    );
  },
);

const CheckpointCard: React.FC<{
  cp: TurnCheckpoint;
  onOpenDiff: (turnIndex: number) => void;
  onRevert: (turnIndex: number) => void;
  reverting: boolean;
}> = React.memo(({ cp, onOpenDiff, onRevert, reverting }) => {
  const [expanded, setExpanded] = useState(false);
  const files = cp.files ?? [];
  if (files.length === 0 && cp.totalAdd === 0 && cp.totalDel === 0) return null;

  return (
    <div
      data-testid="checkpoint-card"
      className="rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] px-3 py-2 text-xs shadow-xs"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="inline-flex items-center gap-2 text-left cursor-pointer"
        >
          {expanded ? (
            <ChevronDown className="w-3.5 h-3.5 text-[var(--text-muted)]" />
          ) : (
            <ChevronRight className="w-3.5 h-3.5 text-[var(--text-muted)]" />
          )}
          <FileDiff className="w-3.5 h-3.5 text-[var(--accent)]" />
          <span className="font-medium text-[var(--text-primary)]">
            {files.length} changed {files.length === 1 ? 'file' : 'files'}
          </span>
          <span className="font-mono text-[var(--diff-add-fg)]">+{cp.totalAdd}</span>
          <span className="font-mono text-[var(--diff-del-fg)]">-{cp.totalDel}</span>
          {cp.skippedLarge ? (
            <span className="text-[10.5px] text-[var(--text-muted)]">({cp.skippedLarge} large skipped)</span>
          ) : null}
        </button>

        <div className="flex items-center gap-1.5">
          <button
            type="button"
            data-testid="checkpoint-open-diff-btn"
            onClick={() => onOpenDiff(cp.turnIndex)}
            className="rounded-md border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            Open diff
          </button>
          <button
            type="button"
            data-testid="checkpoint-revert-btn"
            disabled={reverting}
            onClick={() => onRevert(cp.turnIndex)}
            className="inline-flex items-center gap-1 rounded-md border border-[var(--danger)]/40 px-2 py-0.5 text-[11px] text-[var(--danger)] hover:bg-[var(--danger)]/10 disabled:opacity-40 cursor-pointer"
          >
            <RotateCcw className="w-3 h-3" />
            Revert
          </button>
        </div>
      </div>

      {expanded && files.length > 0 && (
        <div className="mt-2 border-t border-[var(--border)] pt-1.5 space-y-1">
          {files.map((f) => (
            <div key={f.path} className="flex items-center justify-between gap-2 font-mono text-[11px]">
              <div className="flex items-center gap-2 min-w-0">
                <span
                  className={`w-4 text-center rounded text-[10px] font-semibold ${
                    f.status === 'A'
                      ? 'bg-[var(--diff-add-bg)] text-[var(--diff-add-fg)]'
                      : f.status === 'D'
                        ? 'bg-[var(--diff-del-bg)] text-[var(--diff-del-fg)]'
                        : 'bg-[var(--accent-soft)] text-[var(--accent)]'
                  }`}
                >
                  {f.status || 'M'}
                </span>
                <span className="truncate text-[var(--text-secondary)]">{f.path}</span>
              </div>
              <div className="shrink-0 flex items-center gap-1.5">
                {f.additions > 0 && <span className="text-[var(--diff-add-fg)]">+{f.additions}</span>}
                {f.deletions > 0 && <span className="text-[var(--diff-del-fg)]">-{f.deletions}</span>}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
});

interface TurnGroup {
  id: string;
  userItem?: TranscriptItem;
  workItems: TranscriptItem[];
  agentItems: TranscriptItem[];
  noticeItems: TranscriptItem[];
  turnItem?: TranscriptItem;
}

interface Props {
  hostId: string;
  session: SessionSummary;
  detail: SessionDetail | null;
  items: TranscriptItem[];
  findQuery: string;
  activeMatchIdx: number;
  onMatchCountChange?: (count: number) => void;
}

export const Timeline: React.FC<Props> = ({
  hostId,
  session,
  detail,
  items,
  findQuery,
  activeMatchIdx,
  onMatchCountChange,
}) => {
  const {
    settings,
    callHost,
    openRightTab,
    addComposerChip,
    setComposerPrefill,
    selectSession,
    setTerminalDrawerOpen,
    pushToast,
  } = useApp();

  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [expandedWorkGroups, setExpandedWorkGroups] = useState<Record<string, boolean>>({});
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [revertConfirmTurn, setRevertConfirmTurn] = useState<number | null>(null);
  const [reverting, setReverting] = useState(false);
  const [hoveredMini, setHoveredMini] = useState<string | null>(null);

  // Group flat transcript items into logical turns
  const groups = useMemo<TurnGroup[]>(() => {
    const out: TurnGroup[] = [];
    let cur: TurnGroup | null = null;
    const ensure = (id: string) => {
      if (!cur) {
        cur = { id, workItems: [], agentItems: [], noticeItems: [] };
        out.push(cur);
      }
      return cur;
    };

    for (const it of items) {
      if (it.kind === 'user' && it.state === 'queued') continue; // shown in Composer Action Deck
      if (it.kind === 'user') {
        cur = { id: it.id, userItem: it, workItems: [], agentItems: [], noticeItems: [] };
        out.push(cur);
        continue;
      }
      const g = ensure(`turn-${it.id}`);
      if (it.kind === 'agent' || it.kind === 'assistant') {
        g.agentItems.push(it);
      } else if (it.kind === 'turn') {
        g.turnItem = it;
        cur = null; // next item starts a new group
      } else if (it.kind === 'notice') {
        g.noticeItems.push(it);
      } else if (it.kind === 'perm') {
        // Pending permissions live in Composer Action Deck; resolved ones stay in work log
        if (it.perm?.chosen) g.workItems.push(it);
      } else {
        g.workItems.push(it);
      }
    }
    return out;
  }, [items]);

  // Search matches across items
  const matchedIds = useMemo(() => {
    const q = findQuery.trim().toLowerCase();
    if (!q) return [] as string[];
    const ids: string[] = [];
    for (const it of items) {
      const hay = `${it.text || ''} ${it.tool?.title || ''} ${it.tool?.name || ''}`.toLowerCase();
      if (hay.includes(q)) ids.push(it.id);
    }
    return ids;
  }, [items, findQuery]);

  useEffect(() => {
    onMatchCountChange?.(matchedIds.length);
  }, [matchedIds.length, onMatchCountChange]);

  useEffect(() => {
    if (matchedIds.length === 0) return;
    const targetId = matchedIds[activeMatchIdx % matchedIds.length];
    const el = document.getElementById(`msg-${targetId}`);
    el?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }, [matchedIds, activeMatchIdx]);

  // Sticky auto-scroll to bottom when streaming
  const scrollToBottom = useCallback((smooth = false) => {
    const el = scrollRef.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' });
  }, []);

  useEffect(() => {
    if (atBottom) scrollToBottom(false);
  }, [items, atBottom, scrollToBottom]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const dist = el.scrollHeight - el.scrollTop - el.clientHeight;
    setAtBottom(dist < 64);
  };

  // Detect stalled turn (> 10 minutes with no updates while running)
  const isRunning = session.state === 'running' || session.state === 'waiting';
  const lastActivityTs = useMemo(() => {
    const last = items[items.length - 1];
    return last?.ts || session.turnStart || session.updatedAt || Date.now();
  }, [items, session.turnStart, session.updatedAt]);
  const isStalled = isRunning && Date.now() - lastActivityTs > 10 * 60 * 1000;

  // Actions
  const handleCopy = (id: string, text: string) => {
    void navigator.clipboard.writeText(text);
    setCopiedId(id);
    setTimeout(() => setCopiedId((cur) => (cur === id ? null : cur)), 1500);
  };

  const handleFork = async (itemId: string) => {
    try {
      const res = await callHost<{ session?: SessionSummary; prefill?: string }>(hostId, 'session.fork', {
        id: session.id,
        itemId,
      });
      if (res?.session) {
        selectSession(hostId, res.session.id);
        if (res.prefill) setComposerPrefill(res.prefill);
        pushToast({ text: 'Forked thread from message', level: 'success' });
      }
    } catch (e) {
      pushToast({ text: `Fork failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleRewind = async (itemId: string) => {
    if (!window.confirm('Rewind conversation back to this message? Later messages will be removed.')) return;
    try {
      const res = await callHost<{ prefill?: string }>(hostId, 'session.rewind', {
        id: session.id,
        itemId,
        conversationOnly: false,
      });
      if (res?.prefill) setComposerPrefill(res.prefill);
      pushToast({ text: 'Rewound thread', level: 'success' });
    } catch (e) {
      pushToast({ text: `Rewind failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleConfirmRevert = async () => {
    if (revertConfirmTurn === null) return;
    setReverting(true);
    try {
      await callHost(hostId, 'session.checkpointRevert', {
        id: session.id,
        turnIndex: revertConfirmTurn,
        toPre: true,
      });
      pushToast({ text: `Reverted workspace to before turn #${revertConfirmTurn}`, level: 'success' });
      setRevertConfirmTurn(null);
    } catch (e) {
      pushToast({ text: `Checkpoint revert failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setReverting(false);
    }
  };

  const filterWorkItems = (work: TranscriptItem[]) => {
    if (settings.toolVerbosity === 'zen' || settings.toolVerbosity === 'hideAll') {
      // Keep errors even when hiding tools
      return work.filter((w) => w.kind === 'tool' && (w.tool?.status === 'failed' || w.tool?.status === 'error'));
    }
    if (settings.toolVerbosity === 'hideSuccessful') {
      return work.filter((w) => {
        if (w.kind === 'tool') return w.tool?.status !== 'completed' && w.tool?.status !== 'done';
        return true;
      });
    }
    return work;
  };

  const checkpointByTurn = useMemo(() => {
    const map = new Map<number, TurnCheckpoint>();
    for (const cp of detail?.checkpoints ?? []) {
      map.set(cp.turnIndex, cp);
    }
    return map;
  }, [detail?.checkpoints]);

  const handleCite = useCallback(
    (label: string, content: string) => {
      addComposerChip({ kind: 'citation', label, content });
      pushToast({ text: 'Added to composer', level: 'info' });
    },
    [addComposerChip, pushToast],
  );

  return (
    <div className="relative flex-1 min-h-0 flex">
      {/* Main Scrollable Thread Area */}
      <div
        ref={scrollRef}
        onScroll={onScroll}
        data-testid="chat-timeline"
        className="flex-1 overflow-y-auto px-4 py-4"
      >
        <div className="mx-auto space-y-5" style={{ maxWidth: settings.chatMaxWidth }}>
          {/* Stalled Turn Warning */}
          {isStalled && (
            <div className="flex items-center justify-between gap-3 rounded-xl border border-[var(--warning)]/50 bg-[var(--warning)]/10 px-3.5 py-2.5 text-xs">
              <div className="flex items-center gap-2 text-[var(--warning)]">
                <AlertTriangle className="w-4 h-4 shrink-0" />
                <span>This turn has been silent for over 10 minutes.</span>
              </div>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => setTerminalDrawerOpen(true)}
                  className="rounded-md border border-[var(--border)] px-2 py-1 text-[11px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  Open Terminal
                </button>
                <button
                  type="button"
                  onClick={() => void callHost(hostId, 'session.cancel', { id: session.id })}
                  className="rounded-md bg-[var(--danger)] px-2 py-1 text-[11px] font-semibold text-white cursor-pointer"
                >
                  Cancel Turn
                </button>
              </div>
            </div>
          )}

          {groups.length === 0 && !isRunning && (
            <div className="py-16 text-center text-sm text-[var(--text-muted)]">
              No messages yet. Type a prompt below or run <code className="font-mono">!command</code>.
            </div>
          )}

          {groups.map((g, gi) => {
            const isLast = gi === groups.length - 1;
            const visibleWork = filterWorkItems(g.workItems);
            const hasCompletedTurn = Boolean(g.turnItem);
            const defaultExpanded = !hasCompletedTurn || settings.toolVerbosity === 'showAll';
            const workExpanded = expandedWorkGroups[g.id] ?? (hasCompletedTurn ? false : defaultExpanded);

            const turnCp =
              g.turnItem?.turn?.checkpoint ??
              (g.turnItem?.turn?.turnIndex ? checkpointByTurn.get(g.turnItem.turn.turnIndex) : undefined);

            const turnDur = g.turnItem?.turn?.durationMs ?? 0;
            const wasStopped = g.turnItem?.turn?.stopReason === 'cancelled';
            const turnError = g.turnItem?.turn?.error;

            return (
              <div key={g.id} id={`turn-group-${g.id}`} className="space-y-3">
                {/* User Message Bubble */}
                {g.userItem && (
                  <div
                    id={`msg-${g.userItem.id}`}
                    data-testid="user-message"
                    className={`group flex justify-end ${
                      matchedIds.includes(g.userItem.id) ? 'ring-2 ring-[var(--warning)] rounded-2xl' : ''
                    }`}
                  >
                    <div className="max-w-[82%] space-y-1">
                      <div className="rounded-2xl rounded-tr-sm border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3.5 py-2.5 text-[13.5px] leading-relaxed text-[var(--text-primary)] whitespace-pre-wrap break-words">
                        {g.userItem.text}
                        {g.userItem.blocks && g.userItem.blocks.length > 0 && (
                          <div className="mt-2 flex flex-wrap gap-1.5">
                            {g.userItem.blocks.map((b, bi) => (
                              <span
                                key={bi}
                                className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)]"
                              >
                                {b.name || b.type}
                              </span>
                            ))}
                          </div>
                        )}
                      </div>
                      <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity pr-1">
                        <button
                          type="button"
                          onClick={() => handleCopy(g.userItem!.id, g.userItem!.text || '')}
                          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                        >
                          {copiedId === g.userItem.id ? <Check className="w-3 h-3 text-[var(--success)]" /> : <Copy className="w-3 h-3" />}
                          Copy
                        </button>
                        <button
                          type="button"
                          onClick={() => void handleFork(g.userItem!.id)}
                          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                        >
                          <GitFork className="w-3 h-3" />
                          Fork
                        </button>
                        <button
                          type="button"
                          onClick={() => void handleRewind(g.userItem!.id)}
                          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--danger)] cursor-pointer"
                        >
                          <RotateCcw className="w-3 h-3" />
                          Rewind
                        </button>
                      </div>
                    </div>
                  </div>
                )}

                {/* Collapsible Work-Log Summary (thinking + tools + plans) */}
                {visibleWork.length > 0 && (
                  <div className="space-y-1.5">
                    {hasCompletedTurn && (
                      <button
                        type="button"
                        onClick={() =>
                          setExpandedWorkGroups((prev) => ({ ...prev, [g.id]: !workExpanded }))
                        }
                        className="inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                      >
                        {workExpanded ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}
                        <span>
                          {wasStopped
                            ? `You stopped after ${formatDuration(turnDur)}`
                            : `Worked for ${formatDuration(turnDur)}`}
                        </span>
                        <span className="text-[11px] opacity-70">
                          ({visibleWork.length} {visibleWork.length === 1 ? 'step' : 'steps'})
                        </span>
                      </button>
                    )}

                    {(workExpanded || !hasCompletedTurn) && (
                      <div className="space-y-1.5 pl-1 border-l-2 border-[var(--border)] ml-1.5">
                        {visibleWork.map((w) => {
                          const hit = matchedIds.includes(w.id);
                          if (w.kind === 'thought') {
                            return (
                              <details
                                key={w.id}
                                id={`msg-${w.id}`}
                                className={`rounded-lg border border-[var(--border)] bg-[var(--bg-sidebar)]/70 px-2.5 py-1.5 text-xs ${
                                  hit ? 'ring-1 ring-[var(--warning)]' : ''
                                }`}
                              >
                                <summary className="flex items-center gap-1.5 cursor-pointer text-[var(--text-muted)] select-none">
                                  <Brain className="w-3.5 h-3.5 text-[var(--accent)]" />
                                  <span>Thinking</span>
                                </summary>
                                <div className="mt-1.5 text-[var(--text-secondary)] whitespace-pre-wrap text-[12px] leading-relaxed">
                                  {w.text}
                                </div>
                              </details>
                            );
                          }
                          if (w.kind === 'tool') {
                            return (
                              <div key={w.id} id={`msg-${w.id}`} className={hit ? 'ring-1 ring-[var(--warning)] rounded-lg' : ''}>
                                <ToolRow item={w} onCite={handleCite} />
                              </div>
                            );
                          }
                          if (w.kind === 'plan' && Array.isArray(w.plan)) {
                            const entries = w.plan as Array<{ content?: string; status?: string }>;
                            return (
                              <div
                                key={w.id}
                                id={`msg-${w.id}`}
                                className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 text-xs space-y-1"
                              >
                                <div className="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                                  Plan
                                </div>
                                {entries.map((e, ei) => (
                                  <div key={ei} className="flex items-center gap-2 text-[var(--text-secondary)]">
                                    <span
                                      className={`w-2 h-2 rounded-full shrink-0 ${
                                        e.status === 'completed'
                                          ? 'bg-[var(--success)]'
                                          : e.status === 'in_progress'
                                            ? 'bg-[var(--accent)]'
                                            : 'bg-[var(--border-strong)]'
                                      }`}
                                    />
                                    <span className={e.status === 'completed' ? 'line-through opacity-70' : ''}>
                                      {e.content}
                                    </span>
                                  </div>
                                ))}
                              </div>
                            );
                          }
                          if (w.kind === 'perm' && w.perm) {
                            return (
                              <div
                                key={w.id}
                                className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--bg-sidebar)] px-2 py-1 text-[11px] text-[var(--text-muted)]"
                              >
                                <ShieldCheck className="w-3 h-3 text-[var(--success)]" />
                                <span>
                                  {w.perm.tool || w.perm.title || 'Permission'} → <strong>{w.perm.chosen}</strong>
                                </span>
                              </div>
                            );
                          }
                          return null;
                        })}
                      </div>
                    )}
                  </div>
                )}

                {/* Assistant Markdown Messages */}
                {g.agentItems.map((a) => (
                  <div
                    key={a.id}
                    id={`msg-${a.id}`}
                    data-testid="agent-message"
                    className={`rounded-xl px-1 py-0.5 text-[var(--text-primary)] ${
                      matchedIds.includes(a.id) ? 'ring-2 ring-[var(--warning)] bg-[var(--warning)]/5 px-2' : ''
                    }`}
                  >
                    {a.text && <MarkdownRenderer text={a.text} citable />}
                  </div>
                ))}

                {/* Notices */}
                {g.noticeItems.map((n) => (
                  <div
                    key={n.id}
                    id={`msg-${n.id}`}
                    className={`flex items-start gap-2 rounded-lg border px-3 py-2 text-xs ${
                      n.level === 'error'
                        ? 'border-[var(--danger)]/40 bg-[var(--danger)]/10 text-[var(--danger)]'
                        : 'border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-secondary)]'
                    }`}
                  >
                    <AlertCircle className="w-3.5 h-3.5 shrink-0 mt-0.5" />
                    <span className="whitespace-pre-wrap">{n.text}</span>
                  </div>
                ))}

                {/* Turn Error */}
                {turnError && (
                  <div className="flex items-start gap-2 rounded-lg border border-[var(--danger)]/40 bg-[var(--danger)]/10 px-3 py-2 text-xs text-[var(--danger)]">
                    <AlertCircle className="w-3.5 h-3.5 shrink-0 mt-0.5" />
                    <span>{turnError}</span>
                  </div>
                )}

                {/* Per-Turn Checkpoint Changed Files Card */}
                {turnCp && (
                  <CheckpointCard
                    cp={turnCp}
                    reverting={reverting}
                    onOpenDiff={(turnIndex) =>
                      openRightTab({
                        id: `diff-turn-${turnIndex}`,
                        kind: 'diff',
                        title: `Turn #${turnIndex} Diff`,
                        diffScope: 'turn',
                        turnIndex,
                      })
                    }
                    onRevert={(turnIndex) => setRevertConfirmTurn(turnIndex)}
                  />
                )}

                {/* Live Working Timer on active last turn */}
                {isLast && isRunning && (
                  <div className="pt-1">
                    <WorkingTimer startMs={session.turnStart || lastActivityTs} />
                  </div>
                )}
              </div>
            );
          })}

          {groups.length === 0 && isRunning && (
            <div className="pt-2">
              <WorkingTimer startMs={session.turnStart || Date.now()} />
            </div>
          )}
        </div>
      </div>

      {/* Hover-Fisheye Turn Minimap */}
      {groups.length > 1 && (
        <div
          aria-label="Conversation minimap"
          className="hidden md:flex flex-col items-end justify-center gap-1.5 pr-2 py-4 select-none"
        >
          {groups.map((g, idx) => {
            const hasCp = Boolean(g.turnItem?.turn?.checkpoint?.files?.length);
            const isHovered = hoveredMini === g.id;
            const preview = (g.userItem?.text || `Turn ${idx + 1}`).slice(0, 72);
            return (
              <div key={g.id} className="relative flex items-center">
                {isHovered && (
                  <div className="absolute right-6 z-20 w-56 truncate rounded-lg border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-2.5 py-1 text-[11px] text-[var(--text-primary)] shadow-lg pointer-events-none">
                    <span className="font-mono text-[var(--text-muted)] mr-1.5">#{idx + 1}</span>
                    {preview}
                  </div>
                )}
                <button
                  type="button"
                  onMouseEnter={() => setHoveredMini(g.id)}
                  onMouseLeave={() => setHoveredMini(null)}
                  onClick={() => document.getElementById(`turn-group-${g.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}
                  className={`h-1.5 rounded-full transition-all cursor-pointer ${
                    isHovered
                      ? 'w-5 bg-[var(--accent)]'
                      : hasCp
                        ? 'w-3 bg-[var(--success)]/70 hover:bg-[var(--success)]'
                        : 'w-2 bg-[var(--border-strong)] hover:bg-[var(--text-secondary)]'
                  }`}
                />
              </div>
            );
          })}
        </div>
      )}

      {/* Floating Jump-to-Latest Button */}
      {!atBottom && (
        <button
          type="button"
          onClick={() => scrollToBottom(true)}
          className="absolute bottom-4 right-6 z-20 flex items-center gap-1.5 rounded-full border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-1.5 text-xs font-medium text-[var(--text-primary)] shadow-lg hover:bg-[var(--bg-hover)] cursor-pointer"
        >
          <ArrowDown className="w-3.5 h-3.5 text-[var(--accent)]" />
          Latest
        </button>
      )}

      {/* Checkpoint Revert Confirmation Dialog */}
      {revertConfirmTurn !== null && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-md rounded-2xl border border-[var(--danger)]/50 bg-[var(--bg-surface)] p-4 shadow-2xl">
            <div className="text-sm font-semibold text-[var(--danger)] mb-2">
              Revert Workspace to Turn #{revertConfirmTurn}?
            </div>
            <p className="text-xs text-[var(--text-secondary)] mb-4">
              This will restore tracked files to the exact snapshot captured right before Turn #{revertConfirmTurn}. A
              safety backup ref is saved first so nothing is permanently lost.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                disabled={reverting}
                onClick={() => setRevertConfirmTurn(null)}
                className="rounded-lg border border-[var(--border)] px-3 py-1.5 text-xs text-[var(--text-secondary)] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                data-testid="confirm-revert-btn"
                disabled={reverting}
                onClick={() => void handleConfirmRevert()}
                className="rounded-lg bg-[var(--danger)] px-3 py-1.5 text-xs font-semibold text-white cursor-pointer"
              >
                {reverting ? 'Reverting...' : 'Revert Workspace'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
