import type { AgentProfile, DaemonInfo, Project } from '@agentc/protocol';

export type RightTabKind =
  | 'diff'
  | 'files'
  | 'side'
  | 'artifacts'
  | 'pr'
  | 'git'
  | 'terminal'
  | 'browser';

export interface RightTab {
  id: string;
  kind: RightTabKind;
  title: string;
  hostId?: string;
  diffScope?: 'uncommitted' | 'latest' | 'turn' | 'session' | 'branch' | 'staged';
  turnIndex?: number;
  filePath?: string;
  browserUrl?: string;
  termId?: string;
}

export interface HostLike {
  info?: Pick<DaemonInfo, 'home'> | null;
  projects: Array<
    Pick<Project, 'id' | 'path' | 'defaultAgent' | 'defaultModel' | 'defaultCheckout'> & {
      name?: string;
    }
  >;
  agents: Array<Pick<AgentProfile, 'id' | 'available'> & { name?: string }>;
}

export interface DraftLike {
  hostId: string;
  projectId?: string;
  cwd: string;
  agent: string;
  model: string;
  mode: string;
  thought: string;
  skipPermissions: boolean;
  useWorktree: boolean;
  baseBranch: string;
  branchName: string;
}

export interface ResolveHostScopedCwdOptions {
  hostId: string;
  tabHostId?: string;
  tabFilePath?: string;
  activeSession?: { cwd?: string; worktreePath?: string } | null;
  activeSessionHostId?: string;
  draft?: { hostId: string; cwd: string } | null;
  hostHome?: string;
  fallback?: string;
}

/**
 * Resolves a panel working directory strictly scoped to `hostId`, preventing
 * foreign-host or unstamped `tab.filePath` and `draft.cwd` values from leaking across paired hosts.
 */
export function resolveHostScopedCwd(opts: ResolveHostScopedCwdOptions): string {
  const fb = opts.fallback !== undefined ? opts.fallback : '~';
  if (!opts.hostId) return fb;

  if (opts.tabFilePath && Boolean(opts.tabHostId) && opts.tabHostId === opts.hostId) {
    return opts.tabFilePath;
  }

  if (opts.activeSession && (!opts.activeSessionHostId || opts.activeSessionHostId === opts.hostId)) {
    const sessPath = opts.activeSession.worktreePath || opts.activeSession.cwd;
    if (sessPath && sessPath.trim()) {
      return sessPath.trim();
    }
  }

  if (opts.draft && opts.draft.hostId === opts.hostId && opts.draft.cwd.trim()) {
    return opts.draft.cwd.trim();
  }

  if (opts.hostHome && opts.hostHome.trim()) {
    return opts.hostHome.trim();
  }

  return fb;
}

/**
 * Canonicalizes singleton right-panel tab IDs when `tab.id` is omitted while keeping
 * unique IDs for `terminal` tabs and per-turn `diff-turn-*` tabs.
 */
export function canonicalRightTabId(tab: Omit<RightTab, 'id'> & { id?: string }): string | null {
  const explicit = tab.id?.trim();
  if (explicit) return explicit;

  if (tab.kind === 'terminal') {
    return tab.termId ? `term-${tab.termId}` : null;
  }
  if (tab.kind === 'diff') {
    return tab.turnIndex !== undefined ? `diff-turn-${tab.turnIndex}` : 'diff';
  }
  return tab.kind;
}

/**
 * Pure right-panel tab upsert helper that deduplicates singleton tabs, stamps `hostId`,
 * and clears omitted `filePath` / `turnIndex` so foreign-host paths never leak across hosts.
 */
export function upsertRightTab(
  prevTabs: RightTab[],
  tab: Omit<RightTab, 'id'> & { id?: string },
  activeHostId: string,
): { tabs: RightTab[]; activeId: string } {
  const stampedHostId = tab.hostId ?? activeHostId;
  const canonicalId = canonicalRightTabId(tab);
  const existing = canonicalId ? prevTabs.find((t) => t.id === canonicalId) : undefined;

  if (existing) {
    const crossHost = Boolean(existing.hostId && stampedHostId && existing.hostId !== stampedHostId);
    const nextFilePath = crossHost && !tab.hostId ? undefined : tab.filePath;
    const nextTermId = tab.termId ?? (!crossHost ? existing.termId : undefined);
    const nextBrowserUrl = tab.browserUrl ?? existing.browserUrl;

    const updated: RightTab = {
      id: existing.id,
      kind: tab.kind,
      title: tab.title,
      hostId: stampedHostId,
      ...(tab.kind === 'diff'
        ? { diffScope: tab.diffScope ?? 'uncommitted' }
        : tab.diffScope
          ? { diffScope: tab.diffScope }
          : {}),
      ...(tab.turnIndex !== undefined ? { turnIndex: tab.turnIndex } : {}),
      ...(nextFilePath !== undefined ? { filePath: nextFilePath } : {}),
      ...(nextBrowserUrl !== undefined ? { browserUrl: nextBrowserUrl } : {}),
      ...(nextTermId !== undefined ? { termId: nextTermId } : {}),
    };

    return {
      tabs: prevTabs.map((t) => (t.id === existing.id ? updated : t)),
      activeId: existing.id,
    };
  }

  let targetId = canonicalId ?? `${tab.kind}-${Date.now()}`;
  let suffix = 1;
  while (prevTabs.some((t) => t.id === targetId)) {
    targetId = `${canonicalId ?? tab.kind}-${Date.now()}-${suffix++}`;
  }

  const nextTab: RightTab = {
    ...tab,
    id: targetId,
    hostId: stampedHostId,
  };

  return {
    tabs: [...prevTabs, nextTab],
    activeId: targetId,
  };
}

/**
 * Clears pinned `filePath` and updates `hostId` on workspace-following tabs (such as `Files`)
 * when switching active session, draft, or host, while keeping `terminal` tabs pinned to their own `hostId`.
 */
export function clearWorkspacePinnedTabs(tabs: RightTab[], nextHostId: string): RightTab[] {
  if (tabs.length === 0) return tabs;
  let changed = false;
  const next = tabs.map((t) => {
    if (t.kind === 'terminal') {
      return t;
    }
    const targetHostId = nextHostId || t.hostId;
    if (t.filePath === undefined && t.hostId === targetHostId) {
      return t;
    }
    changed = true;
    const { filePath: _cleared, ...rest } = t;
    return {
      ...rest,
      hostId: targetHostId,
    };
  });
  return changed ? next : tabs;
}

/**
 * Computes the next DraftConfig when switching the active host so `draft.hostId`
 * and `activeHostId` never diverge and foreign-host paths/projects are cleared.
 */
export function computeDraftForHostSwitch<T extends DraftLike>(
  prevDraft: T,
  nextHostId: string,
  hosts: Record<string, HostLike>,
): T {
  if (!nextHostId) {
    return {
      ...prevDraft,
      hostId: '',
      projectId: undefined,
      cwd: '',
      baseBranch: '',
      branchName: '',
    };
  }

  const sameHost = prevDraft.hostId === nextHostId;
  if (sameHost && prevDraft.cwd.trim()) {
    return prevDraft;
  }

  const h = hosts[nextHostId];
  const firstProj = h?.projects?.[0];
  const nextCwd =
    (sameHost && prevDraft.cwd.trim()) ||
    firstProj?.path ||
    h?.info?.home ||
    '~';
  const nextProjectId = sameHost && prevDraft.projectId ? prevDraft.projectId : firstProj?.id;

  const hasCurrentAgent =
    Boolean(prevDraft.agent) &&
    (!h?.agents?.length || h.agents.some((a) => a.id === prevDraft.agent && a.available));

  const nextAgent = hasCurrentAgent
    ? prevDraft.agent
    : firstProj?.defaultAgent ||
      h?.agents?.find((a) => a.available && a.id === 'claude')?.id ||
      h?.agents?.find((a) => a.available && a.id === 'codex')?.id ||
      h?.agents?.find((a) => a.available && a.id === 'agy')?.id ||
      h?.agents?.find((a) => a.available)?.id ||
      prevDraft.agent ||
      'claude';

  return {
    ...prevDraft,
    hostId: nextHostId,
    projectId: nextProjectId,
    cwd: nextCwd,
    agent: nextAgent,
    model: nextAgent !== prevDraft.agent ? firstProj?.defaultModel || '' : prevDraft.model,
    useWorktree: sameHost ? prevDraft.useWorktree : firstProj?.defaultCheckout === 'worktree',
    baseBranch: sameHost ? prevDraft.baseBranch : '',
    branchName: sameHost ? prevDraft.branchName : '',
  };
}

export function isTrashUnsupportedError(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err ?? '');
  return /TRASH_UNSUPPORTED/i.test(msg);
}

function cleanPosixSegments(parts: string[]): string[] {
  const stack: string[] = [];
  for (const seg of parts) {
    if (!seg || seg === '.') continue;
    if (seg === '..') {
      stack.pop();
    } else {
      stack.push(seg);
    }
  }
  return stack;
}

/**
 * Canonicalizes a POSIX path (collapsing `//`, resolving `.` and `..`, expanding `~` / `~/...`,
 * and resolving relative segments against `hostHome` matching daemon `fsops.Resolve`).
 */
export function normalizePosixPath(rawPath: string, hostHome?: string): string {
  let p = (rawPath ?? '').trim();
  if (p.startsWith('file://')) {
    p = p.slice('file://'.length).trim();
  }

  const rawHome = (hostHome ?? '').trim();
  const normHome =
    rawHome && rawHome.startsWith('/')
      ? '/' + cleanPosixSegments(rawHome.split('/')).join('/')
      : '';

  if (!p) {
    return normHome || '';
  }
  if (p === '~') {
    return normHome || '~';
  }
  if (p.startsWith('~/')) {
    const rel = p.slice(2);
    if (normHome) {
      return '/' + cleanSegmentsOrRoot(`${normHome}/${rel}`);
    }
    return normalizeRelativeToTilde(rel);
  }
  if (p.startsWith('/')) {
    return '/' + cleanSegmentsOrRoot(p);
  }

  if (normHome) {
    return '/' + cleanSegmentsOrRoot(`${normHome}/${p}`);
  }
  return normalizeRelativeToTilde(p);
}

function cleanSegmentsOrRoot(absPath: string): string {
  return cleanPosixSegments(absPath.split('/')).join('/');
}

function normalizeRelativeToTilde(rel: string): string {
  const stack: string[] = [];
  let upCount = 0;
  for (const seg of rel.split('/')) {
    if (!seg || seg === '.') continue;
    if (seg === '..') {
      if (stack.length > 0) stack.pop();
      else upCount++;
    } else {
      stack.push(seg);
    }
  }
  if (upCount === 0) {
    return stack.length === 0 ? '~' : `~/${stack.join('/')}`;
  }
  return `~/${[...Array(upCount).fill('..'), ...stack].join('/')}`;
}

export function isProtectedDeletePath(entryPath: string, hostHome?: string): boolean {
  const trimmed = (entryPath ?? '').trim();
  if (!trimmed) return true;
  const stripped = trimmed.startsWith('file://') ? trimmed.slice('file://'.length).trim() : trimmed;
  // Reject any raw '..' parent-traversal path segment in delete targets (prevents symlink/../ parent escape)
  if (stripped.split('/').some((seg) => seg === '..')) {
    return true;
  }
  const norm = normalizePosixPath(trimmed, hostHome);
  if (!norm || norm === '/' || norm === '~') {
    return true;
  }
  if (hostHome && hostHome.trim()) {
    const normHome = normalizePosixPath(hostHome, hostHome);
    if (normHome && norm === normHome) {
      return true;
    }
  }
  return false;
}
