import React, { useEffect, useRef, useState } from 'react';
import { Folder, FolderGit2, FolderPlus, GitBranch, Globe, Loader2, X } from 'lucide-react';
import type { FsEntry, Project } from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';

type Mode = 'local' | 'init' | 'clone';

export const AddProjectModal: React.FC = () => {
  const {
    addProjectModalOpen,
    setAddProjectModalOpen,
    hostList,
    activeHostId,
    viewMode,
    draft,
    callHost,
    openDraft,
    pushToast,
  } = useApp();

  const [mode, setMode] = useState<Mode>('local');
  const [hostId, setHostId] = useState('');
  const [folderPath, setFolderPath] = useState('');
  const [resolvedParent, setResolvedParent] = useState('');
  const [entries, setEntries] = useState<FsEntry[]>([]);
  const [projectName, setProjectName] = useState('');
  const [cloneUrl, setCloneUrl] = useState('');
  const [parentDir, setParentDir] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  // Initialize modal state ONLY on false -> true transition so background host events never wipe user input
  const wasOpenRef = useRef(false);
  useEffect(() => {
    if (!addProjectModalOpen) {
      wasOpenRef.current = false;
      return;
    }
    if (wasOpenRef.current) return;
    wasOpenRef.current = true;

    const hId =
      (viewMode === 'draft' && draft.hostId ? draft.hostId : activeHostId) ||
      activeHostId ||
      hostList[0]?.creds.id ||
      '';
    setHostId(hId);
    const home = hostList.find((h) => h.creds.id === hId)?.info?.home || '~';
    setFolderPath(home);
    setParentDir(home);
    setResolvedParent('');
    setProjectName('');
    setCloneUrl('');
    setError('');
  }, [addProjectModalOpen, viewMode, draft.hostId, activeHostId, hostList]);

  useEffect(() => {
    if (!addProjectModalOpen || mode !== 'local' || !hostId || !folderPath) return;
    let cancelled = false;
    void callHost<{ path?: string; parent?: string; entries: FsEntry[] }>(hostId, 'fs.list', {
      path: folderPath,
      hidden: false,
    })
      .then((res) => {
        if (cancelled) return;
        if (folderPath === '~' && res?.path) {
          setFolderPath(res.path);
          setParentDir((prev) => (prev === '~' || !prev ? res.path! : prev));
        }
        setResolvedParent(res?.parent || '');
        setEntries((res?.entries ?? []).filter((e) => e.type === 'dir' || e.targetType === 'dir'));
      })
      .catch(() => {
        if (!cancelled) {
          setEntries([]);
          setResolvedParent('');
        }
      });
    return () => {
      cancelled = true;
    };
  }, [addProjectModalOpen, mode, hostId, folderPath, callHost]);

  if (!addProjectModalOpen) return null;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!hostId) return;
    setBusy(true);
    setError('');
    try {
      let proj: Project;
      if (mode === 'local') {
        proj = await callHost<Project>(hostId, 'projects.add', { path: folderPath, name: projectName });
      } else if (mode === 'init') {
        proj = await callHost<Project>(hostId, 'projects.init', {
          parentDir,
          name: projectName,
          initialCommit: true,
        });
      } else {
        proj = await callHost<Project>(hostId, 'projects.clone', {
          url: cloneUrl,
          parentDir,
          dirName: projectName,
        });
      }
      pushToast({ text: `Added project "${proj.name}"`, level: 'success' });
      setAddProjectModalOpen(false);
      openDraft({ hostId, projectId: proj.id, cwd: proj.path });
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      data-testid="add-project-modal"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
      onClick={() => setAddProjectModalOpen(false)}
    >
      <div
        className="w-full max-w-xl rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] shadow-2xl overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-[var(--border)]">
          <div className="flex items-center gap-2">
            <FolderGit2 className="w-4 h-4 text-[var(--accent)]" />
            <h2 className="text-sm font-semibold">Add Project</h2>
          </div>
          <button
            type="button"
            onClick={() => setAddProjectModalOpen(false)}
            className="p-1 rounded hover:bg-[var(--bg-hover)] text-[var(--text-muted)] cursor-pointer"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <form onSubmit={(e) => void submit(e)} className="p-5 space-y-4">
          {/* Source selector tabs matching T3 */}
          <div className="grid grid-cols-3 gap-2">
            {[
              { id: 'local' as const, label: 'Local folder', icon: Folder },
              { id: 'init' as const, label: 'New Git repo', icon: FolderPlus },
              { id: 'clone' as const, label: 'Clone URL / GitHub', icon: Globe },
            ].map((item) => (
              <button
                key={item.id}
                type="button"
                data-testid={`add-project-tab-${item.id}`}
                onClick={() => setMode(item.id)}
                className={`flex items-center justify-center gap-1.5 py-2 px-3 rounded-lg border text-xs font-medium cursor-pointer ${
                  mode === item.id
                    ? 'bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)]'
                    : 'bg-[var(--bg-base)] border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                }`}
              >
                <item.icon className="w-3.5 h-3.5" />
                {item.label}
              </button>
            ))}
          </div>

          {hostList.length > 1 && (
            <div>
              <label className="block text-xs text-[var(--text-secondary)] mb-1">Host</label>
              <select
                data-testid="add-project-host-select"
                value={hostId}
                onChange={(e) => {
                  const id = e.target.value;
                  setHostId(id);
                  const home = hostList.find((h) => h.creds.id === id)?.info?.home || '~';
                  setFolderPath(home);
                  setParentDir(home);
                  setResolvedParent('');
                }}
                className="w-full px-3 py-2 text-xs rounded-lg bg-[var(--bg-base)] border border-[var(--border)] text-[var(--text-primary)]"
              >
                {hostList.map((h) => (
                  <option key={h.creds.id} value={h.creds.id}>
                    {h.creds.name} ({h.state})
                  </option>
                ))}
              </select>
            </div>
          )}

          {mode === 'local' && (
            <div className="space-y-2.5">
              <div>
                <label className="block text-xs text-[var(--text-secondary)] mb-1">Directory path on host</label>
                <input
                  type="text"
                  data-testid="project-path-input"
                  value={folderPath}
                  onChange={(e) => setFolderPath(e.target.value)}
                  placeholder="/home/user/my-project"
                  className="w-full px-3 py-2 text-xs font-mono rounded-lg bg-[var(--bg-base)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                />
              </div>
              {(entries.length > 0 || (resolvedParent && folderPath !== '/')) && (
                <div className="max-h-44 overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--bg-base)] divide-y divide-[var(--border)]">
                  {folderPath !== '/' && (
                    <button
                      type="button"
                      data-testid="add-project-parent-btn"
                      onClick={() =>
                        setFolderPath(resolvedParent || folderPath.replace(/\/[^/]+\/?$/, '') || '/')
                      }
                      className="w-full text-left px-3 py-1.5 text-xs text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer font-mono"
                    >
                      ..
                    </button>
                  )}
                  {entries.map((e) => (
                    <button
                      key={e.path}
                      type="button"
                      onClick={() => setFolderPath(e.path)}
                      className="w-full flex items-center gap-2 text-left px-3 py-1.5 text-xs text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                    >
                      <Folder className="w-3.5 h-3.5 text-[var(--accent)] shrink-0" />
                      <span className="truncate">{e.name}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}

          {mode === 'init' && (
            <div className="space-y-2.5">
              <div>
                <label className="block text-xs text-[var(--text-secondary)] mb-1">Project name</label>
                <input
                  type="text"
                  data-testid="project-init-name"
                  value={projectName}
                  onChange={(e) => setProjectName(e.target.value)}
                  placeholder="my-awesome-app"
                  className="w-full px-3 py-2 text-xs rounded-lg bg-[var(--bg-base)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                  required
                />
              </div>
              <div>
                <label className="block text-xs text-[var(--text-secondary)] mb-1">Parent folder</label>
                <input
                  type="text"
                  data-testid="project-init-parent"
                  value={parentDir}
                  onChange={(e) => setParentDir(e.target.value)}
                  className="w-full px-3 py-2 text-xs font-mono rounded-lg bg-[var(--bg-base)] border border-[var(--border)] text-[var(--text-primary)]"
                />
              </div>
            </div>
          )}

          {mode === 'clone' && (
            <div className="space-y-2.5">
              <div>
                <label className="block text-xs text-[var(--text-secondary)] mb-1">Git URL or GitHub owner/repo</label>
                <div className="relative">
                  <GitBranch className="w-3.5 h-3.5 absolute left-3 top-2.5 text-[var(--text-muted)]" />
                  <input
                    type="text"
                    value={cloneUrl}
                    onChange={(e) => setCloneUrl(e.target.value)}
                    placeholder="owner/repo or https://github.com/owner/repo.git"
                    className="w-full pl-8 pr-3 py-2 text-xs font-mono rounded-lg bg-[var(--bg-base)] border border-[var(--border)] text-[var(--text-primary)]"
                    required
                  />
                </div>
              </div>
              <div>
                <label className="block text-xs text-[var(--text-secondary)] mb-1">Clone into folder</label>
                <input
                  type="text"
                  value={parentDir}
                  onChange={(e) => setParentDir(e.target.value)}
                  className="w-full px-3 py-2 text-xs font-mono rounded-lg bg-[var(--bg-base)] border border-[var(--border)] text-[var(--text-primary)]"
                />
              </div>
            </div>
          )}

          {error && <div className="text-xs text-[var(--danger)]">{error}</div>}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={() => setAddProjectModalOpen(false)}
              className="px-3.5 py-1.5 rounded-lg border border-[var(--border)] text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="submit"
              data-testid="add-project-submit"
              disabled={busy}
              className="px-4 py-1.5 rounded-lg bg-[var(--accent)] text-[var(--accent-fg)] text-xs font-semibold disabled:opacity-50 cursor-pointer flex items-center gap-1.5"
            >
              {busy && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
              {mode === 'local' ? 'Add Folder' : mode === 'init' ? 'Create Repo' : 'Clone Repo'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
