import React, { useEffect, useMemo, useState } from 'react';
import { formatHostDisplayName } from '../../utils/multiHost.js';
import {
  Folder,
  FolderGit2,
  FolderPlus,
  GitBranch,
  Plus,
  Server,
  Sparkles,
} from 'lucide-react';
import type { GitBranch as GitBranchInfo, SessionSummary } from '@agentc/protocol';
import { providerCatalogKey, useApp } from '../../state/appStore.js';
import { coerceSupportedMode, isFullAccessMode, resolveAvailableModes } from '../../utils/codexModels.js';
import { Composer, type PendingAttachment } from './Composer.js';

const STARTER_PROMPTS = [
  'Summarize the architecture of this repository and key entry points',
  'Review uncommitted changes and highlight any bugs or edge cases',
  'Run the test suite and diagnose any failures',
];

export const DraftView: React.FC<{ openModelPickerSignal?: number }> = ({ openModelPickerSignal }) => {
  const {
    hostList,
    hosts,
    activeHost,
    draft,
    setDraft,
    openDraft,
    providerCatalogs,
    loadProviderModels,
    callHost,
    selectSession,
    setAddProjectModalOpen,
    setPairModalOpen,
    setComposerPrefill,
    pushToast,
  } = useApp();

  const targetHost = activeHost ?? hosts[draft.hostId] ?? hostList[0] ?? null;
  const targetHostId = targetHost?.creds.id ?? '';
  const projects = targetHost?.projects ?? [];

  const selectedProject = useMemo(
    () => projects.find((p) => p.id === draft.projectId) ?? projects.find((p) => p.path === draft.cwd) ?? null,
    [projects, draft.projectId, draft.cwd],
  );

  const effectiveAgent =
    draft.agent ||
    selectedProject?.defaultAgent ||
    targetHost?.agents.find((a) => a.available)?.id ||
    targetHost?.agents[0]?.id ||
    '';

  const activeCatalog =
    targetHostId && effectiveAgent
      ? providerCatalogs[providerCatalogKey(targetHostId, effectiveAgent)]
      : undefined;

  const [branches, setBranches] = useState<GitBranchInfo[]>([]);

  // Load dynamic provider catalog for Draft mode
  useEffect(() => {
    if (!targetHostId || !effectiveAgent) return;
    void loadProviderModels(targetHostId, effectiveAgent, false);
  }, [targetHostId, effectiveAgent, loadProviderModels]);

  // Ensure draft has valid hostId, cwd, default agent, and supported mode
  useEffect(() => {
    if (!targetHost) return;
    const patch: Partial<typeof draft> = {};
    if (draft.hostId !== targetHost.creds.id) {
      patch.hostId = targetHost.creds.id;
    }
    if (!draft.agent && effectiveAgent) {
      patch.agent = effectiveAgent;
    }
    if (!draft.cwd) {
      if (projects.length > 0) {
        patch.cwd = projects[0].path;
        patch.projectId = projects[0].id;
      } else if (targetHost.info?.home) {
        patch.cwd = targetHost.info.home;
      }
    }
    if (effectiveAgent) {
      const modes = resolveAvailableModes({
        agentId: effectiveAgent,
        catalogModes: activeCatalog?.availableModes,
      });
      const coerced = coerceSupportedMode(draft.mode, draft.skipPermissions, modes);
      if (coerced.id !== draft.mode || coerced.skip !== draft.skipPermissions) {
        patch.mode = coerced.id;
        patch.skipPermissions = coerced.skip;
      }
    }
    if (Object.keys(patch).length > 0) {
      setDraft((prev) => ({ ...prev, ...patch }));
    }
  }, [
    targetHost,
    draft.hostId,
    draft.agent,
    draft.cwd,
    draft.mode,
    draft.skipPermissions,
    effectiveAgent,
    activeCatalog?.availableModes,
    projects,
    setDraft,
  ]);

  // Fetch branches when useWorktree enabled or project selected
  useEffect(() => {
    if (!targetHostId || !draft.cwd) {
      setBranches([]);
      return;
    }
    let alive = true;
    void callHost<GitBranchInfo[]>(targetHostId, 'git.branches', { path: draft.cwd })
      .then((list) => {
        if (!alive) return;
        const locals = Array.isArray(list) ? list.filter((b) => !b.remote) : [];
        setBranches(locals);
        if (!draft.baseBranch && locals.length > 0) {
          const cur = locals.find((b) => b.current) ?? locals[0];
          setDraft((prev) => (prev.baseBranch ? prev : { ...prev, baseBranch: cur.name }));
        }
      })
      .catch(() => {
        if (alive) setBranches([]);
      });
    return () => {
      alive = false;
    };
  }, [targetHostId, draft.cwd, callHost]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleCreateDraft = async (text: string, attachments: PendingAttachment[]) => {
    if (!targetHostId) {
      pushToast({ text: 'Pair a host first to start a thread', level: 'error' });
      return;
    }
    const effectiveCwd = draft.cwd.trim() || selectedProject?.path || targetHost?.info?.home || '~';
    const agentToUse = effectiveAgent || 'claude';
    const modes = resolveAvailableModes({
      agentId: agentToUse,
      catalogModes: activeCatalog?.availableModes,
    });
    const resolvedMode = coerceSupportedMode(draft.mode, draft.skipPermissions, modes);
    const isFull = isFullAccessMode(resolvedMode.id);
    const effortVal = draft.thought !== undefined ? draft.thought : '';

    const res = await callHost<{ session?: SessionSummary } | SessionSummary>(targetHostId, 'session.create', {
      agent: agentToUse,
      cwd: effectiveCwd,
      projectId: draft.projectId || selectedProject?.id || undefined,
      model: draft.model || undefined,
      mode: resolvedMode.id || undefined,
      thought: effortVal || undefined,
      effort: effortVal || undefined,
      skipPermissions: isFull,
      flags: {
        model: draft.model || undefined,
        mode: resolvedMode.id || undefined,
        effort: effortVal || undefined,
        thought: effortVal || undefined,
        skipPermissions: isFull,
      },
      useWorktree: draft.useWorktree,
      baseBranch: draft.useWorktree ? draft.baseBranch || undefined : undefined,
      branchName: draft.useWorktree ? draft.branchName.trim() || undefined : undefined,
      prompt: {
        text,
        attachments: attachments.map((a) => ({
          type: a.type,
          name: a.name,
          mime: a.mime,
          data: a.data,
          path: a.path,
        })),
      },
    });
    const created = (res as { session?: SessionSummary })?.session ?? (res as SessionSummary);
    if (created?.id) {
      selectSession(targetHostId, created.id);
    }
  };

  if (hostList.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center p-8 text-center">
        <div className="w-12 h-12 rounded-2xl bg-[var(--accent-soft)] flex items-center justify-center mb-4">
          <Sparkles className="w-6 h-6 text-[var(--accent)]" />
        </div>
        <h1 className="text-xl font-semibold mb-2">Welcome to Agent Companion</h1>
        <p className="text-sm text-[var(--text-secondary)] max-w-md mb-6">
          Connect to your local <code className="font-mono">agentc</code> daemon with one click, or paste a pairing link
          from <code className="font-mono">agentc pair</code> on any remote Linux host.
        </p>
        <button
          type="button"
          data-testid="empty-pair-btn"
          onClick={() => setPairModalOpen(true)}
          className="inline-flex items-center gap-2 rounded-xl bg-[var(--accent)] px-5 py-2.5 text-sm font-semibold text-[var(--accent-fg)] hover:opacity-90 cursor-pointer"
        >
          <Plus className="w-4 h-4" />
          Pair a Host
        </button>
      </div>
    );
  }

  return (
    <div data-testid="draft-view" className="flex-1 flex flex-col min-h-0">
      <div className="flex-1 overflow-y-auto flex flex-col items-center justify-center px-6 py-8">
        <div className="w-full max-w-2xl space-y-6">
          <div className="text-center space-y-1.5">
            <h1 className="text-2xl font-semibold tracking-tight text-[var(--text-primary)]">
              {selectedProject ? `What should we build in ${selectedProject.name}?` : 'Start a new thread'}
            </h1>
            <p className="text-xs text-[var(--text-muted)]">
              Run Claude Code, Antigravity, Codex, or any ACP agent in a local folder or isolated Git worktree.
            </p>
          </div>

          {/* Host + Project + Checkout Controls Card */}
          <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-3 shadow-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {/* Host selector */}
              {hostList.length > 1 && (
                <div>
                  <label className="block text-[11px] font-medium text-[var(--text-muted)] mb-1">Host</label>
                  <div className="relative flex items-center">
                    <Server className="w-3.5 h-3.5 absolute left-2.5 text-[var(--accent)] pointer-events-none" />
                    <select
                      data-testid="draft-host-select"
                      value={targetHostId}
                      onChange={(e) => openDraft({ hostId: e.target.value })}
                      className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] pl-8 pr-2.5 py-1.5 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                    >
                      {hostList.map((h) => (
                        <option key={h.creds.id} value={h.creds.id}>
                          {formatHostDisplayName(h, hostList)} ({h.state})
                        </option>
                      ))}
                    </select>
                  </div>
                </div>
              )}

              {/* Project selector */}
              <div className={hostList.length <= 1 ? 'sm:col-span-2' : ''}>
                <div className="flex items-center justify-between mb-1">
                  <label className="text-[11px] font-medium text-[var(--text-muted)]">Project / Working Directory</label>
                  <button
                    type="button"
                    onClick={() => setAddProjectModalOpen(true)}
                    className="inline-flex items-center gap-1 text-[11px] text-[var(--accent)] hover:underline cursor-pointer"
                  >
                    <FolderPlus className="w-3 h-3" />
                    Add Project
                  </button>
                </div>
                <div className="flex flex-col gap-2">
                  {projects.length > 0 && (
                    <select
                      data-testid="draft-project-select"
                      value={selectedProject?.id ?? ''}
                      onChange={(e) => {
                        const pr = projects.find((p) => p.id === e.target.value);
                        if (pr) {
                          setDraft((prev) => ({
                            ...prev,
                            projectId: pr.id,
                            cwd: pr.path,
                            agent: pr.defaultAgent || prev.agent,
                            model: pr.defaultModel || prev.model,
                            useWorktree: pr.defaultCheckout === 'worktree',
                          }));
                        } else {
                          setDraft((prev) => ({ ...prev, projectId: undefined }));
                        }
                      }}
                      className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)] max-w-[200px]"
                    >
                      <option value="">Custom path</option>
                      {projects.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name}
                        </option>
                      ))}
                    </select>
                  )}
                  <input
                    type="text"
                    data-testid="draft-cwd-input"
                    value={draft.cwd}
                    onChange={(e) => setDraft((prev) => ({ ...prev, cwd: e.target.value }))}
                    placeholder="/home/user/project"
                    className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 font-mono text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                  />
                </div>
              </div>
            </div>

            {/* Checkout Mode: Local vs New Worktree */}
            <div className="flex flex-wrap items-center justify-between gap-3 pt-1 border-t border-[var(--border)]">
              <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-0.5 text-xs">
                <button
                  type="button"
                  data-testid="draft-checkout-local"
                  onClick={() => setDraft((prev) => ({ ...prev, useWorktree: false }))}
                  className={`inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 transition cursor-pointer ${
                    !draft.useWorktree
                      ? 'bg-[var(--bg-elevated)] text-[var(--text-primary)] font-medium shadow-xs'
                      : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
                  }`}
                >
                  <Folder className="w-3.5 h-3.5" />
                  Local
                </button>
                <button
                  type="button"
                  data-testid="draft-checkout-worktree"
                  onClick={() => setDraft((prev) => ({ ...prev, useWorktree: true }))}
                  className={`inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 transition cursor-pointer ${
                    draft.useWorktree
                      ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium shadow-xs'
                      : 'text-[var(--text-muted)] hover:text-[var(--text-primary)]'
                  }`}
                >
                  <FolderGit2 className="w-3.5 h-3.5" />
                  New Worktree
                </button>
              </div>

              {draft.useWorktree && (
                <div className="flex flex-wrap items-center gap-2 flex-1 justify-end">
                  <div className="inline-flex items-center gap-1">
                    <GitBranch className="w-3.5 h-3.5 text-[var(--text-muted)]" />
                    <select
                      value={draft.baseBranch}
                      onChange={(e) => setDraft((prev) => ({ ...prev, baseBranch: e.target.value }))}
                      className="rounded-md border border-[var(--border)] bg-[var(--bg-base)] px-2 py-1 font-mono text-[11px] text-[var(--text-primary)]"
                    >
                      <option value="">Current HEAD</option>
                      {branches.map((b) => (
                        <option key={b.name} value={b.name}>
                          {b.name}
                        </option>
                      ))}
                    </select>
                  </div>
                  <input
                    type="text"
                    data-testid="draft-branch-input"
                    value={draft.branchName}
                    onChange={(e) => setDraft((prev) => ({ ...prev, branchName: e.target.value }))}
                    placeholder="branch name (optional)"
                    className="w-44 rounded-md border border-[var(--border)] bg-[var(--bg-base)] px-2 py-1 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                  />
                </div>
              )}
            </div>
          </div>

          {/* Starter Prompt Chips */}
          <div className="flex flex-wrap justify-center gap-2">
            {STARTER_PROMPTS.map((sp) => (
              <button
                key={sp}
                type="button"
                onClick={() => setComposerPrefill(sp)}
                className="rounded-full border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-1 text-[11.5px] text-[var(--text-secondary)] hover:border-[var(--accent)]/50 hover:text-[var(--text-primary)] transition cursor-pointer"
              >
                {sp}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Bottom Draft Composer */}
      <Composer
        mode="draft"
        hostId={targetHostId}
        cwd={draft.cwd || selectedProject?.path || ''}
        draftConfig={draft}
        onDraftChange={(patch) => setDraft((prev) => ({ ...prev, ...patch }))}
        onCreateDraft={handleCreateDraft}
        openModelPickerSignal={openModelPickerSignal}
      />
    </div>
  );
};
