import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Columns2,
  Eye,
  EyeOff,
  FileCode2,
  MessageSquarePlus,
  RefreshCw,
  Rows3,
  WrapText,
} from 'lucide-react';
import type { DiffResult, FileDiff } from '@agentc/protocol';
import { useApp, type RightTab } from '../../state/appStore.js';
import { isTestFile, parseUnifiedPatch, toSplitRows } from '../../utils/diffParser.js';
import { resolveHostScopedCwd } from '../../utils/multiHost.js';

const SCOPES: Array<{ id: NonNullable<RightTab['diffScope']>; label: string }> = [
  { id: 'uncommitted', label: 'Uncommitted' },
  { id: 'latest', label: 'Last Turn' },
  { id: 'session', label: 'Full Session' },
  { id: 'branch', label: 'Branch' },
  { id: 'staged', label: 'Staged' },
];

export const DiffPanel: React.FC<{ tab: RightTab }> = ({ tab }) => {
  const {
    activeHost,
    activeSession,
    draft,
    settings,
    updateSettings,
    callHost,
    addComposerChip,
    pushToast,
  } = useApp();

  const hostId = activeHost?.creds.id ?? '';
  const cwd = resolveHostScopedCwd({
    hostId,
    activeSession,
    activeSessionHostId: hostId,
    draft,
    fallback: '',
  });

  const [scope, setScope] = useState<NonNullable<RightTab['diffScope']>>(tab.diffScope || 'uncommitted');
  const [turnIndex, setTurnIndex] = useState<number | undefined>(tab.turnIndex);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const [selectedIdx, setSelectedIdx] = useState(0);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [viewed, setViewed] = useState<Record<string, boolean>>({});
  const [commentTarget, setCommentTarget] = useState<{ path: string; line: number; text: string } | null>(null);
  const [commentBody, setCommentBody] = useState('');
  const [showFileTree, setShowFileTree] = useState(true);

  const panelRef = useRef<HTMLDivElement | null>(null);
  const reqSeqRef = useRef(0);

  useEffect(() => {
    setScope(tab.diffScope || 'uncommitted');
    setTurnIndex(tab.turnIndex);
  }, [tab.id, tab.diffScope, tab.turnIndex]);

  useEffect(() => {
    reqSeqRef.current++;
    setDiff(null);
    setViewed({});
    setCollapsed({});
    setCommentTarget(null);
    setCommentBody('');
    setSelectedIdx(0);
    setError('');
  }, [hostId, cwd, activeSession?.id]);

  const loadDiff = useCallback(async () => {
    if (!hostId || (!cwd && !activeSession)) return;
    const seq = ++reqSeqRef.current;
    setLoading(true);
    setError('');
    try {
      let res: DiffResult;
      if ((scope === 'turn' || scope === 'latest' || scope === 'session') && activeSession) {
        res = await callHost<DiffResult>(hostId, 'session.turnDiff', {
          id: activeSession.id,
          scope,
          turnIndex: turnIndex ?? 0,
        });
      } else {
        const gitScope = scope === 'staged' ? 'staged' : scope === 'branch' ? 'branch' : 'uncommitted';
        res = await callHost<DiffResult>(hostId, 'git.scopedDiff', {
          path: cwd,
          scope: gitScope,
        });
      }
      if (seq !== reqSeqRef.current) return;
      setDiff(res);
      setSelectedIdx(0);
    } catch (e) {
      if (seq !== reqSeqRef.current) return;
      setError((e as Error).message);
      setDiff(null);
    } finally {
      if (seq === reqSeqRef.current) {
        setLoading(false);
      }
    }
  }, [hostId, cwd, activeSession, scope, turnIndex, callHost]);

  useEffect(() => {
    void loadDiff();
  }, [loadDiff]);

  const visibleFiles: FileDiff[] = useMemo(() => {
    const raw = diff?.files ?? [];
    if (!settings.diffHideTests) return raw;
    return raw.filter((f) => !isTestFile(f.path));
  }, [diff, settings.diffHideTests]);

  const scrollToFile = useCallback((path: string) => {
    const el = document.getElementById(`diff-file-${encodeURIComponent(path)}`);
    el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, []);

  // Keyboard-first review shortcuts (j/k, n/N, v, c) when panel focused and not typing in an input
  const onPanelKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const tag = (e.target as HTMLElement)?.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
    if (visibleFiles.length === 0) return;

    if (e.key === 'j') {
      e.preventDefault();
      const next = Math.min(visibleFiles.length - 1, selectedIdx + 1);
      setSelectedIdx(next);
      scrollToFile(visibleFiles[next].path);
    } else if (e.key === 'k') {
      e.preventDefault();
      const next = Math.max(0, selectedIdx - 1);
      setSelectedIdx(next);
      scrollToFile(visibleFiles[next].path);
    } else if (e.key === 'v') {
      e.preventDefault();
      const f = visibleFiles[selectedIdx];
      if (!f) return;
      const nextViewed = !viewed[f.path];
      setViewed((prev) => ({ ...prev, [f.path]: nextViewed }));
      setCollapsed((prev) => ({ ...prev, [f.path]: nextViewed }));
    } else if (e.key === 'n' || e.key === 'N') {
      e.preventDefault();
      const dir = e.key === 'n' ? 1 : -1;
      for (let step = 1; step <= visibleFiles.length; step++) {
        const idx = (selectedIdx + dir * step + visibleFiles.length) % visibleFiles.length;
        if (!viewed[visibleFiles[idx].path]) {
          setSelectedIdx(idx);
          scrollToFile(visibleFiles[idx].path);
          break;
        }
      }
    } else if (e.key === 'c') {
      e.preventDefault();
      const f = visibleFiles[selectedIdx];
      if (f) {
        setCommentTarget({ path: f.path, line: 1, text: '' });
        setCommentBody('');
      }
    }
  };

  const submitComment = () => {
    if (!commentTarget || !commentBody.trim()) return;
    const loc = `${commentTarget.path}:${commentTarget.line}`;
    addComposerChip({
      kind: 'diff-comment',
      label: loc,
      content: `Review comment on \`${loc}\`${commentTarget.text ? `\n> ${commentTarget.text}` : ''}\n${commentBody.trim()}`,
    });
    setCommentTarget(null);
    setCommentBody('');
    pushToast({ text: `Review comment on ${loc} added to composer`, level: 'info' });
  };

  const toggleAllCollapsed = () => {
    const anyOpen = visibleFiles.some((f) => !collapsed[f.path]);
    const next: Record<string, boolean> = {};
    for (const f of visibleFiles) next[f.path] = anyOpen;
    setCollapsed(next);
  };

  return (
    <div
      ref={panelRef}
      tabIndex={0}
      onKeyDown={onPanelKeyDown}
      data-testid="diff-panel"
      className="flex-1 flex flex-col min-h-0 outline-none"
    >
      {/* Diff Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-3 py-1.5 text-xs">
        <div className="flex items-center gap-1 flex-wrap">
          {SCOPES.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => {
                setScope(s.id);
                setTurnIndex(undefined);
              }}
              className={`rounded-md px-2 py-0.5 text-[11px] transition cursor-pointer ${
                scope === s.id
                  ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
              }`}
            >
              {s.label}
            </button>
          ))}
          {scope === 'turn' && turnIndex !== undefined && (
            <span className="rounded-md bg-[var(--accent-soft)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent)]">
              Turn #{turnIndex}
            </span>
          )}
        </div>

        <div className="flex items-center gap-1">
          <button
            type="button"
            data-testid="diff-view-toggle"
            onClick={() =>
              updateSettings({ diffViewMode: settings.diffViewMode === 'split' ? 'stacked' : 'split' })
            }
            title={settings.diffViewMode === 'split' ? 'Switch to Stacked view' : 'Switch to Split view'}
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            {settings.diffViewMode === 'split' ? <Rows3 className="w-3 h-3" /> : <Columns2 className="w-3 h-3" />}
            <span>{settings.diffViewMode === 'split' ? 'Split' : 'Stacked'}</span>
          </button>

          <button
            type="button"
            onClick={() => updateSettings({ diffWrap: !settings.diffWrap })}
            title="Toggle line wrapping"
            className={`rounded-md border px-1.5 py-0.5 cursor-pointer ${
              settings.diffWrap
                ? 'border-[var(--accent)]/50 text-[var(--accent)] bg-[var(--accent-soft)]'
                : 'border-[var(--border)] text-[var(--text-muted)] hover:bg-[var(--bg-hover)]'
            }`}
          >
            <WrapText className="w-3.5 h-3.5" />
          </button>

          <button
            type="button"
            onClick={() => updateSettings({ diffHideTests: !settings.diffHideTests })}
            title="Hide test files"
            className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] cursor-pointer ${
              settings.diffHideTests
                ? 'border-[var(--accent)]/50 text-[var(--accent)] bg-[var(--accent-soft)]'
                : 'border-[var(--border)] text-[var(--text-muted)] hover:bg-[var(--bg-hover)]'
            }`}
          >
            {settings.diffHideTests ? <EyeOff className="w-3 h-3" /> : <Eye className="w-3 h-3" />}
            <span>Tests</span>
          </button>

          <button
            type="button"
            onClick={toggleAllCollapsed}
            className="rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            Fold all
          </button>

          <button
            type="button"
            onClick={() => setShowFileTree((v) => !v)}
            className="rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            Tree
          </button>

          <button
            type="button"
            onClick={() => void loadDiff()}
            title="Refresh diff"
            className="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      {/* Main Split: File Tree + Patch Viewport */}
      <div className="flex-1 flex min-h-0">
        {showFileTree && visibleFiles.length > 0 && (
          <div className="w-48 shrink-0 border-r border-[var(--border)] bg-[var(--bg-sidebar)]/50 overflow-y-auto p-1.5 space-y-0.5">
            <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)] flex justify-between">
              <span>{visibleFiles.length} files</span>
              <span className="font-mono">
                <span className="text-[var(--diff-add-fg)]">+{diff?.totalAdd ?? 0}</span>{' '}
                <span className="text-[var(--diff-del-fg)]">-{diff?.totalDel ?? 0}</span>
              </span>
            </div>
            {visibleFiles.map((f, idx) => (
              <button
                key={f.path}
                type="button"
                onClick={() => {
                  setSelectedIdx(idx);
                  setCollapsed((prev) => ({ ...prev, [f.path]: false }));
                  scrollToFile(f.path);
                }}
                className={`flex w-full items-center justify-between gap-1 rounded-md px-2 py-1 text-left text-[11px] font-mono cursor-pointer ${
                  idx === selectedIdx
                    ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]'
                    : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                } ${viewed[f.path] ? 'opacity-50' : ''}`}
              >
                <span className="truncate">{f.path.split('/').pop()}</span>
                <span className="shrink-0 text-[10px] flex items-center gap-1">
                  {viewed[f.path] && <Check className="w-3 h-3 text-[var(--success)]" />}
                  {f.additions > 0 && <span className="text-[var(--diff-add-fg)]">+{f.additions}</span>}
                  {f.deletions > 0 && <span className="text-[var(--diff-del-fg)]">-{f.deletions}</span>}
                </span>
              </button>
            ))}
          </div>
        )}

        <div className="flex-1 overflow-y-auto p-3 space-y-3">
          {error && (
            <div className="rounded-xl border border-[var(--danger)]/40 bg-[var(--danger)]/10 p-3 text-xs text-[var(--danger)]">
              {error}
            </div>
          )}

          {!loading && !error && visibleFiles.length === 0 && (
            <div className="py-12 text-center text-xs text-[var(--text-muted)]">
              No changes in this scope.
            </div>
          )}

          {visibleFiles.map((f, idx) => {
            const isFolded = Boolean(collapsed[f.path]);
            const isViewed = Boolean(viewed[f.path]);
            const parsed = parseUnifiedPatch(f.patch);
            const splitRows = settings.diffViewMode === 'split' ? toSplitRows(parsed) : [];

            return (
              <div
                key={f.path}
                id={`diff-file-${encodeURIComponent(f.path)}`}
                data-testid="diff-file-card"
                className={`rounded-xl border overflow-hidden ${
                  idx === selectedIdx ? 'border-[var(--accent)]/60' : 'border-[var(--border)]'
                } bg-[var(--bg-surface)]`}
              >
                {/* File Header */}
                <div className="flex items-center justify-between gap-2 bg-[var(--bg-elevated)] px-3 py-1.5 text-xs border-b border-[var(--border)]">
                  <button
                    type="button"
                    onClick={() => setCollapsed((prev) => ({ ...prev, [f.path]: !isFolded }))}
                    className="flex items-center gap-1.5 min-w-0 font-mono text-[11.5px] text-[var(--text-primary)] cursor-pointer"
                  >
                    {isFolded ? <ChevronRight className="w-3.5 h-3.5 shrink-0" /> : <ChevronDown className="w-3.5 h-3.5 shrink-0" />}
                    <FileCode2 className="w-3.5 h-3.5 shrink-0 text-[var(--accent)]" />
                    <span className="truncate">{f.path}</span>
                  </button>
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="font-mono text-[11px] text-[var(--diff-add-fg)]">+{f.additions}</span>
                    <span className="font-mono text-[11px] text-[var(--diff-del-fg)]">-{f.deletions}</span>
                    <button
                      type="button"
                      data-testid="diff-comment-file-btn"
                      onClick={() => {
                        setCommentTarget({ path: f.path, line: 1, text: '' });
                        setCommentBody('');
                      }}
                      title="Add review comment to composer (c)"
                      className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--accent)] cursor-pointer"
                    >
                      <MessageSquarePlus className="w-3.5 h-3.5" />
                    </button>
                    <label className="inline-flex items-center gap-1 text-[11px] text-[var(--text-muted)] cursor-pointer select-none">
                      <input
                        type="checkbox"
                        checked={isViewed}
                        onChange={(e) => {
                          setViewed((prev) => ({ ...prev, [f.path]: e.target.checked }));
                          if (e.target.checked) setCollapsed((prev) => ({ ...prev, [f.path]: true }));
                        }}
                      />
                      Viewed
                    </label>
                  </div>
                </div>

                {/* Inline Review Comment Box */}
                {commentTarget?.path === f.path && (
                  <div className="border-b border-[var(--border)] bg-[var(--bg-sidebar)] p-2.5 space-y-2">
                    <div className="text-[11px] text-[var(--accent)] font-mono">
                      Comment on {f.path}:{commentTarget.line}
                    </div>
                    <textarea
                      autoFocus
                      data-testid="diff-comment-input"
                      value={commentBody}
                      onChange={(e) => setCommentBody(e.target.value)}
                      rows={2}
                      placeholder="Write review feedback (added as a context chip to main composer)..."
                      className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-2 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                    />
                    <div className="flex justify-end gap-1.5">
                      <button
                        type="button"
                        onClick={() => setCommentTarget(null)}
                        className="rounded-md border border-[var(--border)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] cursor-pointer"
                      >
                        Cancel
                      </button>
                      <button
                        type="button"
                        data-testid="diff-comment-submit"
                        disabled={!commentBody.trim()}
                        onClick={submitComment}
                        className="rounded-md bg-[var(--accent)] px-2.5 py-1 text-[11px] font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                      >
                        Add to Composer
                      </button>
                    </div>
                  </div>
                )}

                {/* Patch Content */}
                {!isFolded && (
                  <div className="overflow-x-auto font-mono text-[11.5px] leading-[1.5]">
                    {f.binary ? (
                      <div className="p-3 text-xs text-[var(--text-muted)]">Binary file changed</div>
                    ) : parsed.length === 0 ? (
                      <pre className="p-3 text-[var(--text-secondary)] whitespace-pre-wrap">{f.patch || '(empty diff)'}</pre>
                    ) : settings.diffViewMode === 'split' ? (
                      <table className="w-full border-collapse table-fixed">
                        <tbody>
                          {splitRows.map((row, ri) => (
                            <tr key={ri} className="border-b border-[var(--border)]/30">
                              {/* Left (Old) */}
                              <td className="w-10 select-none text-right pr-2 text-[10px] text-[var(--text-muted)] bg-[var(--bg-sidebar)]/40">
                                {row.left?.oldNo ?? ''}
                              </td>
                              <td
                                onClick={() => {
                                  if (row.left && row.left.oldNo) {
                                    setCommentTarget({ path: f.path, line: row.left.oldNo, text: row.left.text });
                                    setCommentBody('');
                                  }
                                }}
                                title="Click line to add inline review comment"
                                className={`w-1/2 px-2 py-0.5 align-top border-r border-[var(--border)] cursor-pointer ${
                                  settings.diffWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'
                                } ${
                                  row.left?.type === 'del'
                                    ? 'bg-[var(--diff-del-bg)] text-[var(--diff-del-fg)]'
                                    : row.left?.type === 'hunk'
                                      ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
                                      : 'text-[var(--text-secondary)]'
                                }`}
                              >
                                {row.left?.text ?? ''}
                              </td>
                              {/* Right (New) */}
                              <td className="w-10 select-none text-right pr-2 text-[10px] text-[var(--text-muted)] bg-[var(--bg-sidebar)]/40">
                                {row.right?.newNo ?? ''}
                              </td>
                              <td
                                onClick={() => {
                                  if (row.right && row.right.newNo) {
                                    setCommentTarget({ path: f.path, line: row.right.newNo, text: row.right.text });
                                    setCommentBody('');
                                  }
                                }}
                                title="Click line to add inline review comment"
                                className={`w-1/2 px-2 py-0.5 align-top cursor-pointer ${
                                  settings.diffWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'
                                } ${
                                  row.right?.type === 'add'
                                    ? 'bg-[var(--diff-add-bg)] text-[var(--diff-add-fg)]'
                                    : row.right?.type === 'hunk'
                                      ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
                                      : 'text-[var(--text-secondary)]'
                                }`}
                              >
                                {row.right?.text ?? ''}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    ) : (
                      <div>
                        {parsed.map((ln, li) => (
                          <div
                            key={li}
                            className={`group flex items-start ${
                              ln.type === 'add'
                                ? 'bg-[var(--diff-add-bg)] text-[var(--diff-add-fg)]'
                                : ln.type === 'del'
                                  ? 'bg-[var(--diff-del-bg)] text-[var(--diff-del-fg)]'
                                  : ln.type === 'hunk'
                                    ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
                                    : 'text-[var(--text-secondary)]'
                            }`}
                          >
                            <span className="w-10 shrink-0 select-none text-right pr-2 text-[10px] text-[var(--text-muted)]">
                              {ln.oldNo ?? ''}
                            </span>
                            <span className="w-10 shrink-0 select-none text-right pr-2 text-[10px] text-[var(--text-muted)]">
                              {ln.newNo ?? ''}
                            </span>
                            <button
                              type="button"
                              onClick={() => {
                                setCommentTarget({
                                  path: f.path,
                                  line: ln.newNo ?? ln.oldNo ?? 1,
                                  text: ln.text,
                                });
                                setCommentBody('');
                              }}
                              title="Comment on this line"
                              className="w-5 shrink-0 opacity-0 group-hover:opacity-100 text-[var(--accent)] text-center cursor-pointer"
                            >
                              +
                            </button>
                            <span
                              className={`flex-1 pr-3 py-0.5 ${
                                settings.diffWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'
                              }`}
                            >
                              {ln.type === 'add' ? '+' : ln.type === 'del' ? '-' : ln.type === 'ctx' ? ' ' : ''}
                              {ln.text}
                            </span>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {/* Keyboard review hint footer */}
      <div className="border-t border-[var(--border)] bg-[var(--bg-sidebar)] px-3 py-1 text-[10.5px] text-[var(--text-muted)] flex justify-between">
        <span>
          <kbd className="font-mono">j/k</kbd> files · <kbd className="font-mono">n/N</kbd> unviewed ·{' '}
          <kbd className="font-mono">v</kbd> mark viewed · <kbd className="font-mono">c</kbd> comment
        </span>
        <span>{diff?.baseRef ? `${diff.baseRef}…${diff.headRef || 'HEAD'}` : scope}</span>
      </div>
    </div>
  );
};
