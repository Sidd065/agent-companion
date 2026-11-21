import React, { useCallback, useEffect, useState } from 'react';
import { FileText, MessageSquarePlus, RefreshCw, Send } from 'lucide-react';
import type { ArtifactInfo } from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';
import { MarkdownRenderer } from '../markdown/MarkdownRenderer.js';

export const ArtifactsPanel: React.FC = () => {
  const { activeHost, activeSession, callHost, addComposerChip, pushToast } = useApp();
  const [artifacts, setArtifacts] = useState<ArtifactInfo[]>([]);
  const [selected, setSelected] = useState<ArtifactInfo | null>(null);
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(false);

  const [commentBlockIdx, setCommentBlockIdx] = useState<number | null>(null);
  const [commentText, setCommentText] = useState('');

  const loadList = useCallback(async () => {
    if (!activeHost || !activeSession) return;
    setLoading(true);
    try {
      const list = await callHost<ArtifactInfo[]>(activeHost.creds.id, 'session.artifacts', {
        id: activeSession.id,
      });
      const arr = Array.isArray(list) ? list : [];
      setArtifacts(arr);
      if (arr.length > 0 && !selected) {
        setSelected(arr[0]);
      }
    } catch {
      setArtifacts([]);
    } finally {
      setLoading(false);
    }
  }, [activeHost, activeSession, callHost, selected]);

  useEffect(() => {
    setSelected(null);
    setContent('');
    void loadList();
  }, [activeSession?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!activeHost || !activeSession || !selected) return;
    void callHost<{ text?: string; content?: string } | string>(activeHost.creds.id, 'session.artifact', {
      id: activeSession.id,
      path: selected.path,
    })
      .then((res) => {
        if (typeof res === 'string') setContent(res);
        else setContent(res?.text ?? res?.content ?? '');
      })
      .catch(() => setContent(''));
  }, [activeHost, activeSession, selected, callHost]);

  if (!activeHost || !activeSession) {
    return (
      <div className="flex-1 flex items-center justify-center p-4 text-xs text-[var(--text-muted)]">
        Select a thread to view artifacts and plans.
      </div>
    );
  }

  const blocks = content ? content.split(/\n\n+/) : [];

  const sendReviewComment = async (quote: string, sendNow: boolean) => {
    if (!selected || !commentText.trim()) return;
    if (sendNow) {
      try {
        await callHost(activeHost.creds.id, 'session.comment', {
          id: activeSession.id,
          comments: [
            {
              kind: 'artifact',
              uri: selected.path,
              quote: quote.slice(0, 300),
              comment: commentText.trim(),
            },
          ],
        });
        pushToast({ text: 'Sent artifact review comment to agent', level: 'success' });
      } catch (e) {
        pushToast({ text: `Comment failed: ${(e as Error).message}`, level: 'error' });
      }
    } else {
      addComposerChip({
        kind: 'citation',
        label: `Artifact: ${selected.name}`,
        content: `Comment on artifact \`${selected.name}\`:\n> ${quote.slice(0, 240)}\n\n${commentText.trim()}`,
      });
      pushToast({ text: 'Artifact comment added to composer', level: 'info' });
    }
    setCommentBlockIdx(null);
    setCommentText('');
  };

  return (
    <div data-testid="artifacts-panel" className="flex-1 flex flex-col min-h-0 text-xs">
      <div className="flex items-center justify-between gap-2 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-3 py-1.5">
        <div className="flex items-center gap-1.5 overflow-x-auto">
          {artifacts.map((a) => (
            <button
              key={a.path}
              type="button"
              onClick={() => setSelected(a)}
              className={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 whitespace-nowrap cursor-pointer ${
                selected?.path === a.path
                  ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
              }`}
            >
              <FileText className="w-3 h-3" />
              {a.name}
            </button>
          ))}
        </div>
        <button
          type="button"
          onClick={() => void loadList()}
          className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-4 space-y-3">
        {artifacts.length === 0 ? (
          <div className="py-12 text-center text-[var(--text-muted)]">
            No artifacts or plans produced in this session yet.
          </div>
        ) : (
          blocks.map((blk, bi) => (
            <div key={bi} className="group relative rounded-lg p-1.5 hover:bg-[var(--bg-surface)]/70">
              <button
                type="button"
                onClick={() => {
                  setCommentBlockIdx(bi);
                  setCommentText('');
                }}
                title="Comment on this section"
                className="absolute right-2 top-2 opacity-0 group-hover:opacity-100 rounded-md border border-[var(--border)] bg-[var(--bg-elevated)] p-1 text-[var(--accent)] cursor-pointer"
              >
                <MessageSquarePlus className="w-3.5 h-3.5" />
              </button>
              <MarkdownRenderer text={blk} citable />
              {commentBlockIdx === bi && (
                <div className="mt-2 rounded-lg border border-[var(--accent)]/40 bg-[var(--bg-sidebar)] p-2 space-y-2">
                  <textarea
                    autoFocus
                    value={commentText}
                    onChange={(e) => setCommentText(e.target.value)}
                    rows={2}
                    placeholder="Feedback on this section..."
                    className="w-full rounded-md border border-[var(--border)] bg-[var(--bg-base)] p-2 text-xs outline-none focus:border-[var(--accent)]"
                  />
                  <div className="flex justify-end gap-1.5">
                    <button
                      type="button"
                      onClick={() => setCommentBlockIdx(null)}
                      className="rounded border border-[var(--border)] px-2 py-1 text-[11px] cursor-pointer"
                    >
                      Cancel
                    </button>
                    <button
                      type="button"
                      onClick={() => void sendReviewComment(blk, false)}
                      className="rounded border border-[var(--accent)]/50 px-2 py-1 text-[11px] text-[var(--accent)] cursor-pointer"
                    >
                      Add to Composer
                    </button>
                    <button
                      type="button"
                      onClick={() => void sendReviewComment(blk, true)}
                      className="inline-flex items-center gap-1 rounded bg-[var(--accent)] px-2 py-1 text-[11px] font-semibold text-[var(--accent-fg)] cursor-pointer"
                    >
                      <Send className="w-3 h-3" />
                      Send now
                    </button>
                  </div>
                </div>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  );
};
