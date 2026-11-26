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
  Server,
  TerminalSquare,
  Trash2,
} from 'lucide-react';
import { useApp, type RightTab } from '../../state/appStore.js';
import { confirmAction } from '../shell/ConfirmDialog.js';
import { resolveHostScopedCwd } from '../../utils/multiHost.js';

interface Props {
  tab?: RightTab;
  initialTermId?: string;
  onCloseDrawer?: () => void;
  isDrawer?: boolean;
}

type TerminalKind = 'shell' | 'claude' | 'agy' | 'codex';

function kindBadgeLabel(kind?: string): string {
  switch (kind) {
    case 'codex':
      return 'CX';
    case 'claude':
      return 'CL';
    case 'agy':
      return 'AG';
    default:
      return '';
  }
}

export const TerminalPanel: React.FC<Props> = ({ tab, initialTermId, onCloseDrawer, isDrawer = false }) => {
  const {
    hosts,
    hostList,
    activeHost,
    activeSession,
    draft,
    callHost,
    terminalCycleSignal,
    upsertHostTerminal,
    refreshHostTerminals,
    addComposerChip,
    pushToast,
  } = useApp();

  const effectiveInitialTermId = initialTermId ?? tab?.termId;
  const targetHost = (tab?.hostId ? hosts[tab.hostId] : null) ?? activeHost;
  const hostId = targetHost?.creds.id ?? '';
  const activeSessionHostId = activeHost?.creds.id ?? '';
  const terminals = targetHost?.terminals ?? [];
  const cwd = resolveHostScopedCwd({
    hostId,
    tabHostId: tab?.hostId,
    tabFilePath: tab?.filePath,
    activeSession,
    activeSessionHostId,
    draft,
    hostHome: targetHost?.info?.home,
    fallback: '~',
  });

  const [selectedId, setSelectedId] = useState<string>(effectiveInitialTermId || '');
  const [creating, setCreating] = useState(false);
  const [statusMsg, setStatusMsg] = useState('');
  const [probingInitialTerm, setProbingInitialTerm] = useState(false);

  const userPickedRef = useRef(false);
  const probedMissingRef = useRef<Set<string>>(new Set());

  const containerRef = useRef<HTMLDivElement | null>(null);
  const xtermRef = useRef<XTerm | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const streamRef = useRef<TerminalStream | null>(null);

  // Reset selected terminal when switching target host or explicit initialTermId
  const prevTargetRef = useRef(`${hostId}:${effectiveInitialTermId || ''}`);
  useEffect(() => {
    const nextKey = `${hostId}:${effectiveInitialTermId || ''}`;
    if (prevTargetRef.current !== nextKey) {
      prevTargetRef.current = nextKey;
      userPickedRef.current = false;
      setSelectedId(effectiveInitialTermId || '');
      setStatusMsg('');
    }
  }, [hostId, effectiveInitialTermId]);

  // Pick a valid terminal automatically when terminals list changes, refreshing once if initialTermId isn't listed yet
  useEffect(() => {
    if (effectiveInitialTermId && !userPickedRef.current) {
      if (terminals.some((t) => t.id === effectiveInitialTermId)) {
        if (selectedId !== effectiveInitialTermId) {
          setSelectedId(effectiveInitialTermId);
        }
        return;
      }
      const probeKey = `${hostId}:${effectiveInitialTermId}`;
      if (hostId && !probedMissingRef.current.has(probeKey)) {
        probedMissingRef.current.add(probeKey);
        if (selectedId !== effectiveInitialTermId) {
          setSelectedId(effectiveInitialTermId);
        }
        setProbingInitialTerm(true);
        void refreshHostTerminals(hostId).finally(() => {
          setProbingInitialTerm(false);
        });
        return;
      }
      if (probingInitialTerm) {
        return;
      }
    }
    if (selectedId && terminals.some((t) => t.id === selectedId)) return;
    const forSession =
      activeSession && activeSessionHostId === hostId
        ? terminals.find((t) => t.sessionId === activeSession.id || t.conversationId === activeSession.id)
        : undefined;
    setSelectedId(forSession?.id || terminals[0]?.id || '');
  }, [
    hostId,
    effectiveInitialTermId,
    terminals,
    selectedId,
    activeSession,
    activeSessionHostId,
    refreshHostTerminals,
    probingInitialTerm,
  ]);

  const prevCycleSeqRef = useRef(terminalCycleSignal.seq);
  useEffect(() => {
    if (terminalCycleSignal.seq === prevCycleSeqRef.current) return;
    prevCycleSeqRef.current = terminalCycleSignal.seq;
    if (terminals.length < 2) return;
    const curIdx = Math.max(
      0,
      terminals.findIndex((t) => t.id === selectedId),
    );
    const nextIdx = (curIdx + terminalCycleSignal.delta + terminals.length) % terminals.length;
    const nextTerm = terminals[nextIdx];
    if (nextTerm) {
      userPickedRef.current = true;
      setSelectedId(nextTerm.id);
    }
  }, [terminalCycleSignal, terminals, selectedId]);

  const activeTerm: TerminalInfo | null = terminals.find((t) => t.id === selectedId) ?? null;

  const createTerminal = useCallback(
    async (kind: TerminalKind = 'shell') => {
      if (!hostId || creating) return;
      setCreating(true);
      try {
        const dirBase = cwd.split('/').filter(Boolean).pop() || 'home';
        const sessionForTerm = activeSessionHostId === hostId ? activeSession?.id || '' : '';
        const created = await callHost<TerminalInfo>(hostId, 'term.create', {
          kind,
          cwd,
          name: `${kind} · ${dirBase}`,
          cols: 100,
          rows: 26,
          sessionId: sessionForTerm,
          remainOnExit: true,
        });
        if (created?.id) {
          upsertHostTerminal(hostId, created);
          userPickedRef.current = true;
          setSelectedId(created.id);
        }
      } catch (e) {
        pushToast({ text: `Create terminal failed: ${(e as Error).message}`, level: 'error' });
      } finally {
        setCreating(false);
      }
    },
    [hostId, creating, cwd, activeSessionHostId, activeSession?.id, callHost, upsertHostTerminal, pushToast],
  );

  const killTerminal = async (termId: string) => {
    if (!hostId) return;
    try {
      await callHost(hostId, 'term.kill', { id: termId });
      if (selectedId === termId) {
        userPickedRef.current = false;
        setSelectedId('');
      }
    } catch (e) {
      pushToast({ text: `Kill terminal failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  // Mount xterm + attach encrypted TerminalStream (or capture dead output)
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !targetHost || !activeTerm) return;

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
      void callHost<{ text?: string } | string>(targetHost.creds.id, 'term.capture', {
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
      const stream = new TerminalStream(targetHost.creds, activeTerm.id, cols, rows, {
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
  }, [targetHost?.creds.id, activeTerm?.id, activeTerm?.dead]); // eslint-disable-line react-hooks/exhaustive-deps

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

  const tuiAgents = (targetHost?.agents ?? []).filter(
    (a): a is typeof a & { id: 'codex' | 'claude' | 'agy' } =>
      a.available && (a.id === 'codex' || a.id === 'claude' || a.id === 'agy'),
  );

  return (
    <div data-testid="terminal-panel" className="flex-1 flex flex-col min-h-0 bg-[#0c0d10] text-xs">
      {/* Terminal Tab Strip */}
      <div className="flex items-center justify-between gap-2 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-2.5 py-1">
        <div className="flex items-center gap-1 overflow-x-auto min-w-0">
          {hostList.length > 1 && targetHost && (
            <span
              data-testid="terminal-host-badge"
              className="inline-flex items-center gap-1 rounded border border-[var(--border)] bg-[var(--bg-elevated)] px-1.5 py-0.5 text-[10px] text-[var(--text-secondary)] shrink-0"
            >
              <Server className="w-2.5 h-2.5 text-[var(--accent)]" />
              {targetHost.creds.name}
            </span>
          )}
          {terminals.map((t) => {
            const badge = kindBadgeLabel(t.kind);
            return (
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
                  onClick={() => {
                    userPickedRef.current = true;
                    setSelectedId(t.id);
                  }}
                  className="inline-flex items-center gap-1.5 cursor-pointer"
                >
                  <span
                    className={`w-1.5 h-1.5 rounded-full ${
                      t.dead ? 'bg-[var(--danger)]' : 'bg-[var(--success)]'
                    }`}
                  />
                  {badge && (
                    <span className="rounded bg-[var(--bg-elevated)] px-1 text-[9.5px] font-mono text-[var(--accent)]">
                      {badge}
                    </span>
                  )}
                  <span className="max-w-[140px] truncate">{t.name || t.id}</span>
                </button>
                <button
                  type="button"
                  onClick={() => {
                    void confirmAction({
                      title: 'Kill Terminal Session',
                      message: `Terminate tmux session "${t.name || t.id}" on the host?`,
                      confirmLabel: 'Kill Session',
                    }).then((ok) => {
                      if (ok) void killTerminal(t.id);
                    });
                  }}
                  title="Kill terminal"
                  className="opacity-50 group-hover:opacity-100 hover:text-[var(--danger)] cursor-pointer"
                >
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            );
          })}
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
          {tuiAgents.map((ag) => (
            <button
              key={ag.id}
              type="button"
              data-testid={`terminal-new-${ag.id}-btn`}
              disabled={creating || !hostId}
              onClick={() => void createTerminal(ag.id)}
              title={`Launch ${ag.name} TUI in ${cwd}`}
              className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--accent)] hover:bg-[var(--accent-soft)] cursor-pointer shrink-0"
            >
              <Plus className="w-3 h-3" />
              {ag.id === 'codex' ? 'Codex' : ag.id === 'claude' ? 'Claude' : 'Agy'}
            </button>
          ))}
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
          <div>No terminals running on {targetHost?.creds.name || 'this host'}.</div>
          <div className="flex flex-wrap items-center justify-center gap-2">
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
            {tuiAgents.map((ag) => (
              <button
                key={ag.id}
                type="button"
                data-testid={`terminal-empty-${ag.id}-btn`}
                disabled={creating || !hostId}
                onClick={() => void createTerminal(ag.id)}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-1.5 text-xs font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <Plus className="w-3.5 h-3.5 text-[var(--accent)]" />
                {ag.id === 'codex' ? 'Start Codex TUI' : ag.id === 'claude' ? 'Start Claude TUI' : 'Start Agy TUI'}
              </button>
            ))}
          </div>
        </div>
      ) : (
        <div ref={containerRef} data-testid="xterm-container" className="flex-1 min-h-0 w-full p-1.5" />
      )}
    </div>
  );
};
