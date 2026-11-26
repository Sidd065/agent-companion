import { describe, expect, it } from 'vitest';
import {
  advanceFilesPanelDirSeq,
  applyDeletedOpenFile,
  applyRenamedOpenFile,
  applySavedFileResult,
  isFilesPanelSaveValid,
  isFilesPanelUploadTargetCurrent,
  resetFilesPanelScopeSeq,
  shouldClearDirtyAfterSave,
  type FilesPanelSeqRefs,
  type ReadResult,
} from '../components/panels/FilesPanel.js';

describe('FilesPanel async file/host switch race guards (D-09)', () => {
  const fileA: ReadResult = {
    path: '/repo/a.ts',
    size: 10,
    mtime: 1000,
    text: 'old A',
    binary: false,
    truncated: false,
    mime: 'text/plain',
  };
  const fileB: ReadResult = {
    path: '/repo/b.ts',
    size: 20,
    mtime: 2000,
    text: 'fresh B',
    binary: false,
    truncated: false,
    mime: 'text/plain',
  };

  it('does NOT overwrite newly opened file B or clear B dirty flag when stale save of file A resolves', () => {
    const afterSaveState = applySavedFileResult(fileB, '/repo/a.ts', 'saved A', 1500, 9999);
    expect(afterSaveState).toBe(fileB);
    expect(afterSaveState?.text).toBe('fresh B');
    expect(afterSaveState?.mtime).toBe(2000);

    expect(
      shouldClearDirtyAfterSave({
        guardSeq: 3,
        currentSeq: 3,
        guardFileSeq: 1,
        currentFileSeq: 2,
        currentOpenPath: '/repo/b.ts',
        savedPath: '/repo/a.ts',
        currentEditText: 'editing B',
        savedText: 'saved A',
      }),
    ).toBe(false);
  });

  it('keeps dirty=true if user kept typing in file A while fs.write was in flight, and clears dirty when unchanged', () => {
    const updatedA = applySavedFileResult(fileA, '/repo/a.ts', 'saved A', 1500, 9999);
    expect(updatedA?.text).toBe('saved A');
    expect(updatedA?.mtime).toBe(1500);

    // User typed more characters while save was in flight -> stay dirty!
    expect(
      shouldClearDirtyAfterSave({
        guardSeq: 4,
        currentSeq: 4,
        guardFileSeq: 2,
        currentFileSeq: 2,
        currentOpenPath: '/repo/a.ts',
        savedPath: '/repo/a.ts',
        currentEditText: 'saved A + more edits',
        savedText: 'saved A',
      }),
    ).toBe(false);

    // Unchanged since save started -> clear dirty!
    expect(
      shouldClearDirtyAfterSave({
        guardSeq: 4,
        currentSeq: 4,
        guardFileSeq: 2,
        currentFileSeq: 2,
        currentOpenPath: '/repo/a.ts',
        savedPath: '/repo/a.ts',
        currentEditText: 'saved A',
        savedText: 'saved A',
      }),
    ).toBe(true);

    // Host/dir switched (currentSeq advanced) -> never clear dirty!
    expect(
      shouldClearDirtyAfterSave({
        guardSeq: 4,
        currentSeq: 5,
        guardFileSeq: 2,
        currentFileSeq: 2,
        currentOpenPath: '/repo/a.ts',
        savedPath: '/repo/a.ts',
        currentEditText: 'saved A',
        savedText: 'saved A',
      }),
    ).toBe(false);
  });

  it('applyRenamedOpenFile updates openFile.path only when fromPath matches current openFile', () => {
    expect(applyRenamedOpenFile(fileB, '/repo/a.ts', '/repo/a-renamed.ts')).toBe(fileB);
    expect(applyRenamedOpenFile(fileA, '/repo/a.ts', '/repo/a-renamed.ts')?.path).toBe('/repo/a-renamed.ts');
  });

  it('same-host loadDir (dirReqSeq increment) does NOT abort in-flight save or leave openFile.mtime stale (R2-D03)', () => {
    const refs: FilesPanelSeqRefs = {
      scopeSeq: { current: 1 },
      dirReqSeq: { current: 5 },
      fileReqSeq: { current: 2 },
      saveReqSeq: { current: 0 },
      uploadReqSeq: { current: 0 },
      deleteReqSeq: { current: 0 },
    };
    const openFileRef: { current: ReadResult | null } = { current: { ...fileA } };
    const editTextRef = { current: 'updated A v1' };

    // Save #1 starts before concurrent same-host directory refresh (e.g. showHidden toggle or folder click).
    const guardScopeSeq = refs.scopeSeq.current;
    const guardFileSeq = refs.fileReqSeq.current;

    // Concurrent same-host loadDir advances dirReqSeq ONLY (scopeSeq stays 1!).
    expect(advanceFilesPanelDirSeq(refs)).toBe(6);
    expect(advanceFilesPanelDirSeq(refs)).toBe(7);
    expect(refs.scopeSeq.current).toBe(1);

    // Save #1 resolves with disk mtime=1500 -> must STILL be valid and update openFileRef.current.mtime!
    expect(
      isFilesPanelSaveValid({
        guardScopeSeq,
        currentScopeSeq: refs.scopeSeq.current,
        guardFileSeq,
        currentFileSeq: refs.fileReqSeq.current,
      }),
    ).toBe(true);

    openFileRef.current = applySavedFileResult(openFileRef.current, '/repo/a.ts', 'updated A v1', 1500);
    expect(openFileRef.current?.mtime).toBe(1500);
    expect(
      shouldClearDirtyAfterSave({
        guardSeq: guardScopeSeq,
        currentSeq: refs.scopeSeq.current,
        guardFileSeq,
        currentFileSeq: refs.fileReqSeq.current,
        currentOpenPath: openFileRef.current?.path,
        savedPath: '/repo/a.ts',
        currentEditText: editTextRef.current,
        savedText: 'updated A v1',
      }),
    ).toBe(true);

    // Subsequent Save #2 immediately reads expectMtime === 1500 (never stale 1000!).
    expect(openFileRef.current?.mtime).toBe(1500);
  });

  it('host/workspace reset advances scopeSeq and blocks stale saves, uploads, and clears deleted openFileRef (R2-D03)', () => {
    const refs: FilesPanelSeqRefs = {
      scopeSeq: { current: 1 },
      dirReqSeq: { current: 2 },
      fileReqSeq: { current: 3 },
      saveReqSeq: { current: 1 },
      uploadReqSeq: { current: 1 },
      deleteReqSeq: { current: 1 },
    };
    const guardScopeSeq = refs.scopeSeq.current;
    const guardFileSeq = refs.fileReqSeq.current;

    // Host switches from host-a to host-b -> resetFilesPanelScopeSeq advances all counters.
    expect(resetFilesPanelScopeSeq(refs)).toBe(2);

    expect(
      isFilesPanelSaveValid({
        guardScopeSeq,
        currentScopeSeq: refs.scopeSeq.current,
        guardFileSeq,
        currentFileSeq: refs.fileReqSeq.current,
      }),
    ).toBe(false);

    expect(
      isFilesPanelUploadTargetCurrent({
        guardScopeSeq,
        currentScopeSeq: refs.scopeSeq.current,
        targetHostId: 'host-a',
        currentHostId: 'host-b',
      }),
    ).toBe(false);

    // Same host & unchanged scopeSeq allows post-upload directory reload.
    expect(
      isFilesPanelUploadTargetCurrent({
        guardScopeSeq: refs.scopeSeq.current,
        currentScopeSeq: refs.scopeSeq.current,
        targetHostId: 'host-b',
        currentHostId: 'host-b',
      }),
    ).toBe(true);

    // Deleting open file or its parent directory clears openFile, while unrelated delete preserves it.
    expect(applyDeletedOpenFile(fileA, '/repo/other.ts', '/home/user')).toBe(fileA);
    expect(applyDeletedOpenFile(fileA, '/repo/a.ts', '/home/user')).toBeNull();
    expect(applyDeletedOpenFile(fileA, '/repo', '/home/user')).toBeNull();
  });

  it('applyRenamedOpenFile rebases descendant openFile paths on ancestor directory rename and invalidates stale saves (R3-D04)', () => {
    const nestedFile: ReadResult = {
      ...fileA,
      path: '/repo/a/b.ts',
    };
    const deepNestedFile: ReadResult = {
      ...fileA,
      path: '/repo/a/sub/deep.ts',
    };
    const siblingPrefixFile: ReadResult = {
      ...fileA,
      path: '/repo/ab/b.ts',
    };
    const homeNestedFile: ReadResult = {
      ...fileA,
      path: '/home/user/proj/src/index.ts',
    };

    // Ancestor directory rename rebases direct and deeply nested children.
    expect(applyRenamedOpenFile(nestedFile, '/repo/a', '/repo/renamed_a', '/home/user')?.path).toBe(
      '/repo/renamed_a/b.ts',
    );
    expect(
      applyRenamedOpenFile(deepNestedFile, '/repo/a/', '/repo/renamed_a/', '/home/user')?.path,
    ).toBe('/repo/renamed_a/sub/deep.ts');

    // Sibling directory sharing string prefix (/repo/ab vs /repo/a) MUST remain untouched.
    expect(applyRenamedOpenFile(siblingPrefixFile, '/repo/a', '/repo/renamed_a', '/home/user')).toBe(
      siblingPrefixFile,
    );

    // Home-relative rename normalizes against hostHome.
    expect(
      applyRenamedOpenFile(homeNestedFile, '~/proj/src', '~/proj/lib', '/home/user')?.path,
    ).toBe('/home/user/proj/lib/index.ts');

    // Simulating an in-flight save when openFile path changes via ancestor rename:
    // advancing fileReqSeq & saveReqSeq invalidates the stale save on the old path.
    const refs: FilesPanelSeqRefs = {
      scopeSeq: { current: 2 },
      dirReqSeq: { current: 4 },
      fileReqSeq: { current: 7 },
      saveReqSeq: { current: 3 },
      uploadReqSeq: { current: 0 },
      deleteReqSeq: { current: 0 },
    };
    const guardScopeSeq = refs.scopeSeq.current;
    const guardFileSeq = refs.fileReqSeq.current;
    const renamed = applyRenamedOpenFile(nestedFile, '/repo/a', '/repo/renamed_a', '/home/user');
    if (renamed && renamed.path !== nestedFile.path) {
      refs.fileReqSeq.current++;
      refs.saveReqSeq.current++;
    }
    expect(
      isFilesPanelSaveValid({
        guardScopeSeq,
        currentScopeSeq: refs.scopeSeq.current,
        guardFileSeq,
        currentFileSeq: refs.fileReqSeq.current,
      }),
    ).toBe(false);
  });
});
