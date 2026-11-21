import React, { useCallback, useEffect, useState } from 'react';
import {
  ChevronUp,
  Edit3,
  Eye,
  File,
  FilePlus,
  Folder,
  FolderPlus,
  MessageSquarePlus,
  RefreshCw,
  Save,
  Search,
  Trash2,
} from 'lucide-react';
import type { FsEntry } from '@agentc/protocol';
import { useApp, type RightTab } from '../../state/appStore.js';
import { MarkdownRenderer } from '../markdown/MarkdownRenderer.js';

interface ReadResult {
  path: string;
  size: number;
  mtime: number;
  text?: string;
  base64?: string;
  binary: boolean;
  truncated: boolean;
  mime: string;
}

export const FilesPanel: React.FC<{ tab: RightTab }> = ({ tab }) => {
  const { activeHost, activeSession, draft, callHost, addComposerChip, pushToast } = useApp();
  const hostId = activeHost?.creds.id ?? '';
  const rootCwd = tab.filePath || activeSession?.worktreePath || activeSession?.cwd || draft.cwd || '~';

  const [dirPath, setDirPath] = useState(rootCwd);
  const [parentPath, setParentPath] = useState('');
  const [entries, setEntries] = useState<FsEntry[]>([]);
  const [showHidden, setShowHidden] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<FsEntry[] | null>(null);

  const [openFile, setOpenFile] = useState<ReadResult | null>(null);
  const [editMode, setEditMode] = useState(false);
  const [editText, setEditText] = useState('');
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(false);

  const loadDir = useCallback(
    async (target: string) => {
      if (!hostId || !target) return;
      setLoading(true);
      try {
        const res = await callHost<{ path: string; parent: string; entries: FsEntry[] }>(hostId, 'fs.list', {
          path: target,
          hidden: showHidden,
        });
        setDirPath(res.path || target);
        setParentPath(res.parent || '');
        setEntries(Array.isArray(res.entries) ? res.entries : []);
      } catch (e) {
        pushToast({ text: `List failed: ${(e as Error).message}`, level: 'error' });
      } finally {
        setLoading(false);
      }
    },
    [hostId, showHidden, callHost, pushToast],
  );

  useEffect(() => {
    void loadDir(rootCwd);
  }, [rootCwd, showHidden]); // eslint-disable-line react-hooks/exhaustive-deps

  const openPath = useCallback(
    async (p: string) => {
      if (!hostId) return;
      try {
        const res = await callHost<ReadResult>(hostId, 'fs.read', { path: p, max: 1024 * 1024 });
        setOpenFile(res);
        setEditText(res.text ?? '');
        setDirty(false);
        setEditMode(false);
      } catch (e) {
        pushToast({ text: `Read failed: ${(e as Error).message}`, level: 'error' });
      }
    },
    [hostId, callHost, pushToast],
  );

  const handleSave = useCallback(async () => {
    if (!hostId || !openFile) return;
    setSaving(true);
    try {
      const updated = await callHost<FsEntry>(hostId, 'fs.write', {
        path: openFile.path,
        text: editText,
        expectMtime: openFile.mtime,
      });
      setOpenFile((prev) => (prev ? { ...prev, text: editText, mtime: updated?.mtime ?? Date.now() } : prev));
      setDirty(false);
      pushToast({ text: `Saved ${openFile.path.split('/').pop()}`, level: 'success' });
    } catch (e) {
      pushToast({ text: `Save failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setSaving(false);
    }
  }, [hostId, openFile, editText, callHost, pushToast]);

  const handleSearch = async () => {
    const q = searchQuery.trim();
    if (!q || !hostId) {
      setSearchResults(null);
      return;
    }
    try {
      const res = await callHost<FsEntry[]>(hostId, 'fs.search', {
        root: dirPath,
        query: q,
        max: 100,
        hidden: showHidden,
      });
      setSearchResults(Array.isArray(res) ? res : []);
    } catch (e) {
      pushToast({ text: `Search failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleCreate = async (kind: 'file' | 'dir') => {
    const name = window.prompt(kind === 'file' ? 'New file name:' : 'New folder name:');
    if (!name?.trim() || !hostId) return;
    const full = `${dirPath.replace(/\/$/, '')}/${name.trim()}`;
    try {
      if (kind === 'file') {
        await callHost(hostId, 'fs.create', { path: full });
        await loadDir(dirPath);
        await openPath(full);
        setEditMode(true);
      } else {
        await callHost(hostId, 'fs.mkdir', { path: full, parents: true });
        await loadDir(dirPath);
      }
    } catch (e) {
      pushToast({ text: `Create failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const handleDelete = async (entry: FsEntry) => {
    if (!window.confirm(`Delete ${entry.name}?`)) return;
    try {
      await callHost(hostId, 'fs.delete', { paths: [entry.path], permanent: false });
      if (openFile?.path === entry.path) setOpenFile(null);
      await loadDir(dirPath);
      pushToast({ text: `Deleted ${entry.name}`, level: 'info' });
    } catch (e) {
      pushToast({ text: `Delete failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const listToDisplay = searchResults ?? entries;
  const isImage = Boolean(openFile?.mime?.startsWith('image/') && openFile?.base64);
  const isMarkdown = Boolean(openFile?.path.toLowerCase().endsWith('.md'));

  return (
    <div data-testid="files-panel" className="flex-1 flex min-h-0 text-xs">
      {/* Left: Remote File Tree */}
      <div className="w-56 shrink-0 border-r border-[var(--border)] bg-[var(--bg-sidebar)]/50 flex flex-col min-h-0">
        <div className="p-2 border-b border-[var(--border)] space-y-1.5">
          <div className="flex items-center gap-1">
            <button
              type="button"
              disabled={!parentPath}
              onClick={() => parentPath && void loadDir(parentPath)}
              title="Up directory"
              className="rounded p-1 hover:bg-[var(--bg-hover)] disabled:opacity-30 cursor-pointer"
            >
              <ChevronUp className="w-3.5 h-3.5" />
            </button>
            <div className="flex-1 truncate font-mono text-[10.5px] text-[var(--text-secondary)]" title={dirPath}>
              {dirPath}
            </div>
            <button
              type="button"
              onClick={() => void handleCreate('file')}
              title="New file"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <FilePlus className="w-3.5 h-3.5" />
            </button>
            <button
              type="button"
              onClick={() => void handleCreate('dir')}
              title="New folder"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <FolderPlus className="w-3.5 h-3.5" />
            </button>
            <button
              type="button"
              onClick={() => void loadDir(dirPath)}
              title="Refresh"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              <RefreshCw className={`w-3 h-3 ${loading ? 'animate-spin' : ''}`} />
            </button>
          </div>
          <div className="flex items-center gap-1">
            <div className="relative flex-1">
              <Search className="w-3 h-3 absolute left-2 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  if (!e.target.value.trim()) setSearchResults(null);
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void handleSearch();
                }}
                placeholder="Search files..."
                className="w-full rounded-md border border-[var(--border)] bg-[var(--bg-base)] pl-6 pr-2 py-1 text-[11px] outline-none focus:border-[var(--accent)]"
              />
            </div>
            <button
              type="button"
              onClick={() => setShowHidden((v) => !v)}
              title="Toggle hidden dotfiles"
              className={`rounded-md border px-1.5 py-1 text-[10px] cursor-pointer ${
                showHidden
                  ? 'border-[var(--accent)] text-[var(--accent)] bg-[var(--accent-soft)]'
                  : 'border-[var(--border)] text-[var(--text-muted)]'
              }`}
            >
              .*
            </button>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-1 space-y-0.5">
          {listToDisplay.map((entry) => {
            const isDir = entry.type === 'dir' || entry.targetType === 'dir';
            const selected = openFile?.path === entry.path;
            return (
              <div
                key={entry.path}
                className={`group flex items-center justify-between gap-1 rounded-md px-2 py-1 ${
                  selected ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]' : 'hover:bg-[var(--bg-hover)]'
                }`}
              >
                <button
                  type="button"
                  data-testid="file-tree-entry"
                  onClick={() => {
                    if (isDir) {
                      setSearchResults(null);
                      void loadDir(entry.path);
                    } else {
                      void openPath(entry.path);
                    }
                  }}
                  className="flex items-center gap-1.5 min-w-0 flex-1 text-left cursor-pointer"
                >
                  {isDir ? (
                    <Folder className="w-3.5 h-3.5 shrink-0 text-[var(--accent)]" />
                  ) : (
                    <File className="w-3.5 h-3.5 shrink-0 text-[var(--text-muted)]" />
                  )}
                  <span className="truncate font-mono text-[11px]">{entry.name}</span>
                </button>
                <button
                  type="button"
                  onClick={() => void handleDelete(entry)}
                  title="Delete"
                  className="opacity-0 group-hover:opacity-100 p-0.5 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                >
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            );
          })}
        </div>
      </div>

      {/* Right: File Viewer / Editor */}
      <div className="flex-1 flex flex-col min-w-0 min-h-0">
        {!openFile ? (
          <div className="flex-1 flex items-center justify-center text-[var(--text-muted)]">
            Select a file to inspect or edit.
          </div>
        ) : (
          <>
            <div className="flex items-center justify-between gap-2 border-b border-[var(--border)] bg-[var(--bg-surface)] px-3 py-1.5">
              <div className="font-mono text-[11.5px] truncate text-[var(--text-primary)]">
                {openFile.path} {dirty && <span className="text-[var(--warning)]">●</span>}
              </div>
              <div className="flex items-center gap-1.5 shrink-0">
                <button
                  type="button"
                  onClick={() => {
                    addComposerChip({
                      kind: 'file-comment',
                      label: openFile.path.split('/').pop() || openFile.path,
                      content: `Referencing file \`${openFile.path}\``,
                    });
                    pushToast({ text: 'File reference added to composer', level: 'info' });
                  }}
                  className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  <MessageSquarePlus className="w-3 h-3" />
                  Cite
                </button>
                {!openFile.binary && (
                  <button
                    type="button"
                    data-testid="files-toggle-edit-btn"
                    onClick={() => setEditMode((v) => !v)}
                    className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    {editMode ? <Eye className="w-3 h-3" /> : <Edit3 className="w-3 h-3" />}
                    {editMode ? 'Preview' : 'Edit'}
                  </button>
                )}
                {editMode && (
                  <button
                    type="button"
                    data-testid="files-save-btn"
                    disabled={saving || !dirty}
                    onClick={() => void handleSave()}
                    className="inline-flex items-center gap-1 rounded-md bg-[var(--accent)] px-2.5 py-0.5 text-[11px] font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                  >
                    <Save className="w-3 h-3" />
                    {saving ? 'Saving...' : 'Save'}
                  </button>
                )}
              </div>
            </div>

            <div className="flex-1 overflow-auto min-h-0">
              {isImage ? (
                <div className="p-4 flex items-center justify-center">
                  <img
                    src={`data:${openFile.mime};base64,${openFile.base64}`}
                    alt={openFile.path}
                    className="max-w-full max-h-[70vh] rounded-lg border border-[var(--border)]"
                  />
                </div>
              ) : openFile.binary ? (
                <div className="p-6 text-center text-[var(--text-muted)]">
                  Binary file ({openFile.mime || 'application/octet-stream'}, {openFile.size.toLocaleString()} bytes)
                </div>
              ) : editMode ? (
                <textarea
                  data-testid="files-editor-textarea"
                  value={editText}
                  onChange={(e) => {
                    setEditText(e.target.value);
                    setDirty(true);
                  }}
                  onKeyDown={(e) => {
                    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
                      e.preventDefault();
                      void handleSave();
                    }
                  }}
                  spellCheck={false}
                  className="w-full h-full resize-none bg-[var(--bg-base)] p-3 font-mono text-[12px] leading-relaxed text-[var(--text-primary)] outline-none"
                />
              ) : isMarkdown ? (
                <div className="p-4">
                  <MarkdownRenderer text={openFile.text ?? ''} citable />
                </div>
              ) : (
                <div className="font-mono text-[12px] leading-[1.55] p-2">
                  {(openFile.text ?? '').split('\n').map((ln, idx) => (
                    <div key={idx} className="group flex hover:bg-[var(--bg-hover)]/40">
                      <span className="w-10 shrink-0 select-none text-right pr-3 text-[10.5px] text-[var(--text-muted)]">
                        {idx + 1}
                      </span>
                      <button
                        type="button"
                        onClick={() => {
                          addComposerChip({
                            kind: 'file-comment',
                            label: `${openFile.path.split('/').pop()}:${idx + 1}`,
                            content: `\`${openFile.path}:${idx + 1}\`\n> ${ln}`,
                          });
                          pushToast({ text: `Added line ${idx + 1} to composer`, level: 'info' });
                        }}
                        title="Cite line in composer"
                        className="w-4 opacity-0 group-hover:opacity-100 text-[var(--accent)] cursor-pointer"
                      >
                        +
                      </button>
                      <span className="flex-1 whitespace-pre-wrap break-all text-[var(--text-primary)]">{ln}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
};
