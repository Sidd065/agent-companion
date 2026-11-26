import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  CheckCircle2,
  CircleDot,
  ExternalLink,
  FolderGit2,
  GitMerge,
  GitPullRequest,
  MessageSquare,
  Plus,
  RefreshCw,
  Sparkles,
  X,
  XCircle,
} from 'lucide-react';
import type { GitHubIssue, PullRequest } from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';
import { resolveHostScopedCwd } from '../../utils/multiHost.js';
import { MarkdownRenderer } from '../markdown/MarkdownRenderer.js';

export const PullRequestPanel: React.FC = () => {
  const { activeHost, activeSession, draft, callHost, openDraft, setComposerPrefill, pushToast } = useApp();
  const hostId = activeHost?.creds.id ?? '';
  const cwd = resolveHostScopedCwd({
    hostId,
    activeSession,
    activeSessionHostId: hostId,
    draft,
    fallback: '',
  });

  const [subTab, setSubTab] = useState<'prs' | 'issues'>('prs');
  const [prs, setPrs] = useState<PullRequest[]>([]);
  const [issues, setIssues] = useState<GitHubIssue[]>([]);
  const [selectedPR, setSelectedPR] = useState<PullRequest | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const [commentText, setCommentText] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [newTitle, setNewTitle] = useState('');
  const [newBody, setNewBody] = useState('');
  const [newDraft, setNewDraft] = useState(false);
  const reqSeqRef = useRef(0);

  const loadData = useCallback(async () => {
    if (!hostId || !cwd) return;
    const seq = ++reqSeqRef.current;
    setLoading(true);
    setError('');
    try {
      if (subTab === 'prs') {
        const list = await callHost<PullRequest[]>(hostId, 'pr.list', { path: cwd, state: 'open', limit: 30 });
        if (seq !== reqSeqRef.current) return;
        setPrs(Array.isArray(list) ? list : []);
      } else {
        const list = await callHost<GitHubIssue[]>(hostId, 'issues.list', { path: cwd, state: 'open', limit: 30 });
        if (seq !== reqSeqRef.current) return;
        setIssues(Array.isArray(list) ? list : []);
      }
    } catch (e) {
      if (seq !== reqSeqRef.current) return;
      setError((e as Error).message);
    } finally {
      if (seq === reqSeqRef.current) {
        setLoading(false);
      }
    }
  }, [hostId, cwd, subTab, callHost]);

  useEffect(() => {
    reqSeqRef.current++;
    setSelectedPR(null);
    setPrs([]);
    setIssues([]);
    setError('');
  }, [hostId, cwd]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  const inspectPR = async (num: number) => {
    const guardSeq = reqSeqRef.current;
    try {
      const full = await callHost<PullRequest>(hostId, 'pr.get', { path: cwd, number: num, includeDiff: false });
      if (guardSeq !== reqSeqRef.current) return;
      setSelectedPR(full);
    } catch (e) {
      if (guardSeq !== reqSeqRef.current) return;
      pushToast({ text: (e as Error).message, level: 'error' });
    }
  };

  const handleCreatePR = async () => {
    if (!newTitle.trim()) return;
    try {
      const pr = await callHost<PullRequest>(hostId, 'pr.create', {
        path: cwd,
        title: newTitle.trim(),
        body: newBody.trim(),
        draft: newDraft,
      });
      setCreateOpen(false);
      setNewTitle('');
      setNewBody('');
      pushToast({ text: `Created PR #${pr.number}`, level: 'success' });
      await loadData();
      setSelectedPR(pr);
    } catch (e) {
      pushToast({ text: `Create PR failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleCheckoutPR = async (num: number) => {
    try {
      const wt = await callHost<{ path: string }>(hostId, 'pr.checkoutWorktree', { path: cwd, number: num });
      pushToast({ text: `Checked out PR #${num} into worktree`, level: 'success' });
      if (wt?.path) {
        openDraft({ hostId, cwd: wt.path, useWorktree: false });
        setComposerPrefill(`Review Pull Request #${num} in this worktree and summarize any issues or improvements.`);
      }
    } catch (e) {
      pushToast({ text: `Checkout PR failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleStartFromIssue = (issue: GitHubIssue) => {
    openDraft({ hostId, cwd, useWorktree: true, branchName: `issue-${issue.number}` });
    setComposerPrefill(`Resolve GitHub Issue #${issue.number}: ${issue.title}\n\n${issue.body || ''}`);
  };

  return (
    <div data-testid="pr-panel" className="flex-1 flex flex-col min-h-0 text-xs">
      <div className="flex items-center justify-between gap-2 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-3 py-1.5">
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => {
              setSubTab('prs');
              setSelectedPR(null);
            }}
            className={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 cursor-pointer ${
              subTab === 'prs'
                ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
            }`}
          >
            <GitPullRequest className="w-3.5 h-3.5" />
            PRs
          </button>
          <button
            type="button"
            onClick={() => {
              setSubTab('issues');
              setSelectedPR(null);
            }}
            className={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 cursor-pointer ${
              subTab === 'issues'
                ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
            }`}
          >
            <CircleDot className="w-3.5 h-3.5" />
            Issues
          </button>
        </div>

        <div className="flex items-center gap-1">
          {subTab === 'prs' && (
            <button
              type="button"
              onClick={() => setCreateOpen(true)}
              className="inline-flex items-center gap-1 rounded-md bg-[var(--accent)] px-2 py-0.5 text-[11px] font-semibold text-[var(--accent-fg)] cursor-pointer"
            >
              <Plus className="w-3 h-3" />
              New PR
            </button>
          )}
          <button
            type="button"
            onClick={() => void loadData()}
            className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-3 space-y-2.5">
        {error && (
          <div className="rounded-xl border border-[var(--warning)]/40 bg-[var(--warning)]/10 p-3 text-[var(--warning)]">
            {error}
          </div>
        )}

        {/* Selected PR Detail View */}
        {selectedPR ? (
          <div className="space-y-3">
            <button
              type="button"
              onClick={() => setSelectedPR(null)}
              className="text-[11px] text-[var(--accent)] hover:underline cursor-pointer"
            >
              ← Back to PR list
            </button>
            <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-3 space-y-2">
              <div className="flex items-start justify-between gap-2">
                <div>
                  <div className="text-sm font-semibold">
                    #{selectedPR.number} {selectedPR.title}
                  </div>
                  <div className="text-[11px] text-[var(--text-muted)] font-mono mt-0.5">
                    {selectedPR.headRefName} → {selectedPR.baseRefName} · by {selectedPR.author}
                  </div>
                </div>
                {selectedPR.url && (
                  <a
                    href={selectedPR.url}
                    target="_blank"
                    rel="noreferrer"
                    className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--accent)]"
                  >
                    <ExternalLink className="w-3.5 h-3.5" />
                  </a>
                )}
              </div>

              <div className="flex flex-wrap gap-1.5 pt-1">
                <button
                  type="button"
                  onClick={() => void handleCheckoutPR(selectedPR.number)}
                  className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-1 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  <FolderGit2 className="w-3 h-3 text-[var(--accent)]" />
                  Checkout Worktree
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setComposerPrefill(`Review PR #${selectedPR.number} ("${selectedPR.title}") and address any failing checks or review comments.`);
                    pushToast({ text: 'Loaded PR review prompt into composer', level: 'info' });
                  }}
                  className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-1 text-[11px] text-[var(--accent)] hover:bg-[var(--accent-soft)] cursor-pointer"
                >
                  <Sparkles className="w-3 h-3" />
                  Review with Agent
                </button>
                <button
                  type="button"
                  onClick={async () => {
                    if (!window.confirm(`Merge PR #${selectedPR.number}?`)) return;
                    try {
                      await callHost(hostId, 'pr.merge', {
                        path: cwd,
                        number: selectedPR.number,
                        method: 'squash',
                        deleteBranch: false,
                      });
                      pushToast({ text: `Merged PR #${selectedPR.number}`, level: 'success' });
                      setSelectedPR(null);
                      await loadData();
                    } catch (e) {
                      pushToast({ text: (e as Error).message, level: 'error' });
                    }
                  }}
                  className="inline-flex items-center gap-1 rounded-md bg-[var(--success)] px-2 py-1 text-[11px] font-semibold text-[#07150d] cursor-pointer"
                >
                  <GitMerge className="w-3 h-3" />
                  Squash Merge
                </button>
              </div>

              {selectedPR.body && (
                <div className="border-t border-[var(--border)] pt-2">
                  <MarkdownRenderer text={selectedPR.body} citable />
                </div>
              )}

              {/* CI Checks */}
              {selectedPR.checks && selectedPR.checks.length > 0 && (
                <div className="border-t border-[var(--border)] pt-2 space-y-1">
                  <div className="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Checks ({selectedPR.checks.length})
                  </div>
                  {selectedPR.checks.map((ch) => (
                    <div key={ch.name} className="flex items-center justify-between text-[11px]">
                      <span className="truncate">{ch.name}</span>
                      <span className="flex items-center gap-1">
                        {ch.conclusion === 'SUCCESS' ? (
                          <CheckCircle2 className="w-3.5 h-3.5 text-[var(--success)]" />
                        ) : ch.conclusion === 'FAILURE' ? (
                          <XCircle className="w-3.5 h-3.5 text-[var(--danger)]" />
                        ) : (
                          <span className="text-[var(--warning)]">{ch.status}</span>
                        )}
                      </span>
                    </div>
                  ))}
                </div>
              )}

              {/* Comments */}
              {selectedPR.comments && selectedPR.comments.length > 0 && (
                <div className="border-t border-[var(--border)] pt-2 space-y-2">
                  <div className="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Comments ({selectedPR.comments.length})
                  </div>
                  {selectedPR.comments.map((cm, idx) => (
                    <div key={idx} className="rounded-lg bg-[var(--bg-base)] p-2 space-y-1">
                      <div className="text-[10.5px] font-medium text-[var(--accent)]">{cm.author}</div>
                      <div className="text-[11.5px] text-[var(--text-secondary)] whitespace-pre-wrap">{cm.body}</div>
                    </div>
                  ))}
                </div>
              )}

              {/* Add Comment */}
              <div className="border-t border-[var(--border)] pt-2 flex gap-1.5">
                <input
                  type="text"
                  value={commentText}
                  onChange={(e) => setCommentText(e.target.value)}
                  placeholder="Add a PR comment..."
                  className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1 text-xs outline-none focus:border-[var(--accent)]"
                />
                <button
                  type="button"
                  disabled={!commentText.trim()}
                  onClick={async () => {
                    try {
                      await callHost(hostId, 'pr.comment', {
                        path: cwd,
                        number: selectedPR.number,
                        body: commentText.trim(),
                      });
                      setCommentText('');
                      pushToast({ text: 'Posted comment', level: 'success' });
                      await inspectPR(selectedPR.number);
                    } catch (e) {
                      pushToast({ text: (e as Error).message, level: 'error' });
                    }
                  }}
                  className="inline-flex items-center gap-1 rounded-lg bg-[var(--accent)] px-2.5 py-1 text-xs font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                >
                  <MessageSquare className="w-3 h-3" />
                  Comment
                </button>
              </div>
            </div>
          </div>
        ) : subTab === 'prs' ? (
          prs.length === 0 && !loading && !error ? (
            <div className="py-10 text-center text-[var(--text-muted)]">No open pull requests.</div>
          ) : (
            prs.map((pr) => (
              <button
                key={pr.number}
                type="button"
                onClick={() => void inspectPR(pr.number)}
                className="flex w-full items-start justify-between gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-2.5 text-left hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <div className="min-w-0">
                  <div className="font-medium text-[var(--text-primary)]">
                    <span className="text-[var(--accent)] font-mono mr-1.5">#{pr.number}</span>
                    {pr.title}
                  </div>
                  <div className="text-[10.5px] text-[var(--text-muted)] font-mono mt-0.5">
                    {pr.headRefName} → {pr.baseRefName} · {pr.author}
                  </div>
                </div>
                <div className="shrink-0 font-mono text-[10.5px] flex items-center gap-1">
                  <span className="text-[var(--diff-add-fg)]">+{pr.additions}</span>
                  <span className="text-[var(--diff-del-fg)]">-{pr.deletions}</span>
                </div>
              </button>
            ))
          )
        ) : issues.length === 0 && !loading && !error ? (
          <div className="py-10 text-center text-[var(--text-muted)]">No open GitHub issues.</div>
        ) : (
          issues.map((iss) => (
            <div
              key={iss.number}
              className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-2.5 space-y-1.5"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="font-medium text-[var(--text-primary)]">
                  <span className="text-[var(--success)] font-mono mr-1.5">#{iss.number}</span>
                  {iss.title}
                </div>
                <button
                  type="button"
                  onClick={() => handleStartFromIssue(iss)}
                  className="shrink-0 rounded-md bg-[var(--accent-soft)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent)] hover:opacity-90 cursor-pointer"
                >
                  Work on Issue
                </button>
              </div>
              {iss.labels && iss.labels.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {iss.labels.map((lb) => (
                    <span
                      key={lb}
                      className="rounded-full border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[10px] text-[var(--text-muted)]"
                    >
                      {lb}
                    </span>
                  ))}
                </div>
              )}
            </div>
          ))
        )}
      </div>

      {/* Create PR Modal */}
      {createOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-md rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-4 shadow-2xl space-y-3">
            <div className="flex items-center justify-between">
              <div className="text-sm font-semibold">Create Pull Request</div>
              <button type="button" onClick={() => setCreateOpen(false)} className="cursor-pointer">
                <X className="w-4 h-4 text-[var(--text-muted)]" />
              </button>
            </div>
            <input
              type="text"
              value={newTitle}
              onChange={(e) => setNewTitle(e.target.value)}
              placeholder="PR title"
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 text-xs outline-none focus:border-[var(--accent)]"
            />
            <textarea
              value={newBody}
              onChange={(e) => setNewBody(e.target.value)}
              rows={4}
              placeholder="PR description (Markdown)..."
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-2.5 text-xs outline-none focus:border-[var(--accent)]"
            />
            <label className="flex items-center gap-1.5 text-xs text-[var(--text-secondary)]">
              <input type="checkbox" checked={newDraft} onChange={(e) => setNewDraft(e.target.checked)} />
              Create as draft PR
            </label>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setCreateOpen(false)}
                className="rounded-lg border border-[var(--border)] px-3 py-1.5 text-xs cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={!newTitle.trim()}
                onClick={() => void handleCreatePR()}
                className="rounded-lg bg-[var(--accent)] px-3 py-1.5 text-xs font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
              >
                Create PR
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
