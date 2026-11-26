import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ChevronUp,
  Download,
  Edit3,
  Eye,
  File,
  FilePlus,
  Folder,
  FolderPlus,
  Home,
  MessageSquarePlus,
  Pencil,
  RefreshCw,
  Save,
  Search,
  Server,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import type { FsEntry } from '@agentc/protocol';
import { useApp, type RightTab } from '../../state/appStore.js';
import {
  isProtectedDeletePath,
  isTrashUnsupportedError,
  normalizePosixPath,
  resolveHostScopedCwd,
} from '../../utils/multiHost.js';
import { MarkdownRenderer } from '../markdown/MarkdownRenderer.js';

export interface ReadResult {
  path: string;
  size: number;
  mtime: number;
  text?: string;
  base64?: string;
  binary: boolean;
  truncated: boolean;
  mime: string;
}

interface DownloadChunkResult {
  data: string;
  offset: number;
  size: number;
  eof: boolean;
  mtime: number;
  mime: string;
}

interface InlinePromptState {
  kind: 'file' | 'dir' | 'rename';
  target?: FsEntry;
  value: string;
}

interface DeletePromptState {
  entry: FsEntry;
  permanent: boolean;
  reason?: string;
}

const UPLOAD_CHUNK_BYTES = 256 * 1024;

function bytesToBase64(bytes: Uint8Array): string {
  let bin = '';
  const step = 0x8000;
  for (let i = 0; i < bytes.length; i += step) {
    bin += String.fromCharCode(...bytes.subarray(i, i + step));
  }
  return btoa(bin);
}

function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}

export function applySavedFileResult(
  prev: ReadResult | null,
  savedPath: string,
  savedText: string,
  updatedMtime: number | undefined,
  nowMs: number = Date.now(),
): ReadResult | null {
  return prev && prev.path === savedPath
    ? { ...prev, text: savedText, mtime: updatedMtime ?? nowMs }
    : prev;
}

export function shouldClearDirtyAfterSave(opts: {
  guardSeq: number;
  currentSeq: number;
  guardFileSeq: number;
  currentFileSeq: number;
  currentOpenPath: string | undefined;
  savedPath: string;
  currentEditText: string;
  savedText: string;
}): boolean {
  return (
    opts.guardSeq === opts.currentSeq &&
    opts.guardFileSeq === opts.currentFileSeq &&
    opts.currentOpenPath === opts.savedPath &&
    opts.currentEditText === opts.savedText
  );
}

export function applyRenamedOpenFile(
  prev: ReadResult | null,
  fromPath: string,
  toPath: string,
  hostHome = '',
): ReadResult | null {
  if (!prev) return null;
  const normFrom = normalizePosixPath(fromPath, hostHome);
  const normTo = normalizePosixPath(toPath, hostHome);
  const normOpen = normalizePosixPath(prev.path, hostHome);
  if (prev.path === fromPath || (normFrom && normOpen === normFrom)) {
    const resolvedTo =
      toPath.startsWith('/') || (hostHome.trim() && (toPath === '~' || toPath.startsWith('~/')))
        ? normTo
        : toPath;
    return { ...prev, path: resolvedTo };
  }
  if (normFrom && normFrom !== '/' && normOpen.startsWith(`${normFrom}/`)) {
    const suffix = normOpen.slice(normFrom.length);
    const rebased = normTo === '/' ? suffix : `${normTo}${suffix}`;
    return { ...prev, path: rebased };
  }
  return prev;
}

export function applyDeletedOpenFile(
  prev: ReadResult | null,
  deletedPath: string,
  hostHome: string,
): ReadResult | null {
  if (!prev) return null;
  const normEntry = normalizePosixPath(deletedPath, hostHome);
  const normOpen = normalizePosixPath(prev.path, hostHome);
  if (
    prev.path === deletedPath ||
    normOpen === normEntry ||
    (normEntry &&
      normEntry !== '/' &&
      (prev.path.startsWith(`${normEntry}/`) || normOpen.startsWith(`${normEntry}/`)))
  ) {
    return null;
  }
  return prev;
}

export interface FilesPanelSeqRefs {
  scopeSeq: { current: number };
  dirReqSeq: { current: number };
  fileReqSeq: { current: number };
  saveReqSeq: { current: number };
  uploadReqSeq: { current: number };
  deleteReqSeq: { current: number };
}

export function advanceFilesPanelDirSeq(refs: Pick<FilesPanelSeqRefs, 'dirReqSeq'>): number {
  refs.dirReqSeq.current += 1;
  return refs.dirReqSeq.current;
}

export function resetFilesPanelScopeSeq(refs: FilesPanelSeqRefs): number {
  refs.scopeSeq.current += 1;
  refs.dirReqSeq.current += 1;
  refs.fileReqSeq.current += 1;
  refs.saveReqSeq.current += 1;
  refs.uploadReqSeq.current += 1;
  refs.deleteReqSeq.current += 1;
  return refs.scopeSeq.current;
}

export function isFilesPanelSaveValid(opts: {
  guardScopeSeq: number;
  currentScopeSeq: number;
  guardFileSeq: number;
  currentFileSeq: number;
}): boolean {
  return opts.guardScopeSeq === opts.currentScopeSeq && opts.guardFileSeq === opts.currentFileSeq;
}

export function isFilesPanelUploadTargetCurrent(opts: {
  guardScopeSeq: number;
  currentScopeSeq: number;
  targetHostId: string;
  currentHostId: string;
}): boolean {
  return (
    Boolean(opts.targetHostId) &&
    opts.guardScopeSeq === opts.currentScopeSeq &&
    opts.targetHostId === opts.currentHostId
  );
}

export const FilesPanel: React.FC<{ tab: RightTab }> = ({ tab }) => {
  const {
    hostList,
    activeHost,
    setActiveHostId,
    activeSession,
    draft,
    rightTabFocusSeq,
    callHost,
    addComposerChip,
    pushToast,
  } = useApp();

  const hostId = activeHost?.creds.id ?? '';
  const hostIdRef = useRef(hostId);
  hostIdRef.current = hostId;
  const hostHome = activeHost?.info?.home ?? '';
  const hostHomeRef = useRef(hostHome);
  hostHomeRef.current = hostHome;
  const rootCwd = resolveHostScopedCwd({
    hostId,
    tabHostId: tab.hostId,
    tabFilePath: tab.filePath,
    activeSession,
    activeSessionHostId: hostId,
    draft,
    hostHome,
    fallback: '~',
  });

  const [dirPath, setDirPath] = useState(rootCwd);
  const dirPathRef = useRef(dirPath);
  dirPathRef.current = dirPath;

  const [parentPath, setParentPath] = useState('');
  const [entries, setEntries] = useState<FsEntry[]>([]);
  const [showHidden, setShowHidden] = useState(false);
  const showHiddenRef = useRef(showHidden);
  showHiddenRef.current = showHidden;

  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<FsEntry[] | null>(null);

  const [openFile, setOpenFile] = useState<ReadResult | null>(null);
  const openFileRef = useRef<ReadResult | null>(openFile);
  openFileRef.current = openFile;
  const [editMode, setEditMode] = useState(false);
  const [editText, setEditText] = useState('');
  const editTextRef = useRef(editText);
  editTextRef.current = editText;
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);

  const [inlinePrompt, setInlinePrompt] = useState<InlinePromptState | null>(null);
  const [deletePrompt, setDeletePrompt] = useState<DeletePromptState | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const scopeSeqRef = useRef(0);
  const dirReqSeqRef = useRef(0);
  const fileReqSeqRef = useRef(0);
  const saveReqSeqRef = useRef(0);
  const uploadReqSeqRef = useRef(0);
  const deleteReqSeqRef = useRef(0);
  const uploadInputRef = useRef<HTMLInputElement | null>(null);

  const loadDir = useCallback(
    async (target: string, explicitHostId?: string, explicitHidden?: boolean) => {
      const targetHost = explicitHostId ?? hostIdRef.current ?? hostId;
      const hiddenFlag = explicitHidden ?? showHiddenRef.current;
      if (!targetHost || !target) return;
      const seq = advanceFilesPanelDirSeq({ dirReqSeq: dirReqSeqRef });
      setLoading(true);
      try {
        const res = await callHost<{ path: string; parent: string; entries: FsEntry[] }>(
          targetHost,
          'fs.list',
          {
            path: target,
            hidden: hiddenFlag,
          },
        );
        if (seq !== dirReqSeqRef.current) return;
        const resolved = res.path || target;
        dirPathRef.current = resolved;
        setDirPath(resolved);
        setParentPath(res.parent || '');
        setEntries(Array.isArray(res.entries) ? res.entries : []);
      } catch (e) {
        if (seq !== dirReqSeqRef.current) return;
        setEntries([]);
        setParentPath('');
        pushToast({ text: `List failed: ${(e as Error).message}`, level: 'error' });
      } finally {
        if (seq === dirReqSeqRef.current) {
          setLoading(false);
        }
      }
    },
    [hostId, callHost, pushToast],
  );

  // Reset all host-bound state & reload directory whenever hostId or rootCwd changes
  useEffect(() => {
    resetFilesPanelScopeSeq({
      scopeSeq: scopeSeqRef,
      dirReqSeq: dirReqSeqRef,
      fileReqSeq: fileReqSeqRef,
      saveReqSeq: saveReqSeqRef,
      uploadReqSeq: uploadReqSeqRef,
      deleteReqSeq: deleteReqSeqRef,
    });
    setOpenFile(null);
    openFileRef.current = null;
    setEditMode(false);
    setEditText('');
    editTextRef.current = '';
    setDirty(false);
    setSaving(false);
    setLoading(false);
    setUploading(false);
    setSearchQuery('');
    setSearchResults(null);
    setEntries([]);
    setParentPath('');
    setInlinePrompt(null);
    setDeletePrompt(null);
    setDeleteBusy(false);
    dirPathRef.current = rootCwd;
    setDirPath(rootCwd);
    if (hostId && rootCwd) {
      void loadDir(rootCwd, hostId, showHiddenRef.current);
    }
  }, [hostId, rootCwd, tab.filePath, tab.filePath ? rightTabFocusSeq : 0, loadDir]);

  // Reload current navigated directory (not rootCwd!) when showHidden toggles
  const prevHiddenRef = useRef(showHidden);
  useEffect(() => {
    if (prevHiddenRef.current === showHidden) return;
    prevHiddenRef.current = showHidden;
    if (hostId) {
      void loadDir(dirPathRef.current || rootCwd, hostId, showHidden);
    }
  }, [showHidden, hostId, rootCwd, loadDir]);

  const openPath = useCallback(
    async (p: string, explicitHostId?: string): Promise<ReadResult | null> => {
      const targetHost = explicitHostId ?? hostIdRef.current ?? hostId;
      if (!targetHost) return null;
      const guardScopeSeq = scopeSeqRef.current;
      const fileSeq = ++fileReqSeqRef.current;
      try {
        const res = await callHost<ReadResult>(targetHost, 'fs.read', { path: p, max: 1024 * 1024 });
        if (guardScopeSeq !== scopeSeqRef.current || fileSeq !== fileReqSeqRef.current) return null;
        openFileRef.current = res;
        editTextRef.current = res.text ?? '';
        setOpenFile(res);
        setEditText(res.text ?? '');
        setDirty(false);
        setEditMode(false);
        return res;
      } catch (e) {
        if (guardScopeSeq !== scopeSeqRef.current || fileSeq !== fileReqSeqRef.current) return null;
        pushToast({ text: `Read failed: ${(e as Error).message}`, level: 'error' });
        return null;
      }
    },
    [hostId, callHost, pushToast],
  );

  const handleSave = useCallback(async () => {
    const currentOpen = openFileRef.current ?? openFile;
    const targetHost = hostIdRef.current || hostId;
    if (!targetHost || !currentOpen || currentOpen.truncated || currentOpen.binary) return;
    const savedPath = currentOpen.path;
    const savedText = editTextRef.current;
    const savedMtime = currentOpen.mtime;
    const guardScopeSeq = scopeSeqRef.current;
    const guardFileSeq = fileReqSeqRef.current;
    const saveSeq = ++saveReqSeqRef.current;
    setSaving(true);
    try {
      const updated = await callHost<FsEntry>(targetHost, 'fs.write', {
        path: savedPath,
        text: savedText,
        expectMtime: savedMtime,
      });
      if (
        !isFilesPanelSaveValid({
          guardScopeSeq,
          currentScopeSeq: scopeSeqRef.current,
          guardFileSeq,
          currentFileSeq: fileReqSeqRef.current,
        })
      ) {
        return;
      }
      const nextOpen = applySavedFileResult(openFileRef.current, savedPath, savedText, updated?.mtime);
      openFileRef.current = nextOpen;
      setOpenFile((prev) => {
        const next = applySavedFileResult(prev, savedPath, savedText, updated?.mtime);
        openFileRef.current = next;
        return next;
      });
      if (
        shouldClearDirtyAfterSave({
          guardSeq: guardScopeSeq,
          currentSeq: scopeSeqRef.current,
          guardFileSeq,
          currentFileSeq: fileReqSeqRef.current,
          currentOpenPath: openFileRef.current?.path,
          savedPath,
          currentEditText: editTextRef.current,
          savedText,
        })
      ) {
        setDirty(false);
      }
      pushToast({ text: `Saved ${savedPath.split('/').pop()}`, level: 'success' });
    } catch (e) {
      if (
        !isFilesPanelSaveValid({
          guardScopeSeq,
          currentScopeSeq: scopeSeqRef.current,
          guardFileSeq,
          currentFileSeq: fileReqSeqRef.current,
        })
      ) {
        return;
      }
      pushToast({ text: `Save failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      if (guardScopeSeq === scopeSeqRef.current && saveSeq === saveReqSeqRef.current) {
        setSaving(false);
      }
    }
  }, [hostId, openFile, editText, callHost, pushToast]);

  const handleSearch = async () => {
    const q = searchQuery.trim();
    const targetHost = hostIdRef.current || hostId;
    if (!q || !targetHost) {
      setSearchResults(null);
      return;
    }
    const guardScopeSeq = scopeSeqRef.current;
    const targetRoot = dirPathRef.current || dirPath;
    try {
      const res = await callHost<FsEntry[]>(targetHost, 'fs.search', {
        root: targetRoot,
        query: q,
        max: 100,
        hidden: showHiddenRef.current,
      });
      if (guardScopeSeq !== scopeSeqRef.current) return;
      setSearchResults(Array.isArray(res) ? res : []);
    } catch (e) {
      if (guardScopeSeq !== scopeSeqRef.current) return;
      pushToast({ text: `Search failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const submitInlinePrompt = async () => {
    const targetHost = hostIdRef.current || hostId;
    if (!inlinePrompt || !targetHost) return;
    const name = inlinePrompt.value.trim();
    if (!name) return;
    const targetDir = dirPathRef.current || dirPath;
    const baseDir = targetDir.replace(/\/+$/, '') || '/';
    const guardScopeSeq = scopeSeqRef.current;

    try {
      if (inlinePrompt.kind === 'file') {
        const full = baseDir === '/' ? `/${name}` : `${baseDir}/${name}`;
        await callHost(targetHost, 'fs.create', { path: full });
        if (guardScopeSeq !== scopeSeqRef.current) return;
        setInlinePrompt(null);
        await loadDir(targetDir, targetHost);
        if (guardScopeSeq !== scopeSeqRef.current) return;
        const opened = await openPath(full, targetHost);
        if (guardScopeSeq !== scopeSeqRef.current) return;
        if (opened && !opened.truncated && !opened.binary && openFileRef.current?.path === full) {
          setEditMode(true);
        }
      } else if (inlinePrompt.kind === 'dir') {
        const full = baseDir === '/' ? `/${name}` : `${baseDir}/${name}`;
        await callHost(targetHost, 'fs.mkdir', { path: full, parents: true });
        if (guardScopeSeq !== scopeSeqRef.current) return;
        setInlinePrompt(null);
        await loadDir(targetDir, targetHost);
      } else if (inlinePrompt.kind === 'rename' && inlinePrompt.target) {
        const fromPath = inlinePrompt.target.path;
        const slashIdx = fromPath.lastIndexOf('/');
        const parent = slashIdx > 0 ? fromPath.slice(0, slashIdx) : '/';
        const toPath = name.startsWith('/') ? name : parent === '/' ? `/${name}` : `${parent}/${name}`;
        if (toPath !== fromPath) {
          await callHost(targetHost, 'fs.rename', { from: fromPath, to: toPath, overwrite: false });
          if (guardScopeSeq !== scopeSeqRef.current) return;
          const prevOpen = openFileRef.current;
          const nextOpen = applyRenamedOpenFile(prevOpen, fromPath, toPath, hostHomeRef.current);
          if (prevOpen && nextOpen && nextOpen.path !== prevOpen.path) {
            fileReqSeqRef.current++;
            saveReqSeqRef.current++;
            setSaving(false);
          }
          openFileRef.current = nextOpen;
          setOpenFile((prev) => {
            const next = applyRenamedOpenFile(prev, fromPath, toPath, hostHomeRef.current);
            openFileRef.current = next;
            return next;
          });
        }
        if (guardScopeSeq !== scopeSeqRef.current) return;
        setInlinePrompt(null);
        await loadDir(targetDir, targetHost);
        if (guardScopeSeq !== scopeSeqRef.current) return;
        pushToast({ text: `Renamed to ${name}`, level: 'success' });
      }
    } catch (e) {
      if (guardScopeSeq !== scopeSeqRef.current) return;
      pushToast({ text: `Operation failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const confirmDelete = async (permanent: boolean) => {
    const targetHostId = hostIdRef.current || hostId;
    if (!deletePrompt || !targetHostId || deleteBusy) return;
    const { entry } = deletePrompt;
    const guardScopeSeq = scopeSeqRef.current;
    const delSeq = ++deleteReqSeqRef.current;
    const targetDir = dirPathRef.current || dirPath;
    if (isProtectedDeletePath(entry.path, hostHomeRef.current)) {
      pushToast({ text: 'Refusing to delete protected root or home directory', level: 'error' });
      setDeletePrompt(null);
      return;
    }

    setDeleteBusy(true);
    try {
      await callHost(targetHostId, 'fs.delete', { paths: [entry.path], permanent });
      if (guardScopeSeq !== scopeSeqRef.current) return;
      const nextOpen = applyDeletedOpenFile(openFileRef.current, entry.path, hostHomeRef.current);
      if (openFileRef.current && nextOpen === null) {
        fileReqSeqRef.current++;
        saveReqSeqRef.current++;
        openFileRef.current = null;
        editTextRef.current = '';
        setOpenFile(null);
        setEditText('');
        setEditMode(false);
        setDirty(false);
        setSaving(false);
      }
      setDeletePrompt(null);
      await loadDir(targetDir, targetHostId);
      if (guardScopeSeq !== scopeSeqRef.current) return;
      pushToast({
        text: permanent ? `Permanently deleted ${entry.name}` : `Deleted ${entry.name}`,
        level: 'info',
      });
    } catch (e) {
      if (guardScopeSeq !== scopeSeqRef.current) return;
      if (!permanent && isTrashUnsupportedError(e)) {
        setDeletePrompt({
          entry,
          permanent: true,
          reason: (e as Error).message,
        });
      } else {
        setDeletePrompt(null);
        pushToast({ text: `Delete failed: ${(e as Error).message}`, level: 'error' });
      }
    } finally {
      if (guardScopeSeq === scopeSeqRef.current && delSeq === deleteReqSeqRef.current) {
        setDeleteBusy(false);
      }
    }
  };

  const handleUploadFiles = async (files: FileList | null) => {
    const targetHostId = hostIdRef.current || hostId;
    if (!files || files.length === 0 || !targetHostId || uploading) return;
    const targetDir = (dirPathRef.current || dirPath).replace(/\/+$/, '') || '~';
    const guardScopeSeq = scopeSeqRef.current;
    const upSeq = ++uploadReqSeqRef.current;
    setUploading(true);
    try {
      for (const file of Array.from(files)) {
        const dest = targetDir === '/' ? `/${file.name}` : `${targetDir}/${file.name}`;
        const buf = new Uint8Array(await file.arrayBuffer());
        let uploadId = '';
        try {
          const started = await callHost<{ id: string }>(targetHostId, 'fs.uploadStart', {
            path: dest,
            overwrite: true,
            mkdirs: true,
          });
          uploadId = started.id;
          let offset = 0;
          while (offset < buf.length) {
            const slice = buf.subarray(offset, offset + UPLOAD_CHUNK_BYTES);
            await callHost(targetHostId, 'fs.uploadChunk', {
              id: uploadId,
              offset,
              data: bytesToBase64(slice),
            });
            offset += slice.length;
          }
          await callHost(targetHostId, 'fs.uploadFinish', { id: uploadId });
        } catch (err) {
          if (uploadId) {
            void callHost(targetHostId, 'fs.uploadAbort', { id: uploadId }).catch(() => {});
          }
          throw err;
        }
      }
      if (
        !isFilesPanelUploadTargetCurrent({
          guardScopeSeq,
          currentScopeSeq: scopeSeqRef.current,
          targetHostId,
          currentHostId: hostIdRef.current,
        })
      ) {
        return;
      }
      await loadDir(dirPathRef.current || targetDir, targetHostId);
      if (guardScopeSeq !== scopeSeqRef.current) return;
      pushToast({ text: `Uploaded ${files.length} file(s)`, level: 'success' });
    } catch (e) {
      if (guardScopeSeq !== scopeSeqRef.current) return;
      pushToast({ text: `Upload failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      if (guardScopeSeq === scopeSeqRef.current && upSeq === uploadReqSeqRef.current) {
        setUploading(false);
      }
    }
  };

  const handleDownloadOpenFile = async () => {
    if (!openFile || !hostId) return;
    const targetHostId = hostId;
    try {
      const chunks: Uint8Array[] = [];
      let offset = 0;
      let mime = openFile.mime || 'application/octet-stream';
      while (true) {
        const res = await callHost<DownloadChunkResult>(targetHostId, 'fs.download', {
          path: openFile.path,
          offset,
          length: UPLOAD_CHUNK_BYTES,
        });
        const bytes = res.data ? base64ToBytes(res.data) : new Uint8Array(0);
        if (bytes.length > 0) chunks.push(bytes);
        offset += bytes.length;
        if (res.mime) mime = res.mime;
        if (res.eof || bytes.length === 0) break;
      }
      const total = chunks.reduce((acc, c) => acc + c.length, 0);
      const merged = new Uint8Array(total);
      let pos = 0;
      for (const c of chunks) {
        merged.set(c, pos);
        pos += c.length;
      }
      const blob = new Blob([merged], { type: mime });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = openFile.path.split('/').pop() || 'download';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 2000);
    } catch (e) {
      pushToast({ text: `Download failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const listToDisplay = searchResults ?? entries;
  const isImage = Boolean(openFile?.mime?.startsWith('image/') && openFile?.base64);
  const isMarkdown = Boolean(openFile?.path.toLowerCase().endsWith('.md'));

  return (
    <div data-testid="files-panel" className="flex-1 flex min-h-0 text-xs">
      {/* Left: Remote File Tree */}
      <div className="w-60 shrink-0 border-r border-[var(--border)] bg-[var(--bg-sidebar)]/50 flex flex-col min-h-0">
        <div className="p-2 border-b border-[var(--border)] space-y-1.5">
          {hostList.length > 1 && (
            <div className="flex items-center gap-1.5">
              <Server className="w-3 h-3 text-[var(--accent)] shrink-0" />
              <select
                data-testid="files-host-select"
                value={hostId}
                onChange={(e) => setActiveHostId(e.target.value)}
                className="flex-1 min-w-0 rounded-md border border-[var(--border)] bg-[var(--bg-base)] px-2 py-1 text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
              >
                {hostList.map((h) => (
                  <option key={h.creds.id} value={h.creds.id}>
                    {h.creds.name} ({h.state})
                  </option>
                ))}
              </select>
            </div>
          )}

          <div className="flex items-center gap-0.5">
            <button
              type="button"
              disabled={!parentPath}
              onClick={() => parentPath && void loadDir(parentPath)}
              title="Up directory"
              className="rounded p-1 hover:bg-[var(--bg-hover)] disabled:opacity-30 cursor-pointer"
            >
              <ChevronUp className="w-3.5 h-3.5" />
            </button>
            <button
              type="button"
              data-testid="files-home-btn"
              onClick={() => void loadDir('~')}
              title="Home (~)"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <Home className="w-3.5 h-3.5" />
            </button>
            <div
              data-testid="files-current-dir"
              className="flex-1 truncate font-mono text-[10.5px] text-[var(--text-secondary)] px-0.5"
              title={dirPath}
            >
              {dirPath}
            </div>
            <button
              type="button"
              data-testid="files-new-file-btn"
              onClick={() => setInlinePrompt({ kind: 'file', value: '' })}
              title="New file"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <FilePlus className="w-3.5 h-3.5" />
            </button>
            <button
              type="button"
              data-testid="files-new-dir-btn"
              onClick={() => setInlinePrompt({ kind: 'dir', value: '' })}
              title="New folder"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <FolderPlus className="w-3.5 h-3.5" />
            </button>
            <button
              type="button"
              data-testid="files-upload-btn"
              disabled={uploading || !hostId}
              onClick={() => uploadInputRef.current?.click()}
              title="Upload file"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <Upload className="w-3.5 h-3.5" />
            </button>
            <input
              ref={uploadInputRef}
              type="file"
              multiple
              className="hidden"
              onChange={(e) => {
                void handleUploadFiles(e.target.files);
                e.target.value = '';
              }}
            />
            <button
              type="button"
              onClick={() => void loadDir(dirPath)}
              title="Refresh"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              <RefreshCw className={`w-3 h-3 ${loading ? 'animate-spin' : ''}`} />
            </button>
          </div>

          {/* Inline Create / Rename Bar (no native prompt dialogs) */}
          {inlinePrompt && (
            <div className="rounded-md border border-[var(--accent)]/50 bg-[var(--bg-base)] p-1.5 space-y-1">
              <div className="flex items-center justify-between text-[10px] text-[var(--accent)] font-medium">
                <span>
                  {inlinePrompt.kind === 'file'
                    ? 'New file name'
                    : inlinePrompt.kind === 'dir'
                      ? 'New folder name'
                      : `Rename ${inlinePrompt.target?.name}`}
                </span>
                <button
                  type="button"
                  onClick={() => setInlinePrompt(null)}
                  className="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <X className="w-3 h-3" />
                </button>
              </div>
              <div className="flex items-center gap-1">
                <input
                  type="text"
                  autoFocus
                  data-testid="files-inline-input"
                  value={inlinePrompt.value}
                  onChange={(e) => setInlinePrompt((prev) => (prev ? { ...prev, value: e.target.value } : null))}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault();
                      void submitInlinePrompt();
                    } else if (e.key === 'Escape') {
                      setInlinePrompt(null);
                    }
                  }}
                  placeholder={inlinePrompt.kind === 'dir' ? 'folder-name' : 'filename.txt'}
                  className="flex-1 min-w-0 rounded border border-[var(--border)] bg-[var(--bg-surface)] px-1.5 py-0.5 font-mono text-[11px] outline-none focus:border-[var(--accent)]"
                />
                <button
                  type="button"
                  data-testid="files-inline-submit"
                  disabled={!inlinePrompt.value.trim()}
                  onClick={() => void submitInlinePrompt()}
                  className="rounded bg-[var(--accent)] px-2 py-0.5 text-[10.5px] font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                >
                  OK
                </button>
              </div>
            </div>
          )}

          {/* Inline Delete & Headless TRASH_UNSUPPORTED Permanent-Delete Confirmation */}
          {deletePrompt && (
            <div
              data-testid="files-delete-prompt"
              className="rounded-md border border-[var(--danger)]/50 bg-[var(--danger)]/10 p-2 space-y-1.5"
            >
              <div className="text-[11px] text-[var(--text-primary)] leading-snug">
                {deletePrompt.permanent ? (
                  <>
                    System trash is unavailable on this host. Permanently delete{' '}
                    <code className="font-mono font-semibold">{deletePrompt.entry.name}</code>?
                  </>
                ) : (
                  <>
                    Delete <code className="font-mono font-semibold">{deletePrompt.entry.name}</code>?
                  </>
                )}
              </div>
              <div className="flex items-center justify-end gap-1.5">
                <button
                  type="button"
                  disabled={deleteBusy}
                  onClick={() => setDeletePrompt(null)}
                  className="rounded border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[10.5px] text-[var(--text-secondary)] cursor-pointer"
                >
                  Cancel
                </button>
                {deletePrompt.permanent ? (
                  <button
                    type="button"
                    data-testid="files-confirm-permanent-delete-btn"
                    disabled={deleteBusy}
                    onClick={() => void confirmDelete(true)}
                    className="rounded bg-[var(--danger)] px-2 py-0.5 text-[10.5px] font-semibold text-white cursor-pointer"
                  >
                    Permanently Delete
                  </button>
                ) : (
                  <button
                    type="button"
                    data-testid="files-confirm-delete-btn"
                    disabled={deleteBusy}
                    onClick={() => void confirmDelete(false)}
                    className="rounded bg-[var(--danger)] px-2 py-0.5 text-[10.5px] font-semibold text-white cursor-pointer"
                  >
                    Delete
                  </button>
                )}
              </div>
            </div>
          )}

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
            const protectedEntry = isProtectedDeletePath(entry.path, hostHome);
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
                {!protectedEntry && (
                  <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 shrink-0">
                    <button
                      type="button"
                      data-testid="files-rename-btn"
                      onClick={() => setInlinePrompt({ kind: 'rename', target: entry, value: entry.name })}
                      title="Rename"
                      className="p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <Pencil className="w-3 h-3" />
                    </button>
                    <button
                      type="button"
                      data-testid="files-delete-btn"
                      onClick={() => setDeletePrompt({ entry, permanent: false })}
                      title="Delete"
                      className="p-0.5 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                    >
                      <Trash2 className="w-3 h-3" />
                    </button>
                  </div>
                )}
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
                  data-testid="files-download-btn"
                  onClick={() => void handleDownloadOpenFile()}
                  title="Download file"
                  className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  <Download className="w-3 h-3" />
                  Download
                </button>
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
                    disabled={Boolean(openFile.truncated)}
                    title={
                      openFile.truncated
                        ? 'Inline editing is disabled for files > 1 MiB to prevent file truncation'
                        : undefined
                    }
                    onClick={() => {
                      if (!openFile.truncated) setEditMode((v) => !v);
                    }}
                    className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                  >
                    {editMode && !openFile.truncated ? <Eye className="w-3 h-3" /> : <Edit3 className="w-3 h-3" />}
                    {editMode && !openFile.truncated ? 'Preview' : 'Edit'}
                  </button>
                )}
                {editMode && !openFile.truncated && !openFile.binary && (
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

            {openFile.truncated && (
              <div
                data-testid="files-truncated-banner"
                className="flex items-center justify-between gap-2 border-b border-[var(--warning)]/40 bg-[var(--warning)]/10 px-3 py-1.5 text-[11px] text-[var(--warning)]"
              >
                <span>
                  Showing first 1 MiB of {(openFile.size / (1024 * 1024)).toFixed(2)} MiB — inline editing is disabled
                  to prevent file truncation; use Download.
                </span>
              </div>
            )}

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
              ) : editMode && !openFile.truncated ? (
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
