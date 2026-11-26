import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  Archive,
  Download,
  FolderGit2,
  GitBranch,
  GitCommit,
  GitMerge,
  History,
  Minus,
  Plus,
  RefreshCw,
  RotateCcw,
  Sparkles,
  Trash2,
  Upload,
} from 'lucide-react';
import type {
  GitBranch as GitBranchInfo,
  GitStatus,
  StashEntry,
  WorktreeInfo,
} from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';
import { confirmAction } from '../shell/ConfirmDialog.js';
import { resolveHostScopedCwd } from '../../utils/multiHost.js';

interface CommitEntry {
  sha: string;
  short: string;
  subject: string;
  author: string;
  ts: number;
  files?: number;
  add?: number;
  del?: number;
}

type SubTab = 'changes' | 'branches' | 'worktrees' | 'stashes' | 'history';

export const SourceControlPanel: React.FC = () => {
  const { activeHost, activeSession, draft, callHost, openDraft, openRightTab, pushToast } = useApp();
  const hostId = activeHost?.creds.id ?? '';
  const cwd = resolveHostScopedCwd({
    hostId,
    activeSession,
    activeSessionHostId: hostId,
    draft,
    fallback: '',
  });

  const [tab, setTab] = useState<SubTab>('changes');
  const [status, setStatus] = useState<GitStatus | null>(null);
  const [branches, setBranches] = useState<GitBranchInfo[]>([]);
  const [worktrees, setWorktrees] = useState<WorktreeInfo[]>([]);
  const [stashes, setStashes] = useState<StashEntry[]>([]);
  const [commits, setCommits] = useState<CommitEntry[]>([]);
  const [selectedCommitDiff, setSelectedCommitDiff] = useState<string>('');
  const [loading, setLoading] = useState(false);

  const [commitMsg, setCommitMsg] = useState('');
  const [amend, setAmend] = useState(false);
  const [newBranch, setNewBranch] = useState('');
  const [newWtBranch, setNewWtBranch] = useState('');
  const [stashMsg, setStashMsg] = useState('');
  const reqSeqRef = useRef(0);

  const refreshAll = useCallback(
    async (force = false) => {
      if (!hostId || !cwd) return;
      const seq = ++reqSeqRef.current;
      setLoading(true);
      try {
        const st = await callHost<GitStatus>(hostId, 'git.status', { path: cwd, force });
        if (seq !== reqSeqRef.current) return;
        setStatus(st);
        if (st.isRepo) {
          const [br, wt, sl, lg] = await Promise.all([
            callHost<GitBranchInfo[]>(hostId, 'git.branches', { path: cwd }).catch(() => []),
            callHost<WorktreeInfo[]>(hostId, 'git.worktrees.list', { path: cwd }).catch(() => []),
            callHost<StashEntry[]>(hostId, 'git.stash.list', { path: cwd }).catch(() => []),
            callHost<{ commits?: CommitEntry[] }>(hostId, 'git.log', { path: cwd, limit: 30 }).catch(() => ({
              commits: [],
            })),
          ]);
          if (seq !== reqSeqRef.current) return;
          setBranches(Array.isArray(br) ? br : []);
          setWorktrees(Array.isArray(wt) ? wt : []);
          setStashes(Array.isArray(sl) ? sl : []);
          setCommits(Array.isArray(lg?.commits) ? lg.commits : []);
        } else {
          setBranches([]);
          setWorktrees([]);
          setStashes([]);
          setCommits([]);
          setSelectedCommitDiff('');
        }
      } catch (e) {
        if (seq !== reqSeqRef.current) return;
        setStatus(null);
        setBranches([]);
        setWorktrees([]);
        setStashes([]);
        setCommits([]);
        pushToast({ text: `Git status failed: ${(e as Error).message}`, level: 'error' });
      } finally {
        if (seq === reqSeqRef.current) {
          setLoading(false);
        }
      }
    },
    [hostId, cwd, callHost, pushToast],
  );

  useEffect(() => {
    reqSeqRef.current++;
    setStatus(null);
    setBranches([]);
    setWorktrees([]);
    setStashes([]);
    setCommits([]);
    setSelectedCommitDiff('');
    void refreshAll(false);
  }, [hostId, cwd, refreshAll]);

  const runGit = async (method: string, params: Record<string, unknown>, okMsg?: string) => {
    try {
      await callHost(hostId, method, { path: cwd, ...params });
      if (okMsg) pushToast({ text: okMsg, level: 'success' });
      await refreshAll(true);
    } catch (e) {
      pushToast({ text: (e as Error).message, level: 'error' });
    }
  };

  if (!cwd) {
    return (
      <div className="flex-1 flex items-center justify-center p-4 text-xs text-[var(--text-muted)]">
        Select a project or thread to inspect Git status.
      </div>
    );
  }

  const staged = (status?.files ?? []).filter((f) => f.staged);
  const unstaged = (status?.files ?? []).filter((f) => f.unstaged || f.untracked);

  return (
    <div data-testid="source-control-panel" className="flex-1 flex flex-col min-h-0 text-xs">
      {/* Header + Subtabs */}
      <div className="border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-2.5 py-1.5 flex flex-wrap items-center justify-between gap-1.5">
        <div className="flex flex-wrap items-center gap-1">
          {(
            [
              ['changes', `Changes (${status?.files?.length ?? 0})`],
              ['branches', `Branches (${branches.filter((b) => !b.remote).length})`],
              ['worktrees', `Worktrees (${worktrees.length})`],
              ['stashes', `Stashes (${stashes.length})`],
              ['history', 'History'],
            ] as Array<[SubTab, string]>
          ).map(([id, label]) => (
            <button
              key={id}
              type="button"
              onClick={() => setTab(id)}
              className={`rounded-md px-2 py-0.5 text-[11px] cursor-pointer ${
                tab === id
                  ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => void runGit('git.fetch', {}, 'Fetched remote')}
            title="Fetch"
            className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <Download className="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            onClick={() => void runGit('git.pull', { rebase: false }, 'Pulled latest')}
            title="Pull"
            className="rounded px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            Pull
          </button>
          <button
            type="button"
            onClick={() => {
              void (async () => {
                const allowDefault = status?.isDefaultBranch
                  ? await confirmAction({
                      title: 'Push to Default Branch',
                      message: `Push commits directly to "${status.branch}"?`,
                      confirmLabel: 'Push',
                      danger: false,
                    })
                  : false;
                if (status?.isDefaultBranch && !allowDefault) return;
                await runGit('git.push', { setUpstream: true, allowDefault }, 'Pushed branch');
              })();
            }}
            title="Push"
            className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            <Upload className="w-3 h-3" />
            Push
          </button>
          <button
            type="button"
            onClick={() => void refreshAll(true)}
            className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      {!status?.isRepo ? (
        <div className="flex-1 flex items-center justify-center p-6 text-[var(--text-muted)]">
          {cwd} is not a Git repository.
        </div>
      ) : (
        <div className="flex-1 overflow-y-auto p-3 space-y-3">
          {/* CHANGES TAB */}
          {tab === 'changes' && (
            <>
              {/* Commit Box */}
              <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-2.5 space-y-2">
                <textarea
                  data-testid="sc-commit-message"
                  value={commitMsg}
                  onChange={(e) => setCommitMsg(e.target.value)}
                  rows={2}
                  placeholder="Commit message..."
                  className="w-full resize-none rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-2 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                />
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={async () => {
                        try {
                          const r = await callHost<{ message: string }>(hostId, 'git.commitMessage', { path: cwd });
                          if (r?.message) setCommitMsg(r.message);
                        } catch (e) {
                          pushToast({ text: (e as Error).message, level: 'error' });
                        }
                      }}
                      className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--accent)] hover:bg-[var(--accent-soft)] cursor-pointer"
                    >
                      <Sparkles className="w-3 h-3" />
                      Suggest
                    </button>
                    <label className="inline-flex items-center gap-1 text-[11px] text-[var(--text-muted)]">
                      <input type="checkbox" checked={amend} onChange={(e) => setAmend(e.target.checked)} />
                      Amend
                    </label>
                  </div>
                  <button
                    type="button"
                    data-testid="sc-commit-btn"
                    disabled={!commitMsg.trim() && !amend}
                    onClick={async () => {
                      await runGit(
                        'git.commit',
                        { message: commitMsg.trim(), stageAll: staged.length === 0, amend },
                        'Committed changes',
                      );
                      setCommitMsg('');
                      setAmend(false);
                    }}
                    className="inline-flex items-center gap-1 rounded-lg bg-[var(--accent)] px-3 py-1 text-xs font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                  >
                    <GitCommit className="w-3.5 h-3.5" />
                    Commit {staged.length === 0 ? 'All' : `(${staged.length})`}
                  </button>
                </div>
              </div>

              {/* Staged Changes */}
              <div className="space-y-1">
                <div className="flex items-center justify-between px-1">
                  <span className="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Staged ({staged.length})
                  </span>
                  {staged.length > 0 && (
                    <button
                      type="button"
                      onClick={() => void runGit('git.unstage', { files: [] }, 'Unstaged all')}
                      className="text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      Unstage all
                    </button>
                  )}
                </div>
                {staged.map((f) => (
                  <div
                    key={`s-${f.path}`}
                    className="flex items-center justify-between gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1"
                  >
                    <button
                      type="button"
                      onClick={() => openRightTab({ id: 'diff', kind: 'diff', title: 'Staged Diff', diffScope: 'staged' })}
                      className="font-mono text-[11px] truncate text-left flex-1 hover:text-[var(--accent)] cursor-pointer"
                    >
                      <span className="text-[var(--success)] mr-1.5">{f.staged || 'M'}</span>
                      {f.path}
                    </button>
                    <button
                      type="button"
                      onClick={() => void runGit('git.unstage', { files: [f.path] })}
                      title="Unstage"
                      className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
                    >
                      <Minus className="w-3 h-3" />
                    </button>
                  </div>
                ))}
              </div>

              {/* Unstaged Changes */}
              <div className="space-y-1">
                <div className="flex items-center justify-between px-1">
                  <span className="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Unstaged ({unstaged.length})
                  </span>
                  {unstaged.length > 0 && (
                    <button
                      type="button"
                      data-testid="sc-stage-all-btn"
                      onClick={() => void runGit('git.stage', { files: [] }, 'Staged all')}
                      className="text-[11px] text-[var(--accent)] hover:underline cursor-pointer"
                    >
                      Stage all
                    </button>
                  )}
                </div>
                {unstaged.map((f) => (
                  <div
                    key={`u-${f.path}`}
                    className="flex items-center justify-between gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1"
                  >
                    <button
                      type="button"
                      onClick={() =>
                        openRightTab({ id: 'diff', kind: 'diff', title: 'Diff', diffScope: 'uncommitted' })
                      }
                      className="font-mono text-[11px] truncate text-left flex-1 hover:text-[var(--accent)] cursor-pointer"
                    >
                      <span className="text-[var(--warning)] mr-1.5">{f.untracked ? '?' : f.unstaged || 'M'}</span>
                      {f.path}
                    </button>
                    <div className="flex items-center gap-0.5 shrink-0">
                      <button
                        type="button"
                        data-testid="sc-stage-file-btn"
                        onClick={() => void runGit('git.stage', { files: [f.path] })}
                        title="Stage file"
                        className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--success)] cursor-pointer"
                      >
                        <Plus className="w-3 h-3" />
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          if (window.confirm(`Discard changes to ${f.path}?`)) {
                            void runGit('git.discard', { files: [f.path] }, `Discarded ${f.path}`);
                          }
                        }}
                        title="Discard changes"
                        className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--danger)] cursor-pointer"
                      >
                        <RotateCcw className="w-3 h-3" />
                      </button>
                    </div>
                  </div>
                ))}
                {staged.length === 0 && unstaged.length === 0 && (
                  <div className="py-6 text-center text-[var(--text-muted)]">Working tree clean.</div>
                )}
              </div>
            </>
          )}

          {/* BRANCHES TAB */}
          {tab === 'branches' && (
            <div className="space-y-2.5">
              <div className="flex gap-1.5">
                <input
                  type="text"
                  value={newBranch}
                  onChange={(e) => setNewBranch(e.target.value)}
                  placeholder="Create and switch to new branch..."
                  className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 font-mono text-xs outline-none focus:border-[var(--accent)]"
                />
                <button
                  type="button"
                  disabled={!newBranch.trim()}
                  onClick={async () => {
                    await runGit('git.checkout', { branch: newBranch.trim(), create: true }, `Created ${newBranch}`);
                    setNewBranch('');
                  }}
                  className="rounded-lg bg-[var(--accent)] px-3 py-1 text-xs font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                >
                  Create
                </button>
              </div>
              <div className="space-y-1">
                {branches
                  .filter((b) => !b.remote)
                  .map((b) => (
                    <div
                      key={b.name}
                      className={`flex items-center justify-between gap-2 rounded-lg border px-2.5 py-1.5 ${
                        b.current
                          ? 'border-[var(--accent)]/50 bg-[var(--accent-soft)]'
                          : 'border-[var(--border)] bg-[var(--bg-surface)]'
                      }`}
                    >
                      <button
                        type="button"
                        onClick={() => !b.current && void runGit('git.checkout', { branch: b.name }, `Switched to ${b.name}`)}
                        className="flex items-center gap-2 min-w-0 flex-1 text-left cursor-pointer"
                      >
                        <GitBranch className="w-3.5 h-3.5 shrink-0 text-[var(--accent)]" />
                        <div className="min-w-0">
                          <div className="font-mono font-medium truncate">{b.name}</div>
                          {b.subject && <div className="text-[10.5px] text-[var(--text-muted)] truncate">{b.subject}</div>}
                        </div>
                      </button>
                      {!b.current && (
                        <button
                          type="button"
                          onClick={() => {
                            if (window.confirm(`Delete branch ${b.name}?`)) {
                              void runGit('git.branch.delete', { branch: b.name, force: false }, `Deleted ${b.name}`);
                            }
                          }}
                          className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      )}
                    </div>
                  ))}
              </div>
            </div>
          )}

          {/* WORKTREES TAB */}
          {tab === 'worktrees' && (
            <div className="space-y-2.5">
              <div className="flex gap-1.5">
                <input
                  type="text"
                  value={newWtBranch}
                  onChange={(e) => setNewWtBranch(e.target.value)}
                  placeholder="New worktree branch name..."
                  className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 font-mono text-xs outline-none focus:border-[var(--accent)]"
                />
                <button
                  type="button"
                  onClick={async () => {
                    await runGit(
                      'git.worktrees.create',
                      { branchName: newWtBranch.trim() || undefined },
                      'Created worktree',
                    );
                    setNewWtBranch('');
                  }}
                  className="rounded-lg bg-[var(--accent)] px-3 py-1 text-xs font-semibold text-[var(--accent-fg)] cursor-pointer"
                >
                  New Worktree
                </button>
              </div>
              <div className="space-y-1.5">
                {worktrees.map((wt) => (
                  <div
                    key={wt.path}
                    className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-2.5 space-y-1.5"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-1.5 font-mono font-medium">
                        <FolderGit2 className="w-3.5 h-3.5 text-[var(--accent)]" />
                        <span>{wt.branch || '(detached)'}</span>
                        {wt.isMain && (
                          <span className="rounded bg-[var(--bg-elevated)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
                            main
                          </span>
                        )}
                      </div>
                      <div className="flex items-center gap-1">
                        <button
                          type="button"
                          onClick={() => openDraft({ hostId, cwd: wt.path, useWorktree: false })}
                          className="rounded border border-[var(--border)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
                        >
                          New Thread
                        </button>
                        {!wt.isMain && wt.branch && status?.defaultBranch && (
                          <button
                            type="button"
                            onClick={() =>
                              void runGit(
                                'git.worktrees.merge',
                                { sourceBranch: wt.branch, targetBranch: status.defaultBranch, squash: false },
                                `Merged ${wt.branch} into ${status.defaultBranch}`,
                              )
                            }
                            title={`Merge into ${status.defaultBranch}`}
                            className="inline-flex items-center gap-1 rounded border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--success)] hover:bg-[var(--bg-hover)] cursor-pointer"
                          >
                            <GitMerge className="w-3 h-3" />
                            Merge
                          </button>
                        )}
                        {!wt.isMain && (
                          <button
                            type="button"
                            onClick={() => {
                              if (window.confirm(`Remove worktree ${wt.path}?`)) {
                                void runGit(
                                  'git.worktrees.remove',
                                  { worktreePath: wt.path, force: true, deleteBranch: false },
                                  'Removed worktree',
                                );
                              }
                            }}
                            className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                          </button>
                        )}
                      </div>
                    </div>
                    <div className="font-mono text-[10.5px] text-[var(--text-muted)] truncate">{wt.path}</div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* STASHES TAB */}
          {tab === 'stashes' && (
            <div className="space-y-2.5">
              <div className="flex gap-1.5">
                <input
                  type="text"
                  value={stashMsg}
                  onChange={(e) => setStashMsg(e.target.value)}
                  placeholder="Stash message (optional)..."
                  className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 text-xs outline-none focus:border-[var(--accent)]"
                />
                <button
                  type="button"
                  onClick={async () => {
                    await runGit('git.stash.push', { message: stashMsg.trim(), includeUntracked: true }, 'Stashed changes');
                    setStashMsg('');
                  }}
                  className="inline-flex items-center gap-1 rounded-lg bg-[var(--accent)] px-3 py-1 text-xs font-semibold text-[var(--accent-fg)] cursor-pointer"
                >
                  <Archive className="w-3 h-3" />
                  Stash
                </button>
              </div>
              {stashes.length === 0 ? (
                <div className="py-6 text-center text-[var(--text-muted)]">No stashes.</div>
              ) : (
                stashes.map((s) => (
                  <div
                    key={s.ref}
                    className="flex items-center justify-between gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5"
                  >
                    <div className="min-w-0">
                      <div className="font-mono text-[11px] text-[var(--accent)]">{s.ref}</div>
                      <div className="truncate text-[var(--text-secondary)]">{s.message}</div>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                      <button
                        type="button"
                        onClick={() => void runGit('git.stash.pop', { ref: s.ref }, `Popped ${s.ref}`)}
                        className="rounded border border-[var(--border)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
                      >
                        Pop
                      </button>
                      <button
                        type="button"
                        onClick={() => void runGit('git.stash.drop', { ref: s.ref }, `Dropped ${s.ref}`)}
                        className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                      >
                        <Trash2 className="w-3 h-3" />
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>
          )}

          {/* HISTORY TAB */}
          {tab === 'history' && (
            <div className="space-y-1.5">
              {commits.map((c) => (
                <button
                  key={c.sha}
                  type="button"
                  onClick={async () => {
                    try {
                      const res = await callHost<{ diff?: string } | string>(hostId, 'git.show', {
                        path: cwd,
                        sha: c.sha,
                      });
                      setSelectedCommitDiff(typeof res === 'string' ? res : res?.diff || JSON.stringify(res, null, 2));
                    } catch (e) {
                      pushToast({ text: (e as Error).message, level: 'error' });
                    }
                  }}
                  className="flex w-full items-start justify-between gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 text-left hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  <div className="min-w-0">
                    <div className="font-medium text-[var(--text-primary)] truncate">{c.subject}</div>
                    <div className="text-[10.5px] text-[var(--text-muted)] flex items-center gap-2">
                      <History className="w-3 h-3" />
                      <span className="font-mono text-[var(--accent)]">{c.short}</span>
                      <span>{c.author}</span>
                    </div>
                  </div>
                </button>
              ))}
              {selectedCommitDiff && (
                <pre className="max-h-72 overflow-auto rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-2.5 font-mono text-[11px] text-[var(--text-secondary)] whitespace-pre-wrap">
                  {selectedCommitDiff}
                </pre>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
};
