import React, { useEffect, useRef, useState } from 'react';
import { ChevronDown, ChevronUp, Search, X } from 'lucide-react';
import { useApp } from '../../state/appStore.js';
import { Composer } from './Composer.js';
import { ThreadInfoCard } from './ThreadInfoCard.js';
import { Timeline } from './Timeline.js';

interface Props {
  findSignal?: number;
  openModelPickerSignal?: number;
}

export const ThreadView: React.FC<Props> = ({ findSignal = 0, openModelPickerSignal = 0 }) => {
  const { activeHost, activeSession, activeDetail, activeTranscript, infoCardOpen } = useApp();
  const [findOpen, setFindOpen] = useState(false);
  const [findQuery, setFindQuery] = useState('');
  const [matchCount, setMatchCount] = useState(0);
  const [activeMatchIdx, setActiveMatchIdx] = useState(0);
  const findInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (findSignal > 0) {
      setFindOpen(true);
      setTimeout(() => findInputRef.current?.focus(), 20);
    }
  }, [findSignal]);

  useEffect(() => {
    setActiveMatchIdx(0);
  }, [findQuery, activeSession?.id]);

  if (!activeHost || !activeSession) {
    return (
      <div className="flex-1 flex items-center justify-center text-sm text-[var(--text-muted)]">
        Select a thread or start a new one.
      </div>
    );
  }

  return (
    <div data-testid="thread-view" className="flex-1 flex flex-col min-h-0 min-w-0">
      {infoCardOpen && (
        <ThreadInfoCard hostId={activeHost.creds.id} session={activeSession} detail={activeDetail} />
      )}

      {/* In-Thread Find Bar (Ctrl+F) */}
      {findOpen && (
        <div className="flex items-center justify-end gap-2 border-b border-[var(--border)] bg-[var(--bg-surface)] px-4 py-1.5 text-xs">
          <Search className="w-3.5 h-3.5 text-[var(--text-muted)]" />
          <input
            ref={findInputRef}
            type="text"
            value={findQuery}
            onChange={(e) => setFindQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                setFindOpen(false);
                setFindQuery('');
              } else if (e.key === 'Enter' && matchCount > 0) {
                setActiveMatchIdx((i) => (e.shiftKey ? (i - 1 + matchCount) % matchCount : (i + 1) % matchCount));
              }
            }}
            placeholder="Find in thread..."
            className="w-60 rounded-md border border-[var(--border)] bg-[var(--bg-base)] px-2 py-1 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
          />
          <span className="font-mono text-[11px] text-[var(--text-muted)] min-w-[46px] text-center">
            {matchCount > 0 ? `${(activeMatchIdx % matchCount) + 1}/${matchCount}` : '0/0'}
          </span>
          <button
            type="button"
            disabled={matchCount === 0}
            onClick={() => setActiveMatchIdx((i) => (i - 1 + matchCount) % matchCount)}
            className="rounded p-1 hover:bg-[var(--bg-hover)] disabled:opacity-30 cursor-pointer"
          >
            <ChevronUp className="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            disabled={matchCount === 0}
            onClick={() => setActiveMatchIdx((i) => (i + 1) % matchCount)}
            className="rounded p-1 hover:bg-[var(--bg-hover)] disabled:opacity-30 cursor-pointer"
          >
            <ChevronDown className="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            onClick={() => {
              setFindOpen(false);
              setFindQuery('');
            }}
            className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      )}

      <Timeline
        hostId={activeHost.creds.id}
        session={activeSession}
        detail={activeDetail}
        items={activeTranscript.items}
        findQuery={findOpen ? findQuery : ''}
        activeMatchIdx={activeMatchIdx}
        onMatchCountChange={setMatchCount}
      />

      <Composer
        mode="thread"
        hostId={activeHost.creds.id}
        cwd={activeSession.worktreePath || activeSession.cwd}
        session={activeSession}
        detail={activeDetail}
        transcriptItems={activeTranscript.items}
        openModelPickerSignal={openModelPickerSignal}
      />
    </div>
  );
};
