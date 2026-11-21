import React, { useCallback, useEffect, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal as XTerm } from '@xterm/xterm';
import { TerminalStream } from '@agentc/client-core';
import type { TerminalInfo } from '@agentc/protocol';
import {
  ChevronDown,
  MessageSquarePlus,
  Plus,
  TerminalSquare,
  Trash2,
  X,
} from 'lucide-react';
import { useApp } from '../../state/appStore.js';

interface Props {
  initialTermId?: string;
  onCloseDrawer?: () => void;
  isDrawer?: boolean;
}

export const TerminalPanel: React.FC<Props> = ({ initialTermId, onCloseDrawer, isDrawer = false }) => {
  const {
    activeHost,
    activeSession,
    draft,
    callHost,
    addComposerChip,
    pushToast,
  } = useApp();

  const hostId = activeHost?.creds.id ?? '';
  const terminals = activeHost?.terminals ?? [];
  const cwd = activeSession?.worktreePath || activeSession?.cwd || draft.cwd || '~';

  const [selectedId, setSelectedId] = useState<string>(initialTermId || '');
  const [creating, setCreating] = useState(false);
  const [statusMsg, setStatusMsg] = useState('');

  const containerRef = useRef<HTMLDivElement | null>(null);
  const xtermRef = useRef<XTerm | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const streamRef = useRef<TerminalStream | null>(null);

  // Pick a valid terminal automatically when terminals list changes
  useEffect(() => {
    if (initialTermId && terminals.some((t) => t.id === initialTermId)) {
      setSelectedId(initialTermId);
      return;
    }
    if (selectedId && terminals.some((t) => t.id === selectedId)) return;
    const forSession = activeSession
      ? terminals.find((t) => t.sessionId === activeSession.id)
      : undefined;
    setSelectedId(forSession?.id || terminals[0]?.id || '');
  }, [initialTermId, terminals, selectedId, activeSession]);

  const activeTerm: TerminalInfo | null = terminals.find((t) => t.id === selectedId) ?? null;

  const createTerminal = useCallback(
    async (kind: 'shell' | 'claude' | 'agy' = 'shell') => {
      if (!hostId || creating) return;
      setCreating(true);
      try {
        const created = await callHost<TerminalInfo>(hostId, 'term.create', {
          kind,
          cwd,
          name: kind === 'shell' ? `shell · ${cwd.split('/').pop() || 'home'}` : `${kind} · ${cwd.split('/').pop() || 'home'}`,
          cols: 100,
          rows: 26,
          sessionId: activeSession?.id || '',
          remainOnExit: true,
        });
        if (created?.id) setSelectedId(created.id);
      } catch (e) {
        pushToast({ text: `Create terminal failed: ${(e as Error).message}`, level: 'error' });
      } finally {
        setCreating(false);
      }
    },
    [hostId, creating, cwd, activeSession?.id, callHost, pushToast],
  );

  const killTerminal = async (termId: string) => {
    if (!hostId) return;
    try {
      await callHost(hostId, 'term.kill', { id: termId });
      if (selectedId === termId) setSelectedId('');
    } catch (e) {
      pushToast({ text: `Kill terminal failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  // Mount xterm + attach encrypted TerminalStream (or capture dead output)
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !activeHost || !activeTerm) return;

    setStatusMsg('');
    const term = new XTerm({
      cursorBlink: true,
      fontSize: 12.5,
      fontFamily: "'JetBrains Mono', 'Fira Code', ui-monospace, monospace",
      theme: {
        background: '#0c0d10',
        foreground: '#eceef4',
        cursor: '#6ea8ff',
        selectionBackground: 'rgba(110,168,255,0.32)',
      },
      scrollback: 5000,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(el);
    try {
      fit.fit();
    } catch {
      // ignore initial layout before visible
    }
    xtermRef.current = term;
    fitRef.current = fit;

    const decoder = new TextDecoder();

    if (activeTerm.dead) {
      setStatusMsg(`Process exited (${activeTerm.deadStatus ?? 0}) — preserved output`);
      void callHost<{ text?: string } | string>(activeHost.creds.id, 'term.capture', {
        id: activeTerm.id,
        lines: 400,
      })
        .then((res) => {
          const text = typeof res === 'string' ? res : res?.text ?? '';
          if (text) term.write(text.replace(/\r?\n/g, '\r\n'));
        })
        .catch(() => {});
    } else {
      const cols = Math.max(20, term.cols || 100);
      const rows = Math.max(6, term.rows || 24);
      const stream = new TerminalStream(activeHost.creds, activeTerm.id, cols, rows, {
        onOpen: () => {
          setStatusMsg('');
          try {
            fit.fit();
            stream.resize(term.cols, term.rows);
          } catch {
            // ignore
          }
        },
        onData: (data) => {
          term.write(decoder.decode(data, { stream: true }));
        },
        onExit: (msg) => {
          setStatusMsg(msg ? `Disconnected: ${msg}` : 'Session exited');
        },
        onError: (msg) => {
          setStatusMsg(`Error: ${msg}`);
        },
      });
      streamRef.current = stream;
      stream.connect();

      const dataDisp = term.onData((data) => {
        stream.write(data);
      });

      const ro = new ResizeObserver(() => {
        try {
          fit.fit();
          stream.resize(term.cols, term.rows);
        } catch {
          // ignore
        }
      });
      ro.observe(el);

      return () => {
        ro.disconnect();
        dataDisp.dispose();
        stream.close();
        streamRef.current = null;
        term.dispose();
        xtermRef.current = null;
        fitRef.current = null;
      };
    }

    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        // ignore
      }
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      term.dispose();
      xtermRef.current = null;
      fitRef.current = null;
    };
  }, [activeHost?.creds.id, activeTerm?.id, activeTerm?.dead]); // eslint-disable-line react-hooks/exhaustive-deps

  const citeSelection = () => {
    const sel = xtermRef.current?.getSelection()?.trim();
    if (!sel) {
      pushToast({ text: 'Select text in the terminal first to cite it', level: 'info' });
      return;
    }
    addComposerChip({
      kind: 'terminal',
      label: `Terminal (${activeTerm?.name || 'shell'})`,
      content: `\`\`\`\n${sel.slice(0, 2000)}\n\`\`\``,
    });
    pushToast({ text: 'Terminal selection added to composer', level: 'info' });
  };

  return (
    <div data-testid="terminal-panel" className="flex-1 flex flex-col min-h-0 bg-[#0c0d10] text-xs">
      {/* Terminal Tab Strip */}
      <div className="flex items-center justify-between gap-2 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-2.5 py-1">
        <div className="flex items-center gap-1 overflow-x-auto min-w-0">
          {terminals.map((t) => (
            <div
              key={t.id}
              className={`group inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-[11px] shrink-0 ${
                selectedId === t.id
                  ? 'bg-[var(--accent-soft)] text-[var(--text-primary)] font-medium'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
              }`}
            >
              <button
                type="button"
                data-testid="terminal-tab"
                onClick={() => setSelectedId(t.id)}
                className="inline-flex items-center gap-1.5 cursor-pointer"
              >
                <span
                  className={`w-1.5 h-1.5 rounded-full ${
                    t.dead ? 'bg-[var(--danger)]' : 'bg-[var(--success)]'
                  }`}
                />
                <span className="max-w-[140px] truncate">{t.name || t.id}</span>
              </button>
              <button
                type="button"
                onClick={() => void killTerminal(t.id)}
                title="Kill terminal"
                className="opacity-50 group-hover:opacity-100 hover:text-[var(--danger)] cursor-pointer"
              >
                <Trash2 className="w-3 h-3" />
              </button>
            </div>
          ))}
          <button
            type="button"
            data-testid="terminal-new-btn"
            disabled={creating || !hostId}
            onClick={() => void createTerminal('shell')}
            title="New shell terminal"
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer shrink-0"
          >
            <Plus className="w-3 h-3" />
            Shell
          </button>
        </div>

        <div className="flex items-center gap-1.5 shrink-0">
          {statusMsg && <span className="text-[10.5px] text-[var(--warning)]">{statusMsg}</span>}
          <button
            type="button"
            onClick={citeSelection}
            title="Add selected terminal text to composer"
            className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          >
            <MessageSquarePlus className="w-3 h-3" />
            Cite
          </button>
          {isDrawer && onCloseDrawer && (
            <button
              type="button"
              onClick={onCloseDrawer}
              title="Hide terminal drawer (Ctrl+J)"
              className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <ChevronDown className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
      </div>

      {/* Terminal Body */}
      {!activeTerm ? (
        <div className="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-[var(--text-muted)]">
          <TerminalSquare className="w-7 h-7 opacity-40" />
          <div>No terminals running on {activeHost?.creds.name || 'this host'}.</div>
          <button
            type="button"
            data-testid="terminal-empty-create-btn"
            disabled={creating || !hostId}
            onClick={() => void createTerminal('shell')}
            className="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent)] px-3 py-1.5 text-xs font-semibold text-[var(--accent-fg)] cursor-pointer"
          >
            <Plus className="w-3.5 h-3.5" />
            Start Interactive Shell
          </button>
        </div>
      ) : (
        <div ref={containerRef} data-testid="xterm-container" className="flex-1 min-h-0 w-full p-1.5" />
      )}
    </div>
  );
};
