import React, { useMemo } from 'react';
import { CheckSquare, ListTodo, Send, Square, StickyNote, Trash2 } from 'lucide-react';
import { useApp, threadKey } from '../../state/appStore.js';
import {
  nextNoteSummary,
  parseNoteChecklist,
  plainInlineTitle,
  toggleNoteChecklistLine,
} from '../../utils/sessionHelpers.js';

export const NotesPanel: React.FC = () => {
  const {
    activeHost,
    activeSession,
    sessionNotes,
    setSessionNote,
    setComposerPrefill,
    pushToast,
  } = useApp();

  if (!activeHost || !activeSession) {
    return (
      <div className="flex-1 flex items-center justify-center p-6 text-xs text-[var(--text-muted)]">
        Select a thread to write private client-side notes for what you want to do next.
      </div>
    );
  }

  const hostId = activeHost.creds.id;
  const sessionId = activeSession.id;
  const record = sessionNotes[threadKey(hostId, sessionId)];
  const noteText = record?.text ?? '';
  const checklist = useMemo(() => parseNoteChecklist(noteText), [noteText]);

  const handleUpdate = (next: string) => {
    setSessionNote(hostId, sessionId, next);
  };

  return (
    <div data-testid="notes-panel" className="flex-1 flex flex-col min-h-0 text-xs">
      <div className="flex items-center justify-between gap-2 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-3 py-2">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5 font-medium text-[var(--text-primary)] truncate">
            <StickyNote className="w-3.5 h-3.5 text-[var(--accent)] shrink-0" />
            <span className="truncate">
              {plainInlineTitle(activeSession.title) || 'Session Notes'}
            </span>
          </div>
          <div className="text-[10.5px] text-[var(--text-muted)]">
            Stored locally on this client · never sent unless inserted
          </div>
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <button
            type="button"
            data-testid="notes-add-todo-btn"
            onClick={() => {
              const prefix = !noteText || noteText.endsWith('\n') ? '' : '\n';
              handleUpdate(`${noteText}${prefix}- [ ] `);
            }}
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            <ListTodo className="w-3 h-3 text-[var(--accent)]" />
            + To-do
          </button>
          {noteText.trim() && (
            <>
              <button
                type="button"
                data-testid="notes-insert-composer-btn"
                onClick={() => {
                  const snippet = nextNoteSummary(noteText) || noteText.trim();
                  setComposerPrefill(snippet);
                  pushToast({ text: 'Inserted next note item into composer', level: 'info' });
                }}
                className="inline-flex items-center gap-1 rounded-md bg-[var(--accent-soft)] px-2 py-1 text-[11px] font-medium text-[var(--accent)] hover:opacity-90 cursor-pointer"
              >
                <Send className="w-3 h-3" />
                Insert Next
              </button>
              <button
                type="button"
                onClick={() => handleUpdate('')}
                title="Clear session notes"
                className="rounded-md p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
              >
                <Trash2 className="w-3.5 h-3.5" />
              </button>
            </>
          )}
        </div>
      </div>

      {checklist.length > 0 && (
        <div className="border-b border-[var(--border)] bg-[var(--bg-surface)]/70 px-3 py-2 space-y-1 max-h-44 overflow-y-auto">
          <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
            Checklist ({checklist.filter((i) => i.checked).length}/{checklist.length} done)
          </div>
          {checklist.map((item) => (
            <button
              key={item.lineIndex}
              type="button"
              onClick={() => handleUpdate(toggleNoteChecklistLine(noteText, item.lineIndex))}
              className="flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left hover:bg-[var(--bg-hover)]/60 cursor-pointer"
            >
              {item.checked ? (
                <CheckSquare className="w-3.5 h-3.5 shrink-0 text-[var(--success)]" />
              ) : (
                <Square className="w-3.5 h-3.5 shrink-0 text-[var(--text-muted)]" />
              )}
              <span
                className={
                  item.checked
                    ? 'line-through text-[var(--text-muted)]'
                    : 'text-[var(--text-primary)]'
                }
              >
                {item.text || '(empty item)'}
              </span>
            </button>
          ))}
        </div>
      )}

      <textarea
        data-testid="notes-textarea"
        value={noteText}
        onChange={(e) => handleUpdate(e.target.value)}
        placeholder={
          'Write notes on what you want to do next in this session...\n\nTips:\n- [ ] Add follow-up tasks as checkboxes\n- Notes stay strictly on this device'
        }
        className="flex-1 w-full resize-none bg-[var(--bg-base)] p-3.5 text-xs leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-muted)] outline-none"
      />
    </div>
  );
};
