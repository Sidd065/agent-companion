import React, { useState } from 'react';
import {
  FileCode2,
  FileDiff,
  FileText,
  GitBranch,
  GitPullRequest,
  Globe,
  Maximize2,
  MessageSquare,
  Minimize2,
  Plus,
  TerminalSquare,
  X,
} from 'lucide-react';
import { useApp, type RightTabKind } from '../../state/appStore.js';
import { ArtifactsPanel } from './ArtifactsPanel.js';
import { BrowserPanel } from './BrowserPanel.js';
import { DiffPanel } from './DiffPanel.js';
import { FilesPanel } from './FilesPanel.js';
import { PullRequestPanel } from './PullRequestPanel.js';
import { SideChatPanel } from './SideChatPanel.js';
import { SourceControlPanel } from './SourceControlPanel.js';
import { TerminalPanel } from './TerminalPanel.js';

const TAB_CHOICES: Array<{ kind: RightTabKind; title: string; icon: React.FC<{ className?: string }> }> = [
  { kind: 'diff', title: 'Diff', icon: FileDiff },
  { kind: 'files', title: 'Files', icon: FileCode2 },
  { kind: 'git', title: 'Source Control', icon: GitBranch },
  { kind: 'pr', title: 'Pull Requests', icon: GitPullRequest },
  { kind: 'side', title: 'Side Chat', icon: MessageSquare },
  { kind: 'artifacts', title: 'Artifacts', icon: FileText },
  { kind: 'terminal', title: 'Terminal', icon: TerminalSquare },
  { kind: 'browser', title: 'Preview', icon: Globe },
];

export const RightPanel: React.FC<{ widthPx: number }> = ({ widthPx }) => {
  const {
    rightTabs,
    activeRightTabId,
    setActiveRightTabId,
    openRightTab,
    closeRightTab,
    setRightPanelOpen,
    rightPanelMaximized,
    setRightPanelMaximized,
  } = useApp();

  const [pickerOpen, setPickerOpen] = useState(false);

  const activeTab = rightTabs.find((t) => t.id === activeRightTabId) ?? rightTabs[0] ?? null;

  return (
    <div
      data-testid="right-panel"
      style={rightPanelMaximized ? undefined : { width: widthPx }}
      className={`flex flex-col min-h-0 border-l border-[var(--border)] bg-[var(--bg-base)] ${
        rightPanelMaximized ? 'flex-1' : 'shrink-0'
      }`}
    >
      {/* Multi-Tab Bar */}
      <div className="flex items-center justify-between gap-1 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-2 py-1">
        <div className="flex items-center gap-1 overflow-x-auto min-w-0">
          {rightTabs.map((t) => {
            const active = t.id === activeTab?.id;
            const meta = TAB_CHOICES.find((c) => c.kind === t.kind);
            const Icon = meta?.icon ?? FileDiff;
            return (
              <div
                key={t.id}
                className={`group inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs shrink-0 ${
                  active
                    ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] font-medium border border-[var(--border)]'
                    : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                }`}
              >
                <button
                  type="button"
                  onClick={() => setActiveRightTabId(t.id)}
                  className="inline-flex items-center gap-1.5 cursor-pointer"
                >
                  <Icon className="w-3.5 h-3.5 text-[var(--accent)]" />
                  <span className="max-w-[120px] truncate">{t.title}</span>
                </button>
                <button
                  type="button"
                  onClick={() => closeRightTab(t.id)}
                  title="Close tab (Ctrl+W)"
                  className="opacity-50 group-hover:opacity-100 hover:text-[var(--danger)] cursor-pointer"
                >
                  <X className="w-3 h-3" />
                </button>
              </div>
            );
          })}

          {/* + Tab Picker */}
          <div className="relative">
            <button
              type="button"
              data-testid="right-panel-add-tab"
              onClick={() => setPickerOpen((v) => !v)}
              title="Open panel tab"
              className="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <Plus className="w-3.5 h-3.5" />
            </button>
            {pickerOpen && (
              <div className="absolute left-0 top-full mt-1 z-40 w-44 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-1 shadow-xl">
                {TAB_CHOICES.map((ch) => {
                  const Icon = ch.icon;
                  return (
                    <button
                      key={ch.kind}
                      type="button"
                      onClick={() => {
                        setPickerOpen(false);
                        openRightTab({ id: ch.kind, kind: ch.kind, title: ch.title });
                      }}
                      className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <Icon className="w-3.5 h-3.5 text-[var(--accent)]" />
                      <span>{ch.title}</span>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </div>

        <div className="flex items-center gap-0.5 shrink-0">
          <button
            type="button"
            onClick={() => setRightPanelMaximized((v) => !v)}
            title={rightPanelMaximized ? 'Restore split' : 'Maximize panel'}
            className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            {rightPanelMaximized ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
          </button>
          <button
            type="button"
            onClick={() => {
              setRightPanelMaximized(false);
              setRightPanelOpen(false);
            }}
            title="Close panel"
            className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>

      {/* Active Tab Content */}
      <div className="flex-1 flex flex-col min-h-0">
        {!activeTab ? (
          <div className="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-xs text-[var(--text-muted)]">
            <div>Choose a workbench panel:</div>
            <div className="flex flex-wrap justify-center gap-1.5 max-w-xs">
              {TAB_CHOICES.map((ch) => (
                <button
                  key={ch.kind}
                  type="button"
                  onClick={() => openRightTab({ id: ch.kind, kind: ch.kind, title: ch.title })}
                  className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 text-xs hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  {ch.title}
                </button>
              ))}
            </div>
          </div>
        ) : activeTab.kind === 'diff' ? (
          <DiffPanel tab={activeTab} />
        ) : activeTab.kind === 'files' ? (
          <FilesPanel tab={activeTab} />
        ) : activeTab.kind === 'side' ? (
          <SideChatPanel />
        ) : activeTab.kind === 'artifacts' ? (
          <ArtifactsPanel />
        ) : activeTab.kind === 'pr' ? (
          <PullRequestPanel />
        ) : activeTab.kind === 'git' ? (
          <SourceControlPanel />
        ) : activeTab.kind === 'terminal' ? (
          <TerminalPanel initialTermId={activeTab.termId} />
        ) : (
          <BrowserPanel tab={activeTab} />
        )}
      </div>
    </div>
  );
};
