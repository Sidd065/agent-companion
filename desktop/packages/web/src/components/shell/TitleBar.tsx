import React from 'react';
import {
  Activity,
  Clock,
  Command,
  GitBranch,
  Info,
  PanelLeft,
  PanelRight,
  Plus,
  Server,
  Settings,
  TerminalSquare,
} from 'lucide-react';
import { useApp } from '../../state/appStore.js';

export const TitleBar: React.FC = () => {
  const {
    activeHost,
    activeSession,
    activeDetail,
    viewMode,
    setViewMode,
    sidebarOpen,
    setSidebarOpen,
    infoCardOpen,
    setInfoCardOpen,
    terminalDrawerOpen,
    setTerminalDrawerOpen,
    rightPanelOpen,
    setRightPanelOpen,
    rightTabs,
    openRightTab,
    setCommandPaletteOpen,
    setPairModalOpen,
    openSettingsTab,
  } = useApp();

  const projectName = activeHost?.projects.find((p) => p.id === activeSession?.projectId)?.name;

  return (
    <header
      className="app-drag h-[38px] shrink-0 flex items-center justify-between px-3 border-b border-[var(--border)] bg-[var(--bg-sidebar)] select-none"
      style={{ paddingRight: window.agentcDesktop ? '138px' : '12px' }}
    >
      <div className="app-no-drag flex items-center gap-1.5 min-w-0">
        <button
          type="button"
          data-testid="toggle-sidebar-btn"
          title="Toggle Sidebar (Ctrl+B)"
          onClick={() => setSidebarOpen((v) => !v)}
          className={`p-1.5 rounded hover:bg-[var(--bg-hover)] cursor-pointer ${sidebarOpen ? 'text-[var(--text-primary)]' : 'text-[var(--text-muted)]'}`}
        >
          <PanelLeft className="w-4 h-4" />
        </button>

        <div className="flex items-center gap-1.5 text-xs text-[var(--text-secondary)] truncate ml-1">
          {activeHost && (
            <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] text-[11px]">
              <span
                className={`w-1.5 h-1.5 rounded-full ${
                  activeHost.state === 'connected'
                    ? 'bg-[var(--success)]'
                    : activeHost.state === 'reconnecting' || activeHost.state === 'connecting'
                      ? 'bg-[var(--warning)]'
                      : 'bg-[var(--danger)]'
                }`}
              />
              {activeHost.creds.name}
            </span>
          )}
          {viewMode === 'thread' && activeSession && (
            <>
              {projectName && <span className="text-[var(--text-muted)]">/ {projectName}</span>}
              <span className="font-medium text-[var(--text-primary)] truncate max-w-[340px]">
                / {activeSession.title || 'Untitled session'}
              </span>
              {(activeSession.branch || activeDetail?.branch) && (
                <span className="inline-flex items-center gap-1 text-[11px] text-[var(--text-muted)] bg-[var(--bg-surface)] px-1.5 py-0.5 rounded border border-[var(--border)]">
                  <GitBranch className="w-3 h-3" />
                  {activeSession.branch || activeDetail?.branch}
                </span>
              )}
            </>
          )}
          {viewMode === 'draft' && <span className="text-[var(--text-muted)]">/ New thread</span>}
          {viewMode === 'usage' && <span className="font-medium text-[var(--text-primary)]">/ Usage & Cost</span>}
          {viewMode === 'schedules' && <span className="font-medium text-[var(--text-primary)]">/ Scheduled Tasks</span>}
        </div>
      </div>

      <div className="app-no-drag flex items-center gap-1">
        <button
          type="button"
          data-testid="command-palette-btn"
          onClick={() => setCommandPaletteOpen(true)}
          className="flex items-center gap-1.5 px-2.5 py-1 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer mr-1"
          title="Command Palette (Ctrl+K)"
        >
          <Command className="w-3.5 h-3.5" />
          <span className="hidden sm:inline">Search</span>
          <kbd className="text-[10px] text-[var(--text-muted)] bg-[var(--bg-elevated)] px-1 rounded">Ctrl+K</kbd>
        </button>

        <button
          type="button"
          data-testid="pair-host-btn"
          onClick={() => setPairModalOpen(true)}
          title="Hosts & Pairing"
          className="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] cursor-pointer flex items-center gap-1 text-xs"
        >
          <Server className="w-3.5 h-3.5" />
          <Plus className="w-3 h-3 -ml-0.5" />
        </button>

        <button
          type="button"
          data-testid="usage-view-btn"
          onClick={() => setViewMode(viewMode === 'usage' ? 'thread' : 'usage')}
          title="Usage & Cost Dashboard (Ctrl+U)"
          className={`p-1.5 rounded hover:bg-[var(--bg-hover)] cursor-pointer ${viewMode === 'usage' ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--text-secondary)]'}`}
        >
          <Activity className="w-4 h-4" />
        </button>

        <button
          type="button"
          data-testid="schedules-view-btn"
          onClick={() => setViewMode(viewMode === 'schedules' ? 'thread' : 'schedules')}
          title="Scheduled Tasks"
          className={`p-1.5 rounded hover:bg-[var(--bg-hover)] cursor-pointer ${viewMode === 'schedules' ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--text-secondary)]'}`}
        >
          <Clock className="w-4 h-4" />
        </button>

        <div className="w-px h-4 bg-[var(--border)] mx-1" />

        <button
          type="button"
          data-testid="toggle-info-card-btn"
          title="Toggle Thread Info Card"
          onClick={() => setInfoCardOpen((v) => !v)}
          className={`p-1.5 rounded hover:bg-[var(--bg-hover)] cursor-pointer ${infoCardOpen ? 'text-[var(--accent)]' : 'text-[var(--text-secondary)]'}`}
        >
          <Info className="w-4 h-4" />
        </button>

        <button
          type="button"
          data-testid="toggle-terminal-btn"
          title="Toggle Terminal Drawer (Ctrl+J)"
          onClick={() => setTerminalDrawerOpen((v) => !v)}
          className={`p-1.5 rounded hover:bg-[var(--bg-hover)] cursor-pointer ${terminalDrawerOpen ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--text-secondary)]'}`}
        >
          <TerminalSquare className="w-4 h-4" />
        </button>

        <button
          type="button"
          data-testid="toggle-right-panel-btn"
          title="Toggle Right Panel (Ctrl+Alt+B)"
          onClick={() => {
            if (!rightPanelOpen && rightTabs.length === 0) {
              openRightTab({ kind: 'diff', title: 'Diff', diffScope: 'uncommitted' });
            } else {
              setRightPanelOpen((v) => !v);
            }
          }}
          className={`p-1.5 rounded hover:bg-[var(--bg-hover)] cursor-pointer ${rightPanelOpen ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--text-secondary)]'}`}
        >
          <PanelRight className="w-4 h-4" />
        </button>

        <button
          type="button"
          data-testid="open-settings-btn"
          title="Settings (Ctrl+,)"
          onClick={() => openSettingsTab('general')}
          className="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] cursor-pointer"
        >
          <Settings className="w-4 h-4" />
        </button>
      </div>
    </header>
  );
};
