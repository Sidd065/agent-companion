import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import {
  applyOps,
  applySnapshot,
  emptyTranscript,
  HostSupervisor,
  pairWithLink,
  type TranscriptState,
} from '@agentc/client-core';
import type {
  AgentProfile,
  AsideItem,
  DaemonInfo,
  Device,
  HostConnectionState,
  Notification,
  PairedHostCredentials,
  Project,
  ScheduledTask,
  ScheduleRunRecord,
  SessionDetail,
  SessionSummary,
  TerminalInfo,
  TranscriptItem,
  TranscriptOp,
} from '@agentc/protocol';
import type { DetectedEditor, LocalDaemonStatus } from '../types/desktop.js';
import { applyTheme } from './themes.js';

export interface ClientSettings {
  themeId: string;
  diffPalette: 'red-green' | 'blue-orange';
  diffViewMode: 'stacked' | 'split';
  diffWrap: boolean;
  diffIgnoreWhitespace: boolean;
  diffHideTests: boolean;
  toolVerbosity: 'showAll' | 'hideSuccessful' | 'hideAll' | 'zen';
  sendShortcut: 'enter' | 'mod-enter';
  followUpBehavior: 'steer' | 'queue';
  showWorkingShelf: boolean;
  groupByProject: boolean;
  notificationsEnabled: boolean;
  closeToTray: boolean;
  renderMath: boolean;
  chatMaxWidth: number;
  fontSize: number;
  customCss: string;
  favoriteModels: string[];
  favoriteEditor: string;
  keybindings: Record<string, string>;
}

export const DEFAULT_SETTINGS: ClientSettings = {
  themeId: 'obsidian',
  diffPalette: 'red-green',
  diffViewMode: 'stacked',
  diffWrap: true,
  diffIgnoreWhitespace: false,
  diffHideTests: false,
  toolVerbosity: 'showAll',
  sendShortcut: 'enter',
  followUpBehavior: 'queue',
  showWorkingShelf: true,
  groupByProject: false,
  notificationsEnabled: true,
  closeToTray: false,
  renderMath: true,
  chatMaxWidth: 860,
  fontSize: 13.5,
  customCss: '',
  favoriteModels: [],
  favoriteEditor: 'code',
  keybindings: {},
};

export interface HostRuntime {
  creds: PairedHostCredentials;
  state: HostConnectionState;
  error?: string;
  info?: DaemonInfo;
  agents: AgentProfile[];
  projects: Project[];
  sessions: SessionSummary[];
  terminals: TerminalInfo[];
  devices: Device[];
  schedules: { tasks: ScheduledTask[]; runs: ScheduleRunRecord[] };
}

export type RightTabKind = 'diff' | 'files' | 'side' | 'artifacts' | 'pr' | 'git' | 'terminal' | 'browser';

export interface RightTab {
  id: string;
  kind: RightTabKind;
  title: string;
  diffScope?: 'uncommitted' | 'latest' | 'turn' | 'session' | 'branch' | 'staged';
  turnIndex?: number;
  filePath?: string;
  browserUrl?: string;
  termId?: string;
}

export interface ComposerContextChip {
  id: string;
  kind: 'citation' | 'diff-comment' | 'file-comment' | 'terminal' | 'side-chat';
  label: string;
  content: string;
}

export interface ToastNotice {
  id: string;
  text: string;
  level: 'info' | 'success' | 'error';
  undoLabel?: string;
  onUndo?: () => void;
}

export interface DraftConfig {
  hostId: string;
  projectId?: string;
  cwd: string;
  agent: string;
  model: string;
  mode: string;
  thought: string;
  skipPermissions: boolean;
  useWorktree: boolean;
  baseBranch: string;
  branchName: string;
}

export type MainViewMode = 'thread' | 'draft' | 'usage' | 'schedules';

export function threadKey(hostId: string, sessionId: string): string {
  return `${hostId}:${sessionId}`;
}

interface AppContextValue {
  hosts: Record<string, HostRuntime>;
  hostList: HostRuntime[];
  localDaemon: LocalDaemonStatus | null;
  editors: DetectedEditor[];
  settings: ClientSettings;
  updateSettings: (patch: Partial<ClientSettings>) => void;
  resetSettings: () => void;

  // Navigation & layout
  viewMode: MainViewMode;
  setViewMode: (m: MainViewMode) => void;
  activeHostId: string;
  setActiveHostId: (id: string) => void;
  activeSessionId: string | null;
  selectSession: (hostId: string, sessionId: string) => void;
  openDraft: (partial?: Partial<DraftConfig>) => void;
  draft: DraftConfig;
  setDraft: React.Dispatch<React.SetStateAction<DraftConfig>>;

  // Modals & layout panels
  sidebarOpen: boolean;
  setSidebarOpen: React.Dispatch<React.SetStateAction<boolean>>;
  infoCardOpen: boolean;
  setInfoCardOpen: React.Dispatch<React.SetStateAction<boolean>>;
  terminalDrawerOpen: boolean;
  setTerminalDrawerOpen: React.Dispatch<React.SetStateAction<boolean>>;
  rightPanelOpen: boolean;
  setRightPanelOpen: React.Dispatch<React.SetStateAction<boolean>>;
  rightPanelMaximized: boolean;
  setRightPanelMaximized: React.Dispatch<React.SetStateAction<boolean>>;
  rightTabs: RightTab[];
  activeRightTabId: string | null;
  setActiveRightTabId: (id: string | null) => void;
  openRightTab: (tab: Omit<RightTab, 'id'> & { id?: string }) => void;
  closeRightTab: (id: string) => void;
  reopenClosedTab: () => void;

  pairModalOpen: boolean;
  setPairModalOpen: (v: boolean) => void;
  addProjectModalOpen: boolean;
  setAddProjectModalOpen: (v: boolean) => void;
  settingsModalOpen: boolean;
  setSettingsModalOpen: (v: boolean) => void;
  settingsInitialTab: string;
  openSettingsTab: (tab: string) => void;
  commandPaletteOpen: boolean;
  setCommandPaletteOpen: React.Dispatch<React.SetStateAction<boolean>>;

  // Active thread state
  activeHost: HostRuntime | null;
  activeSession: SessionSummary | null;
  activeDetail: SessionDetail | null;
  activeTranscript: TranscriptState;
  activeAsides: AsideItem[];
  allSessions: Array<SessionSummary & { hostId: string; hostName: string }>;
  allProjects: Array<Project & { hostId: string; hostName: string }>;

  // Composer bridge
  composerChips: ComposerContextChip[];
  addComposerChip: (chip: Omit<ComposerContextChip, 'id'>) => void;
  removeComposerChip: (id: string) => void;
  clearComposerChips: () => void;
  composerPrefill: string;
  setComposerPrefill: (s: string) => void;

  // Toast & Undo
  toasts: ToastNotice[];
  pushToast: (t: Omit<ToastNotice, 'id'>) => void;
  dismissToast: (id: string) => void;
  triggerUndo: () => void;

  // RPC & Host Actions
  callHost: <T = unknown>(hostId: string, method: string, params?: unknown, timeoutMs?: number) => Promise<T>;
  pairHostByLink: (rawLink: string, isLocal?: boolean) => Promise<PairedHostCredentials>;
  pairLocalHost: () => Promise<PairedHostCredentials>;
  refreshLocalDaemon: () => Promise<LocalDaemonStatus | null>;
  removeHost: (hostId: string) => Promise<void>;
  reconnectHost: (hostId: string) => void;
  renameHost: (hostId: string, name: string) => Promise<void>;
  refreshHostSessions: (hostId: string, force?: boolean) => Promise<void>;
  subscribeSession: (hostId: string, sessionId: string) => Promise<void>;
}

const AppContext = createContext<AppContextValue | null>(null);

const LS_HOSTS = 'agentc.desktop.hosts.v1';
const LS_SETTINGS = 'agentc.desktop.settings.v1';

async function loadSavedHosts(): Promise<PairedHostCredentials[]> {
  if (window.agentcDesktop) {
    return window.agentcDesktop.loadHosts();
  }
  try {
    return JSON.parse(localStorage.getItem(LS_HOSTS) || '[]') as PairedHostCredentials[];
  } catch {
    return [];
  }
}

async function persistHosts(hosts: PairedHostCredentials[]): Promise<void> {
  if (window.agentcDesktop) {
    await window.agentcDesktop.saveHosts(hosts);
  } else {
    localStorage.setItem(LS_HOSTS, JSON.stringify(hosts));
  }
}

async function loadSavedSettings(): Promise<Partial<ClientSettings>> {
  if (window.agentcDesktop) {
    return (await window.agentcDesktop.loadSettings()) as Partial<ClientSettings>;
  }
  try {
    return JSON.parse(localStorage.getItem(LS_SETTINGS) || '{}') as Partial<ClientSettings>;
  } catch {
    return {};
  }
}

async function persistSettings(s: ClientSettings): Promise<void> {
  if (window.agentcDesktop) {
    await window.agentcDesktop.saveSettings(s as unknown as Record<string, unknown>);
  } else {
    localStorage.setItem(LS_SETTINGS, JSON.stringify(s));
  }
}

export const AppProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const supervisorsRef = useRef<Map<string, HostSupervisor>>(new Map());
  const [hosts, setHosts] = useState<Record<string, HostRuntime>>({});
  const [localDaemon, setLocalDaemon] = useState<LocalDaemonStatus | null>(null);
  const [editors, setEditors] = useState<DetectedEditor[]>([]);
  const [settings, setSettings] = useState<ClientSettings>(DEFAULT_SETTINGS);

  const [viewMode, setViewMode] = useState<MainViewMode>('draft');
  const [activeHostId, setActiveHostId] = useState<string>('');
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [draft, setDraft] = useState<DraftConfig>({
    hostId: '',
    cwd: '',
    agent: '',
    model: '',
    mode: 'supervised',
    thought: 'medium',
    skipPermissions: true,
    useWorktree: false,
    baseBranch: '',
    branchName: '',
  });

  const [transcripts, setTranscripts] = useState<Record<string, TranscriptState>>({});
  const transcriptsRef = useRef(transcripts);
  transcriptsRef.current = transcripts;

  const [details, setDetails] = useState<Record<string, SessionDetail>>({});
  const [asidesMap, setAsidesMap] = useState<Record<string, AsideItem[]>>({});

  // Layout state
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [infoCardOpen, setInfoCardOpen] = useState(true);
  const [terminalDrawerOpen, setTerminalDrawerOpen] = useState(false);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const [rightPanelMaximized, setRightPanelMaximized] = useState(false);
  const [rightTabs, setRightTabs] = useState<RightTab[]>([]);
  const [activeRightTabId, setActiveRightTabId] = useState<string | null>(null);
  const closedTabsRef = useRef<RightTab[]>([]);

  const [pairModalOpen, setPairModalOpen] = useState(false);
  const [addProjectModalOpen, setAddProjectModalOpen] = useState(false);
  const [settingsModalOpen, setSettingsModalOpen] = useState(false);
  const [settingsInitialTab, setSettingsInitialTab] = useState('general');
  const [commandPaletteOpen, setCommandPaletteOpen] = useState(false);

  const [composerChips, setComposerChips] = useState<ComposerContextChip[]>([]);
  const [composerPrefill, setComposerPrefill] = useState('');
  const [toasts, setToasts] = useState<ToastNotice[]>([]);
  const lastUndoRef = useRef<{ fn: () => void; expiresAt: number } | null>(null);

  const pushToast = useCallback((t: Omit<ToastNotice, 'id'>) => {
    const id = `toast-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
    if (t.onUndo) {
      lastUndoRef.current = { fn: t.onUndo, expiresAt: Date.now() + 5_500 };
    }
    setToasts((prev) => [...prev.slice(-4), { ...t, id }]);
    setTimeout(() => {
      setToasts((prev) => prev.filter((x) => x.id !== id));
    }, t.onUndo ? 5_200 : 3_800);
  }, []);

  const dismissToast = useCallback((id: string) => {
    setToasts((prev) => prev.filter((x) => x.id !== id));
  }, []);

  const triggerUndo = useCallback(() => {
    const u = lastUndoRef.current;
    if (u && Date.now() <= u.expiresAt) {
      lastUndoRef.current = null;
      u.fn();
    }
  }, []);

  const openSettingsTab = useCallback((tab: string) => {
    setSettingsInitialTab(tab);
    setSettingsModalOpen(true);
  }, []);

  const updateSettings = useCallback((patch: Partial<ClientSettings>) => {
    setSettings((prev) => {
      const next = { ...prev, ...patch };
      void persistSettings(next);
      return next;
    });
  }, []);

  const resetSettings = useCallback(() => {
    setSettings(DEFAULT_SETTINGS);
    void persistSettings(DEFAULT_SETTINGS);
  }, []);

  // Apply theme & font size live
  useEffect(() => {
    applyTheme(settings.themeId, settings.diffPalette, settings.customCss);
    document.documentElement.style.fontSize = `${settings.fontSize}px`;
  }, [settings.themeId, settings.diffPalette, settings.customCss, settings.fontSize]);

  const activeHostRef = useRef(activeHostId);
  activeHostRef.current = activeHostId;
  const activeSessionRef = useRef(activeSessionId);
  activeSessionRef.current = activeSessionId;
  const settingsRef = useRef(settings);
  settingsRef.current = settings;

  const callHost = useCallback(
    async <T = unknown,>(hostId: string, method: string, params?: unknown, timeoutMs?: number): Promise<T> => {
      const sup = supervisorsRef.current.get(hostId);
      if (!sup) throw new Error(`Unknown host ${hostId}`);
      return sup.call<T>(method, params, timeoutMs);
    },
    [],
  );

  const subscribeSession = useCallback(async (hostId: string, sessionId: string) => {
    const sup = supervisorsRef.current.get(hostId);
    if (!sup || sup.connectionState !== 'connected') return;
    const key = threadKey(hostId, sessionId);
    const cur = transcriptsRef.current[key];
    const since = cur?.seq ?? 0;
    try {
      await sup.call('session.subscribe', { id: sessionId, since });
      // Also fetch asides lazily
      const asides = await sup.call<AsideItem[]>('session.asides', { id: sessionId }).catch(() => []);
      setAsidesMap((prev) => ({ ...prev, [key]: Array.isArray(asides) ? asides : [] }));
    } catch {
      // Ignore subscribe error while reconnecting
    }
  }, []);

  const saveAllCreds = useCallback(() => {
    const list = Array.from(supervisorsRef.current.values()).map((s) => s.creds);
    void persistHosts(list);
  }, []);

  const mountSupervisor = useCallback(
    (creds: PairedHostCredentials) => {
      const existing = supervisorsRef.current.get(creds.id);
      if (existing) {
        existing.updateCredentials(creds);
        setHosts((prev) => ({
          ...prev,
          [creds.id]: { ...(prev[creds.id] ?? makeEmptyHost(creds)), creds },
        }));
        return;
      }

      setHosts((prev) => ({
        ...prev,
        [creds.id]: makeEmptyHost(creds),
      }));

      const sup = new HostSupervisor(
        creds,
        {
          onStateChange: (hostId, state, error) => {
            setHosts((prev) => {
              const cur = prev[hostId];
              if (!cur) return prev;
              return { ...prev, [hostId]: { ...cur, state, error } };
            });
          },
          onDaemonInfo: (hostId, info) => {
            setHosts((prev) => {
              const cur = prev[hostId];
              if (!cur) return prev;
              return {
                ...prev,
                [hostId]: {
                  ...cur,
                  info,
                  agents: info.agents ?? [],
                  projects: info.projects ?? cur.projects,
                  sessions: info.sessions ?? cur.sessions,
                },
              };
            });
            // Fetch terminals, devices, schedules in background
            void sup.call<TerminalInfo[]>('term.list').then((terms) => {
              setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], terminals: terms ?? [] } } : prev));
            }).catch(() => {});
            void sup.call<Device[]>('devices.list').then((devs) => {
              setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], devices: devs ?? [] } } : prev));
            }).catch(() => {});
            void sup.call<{ tasks: ScheduledTask[]; runs: ScheduleRunRecord[] }>('schedules.list').then((sc) => {
              if (sc) {
                setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], schedules: sc } } : prev));
              }
            }).catch(() => {});

            // Re-subscribe active session cleanly on reconnect!
            if (activeHostRef.current === hostId && activeSessionRef.current) {
              void subscribeSession(hostId, activeSessionRef.current);
            }
          },
          onCredentialsUpdated: (updated) => {
            setHosts((prev) => (prev[updated.id] ? { ...prev, [updated.id]: { ...prev[updated.id], creds: updated } } : prev));
            saveAllCreds();
          },
          onEvent: (hostId, event, data) => {
            handleServerEvent(hostId, event, data);
          },
        },
        {
          deviceName: window.agentcDesktop?.hostname
            ? `Desktop (${window.agentcDesktop.hostname})`
            : 'Agent Companion Web',
          platform: window.agentcDesktop ? 'linux-desktop' : 'web',
          appVersion: window.agentcDesktop?.version ?? '0.4.0',
        },
      );
      supervisorsRef.current.set(creds.id, sup);
      sup.start();
    },
    [saveAllCreds, subscribeSession],
  );

  const handleServerEvent = useCallback(
    (hostId: string, event: string, data: unknown) => {
      switch (event) {
        case 'session.changed': {
          const sum = data as SessionSummary;
          setHosts((prev) => {
            const h = prev[hostId];
            if (!h) return prev;
            const exists = h.sessions.some((s) => s.id === sum.id);
            const nextSessions = exists
              ? h.sessions.map((s) => (s.id === sum.id ? sum : s))
              : [sum, ...h.sessions];
            nextSessions.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
            return { ...prev, [hostId]: { ...h, sessions: nextSessions } };
          });
          break;
        }
        case 'session.removed': {
          const { id } = data as { id: string };
          setHosts((prev) => {
            const h = prev[hostId];
            if (!h) return prev;
            return { ...prev, [hostId]: { ...h, sessions: h.sessions.filter((s) => s.id !== id) } };
          });
          break;
        }
        case 'sessions.invalidate': {
          const sup = supervisorsRef.current.get(hostId);
          if (sup?.connectionState === 'connected') {
            void sup.call<SessionSummary[]>('sessions.list', { force: false }).then((list) => {
              setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], sessions: list ?? [] } } : prev));
            }).catch(() => {});
          }
          break;
        }
        case 'session.snapshot': {
          const snap = data as { id: string; session: SessionDetail; seq: number; items: TranscriptItem[] };
          const key = threadKey(hostId, snap.id);
          setDetails((prev) => ({ ...prev, [key]: snap.session }));
          setTranscripts((prev) => ({ ...prev, [key]: applySnapshot(snap.items ?? [], snap.seq ?? 0) }));
          break;
        }
        case 'session.delta': {
          const delta = data as { id: string; session: SessionDetail; seq: number; ops: TranscriptOp[] };
          const key = threadKey(hostId, delta.id);
          setDetails((prev) => ({ ...prev, [key]: delta.session }));
          setTranscripts((prev) => ({
            ...prev,
            [key]: applyOps(prev[key] ?? emptyTranscript(), delta.ops ?? [], delta.seq),
          }));
          break;
        }
        case 'session.ops': {
          const payload = data as { id: string; ops: TranscriptOp[] };
          const key = threadKey(hostId, payload.id);
          setTranscripts((prev) => ({
            ...prev,
            [key]: applyOps(prev[key] ?? emptyTranscript(), payload.ops ?? []),
          }));
          break;
        }
        case 'session.config': {
          const payload = data as { id: string; session: SessionDetail };
          const key = threadKey(hostId, payload.id);
          setDetails((prev) => ({ ...prev, [key]: payload.session }));
          break;
        }
        case 'session.usage': {
          const u = data as { id: string; used: number; size: number };
          const key = threadKey(hostId, u.id);
          setDetails((prev) => (prev[key] ? { ...prev, [key]: { ...prev[key], contextUsed: u.used, contextSize: u.size } } : prev));
          break;
        }
        case 'session.aside': {
          const payload = data as { id: string; aside: AsideItem };
          const key = threadKey(hostId, payload.id);
          setAsidesMap((prev) => {
            const cur = prev[key] ?? [];
            const exists = cur.some((a) => a.id === payload.aside.id);
            const next = exists ? cur.map((a) => (a.id === payload.aside.id ? payload.aside : a)) : [...cur, payload.aside];
            return { ...prev, [key]: next };
          });
          break;
        }
        case 'session.asides': {
          const payload = data as { id: string; asides: AsideItem[] };
          const key = threadKey(hostId, payload.id);
          setAsidesMap((prev) => ({ ...prev, [key]: payload.asides ?? [] }));
          break;
        }
        case 'projects.changed': {
          const list = data as Project[];
          setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], projects: list ?? [] } } : prev));
          break;
        }
        case 'providers.changed': {
          const list = data as AgentProfile[];
          setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], agents: list ?? [] } } : prev));
          break;
        }
        case 'terms.changed': {
          const list = data as TerminalInfo[];
          setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], terminals: list ?? [] } } : prev));
          break;
        }
        case 'devices.changed': {
          const list = data as Device[];
          setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], devices: list ?? [] } } : prev));
          break;
        }
        case 'schedules.changed': {
          const sc = data as { tasks: ScheduledTask[]; runs: ScheduleRunRecord[] };
          setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], schedules: sc } } : prev));
          break;
        }
        case 'notify': {
          const n = data as Notification;
          const isCurrent = activeHostRef.current === hostId && activeSessionRef.current === n.sessionId && document.hasFocus();
          if (settingsRef.current.notificationsEnabled && !isCurrent) {
            if (window.agentcDesktop) {
              void window.agentcDesktop.showNotification({
                title: n.title || 'Agent Companion',
                body: n.body || '',
                hostId,
                sessionId: n.sessionId,
              });
            }
          }
          break;
        }
      }
    },
    [],
  );

  const selectSession = useCallback(
    (hostId: string, sessionId: string) => {
      setActiveHostId(hostId);
      setActiveSessionId(sessionId);
      setViewMode('thread');
      void subscribeSession(hostId, sessionId);
    },
    [subscribeSession],
  );

  const openDraft = useCallback(
    (partial?: Partial<DraftConfig>) => {
      setViewMode('draft');
      setActiveSessionId(null);
      setDraft((prev) => {
        const hostId = partial?.hostId || activeHostRef.current || prev.hostId || Object.keys(hosts)[0] || '';
        const h = hosts[hostId];
        const defaultAgent =
          partial?.agent ||
          prev.agent ||
          h?.agents.find((a) => a.available && a.id === 'claude')?.id ||
          h?.agents.find((a) => a.available && a.id === 'agy')?.id ||
          h?.agents.find((a) => a.available)?.id ||
          'claude';
        const defaultCwd = partial?.cwd ?? (partial?.projectId ? h?.projects.find((p) => p.id === partial.projectId)?.path : undefined) ?? prev.cwd ?? h?.projects[0]?.path ?? h?.info?.home ?? '';
        return {
          ...prev,
          ...partial,
          hostId,
          agent: defaultAgent,
          cwd: defaultCwd,
        };
      });
    },
    [hosts],
  );

  const pairHostByLink = useCallback(
    async (rawLink: string, isLocal = false): Promise<PairedHostCredentials> => {
      const { credentials } = await pairWithLink(rawLink, {
        deviceName: window.agentcDesktop?.hostname
          ? `Desktop (${window.agentcDesktop.hostname})`
          : 'Agent Companion Web',
        platform: window.agentcDesktop ? 'linux-desktop' : 'web',
        appVersion: window.agentcDesktop?.version ?? '0.4.0',
        isLocal,
      });
      mountSupervisor(credentials);
      saveAllCreds();
      setActiveHostId(credentials.id);
      setDraft((prev) => (prev.hostId ? prev : { ...prev, hostId: credentials.id }));
      pushToast({ text: `Paired with ${credentials.name}`, level: 'success' });
      return credentials;
    },
    [mountSupervisor, pushToast, saveAllCreds],
  );

  const refreshLocalDaemon = useCallback(async () => {
    if (!window.agentcDesktop) return null;
    const status = await window.agentcDesktop.detectLocalDaemon();
    setLocalDaemon(status);
    return status;
  }, []);

  const pairLocalHost = useCallback(async () => {
    if (!window.agentcDesktop) throw new Error('One-click local pairing requires the desktop app.');
    const res = await window.agentcDesktop.requestLocalPairLink();
    return pairHostByLink(res.url, true);
  }, [pairHostByLink]);

  const removeHost = useCallback(
    async (hostId: string) => {
      const sup = supervisorsRef.current.get(hostId);
      if (sup) {
        sup.dispose();
        supervisorsRef.current.delete(hostId);
      }
      setHosts((prev) => {
        const copy = { ...prev };
        delete copy[hostId];
        return copy;
      });
      if (activeHostRef.current === hostId) {
        const nextId = Array.from(supervisorsRef.current.keys())[0] ?? '';
        setActiveHostId(nextId);
        setActiveSessionId(null);
      }
      saveAllCreds();
    },
    [saveAllCreds],
  );

  const reconnectHost = useCallback((hostId: string) => {
    supervisorsRef.current.get(hostId)?.reconnectNow();
  }, []);

  const renameHost = useCallback(
    async (hostId: string, name: string) => {
      const sup = supervisorsRef.current.get(hostId);
      if (!sup) return;
      sup.creds = { ...sup.creds, name: name.trim() || sup.creds.name };
      setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], creds: sup.creds } } : prev));
      saveAllCreds();
    },
    [saveAllCreds],
  );

  const refreshHostSessions = useCallback(async (hostId: string, force = false) => {
    const list = await callHost<SessionSummary[]>(hostId, 'sessions.list', { force });
    setHosts((prev) => (prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], sessions: list ?? [] } } : prev));
  }, [callHost]);

  // Right-panel tab management with Ctrl+Shift+T reopen stack (up to 20 tabs)
  const openRightTab = useCallback((tab: Omit<RightTab, 'id'> & { id?: string }) => {
    setRightPanelOpen(true);
    setRightTabs((prev) => {
      const existing = prev.find((t) => (tab.id ? t.id === tab.id : t.kind === tab.kind && t.filePath === tab.filePath));
      if (existing) {
        const updated = { ...existing, ...tab, id: existing.id };
        setActiveRightTabId(existing.id);
        return prev.map((t) => (t.id === existing.id ? updated : t));
      }
      const id = tab.id ?? `${tab.kind}-${Date.now()}`;
      const nextTab: RightTab = { ...tab, id };
      setActiveRightTabId(id);
      return [...prev, nextTab];
    });
  }, []);

  const closeRightTab = useCallback((id: string) => {
    setRightTabs((prev) => {
      const target = prev.find((t) => t.id === id);
      if (target) {
        closedTabsRef.current = [target, ...closedTabsRef.current].slice(0, 20);
      }
      const next = prev.filter((t) => t.id !== id);
      setActiveRightTabId((cur) => {
        if (cur !== id) return cur;
        return next[next.length - 1]?.id ?? null;
      });
      if (next.length === 0) {
        setRightPanelOpen(false);
        setRightPanelMaximized(false);
      }
      return next;
    });
  }, []);

  const reopenClosedTab = useCallback(() => {
    const popped = closedTabsRef.current.shift();
    if (!popped) return;
    openRightTab(popped);
  }, [openRightTab]);

  // Boot: load hosts, settings, editors, local daemon, subscribe Electron IPC
  useEffect(() => {
    let mounted = true;
    (async () => {
      const [savedHosts, savedSettings] = await Promise.all([loadSavedHosts(), loadSavedSettings()]);
      if (!mounted) return;
      setSettings((prev) => ({ ...prev, ...savedSettings }));
      for (const h of savedHosts) {
        mountSupervisor(h);
      }
      if (savedHosts.length > 0) {
        setActiveHostId(savedHosts[0].id);
        setDraft((prev) => ({ ...prev, hostId: savedHosts[0].id }));
      }
      if (window.agentcDesktop) {
        const [ld, eds] = await Promise.all([
          window.agentcDesktop.detectLocalDaemon(),
          window.agentcDesktop.listEditors(),
        ]);
        if (!mounted) return;
        setLocalDaemon(ld);
        setEditors(eds);
        await window.agentcDesktop.signalReady();
      }
    })();

    const cleanups: Array<() => void> = [];
    if (window.agentcDesktop) {
      cleanups.push(
        window.agentcDesktop.onDeepLink((url) => {
          void pairHostByLink(url).catch((err) => {
            pushToast({ text: (err as Error).message, level: 'error' });
          });
        }),
      );
      cleanups.push(
        window.agentcDesktop.onNotificationClick((target) => {
          if (target.hostId && target.sessionId) {
            selectSession(target.hostId, target.sessionId);
          }
        }),
      );
      cleanups.push(
        window.agentcDesktop.onWindowFocus(() => {
          for (const sup of supervisorsRef.current.values()) {
            void sup.wakeup();
          }
        }),
      );
      cleanups.push(
        window.agentcDesktop.onCloseActiveTab(() => {
          setActiveRightTabId((cur) => {
            if (cur) {
              closeRightTab(cur);
            } else {
              setTerminalDrawerOpen(false);
            }
            return cur;
          });
        }),
      );
    }

    const onOnline = () => {
      for (const sup of supervisorsRef.current.values()) {
        void sup.wakeup();
      }
    };
    window.addEventListener('online', onOnline);
    return () => {
      mounted = false;
      window.removeEventListener('online', onOnline);
      cleanups.forEach((fn) => fn());
      for (const sup of supervisorsRef.current.values()) sup.dispose();
      supervisorsRef.current.clear();
    };
  }, [closeRightTab, mountSupervisor, pairHostByLink, pushToast, selectSession]);

  // Update desktop dock badge count with pending approvals across all hosts
  useEffect(() => {
    let pending = 0;
    for (const h of Object.values(hosts)) {
      for (const s of h.sessions) {
        if (!s.archived && !s.settled && (s.pendingPermissions > 0 || s.attention === 'permission' || s.attention === 'question' || s.attention === 'plan')) {
          pending++;
        }
      }
    }
    void window.agentcDesktop?.setBadgeCount(pending);
  }, [hosts]);

  const hostList = useMemo(() => Object.values(hosts), [hosts]);
  const activeHost = hosts[activeHostId] ?? hostList[0] ?? null;

  const activeSession = useMemo(() => {
    if (!activeHost || !activeSessionId) return null;
    return activeHost.sessions.find((s) => s.id === activeSessionId) ?? null;
  }, [activeHost, activeSessionId]);

  const activeKey = activeHost && activeSessionId ? threadKey(activeHost.creds.id, activeSessionId) : '';
  const activeDetail = activeKey ? details[activeKey] ?? null : null;
  const activeTranscript = activeKey ? transcripts[activeKey] ?? emptyTranscript() : emptyTranscript();
  const activeAsides = activeKey ? asidesMap[activeKey] ?? [] : [];

  const allSessions = useMemo(() => {
    const out: Array<SessionSummary & { hostId: string; hostName: string }> = [];
    for (const h of hostList) {
      for (const s of h.sessions) {
        out.push({ ...s, hostId: h.creds.id, hostName: h.creds.name });
      }
    }
    out.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
    return out;
  }, [hostList]);

  const allProjects = useMemo(() => {
    const out: Array<Project & { hostId: string; hostName: string }> = [];
    for (const h of hostList) {
      for (const p of h.projects) {
        out.push({ ...p, hostId: h.creds.id, hostName: h.creds.name });
      }
    }
    return out;
  }, [hostList]);

  const addComposerChip = useCallback((chip: Omit<ComposerContextChip, 'id'>) => {
    setComposerChips((prev) => [...prev, { ...chip, id: `chip-${Date.now()}-${Math.random().toString(36).slice(2, 6)}` }]);
  }, []);
  const removeComposerChip = useCallback((id: string) => {
    setComposerChips((prev) => prev.filter((c) => c.id !== id));
  }, []);
  const clearComposerChips = useCallback(() => setComposerChips([]), []);

  const value: AppContextValue = {
    hosts,
    hostList,
    localDaemon,
    editors,
    settings,
    updateSettings,
    resetSettings,
    viewMode,
    setViewMode,
    activeHostId: activeHost?.creds.id ?? '',
    setActiveHostId,
    activeSessionId,
    selectSession,
    openDraft,
    draft,
    setDraft,
    sidebarOpen,
    setSidebarOpen,
    infoCardOpen,
    setInfoCardOpen,
    terminalDrawerOpen,
    setTerminalDrawerOpen,
    rightPanelOpen,
    setRightPanelOpen,
    rightPanelMaximized,
    setRightPanelMaximized,
    rightTabs,
    activeRightTabId,
    setActiveRightTabId,
    openRightTab,
    closeRightTab,
    reopenClosedTab,
    pairModalOpen,
    setPairModalOpen,
    addProjectModalOpen,
    setAddProjectModalOpen,
    settingsModalOpen,
    setSettingsModalOpen,
    settingsInitialTab,
    openSettingsTab,
    commandPaletteOpen,
    setCommandPaletteOpen,
    activeHost,
    activeSession,
    activeDetail,
    activeTranscript,
    activeAsides,
    allSessions,
    allProjects,
    composerChips,
    addComposerChip,
    removeComposerChip,
    clearComposerChips,
    composerPrefill,
    setComposerPrefill,
    toasts,
    pushToast,
    dismissToast,
    triggerUndo,
    callHost,
    pairHostByLink,
    pairLocalHost,
    refreshLocalDaemon,
    removeHost,
    reconnectHost,
    renameHost,
    refreshHostSessions,
    subscribeSession,
  };

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
};

function makeEmptyHost(creds: PairedHostCredentials): HostRuntime {
  return {
    creds,
    state: 'disconnected',
    agents: [],
    projects: [],
    sessions: [],
    terminals: [],
    devices: [],
    schedules: { tasks: [], runs: [] },
  };
}

export function useApp(): AppContextValue {
  const ctx = useContext(AppContext);
  if (!ctx) throw new Error('useApp must be used within AppProvider');
  return ctx;
}
