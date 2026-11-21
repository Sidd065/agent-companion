import React, { useEffect, useRef, useState } from 'react';
import { Undo2, X } from 'lucide-react';
import { DraftView } from './components/chat/DraftView.js';
import { ThreadView } from './components/chat/ThreadView.js';
import { RightPanel } from './components/panels/RightPanel.js';
import { TerminalPanel } from './components/panels/TerminalPanel.js';
import { AddProjectModal } from './components/shell/AddProjectModal.js';
import { CommandPalette } from './components/shell/CommandPalette.js';
import { OnboardingModal } from './components/shell/OnboardingModal.js';
import { Sidebar } from './components/shell/Sidebar.js';
import { TitleBar } from './components/shell/TitleBar.js';
import { SchedulesView } from './components/views/SchedulesView.js';
import { SettingsModal } from './components/views/SettingsModal.js';
import { UsageView } from './components/views/UsageView.js';
import { AppProvider, useApp } from './state/appStore.js';
import { DEFAULT_KEYBINDINGS, matchChord } from './state/keybindings.js';

const Shell: React.FC = () => {
  const {
    settings,
    viewMode,
    setViewMode,
    activeHost,
    activeSession,
    sidebarOpen,
    setSidebarOpen,
    terminalDrawerOpen,
    setTerminalDrawerOpen,
    rightPanelOpen,
    setRightPanelOpen,
    rightPanelMaximized,
    openRightTab,
    reopenClosedTab,
    setCommandPaletteOpen,
    openSettingsTab,
    openDraft,
    callHost,
    toasts,
    dismissToast,
    triggerUndo,
  } = useApp();

  const [findSignal, setFindSignal] = useState(0);
  const [modelPickerSignal, setModelPickerSignal] = useState(0);
  const [rightWidth, setRightWidth] = useState(520);
  const [termHeight, setTermHeight] = useState(250);

  const dragRef = useRef<{ kind: 'right' | 'term'; startPos: number; startSize: number } | null>(null);

  // Drag resize listeners for right panel and terminal drawer
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      const d = dragRef.current;
      if (!d) return;
      if (d.kind === 'right') {
        const delta = d.startPos - e.clientX;
        setRightWidth(Math.max(320, Math.min(window.innerWidth - 360, d.startSize + delta)));
      } else {
        const delta = d.startPos - e.clientY;
        setTermHeight(Math.max(140, Math.min(window.innerHeight - 240, d.startSize + delta)));
      }
    };
    const onUp = () => {
      dragRef.current = null;
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, []);

  // Global keyboard shortcut dispatcher
  useEffect(() => {
    const chordOf = (id: string) =>
      settings.keybindings[id] || DEFAULT_KEYBINDINGS.find((k) => k.id === id)?.defaultChord || '';

    const onKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const inEditable =
        target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || target?.isContentEditable;

      if (matchChord(e, chordOf('commandPalette'))) {
        e.preventDefault();
        setCommandPaletteOpen((v) => !v);
        return;
      }
      if (matchChord(e, chordOf('globalSearch'))) {
        e.preventDefault();
        setCommandPaletteOpen(true);
        return;
      }
      if (matchChord(e, chordOf('fileFinder'))) {
        e.preventDefault();
        openRightTab({ id: 'files', kind: 'files', title: 'Files' });
        return;
      }
      if (matchChord(e, chordOf('findInThread'))) {
        e.preventDefault();
        setFindSignal((s) => s + 1);
        return;
      }
      if (matchChord(e, chordOf('openUsage'))) {
        e.preventDefault();
        setViewMode(viewMode === 'usage' ? (activeSession ? 'thread' : 'draft') : 'usage');
        return;
      }
      if (matchChord(e, chordOf('openSettings'))) {
        e.preventDefault();
        openSettingsTab('general');
        return;
      }
      if (matchChord(e, chordOf('toggleSidebar'))) {
        e.preventDefault();
        setSidebarOpen((v) => !v);
        return;
      }
      if (matchChord(e, chordOf('toggleRightPanel'))) {
        e.preventDefault();
        setRightPanelOpen((v) => !v);
        return;
      }
      if (matchChord(e, chordOf('toggleTerminal'))) {
        e.preventDefault();
        setTerminalDrawerOpen((v) => !v);
        return;
      }
      if (matchChord(e, chordOf('toggleDiff'))) {
        e.preventDefault();
        openRightTab({ id: 'diff', kind: 'diff', title: 'Diff', diffScope: 'uncommitted' });
        return;
      }
      if (matchChord(e, chordOf('reopenClosedTab'))) {
        e.preventDefault();
        reopenClosedTab();
        return;
      }
      if (matchChord(e, chordOf('newThread'))) {
        e.preventDefault();
        openDraft();
        return;
      }
      if (matchChord(e, chordOf('openSideChat'))) {
        e.preventDefault();
        openRightTab({ id: 'side', kind: 'side', title: 'Side Chat' });
        return;
      }
      if (matchChord(e, chordOf('openModelPicker'))) {
        e.preventDefault();
        setModelPickerSignal((s) => s + 1);
        return;
      }
      if (matchChord(e, chordOf('settleThread')) && activeHost && activeSession) {
        e.preventDefault();
        void callHost(activeHost.creds.id, 'session.settle', {
          id: activeSession.id,
          settled: !activeSession.settled,
        });
        return;
      }
      if (matchChord(e, chordOf('pinThread')) && activeHost && activeSession) {
        e.preventDefault();
        void callHost(activeHost.creds.id, 'session.pin', {
          id: activeSession.id,
          pinned: !activeSession.pinned,
        });
        return;
      }
      if (!inEditable && matchChord(e, chordOf('undoThreadAction'))) {
        e.preventDefault();
        triggerUndo();
      }
    };

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [
    settings.keybindings,
    viewMode,
    activeHost,
    activeSession,
    setCommandPaletteOpen,
    openRightTab,
    setViewMode,
    openSettingsTab,
    setSidebarOpen,
    setRightPanelOpen,
    setTerminalDrawerOpen,
    reopenClosedTab,
    openDraft,
    callHost,
    triggerUndo,
  ]);

  return (
    <div className="h-screen w-screen flex flex-col bg-[var(--bg-base)] text-[var(--text-primary)] overflow-hidden">
      <TitleBar />

      <div className="flex-1 flex min-h-0 min-w-0">
        {sidebarOpen && <Sidebar />}

        {/* Center Workspace Column (Stage + Collapsible Bottom Terminal Drawer) */}
        {!(rightPanelOpen && rightPanelMaximized) && (
          <div className="flex-1 flex flex-col min-w-0 min-h-0">
            <div className="flex-1 flex flex-col min-w-0 min-h-0">
              {viewMode === 'usage' ? (
                <UsageView />
              ) : viewMode === 'schedules' ? (
                <SchedulesView />
              ) : viewMode === 'thread' && activeSession ? (
                <ThreadView findSignal={findSignal} openModelPickerSignal={modelPickerSignal} />
              ) : (
                <DraftView openModelPickerSignal={modelPickerSignal} />
              )}
            </div>

            {/* Bottom Resizable Terminal Drawer */}
            {terminalDrawerOpen && (
              <>
                <div
                  onMouseDown={(e) => {
                    dragRef.current = { kind: 'term', startPos: e.clientY, startSize: termHeight };
                  }}
                  className="h-1 bg-[var(--border)] hover:bg-[var(--accent)] cursor-row-resize shrink-0 transition-colors"
                />
                <div style={{ height: termHeight }} className="shrink-0 flex flex-col min-h-0">
                  <TerminalPanel isDrawer onCloseDrawer={() => setTerminalDrawerOpen(false)} />
                </div>
              </>
            )}
          </div>
        )}

        {/* Right Workbench Panel */}
        {rightPanelOpen && (
          <>
            {!rightPanelMaximized && (
              <div
                onMouseDown={(e) => {
                  dragRef.current = { kind: 'right', startPos: e.clientX, startSize: rightWidth };
                }}
                className="w-1 bg-[var(--border)] hover:bg-[var(--accent)] cursor-col-resize shrink-0 transition-colors"
              />
            )}
            <RightPanel widthPx={rightWidth} />
          </>
        )}
      </div>

      {/* Global Modals */}
      <OnboardingModal />
      <AddProjectModal />
      <CommandPalette />
      <SettingsModal />

      {/* Toast Stack with 5s Undo Support */}
      {toasts.length > 0 && (
        <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2 max-w-sm">
          {toasts.map((t) => (
            <div
              key={t.id}
              data-testid="toast-notice"
              className={`flex items-center justify-between gap-3 rounded-xl border px-3.5 py-2.5 text-xs shadow-xl ${
                t.level === 'error'
                  ? 'border-[var(--danger)]/60 bg-[var(--bg-elevated)] text-[var(--danger)]'
                  : t.level === 'success'
                    ? 'border-[var(--success)]/50 bg-[var(--bg-elevated)] text-[var(--text-primary)]'
                    : 'border-[var(--border-strong)] bg-[var(--bg-elevated)] text-[var(--text-primary)]'
              }`}
            >
              <span className="leading-snug">{t.text}</span>
              <div className="flex items-center gap-1.5 shrink-0">
                {t.onUndo && (
                  <button
                    type="button"
                    onClick={() => {
                      t.onUndo?.();
                      dismissToast(t.id);
                    }}
                    className="inline-flex items-center gap-1 rounded-md bg-[var(--accent-soft)] px-2 py-0.5 font-semibold text-[var(--accent)] hover:opacity-90 cursor-pointer"
                  >
                    <Undo2 className="w-3 h-3" />
                    {t.undoLabel || 'Undo'}
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => dismissToast(t.id)}
                  className="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export const App: React.FC = () => (
  <AppProvider>
    <Shell />
  </AppProvider>
);

export default App;
