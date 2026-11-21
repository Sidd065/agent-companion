import React, { useCallback, useEffect, useState } from 'react';
import {
  ArrowRightLeft,
  ChevronDown,
  Code2,
  FolderGit2,
  GitBranch,
  GitCommit,
  GitPullRequest,
  Play,
  RefreshCw,
  Server,
  Sparkles,
  Upload,
  X,
} from 'lucide-react';
import type {
  GitBranch as GitBranchInfo,
  GitStatus,
  PullRequest,
  ScriptDef,
  SessionDetail,
  SessionSummary,
  TerminalInfo,
} from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';

interface Props {
  hostId: string;
  session: SessionSummary;
  detail: SessionDetail | null;
}

export const ThreadInfoCard: React.FC<Props> = ({ hostId, session, detail }) => {
  const {
    hosts,
    editors,
    settings,
    updateSettings,
    callHost,
    openRightTab,
    setTerminalDrawerOpen,
    selectSession,
    pushToast,
  } = useApp();

  const host = hosts[hostId] ?? null;
  const cwd = session.worktreePath || session.cwd;

  const [gitStatus, setGitStatus] = useState<GitStatus | null>(null);
  const [branches, setBranches] = useState<GitBranchInfo[]>([]);
  const [scripts, setScripts] = useState<ScriptDef[]>([]);
  const [branchPR, setBranchPR] = useState<PullRequest | null>(null);

  const [branchMenuOpen, setBranchMenuOpen] = useState(false);
  const [newBranchName, setNewBranchName] = useState('');
  const [editorMenuOpen, setEditorMenuOpen] = useState(false);
  const [commitModalOpen, setCommitModalOpen] = useState(false);
  const [commitMsg, setCommitMsg] = useState('');
  const [commitStageAll, setCommitStageAll] = useState(true);
  const [commitBusy, setCommitBusy] = useState(false);

  const [pushConfirmOpen, setPushConfirmOpen] = useState(false);
  const [handoffOpen, setHandoffOpen] = useState(false);
  const [handoffAgent, setHandoffAgent] = useState('');
  const [handoffPrompt, setHandoffPrompt] = useState('');
  const [handoffBusy, setHandoffBusy] = useState(false);

  const refreshGitAndScripts = useCallback(async () => {
    if (!hostId || !cwd) return;
    try {
      const [st, sc] = await Promise.all([
        callHost<GitStatus>(hostId, 'git.status', { path: cwd, force: false }).catch(() => null),
        callHost<ScriptDef[]>(hostId, 'scripts.list', { path: cwd, projectId: session.projectId }).catch(() => []),
      ]);
      if (st) {
        setGitStatus(st);
        if (st.isRepo && st.branch && st.forge === 'github') {
          void callHost<{ pr: PullRequest | null }>(hostId, 'pr.forBranch', { path: cwd, branch: st.branch })
            .then((r) => setBranchPR(r?.pr ?? null))
            .catch(() => setBranchPR(null));
        } else {
          setBranchPR(null);
        }
      }
      if (Array.isArray(sc)) setScripts(sc);
    } catch {
      // ignore
    }
  }, [hostId, cwd, session.projectId, callHost]);

  useEffect(() => {
    void refreshGitAndScripts();
  }, [refreshGitAndScripts, session.turns, session.updatedAt]);

  const loadBranches = async () => {
    try {
      const list = await callHost<GitBranchInfo[]>(hostId, 'git.branches', { path: cwd });
      setBranches(Array.isArray(list) ? list : []);
    } catch {
      setBranches([]);
    }
  };

  const handleCheckout = async (branch: string, create = false) => {
    try {
      await callHost(hostId, 'git.checkout', { path: cwd, branch, create });
      setBranchMenuOpen(false);
      setNewBranchName('');
      pushToast({ text: `Switched to branch ${branch}`, level: 'success' });
      void refreshGitAndScripts();
    } catch (e) {
      pushToast({ text: `Checkout failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleRunScript = async (sc: ScriptDef) => {
    try {
      const term = await callHost<TerminalInfo>(hostId, 'scripts.run', {
        path: sc.cwd || cwd,
        command: sc.command,
        name: sc.name,
        scriptId: sc.id,
        sessionId: session.id,
      });
      setTerminalDrawerOpen(true);
      pushToast({ text: `Started script "${sc.name}" (${term?.name || sc.command})`, level: 'info' });
    } catch (e) {
      pushToast({ text: `Script failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleGenerateCommitMsg = async () => {
    setCommitBusy(true);
    try {
      const res = await callHost<{ message: string }>(hostId, 'git.commitMessage', { path: cwd });
      if (res?.message) setCommitMsg(res.message);
    } catch (e) {
      pushToast({ text: `Generate message failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setCommitBusy(false);
    }
  };

  const handleCommit = async () => {
    if (!commitMsg.trim()) return;
    setCommitBusy(true);
    try {
      await callHost(hostId, 'git.commit', { path: cwd, message: commitMsg.trim(), stageAll: commitStageAll });
      setCommitModalOpen(false);
      setCommitMsg('');
      pushToast({ text: 'Committed changes', level: 'success' });
      void refreshGitAndScripts();
    } catch (e) {
      pushToast({ text: `Commit failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setCommitBusy(false);
    }
  };

  const handlePush = async (allowDefault = false) => {
    if (gitStatus?.isDefaultBranch && !allowDefault) {
      setPushConfirmOpen(true);
      return;
    }
    try {
      await callHost(hostId, 'git.push', { path: cwd, setUpstream: true, allowDefault });
      setPushConfirmOpen(false);
      pushToast({ text: `Pushed ${gitStatus?.branch || 'branch'}`, level: 'success' });
      void refreshGitAndScripts();
    } catch (e) {
      pushToast({ text: `Push failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleOpenInEditor = async (editorId: string) => {
    setEditorMenuOpen(false);
    updateSettings({ favoriteEditor: editorId });
    if (window.agentcDesktop) {
      const ok = await window.agentcDesktop.openInEditor(editorId, cwd);
      if (!ok) pushToast({ text: 'Could not launch editor', level: 'error' });
    } else {
      pushToast({ text: `Editor launch requires Linux desktop app (${cwd})`, level: 'info' });
    }
  };

  const handleHandoff = async () => {
    if (!handoffAgent) return;
    setHandoffBusy(true);
    try {
      const res = await callHost<{ session?: SessionSummary; warning?: string }>(hostId, 'session.handoff', {
        id: session.id,
        targetAgent: handoffAgent,
        cwd,
        prompt: handoffPrompt.trim() || undefined,
      });
      setHandoffOpen(false);
      setHandoffPrompt('');
      if (res?.session) {
        selectSession(hostId, res.session.id);
        pushToast({ text: `Handed off to ${handoffAgent}`, level: 'success' });
      }
      if (res?.warning) {
        pushToast({ text: res.warning, level: 'info' });
      }
    } catch (e) {
      pushToast({ text: `Handoff failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setHandoffBusy(false);
    }
  };

  const ctxUsed = session.contextUsed || detail?.contextUsed || 0;
  const ctxSize = session.contextSize || detail?.contextSize || 0;
  const ctxPct = ctxSize > 0 ? Math.min(100, Math.round((ctxUsed / ctxSize) * 100)) : 0;
  const totalCost = session.usage?.costUsd ?? detail?.usage?.costUsd ?? 0;
  const dirtyCount = gitStatus?.files?.length ?? 0;

  return (
    <div className="border-b border-[var(--border)] bg-[var(--bg-sidebar)]/70 px-4 py-2 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {/* Left: Host, Path, Branch, Uncommitted, PR */}
        <div className="flex flex-wrap items-center gap-2 min-w-0">
          {host && (
            <span className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-elevated)] px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)]">
              <Server className="w-3 h-3 text-[var(--accent)]" />
              {host.creds.name}
            </span>
          )}

          <button
            type="button"
            onClick={() => openRightTab({ id: 'files', kind: 'files', title: 'Files', filePath: cwd })}
            title={cwd}
            className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] max-w-[260px] truncate cursor-pointer"
          >
            {session.worktreePath && <FolderGit2 className="w-3.5 h-3.5 text-[var(--accent)] shrink-0" />}
            <span className="truncate">{cwd}</span>
          </button>

          {/* Branch Selector */}
          {gitStatus?.isRepo && (
            <div className="relative">
              <button
                type="button"
                data-testid="info-branch-btn"
                onClick={() => {
                  const next = !branchMenuOpen;
                  setBranchMenuOpen(next);
                  if (next) void loadBranches();
                }}
                className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <GitBranch className="w-3 h-3 text-[var(--accent)]" />
                <span className="font-mono">{gitStatus.branch || 'HEAD'}</span>
                {(gitStatus.ahead > 0 || gitStatus.behind > 0) && (
                  <span className="text-[10px] text-[var(--text-muted)]">
                    {gitStatus.ahead > 0 ? `↑${gitStatus.ahead}` : ''}
                    {gitStatus.behind > 0 ? `↓${gitStatus.behind}` : ''}
                  </span>
                )}
                <ChevronDown className="w-3 h-3 opacity-60" />
              </button>

              {branchMenuOpen && (
                <div className="absolute left-0 top-full mt-1 z-30 w-64 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-2 shadow-xl">
                  <div className="flex gap-1 mb-2">
                    <input
                      type="text"
                      value={newBranchName}
                      onChange={(e) => setNewBranchName(e.target.value)}
                      placeholder="New branch name..."
                      className="flex-1 rounded-md border border-[var(--border)] bg-[var(--bg-base)] px-2 py-1 text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                    />
                    <button
                      type="button"
                      disabled={!newBranchName.trim()}
                      onClick={() => void handleCheckout(newBranchName.trim(), true)}
                      className="rounded-md bg-[var(--accent)] px-2 py-1 text-[11px] font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                    >
                      Create
                    </button>
                  </div>
                  <div className="max-h-48 overflow-y-auto space-y-0.5">
                    {branches
                      .filter((b) => !b.remote)
                      .map((b) => (
                        <button
                          key={b.name}
                          type="button"
                          onClick={() => void handleCheckout(b.name, false)}
                          className={`flex w-full items-center justify-between rounded-md px-2 py-1 text-left text-[11px] font-mono cursor-pointer ${
                            b.current
                              ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-semibold'
                              : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                          }`}
                        >
                          <span className="truncate">{b.name}</span>
                          {b.worktreePath && <span className="text-[9px] text-[var(--text-muted)]">wt</span>}
                        </button>
                      ))}
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Uncommitted Diff Pill */}
          {gitStatus?.isRepo && (dirtyCount > 0 || gitStatus.totalAdd > 0 || gitStatus.totalDel > 0) && (
            <button
              type="button"
              data-testid="info-uncommitted-pill"
              onClick={() => openRightTab({ id: 'diff', kind: 'diff', title: 'Diff', diffScope: 'uncommitted' })}
              className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              <span className="text-[var(--text-secondary)]">{dirtyCount} changed</span>
              <span className="font-mono text-[var(--diff-add-fg)]">+{gitStatus.totalAdd}</span>
              <span className="font-mono text-[var(--diff-del-fg)]">-{gitStatus.totalDel}</span>
            </button>
          )}

          {/* Active Branch PR Badge */}
          {branchPR && (
            <button
              type="button"
              onClick={() => openRightTab({ id: 'pr', kind: 'pr', title: `PR #${branchPR.number}` })}
              className="inline-flex items-center gap-1 rounded-md border border-[var(--success)]/40 bg-[var(--success)]/10 px-2 py-0.5 text-[11px] text-[var(--success)] hover:opacity-90 cursor-pointer"
            >
              <GitPullRequest className="w-3 h-3" />
              <span>#{branchPR.number}</span>
            </button>
          )}
        </div>

        {/* Right: Scripts, Git Commit/Push, Handoff, Open in Editor, Context Meter */}
        <div className="flex flex-wrap items-center gap-1.5">
          {/* Quick Project Script Pills (up to 3 inline) */}
          {scripts.slice(0, 3).map((sc) => (
            <button
              key={sc.id}
              type="button"
              onClick={() => void handleRunScript(sc)}
              title={`Run: ${sc.command}`}
              className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <Play className="w-2.5 h-2.5 text-[var(--success)]" />
              <span>{sc.name}</span>
            </button>
          ))}

          {/* Quick Commit & Push */}
          {gitStatus?.isRepo && (
            <>
              <button
                type="button"
                onClick={() => setCommitModalOpen(true)}
                className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <GitCommit className="w-3 h-3" />
                <span>Commit</span>
              </button>
              <button
                type="button"
                onClick={() => void handlePush(false)}
                className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <Upload className="w-3 h-3" />
                <span>Push</span>
              </button>
            </>
          )}

          {/* Switch Provider / Portable Handoff */}
          <button
            type="button"
            data-testid="info-handoff-btn"
            onClick={() => {
              const other = host?.agents.find((a) => a.available && a.id !== session.agent)?.id || host?.agents[0]?.id || '';
              setHandoffAgent(other);
              setHandoffOpen(true);
            }}
            title="Portable handoff to another provider"
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <ArrowRightLeft className="w-3 h-3 text-[var(--accent)]" />
            <span>Handoff</span>
          </button>

          {/* Open in Editor */}
          {editors.length > 0 && (
            <div className="relative">
              <button
                type="button"
                onClick={() => setEditorMenuOpen((v) => !v)}
                className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <Code2 className="w-3 h-3" />
                <span>Open</span>
                <ChevronDown className="w-3 h-3 opacity-60" />
              </button>
              {editorMenuOpen && (
                <div className="absolute right-0 top-full mt-1 z-30 w-44 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-1 shadow-xl">
                  {editors.map((ed) => (
                    <button
                      key={ed.id}
                      type="button"
                      onClick={() => void handleOpenInEditor(ed.id)}
                      className={`flex w-full items-center justify-between rounded-md px-2 py-1 text-left text-xs hover:bg-[var(--bg-hover)] cursor-pointer ${
                        settings.favoriteEditor === ed.id ? 'text-[var(--accent)] font-medium' : 'text-[var(--text-secondary)]'
                      }`}
                    >
                      <span>{ed.name}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* Context Meter + Cost */}
          {(ctxSize > 0 || totalCost > 0) && (
            <div
              title={`Context: ${ctxUsed.toLocaleString()} / ${ctxSize.toLocaleString()} tokens (${ctxPct}%)`}
              className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[10.5px] text-[var(--text-muted)]"
            >
              {ctxSize > 0 && (
                <>
                  <div className="w-12 h-1.5 rounded-full bg-[var(--bg-base)] overflow-hidden">
                    <div
                      className={`h-full rounded-full ${
                        ctxPct > 85 ? 'bg-[var(--danger)]' : ctxPct > 65 ? 'bg-[var(--warning)]' : 'bg-[var(--accent)]'
                      }`}
                      style={{ width: `${ctxPct}%` }}
                    />
                  </div>
                  <span className="font-mono">{ctxPct}%</span>
                </>
              )}
              {totalCost > 0 && <span className="font-mono text-[var(--text-secondary)]">${totalCost.toFixed(3)}</span>}
            </div>
          )}

          <button
            type="button"
            onClick={() => void refreshGitAndScripts()}
            title="Refresh git status & scripts"
            className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <RefreshCw className="w-3 h-3" />
          </button>
        </div>
      </div>

      {/* Commit Modal */}
      {commitModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-md rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-4 shadow-2xl">
            <div className="flex items-center justify-between mb-3">
              <div className="text-sm font-semibold">Commit Changes</div>
              <button
                type="button"
                onClick={() => setCommitModalOpen(false)}
                className="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <textarea
              value={commitMsg}
              onChange={(e) => setCommitMsg(e.target.value)}
              rows={3}
              placeholder="Commit message..."
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-2.5 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
            />
            <div className="flex items-center justify-between mt-2.5">
              <label className="flex items-center gap-1.5 text-xs text-[var(--text-secondary)]">
                <input
                  type="checkbox"
                  checked={commitStageAll}
                  onChange={(e) => setCommitStageAll(e.target.checked)}
                />
                Stage all changes
              </label>
              <button
                type="button"
                disabled={commitBusy}
                onClick={() => void handleGenerateCommitMsg()}
                className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-1 text-[11px] text-[var(--accent)] hover:bg-[var(--accent-soft)] cursor-pointer"
              >
                <Sparkles className="w-3 h-3" />
                Generate message
              </button>
            </div>
            <div className="flex justify-end gap-2 mt-4">
              <button
                type="button"
                onClick={() => setCommitModalOpen(false)}
                className="rounded-lg border border-[var(--border)] px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={commitBusy || !commitMsg.trim()}
                onClick={() => void handleCommit()}
                className="rounded-lg bg-[var(--accent)] px-3 py-1.5 text-xs font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
              >
                Commit
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Default-Branch Push Guard Confirmation */}
      {pushConfirmOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-sm rounded-2xl border border-[var(--warning)]/50 bg-[var(--bg-surface)] p-4 shadow-2xl">
            <div className="text-sm font-semibold text-[var(--warning)] mb-2">Push to Default Branch?</div>
            <p className="text-xs text-[var(--text-secondary)] mb-4">
              You are about to push directly to <code className="font-mono text-[var(--text-primary)]">{gitStatus?.branch}</code>.
              Confirm if you intend to push without a feature branch.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setPushConfirmOpen(false)}
                className="rounded-lg border border-[var(--border)] px-3 py-1.5 text-xs text-[var(--text-secondary)] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => void handlePush(true)}
                className="rounded-lg bg-[var(--warning)] px-3 py-1.5 text-xs font-semibold text-[#1a1204] cursor-pointer"
              >
                Push to {gitStatus?.branch}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Portable Provider Handoff Modal */}
      {handoffOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-md rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-4 shadow-2xl">
            <div className="flex items-center justify-between mb-2">
              <div className="text-sm font-semibold">Portable Provider Handoff</div>
              <button
                type="button"
                onClick={() => setHandoffOpen(false)}
                className="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <p className="text-xs text-[var(--text-secondary)] mb-3">
              Generate a compact transcript &amp; git state summary and continue this task in another agent provider.
            </p>
            <label className="block text-[11px] font-medium text-[var(--text-muted)] mb-1">Target Provider</label>
            <select
              value={handoffAgent}
              onChange={(e) => setHandoffAgent(e.target.value)}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 text-xs text-[var(--text-primary)] mb-3"
            >
              {(host?.agents ?? []).map((a) => (
                <option key={a.id} value={a.id} disabled={!a.available}>
                  {a.name} {!a.available ? '(unavailable)' : ''}
                </option>
              ))}
            </select>
            <label className="block text-[11px] font-medium text-[var(--text-muted)] mb-1">
              Additional instruction (optional)
            </label>
            <textarea
              value={handoffPrompt}
              onChange={(e) => setHandoffPrompt(e.target.value)}
              rows={2}
              placeholder="Continue implementing the remaining tests..."
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-2 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
            />
            <div className="flex justify-end gap-2 mt-4">
              <button
                type="button"
                onClick={() => setHandoffOpen(false)}
                className="rounded-lg border border-[var(--border)] px-3 py-1.5 text-xs text-[var(--text-secondary)] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={handoffBusy || !handoffAgent}
                onClick={() => void handleHandoff()}
                className="rounded-lg bg-[var(--accent)] px-3 py-1.5 text-xs font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
              >
                {handoffBusy ? 'Handing off...' : 'Create Handoff Thread'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
