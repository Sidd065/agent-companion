import { describe, expect, it } from 'vitest';
import {
  clearWorkspacePinnedTabs,
  computeDraftForHostSwitch,
  isProtectedDeletePath,
  isTrashUnsupportedError,
  normalizePosixPath,
  resolveHostScopedCwd,
  upsertRightTab,
  type DraftLike,
  type HostLike,
  type RightTab,
} from './multiHost.js';

describe('multiHost utilities', () => {
  it('resolveHostScopedCwd blocks cross-host and unstamped tab.filePath & draft.cwd leaks', () => {
    expect(
      resolveHostScopedCwd({
        hostId: 'host-b',
        tabHostId: 'host-a',
        tabFilePath: '/home/host-a/project',
        draft: { hostId: 'host-a', cwd: '/home/host-a/project' },
      }),
    ).toBe('~');

    // Unstamped tabFilePath (tabHostId undefined or empty) MUST NOT be trusted (CHAL-4)
    expect(
      resolveHostScopedCwd({
        hostId: 'host-b',
        tabHostId: undefined,
        tabFilePath: '/a/unstamped-path',
        hostHome: '/home/host-b',
      }),
    ).toBe('/home/host-b');

    expect(
      resolveHostScopedCwd({
        hostId: 'host-b',
        tabHostId: '',
        tabFilePath: '/a/unstamped-path',
      }),
    ).toBe('~');

    expect(
      resolveHostScopedCwd({
        hostId: 'host-b',
        tabHostId: 'host-b',
        tabFilePath: '/home/host-b/repo',
        draft: { hostId: 'host-a', cwd: '/home/host-a/project' },
      }),
    ).toBe('/home/host-b/repo');

    expect(
      resolveHostScopedCwd({
        hostId: 'host-b',
        draft: { hostId: 'host-b', cwd: '/home/host-b/work' },
      }),
    ).toBe('/home/host-b/work');

    expect(
      resolveHostScopedCwd({
        hostId: 'host-b',
        draft: { hostId: 'host-b', cwd: '' },
        hostHome: '/home/host-b',
      }),
    ).toBe('/home/host-b');

    expect(
      resolveHostScopedCwd({
        hostId: 'host-b',
        draft: { hostId: 'host-a', cwd: '/home/host-a/repo' },
        fallback: '',
      }),
    ).toBe('');
  });

  it('upsertRightTab deduplicates singleton tabs, clears omitted filePath/turnIndex across hosts, and keeps unique terminal/turn-diff tabs', () => {
    // 1. Open Files on Host A with explicit filePath (e.g. ThreadInfoCard cwd click)
    const step1 = upsertRightTab(
      [],
      { id: 'files', kind: 'files', title: 'Files', filePath: '/home/alice/proj-a' },
      'host-a',
    );
    expect(step1.activeId).toBe('files');
    expect(step1.tabs).toEqual([
      {
        id: 'files',
        kind: 'files',
        title: 'Files',
        hostId: 'host-a',
        filePath: '/home/alice/proj-a',
      },
    ]);

    // 2. Switch to Host B and re-open Files via Command Palette (id & filePath omitted!) -> NO duplicate tab & NO leaked Host A filePath!
    const step2 = upsertRightTab(
      step1.tabs,
      { kind: 'files', title: 'Files' },
      'host-b',
    );
    expect(step2.activeId).toBe('files');
    expect(step2.tabs).toHaveLength(1);
    expect(step2.tabs[0]).toEqual({
      id: 'files',
      kind: 'files',
      title: 'Files',
      hostId: 'host-b',
    });
    expect(step2.tabs[0].filePath).toBeUndefined();

    // 3. Per-turn diff vs workspace singleton diff do not collide and omitted turnIndex is cleared
    const step3 = upsertRightTab(
      step2.tabs,
      { kind: 'diff', title: 'Turn #2 Diff', diffScope: 'turn', turnIndex: 2 },
      'host-b',
    );
    expect(step3.activeId).toBe('diff-turn-2');
    expect(step3.tabs).toHaveLength(2);

    const step4 = upsertRightTab(
      step3.tabs,
      { kind: 'diff', title: 'Diff', diffScope: 'uncommitted' },
      'host-b',
    );
    expect(step4.activeId).toBe('diff');
    expect(step4.tabs).toHaveLength(3);
    expect(step4.tabs.find((t) => t.id === 'diff')?.turnIndex).toBeUndefined();

    // 4. Terminal tabs without id remain unique while termId-stamped terminal tabs deduplicate by term-<id>
    const termA = upsertRightTab(step4.tabs, { kind: 'terminal', title: 'Terminal' }, 'host-b');
    const termB = upsertRightTab(termA.tabs, { kind: 'terminal', title: 'Terminal' }, 'host-b');
    expect(termB.tabs.filter((t) => t.kind === 'terminal')).toHaveLength(2);

    const cliTerm1 = upsertRightTab(
      termB.tabs,
      { id: 'term-t99', kind: 'terminal', title: 'codex · fix', termId: 't99', hostId: 'host-a' },
      'host-b',
    );
    expect(cliTerm1.tabs.find((t) => t.id === 'term-t99')?.hostId).toBe('host-a');
  });

  it('clearWorkspacePinnedTabs resets workspace-following filePath/hostId while keeping terminal tabs host-pinned', () => {
    const initial: RightTab[] = [
      { id: 'files', kind: 'files', title: 'Files', hostId: 'host-a', filePath: '/home/alice/proj' },
      { id: 'term-t1', kind: 'terminal', title: 'codex · task', hostId: 'host-a', termId: 't1' },
      { id: 'git', kind: 'git', title: 'Source Control', hostId: 'host-a' },
    ];

    const afterSwitch = clearWorkspacePinnedTabs(initial, 'host-b');
    expect(afterSwitch[0]).toEqual({
      id: 'files',
      kind: 'files',
      title: 'Files',
      hostId: 'host-b',
    });
    expect(afterSwitch[0].filePath).toBeUndefined();
    // Terminal tab MUST stay bound to host-a!
    expect(afterSwitch[1]).toEqual({
      id: 'term-t1',
      kind: 'terminal',
      title: 'codex · task',
      hostId: 'host-a',
      termId: 't1',
    });
    expect(afterSwitch[2].hostId).toBe('host-b');
  });

  it('computeDraftForHostSwitch resets cross-host cwd/projectId and handles empty prev.cwd', () => {
    const hosts: Record<string, HostLike> = {
      'host-a': {
        info: { home: '/home/alice' },
        projects: [{ id: 'pa', name: 'proj-a', path: '/home/alice/proj-a' }],
        agents: [{ id: 'claude', name: 'Claude', available: true }],
      },
      'host-b': {
        info: { home: '/home/bob' },
        projects: [],
        agents: [{ id: 'codex', name: 'Codex', available: true }],
      },
    };

    const initialDraft: DraftLike = {
      hostId: 'host-a',
      projectId: 'pa',
      cwd: '/home/alice/proj-a',
      agent: 'claude',
      model: 'claude-sonnet-4-5',
      mode: 'supervised',
      thought: 'medium',
      skipPermissions: false,
      useWorktree: true,
      baseBranch: 'main',
      branchName: 'feat-a',
    };

    const switched = computeDraftForHostSwitch(initialDraft, 'host-b', hosts);
    expect(switched.hostId).toBe('host-b');
    expect(switched.projectId).toBeUndefined();
    expect(switched.cwd).toBe('/home/bob');
    expect(switched.agent).toBe('codex');
    expect(switched.model).toBe('');
    expect(switched.useWorktree).toBe(false);
    expect(switched.baseBranch).toBe('');
    expect(switched.branchName).toBe('');

    // Empty cwd on same host populates from projects[0] instead of staying ''
    const emptyOnA = computeDraftForHostSwitch({ ...switched, hostId: 'host-a', cwd: '' }, 'host-a', hosts);
    expect(emptyOnA.cwd).toBe('/home/alice/proj-a');
    expect(emptyOnA.projectId).toBe('pa');
  });

  it('detects TRASH_UNSUPPORTED errors accurately', () => {
    expect(
      isTrashUnsupportedError(
        new Error('TRASH_UNSUPPORTED: system trash unavailable on this host (gio not found); confirm permanent delete'),
      ),
    ).toBe(true);
    expect(isTrashUnsupportedError('trash_unsupported: headless')).toBe(true);
    expect(isTrashUnsupportedError(new Error('permission denied'))).toBe(false);
    expect(isTrashUnsupportedError(null)).toBe(false);
  });

  it('canonicalizes POSIX dot-segments, traversals, double slashes, and relative aliases in isProtectedDeletePath (CHAL-2)', () => {
    expect(normalizePosixPath('/home/user/../../home/user', '/home/user')).toBe('/home/user');
    expect(normalizePosixPath('/home/user/../user', '/home/user')).toBe('/home/user');
    expect(normalizePosixPath('/.', '/home/user')).toBe('/');
    expect(normalizePosixPath('/..', '/home/user')).toBe('/');
    expect(normalizePosixPath('~/.', '/home/user')).toBe('/home/user');
    expect(normalizePosixPath('~/../user', '/home/user')).toBe('/home/user');
    expect(normalizePosixPath('.', '/home/user')).toBe('/home/user');

    // Original protected paths
    expect(isProtectedDeletePath('/', '/home/siddharth')).toBe(true);
    expect(isProtectedDeletePath('~', '/home/siddharth')).toBe(true);
    expect(isProtectedDeletePath('', '/home/siddharth')).toBe(true);
    expect(isProtectedDeletePath('/home/siddharth', '/home/siddharth')).toBe(true);
    expect(isProtectedDeletePath('/home/siddharth/', '/home/siddharth')).toBe(true);

    // All 8 CHAL-2 boundary cases
    expect(isProtectedDeletePath('/home/user/../home/user', '/home/user')).toBe(true);
    expect(isProtectedDeletePath('/home/user/.', '/home/user')).toBe(true);
    expect(isProtectedDeletePath('/home//user', '/home/user')).toBe(true);
    expect(isProtectedDeletePath('/.', '/home/user')).toBe(true);
    expect(isProtectedDeletePath('/..', '/home/user')).toBe(true);
    expect(isProtectedDeletePath('~/.', '/home/user')).toBe(true);
    expect(isProtectedDeletePath('~/../user', '/home/user')).toBe(true);
    expect(isProtectedDeletePath('.', '/home/user')).toBe(true);

    // Allowed child & sibling paths
    expect(isProtectedDeletePath('/home/siddharth/file.txt', '/home/siddharth')).toBe(false);
    expect(isProtectedDeletePath('/home/siddharth2', '/home/siddharth')).toBe(false);
    expect(isProtectedDeletePath('~/notes.txt', '/home/user')).toBe(false);
    expect(isProtectedDeletePath('sub/file.txt', '/home/user')).toBe(false);
    expect(isProtectedDeletePath('/home/user/.env', '/home/user')).toBe(false);
    expect(isProtectedDeletePath('/home/user/archive..tar.gz', '/home/user')).toBe(false);
  });
});
