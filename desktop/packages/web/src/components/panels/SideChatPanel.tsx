import React, { useState } from 'react';
import { ArrowUp, Loader2, MessageSquarePlus, Trash2 } from 'lucide-react';
import { useApp } from '../../state/appStore.js';
import { MarkdownRenderer } from '../markdown/MarkdownRenderer.js';

export const SideChatPanel: React.FC = () => {
  const { activeHost, activeSession, activeAsides, callHost, addComposerChip, pushToast } = useApp();
  const [question, setQuestion] = useState('');
  const [sending, setSending] = useState(false);

  if (!activeHost || !activeSession) {
    return (
      <div className="flex-1 flex items-center justify-center p-4 text-xs text-[var(--text-muted)]">
        Open a thread to ask side questions (/btw) without altering its main context.
      </div>
    );
  }

  const ask = async () => {
    const q = question.trim();
    if (!q || sending) return;
    setSending(true);
    setQuestion('');
    try {
      await callHost(activeHost.creds.id, 'session.aside', {
        id: activeSession.id,
        question: q,
      });
    } catch (e) {
      pushToast({ text: `Side question failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setSending(false);
    }
  };

  const removeAside = async (asideId: string) => {
    try {
      await callHost(activeHost.creds.id, 'session.asideDelete', {
        id: activeSession.id,
        asideId,
      });
    } catch (e) {
      pushToast({ text: `Delete failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  return (
    <div data-testid="side-chat-panel" className="flex-1 flex flex-col min-h-0">
      <div className="border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-3 py-2 text-[11px] text-[var(--text-muted)]">
        Side questions (<code className="font-mono">/btw</code>) see the active thread context without adding to its
        turn history.
      </div>

      <div className="flex-1 overflow-y-auto p-3 space-y-3">
        {activeAsides.length === 0 && (
          <div className="py-10 text-center text-xs text-[var(--text-muted)]">
            Ask a quick clarification or architecture check below.
          </div>
        )}
        {activeAsides.map((a) => (
          <div
            key={a.id}
            data-testid="aside-card"
            className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-3 space-y-2 text-xs"
          >
            <div className="flex items-start justify-between gap-2">
              <div className="font-medium text-[var(--text-primary)]">{a.question}</div>
              <div className="flex items-center gap-1 shrink-0">
                {a.answer && (
                  <button
                    type="button"
                    onClick={() => {
                      addComposerChip({
                        kind: 'side-chat',
                        label: `Side chat: ${a.question.slice(0, 32)}`,
                        content: `Q: ${a.question}\nA: ${a.answer}`,
                      });
                      pushToast({ text: 'Side chat answer added to main composer', level: 'info' });
                    }}
                    title="Add to main chat composer"
                    className="rounded p-1 text-[var(--accent)] hover:bg-[var(--accent-soft)] cursor-pointer"
                  >
                    <MessageSquarePlus className="w-3.5 h-3.5" />
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => void removeAside(a.id)}
                  title="Delete"
                  className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </button>
              </div>
            </div>

            {a.state === 'running' || a.state === 'pending' ? (
              <div className="flex items-center gap-1.5 text-[var(--accent)]">
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
                <span>Thinking...</span>
              </div>
            ) : a.error ? (
              <div className="text-[var(--danger)]">{a.error}</div>
            ) : a.answer ? (
              <div className="border-t border-[var(--border)] pt-2 text-[var(--text-secondary)]">
                <MarkdownRenderer text={a.answer} citable />
              </div>
            ) : null}
          </div>
        ))}
      </div>

      <div className="border-t border-[var(--border)] p-2.5">
        <div className="flex items-end gap-2 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-base)] px-2.5 py-2 focus-within:border-[var(--accent)]">
          <textarea
            data-testid="side-chat-input"
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                void ask();
              }
            }}
            rows={2}
            placeholder="Ask a side question (/btw)..."
            className="flex-1 resize-none bg-transparent text-xs text-[var(--text-primary)] placeholder:text-[var(--text-muted)] outline-none"
          />
          <button
            type="button"
            data-testid="side-chat-send"
            disabled={sending || !question.trim()}
            onClick={() => void ask()}
            className="rounded-lg bg-[var(--accent)] p-1.5 text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
          >
            <ArrowUp className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
    </div>
  );
};
