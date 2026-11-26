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
  CustomModel,
  DaemonInfo,
  Device,
  HostConnectionState,
  Notification,
  PairedHostCredentials,
  Project,
  ProviderConfig,
  ProviderModelsResult,
  ScheduledTask,
  ScheduleRunRecord,
  SessionDetail,
  SessionSummary,
  TerminalInfo,
  TranscriptItem,
  TranscriptOp,
} from '@agentc/protocol';
import type { DetectedEditor, LocalDaemonStatus } from '../types/desktop.js';
import { buildProviderUpsertPayload } from '../utils/codexModels.js';
import {
  clearWorkspacePinnedTabs,
  computeDraftForHostSwitch,
  upsertRightTab,
  type RightTab,
  type RightTabKind,
} from '../utils/multiHost.js';

export type { RightTab, RightTabKind };
import { allowsDesktopNotification, type SessionNoteRecord } from '../utils/sessionHelpers.js';
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
  notifyOnInput: boolean;
  notifyOnDone: boolean;
  notifyOnHalted: boolean;
  notifyOnProgress: boolean;
  expandToolCalls: boolean;
  agyAutoAllowReads: boolean;
  sidebarWidth: number;
  closeToTray: boolean;
  renderMath: boolean;
  chatMaxWidth: number;
  fontSize: number;
  customCss: string;
  favoriteModels: string[];
  customModelsByScope: Record<string, CustomModel[]>;
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
  notifyOnInput: true,
  notifyOnDone: true,
  notifyOnHalted: true,
  notifyOnProgress: false,
  expandToolCalls: false,
  agyAutoAllowReads: false,
  sidebarWidth: 276,
  closeToTray: false,
  renderMath: true,
  chatMaxWidth: 860,
  fontSize: 13.5,
  customCss: '',
  favoriteModels: [],
  customModelsByScope: {},
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

export function providerCatalogKey(hostId: string, provider: string): string {
  return `${hostId}:${provider}`;
}

export function customModelsScopeKey(hostId: string, provider: string): string {
  return `${hostId}:${provider.trim() || 'codex'}`;
}

function pruneHostPrefixMap<T>(prev: Record<string, T>, hostId: string): Record<string, T> {
  const prefix = `${hostId}:`;
  let changed = false;
  const next: Record<string, T> = {};
  for (const [k, v] of Object.entries(prev)) {
    if (k.startsWith(prefix)) {
      changed = true;
    } else {
      next[k] = v;
    }
  }
  return changed ? next : prev;
}

interface AppContextValue {
  hosts: Record<string, HostRuntime>;
  hostList: HostRuntime[];
  localDaemon: LocalDaemonStatus | null;
  editors: DetectedEditor[];
  settings: ClientSettings;
  updateSettings: (patch: Partial<ClientSettings>) => void;
  resetSettings: () => void;

  // Provider catalog & custom models
  providerCatalogs: Record<string, ProviderModelsResult>;
  loadProviderModels: (hostId: string, provider: string, refresh?: boolean) => Promise<ProviderModelsResult | null>;
  refreshHostInfo: (hostId: string) => Promise<DaemonInfo | null>;
  saveCustomModelSlug: (hostId: string, provider: string, model: CustomModel) => Promise<void>;
  removeCustomModelSlug: (hostId: string, provider: string, slug: string) => Promise<void>;

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
  rightTabFocusSeq: number;
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

  // Client-side session notes & quick navigation signals
  sessionNotes: Record<string, SessionNoteRecord>;
  setSessionNote: (hostId: string, sessionId: string, text: string) => void;
  switchToPreviousThread: () => void;
  jumpToLastNotifiedThread: () => void;
  terminalCycleSignal: { delta: number; seq: number };
  cycleTerminal: (delta: number) => void;

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
  upsertHostTerminal: (hostId: string, term: TerminalInfo) => void;
  refreshHostTerminals: (hostId: string) => Promise<TerminalInfo[]>;
  subscribeSession: (hostId: string, sessionId: string) => Promise<void>;
}

const AppContext = createContext<AppContextValue | null>(null);

const LS_HOSTS = 'agentc.desktop.hosts.v1';
const LS_SETTINGS = 'agentc.desktop.settings.v1';
const LS_NOTES = 'agentc.desktop.notes.v1';

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

async function loadSavedNotes(): Promise<Record<string, SessionNoteRecord>> {
  try {
    const raw = window.agentcDesktop
      ? await window.agentcDesktop.loadNotes()
      : (JSON.parse(localStorage.getItem(LS_NOTES) || '{}') as Record<string, unknown>);
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
    const out: Record<string, SessionNoteRecord> = {};
    for (const [k, v] of Object.entries(raw)) {
      if (v && typeof v === 'object' && typeof (v as { text?: unknown }).text === 'string') {
        const text = (v as { text: string }).text;
        if (text.trim()) {
          out[k] = {
            text,
            updatedAt: Number((v as { updatedAt?: unknown }).updatedAt) || 0,
          };
        }
      }
    }
    return out;
  } catch {
    return {};
  }
}

async function persistNotes(notes: Record<string, SessionNoteRecord>): Promise<void> {
  if (window.agentcDesktop) {
    await window.agentcDesktop.saveNotes(notes as unknown as Record<string, unknown>);
  } else {
    localStorage.setItem(LS_NOTES, JSON.stringify(notes));
  }
}

export const AppProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const supervisorsRef = useRef<Map<string, HostSupervisor>>(new Map());
  const [hosts, setHosts] = useState<Record<string, HostRuntime>>({});
  const hostsRef = useRef(hosts);
  hostsRef.current = hosts;

  const [localDaemon, setLocalDaemon] = useState<LocalDaemonStatus | null>(null);
  const [editors, setEditors] = useState<DetectedEditor[]>([]);
  const [settings, setSettings] = useState<ClientSettings>(DEFAULT_SETTINGS);
  const [providerCatalogs, setProviderCatalogs] = useState<Record<string, ProviderModelsResult>>({});
  const catalogInFlightRef = useRef<Map<string, Promise<ProviderModelsResult | null>>>(new Map());

  const [viewMode, setViewMode] = useState<MainViewMode>('draft');
  const [activeHostId, setActiveHostIdState] = useState<string>('');
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [draft, setDraft] = useState<DraftConfig>({
    hostId: '',
    cwd: '',
    agent: '',
    model: '',
    mode: 'supervised',
    thought: 'medium',
    skipPermissions: false,
    useWorktree: false,
    baseBranch: '',
    branchName: '',
  });
  const draftRef = useRef(draft);
  draftRef.current = draft;

  const [transcripts, setTranscripts] = useState<Record<string, TranscriptState>>({});
  const transcriptsRef = useRef(transcripts);
  transcriptsRef.current = transcripts;

  const [details, setDetails] = useState<Record<string, SessionDetail>>({});
  const [asidesMap, setAsidesMap] = useState<Record<string, AsideItem[]>>({});
  const [sessionNotes, setSessionNotes] = useState<Record<string, SessionNoteRecord>>({});
  const recentThreadsRef = useRef<Array<{ hostId: string; sessionId: string }>>([]);
  const lastNotifiedRef = useRef<{ hostId: string; sessionId: string } | null>(null);
  const [terminalCycleSignal, setTerminalCycleSignal] = useState<{ delta: number; seq: number }>({
    delta: 0,
    seq: 0,
  });

  // Layout state
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [infoCardOpen, setInfoCardOpen] = useState(true);
  const [terminalDrawerOpen, setTerminalDrawerOpen] = useState(false);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const [rightPanelMaximized, setRightPanelMaximized] = useState(false);
  const [rightTabs, setRightTabs] = useState<RightTab[]>([]);
  const rightTabsRef = useRef(rightTabs);
  rightTabsRef.current = rightTabs;
  const [rightTabFocusSeq, setRightTabFocusSeq] = useState(0);
  const [activeRightTabId, setActiveRightTabId] = useState<string | null>(null);
  const activeRightTabIdRef = useRef(activeRightTabId);
  activeRightTabIdRef.current = activeRightTabId;
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

  const viewModeRef = useRef(viewMode);
  viewModeRef.current = viewMode;
  const activeHostRef = useRef(activeHostId);
  activeHostRef.current = activeHostId;
  const activeSessionRef = useRef(activeSessionId);
  activeSessionRef.current = activeSessionId;
  const settingsRef = useRef(settings);
  settingsRef.current = settings;

  const applyWorkspaceTabReset = useCallback((nextHostId: string) => {
    const prev = rightTabsRef.current;
    const next = clearWorkspacePinnedTabs(prev, nextHostId);
    if (next !== prev) {
      rightTabsRef.current = next;
      setRightTabs(next);
    }
  }, []);

  const setActiveHostId = useCallback(
    (nextHostId: string) => {
      setActiveHostIdState(nextHostId);
      activeHostRef.current = nextHostId;
      applyWorkspaceTabReset(nextHostId);
      const targetHost = hostsRef.current[nextHostId];
      const curSessId = activeSessionRef.current;
      if (curSessId && (!targetHost || !targetHost.sessions.some((s) => s.id === curSessId))) {
        setActiveSessionId(null);
        activeSessionRef.current = null;
        setViewMode((m) => (m === 'thread' ? 'draft' : m));
      }
      setDraft((prev) => computeDraftForHostSwitch(prev, nextHostId, hostsRef.current));
    },
    [applyWorkspaceTabReset],
  );

  const callHost = useCallback(
    async <T = unknown,>(hostId: string, method: string, params?: unknown, timeoutMs?: number): Promise<T> => {
      const sup = supervisorsRef.current.get(hostId);
      if (!sup) throw new Error(`Unknown host ${hostId}`);
      return sup.call<T>(method, params, timeoutMs);
    },
    [],
  );

  const loadProviderModels = useCallback(
    async (hostId: string, provider: string, refresh = false): Promise<ProviderModelsResult | null> => {
      const prov = provider.trim() || 'codex';
      if (!hostId) return null;
      const sup = supervisorsRef.current.get(hostId);
      if (!sup || sup.connectionState !== 'connected') return null;

      const key = providerCatalogKey(hostId, prov);
      if (!refresh) {
        const existingPromise = catalogInFlightRef.current.get(key);
        if (existingPromise) return existingPromise;
      }

      const req = sup
        .call<ProviderModelsResult>('providers.models', { provider: prov, refresh })
        .then((res) => {
          if (res && typeof res === 'object') {
            setProviderCatalogs((prev) => ({ ...prev, [key]: res }));
            return res;
          }
          return null;
        })
        .catch(() => null)
        .finally(() => {
          if (catalogInFlightRef.current.get(key) === req) {
            catalogInFlightRef.current.delete(key);
          }
        });

      catalogInFlightRef.current.set(key, req);
      return req;
    },
    [],
  );

  const refreshHostInfo = useCallback(async (hostId: string): Promise<DaemonInfo | null> => {
    if (!hostId) return null;
    const sup = supervisorsRef.current.get(hostId);
    if (!sup || sup.connectionState !== 'connected') return null;
    try {
      const info = await sup.call<DaemonInfo>('daemon.info', {});
      if (info) {
        sup.info = info;
        setHosts((prev) => {
          const cur = prev[hostId];
          if (!cur) return prev;
          return {
            ...prev,
            [hostId]: {
              ...cur,
              info,
              agents: info.agents ?? cur.agents,
              projects: info.projects ?? cur.projects,
              sessions: info.sessions ?? cur.sessions,
            },
          };
        });
      }
      return info;
    } catch {
      return null;
    }
  }, []);

  const saveCustomModelSlug = useCallback(
    async (hostId: string, provider: string, model: CustomModel): Promise<void> => {
      const slug = model.slug.trim();
      const prov = provider.trim() || 'codex';
      if (!slug || !hostId) return;
      const scopeKey = customModelsScopeKey(hostId, prov);
      const entry: CustomModel = { slug, name: model.name?.trim() || slug };

      setSettings((prev) => {
        const prevMap =
          prev.customModelsByScope && typeof prev.customModelsByScope === 'object'
            ? prev.customModelsByScope
            : {};
        const curList = Array.isArray(prevMap[scopeKey]) ? prevMap[scopeKey] : [];
        const nextCustom = [...curList.filter((c) => c.slug !== slug), entry];
        const next: ClientSettings = {
          ...prev,
          customModelsByScope: {
            ...prevMap,
            [scopeKey]: nextCustom,
          },
        };
        void persistSettings(next);
        return next;
      });

      try {
        const listRes = await callHost<{ configs?: ProviderConfig[] }>(hostId, 'providers.list', {});
        const existing = (listRes?.configs ?? []).find((c) => c.id === prov);
        const mergedDaemonCustom = [
          ...(existing?.customModels ?? []).filter((c) => c.slug !== slug),
          entry,
        ];
        const payload = buildProviderUpsertPayload(existing, {
          id: prov,
          customModels: mergedDaemonCustom,
        });
        await callHost(hostId, 'providers.upsert', payload);
        await loadProviderModels(hostId, prov, false);
      } catch (e) {
        pushToast({ text: `Saved custom model locally (${(e as Error).message})`, level: 'info' });
      }
    },
    [callHost, loadProviderModels, pushToast],
  );

  const removeCustomModelSlug = useCallback(
    async (hostId: string, provider: string, slug: string): Promise<void> => {
      const target = slug.trim();
      const prov = provider.trim() || 'codex';
      if (!target || !hostId) return;
      const scopeKey = customModelsScopeKey(hostId, prov);

      setSettings((prev) => {
        const prevMap =
          prev.customModelsByScope && typeof prev.customModelsByScope === 'object'
            ? prev.customModelsByScope
            : {};
        const curList = Array.isArray(prevMap[scopeKey]) ? prevMap[scopeKey] : [];
        const next: ClientSettings = {
          ...prev,
          customModelsByScope: {
            ...prevMap,
            [scopeKey]: curList.filter((c) => c.slug !== target),
          },
        };
        void persistSettings(next);
        return next;
      });

      try {
        const listRes = await callHost<{ configs?: ProviderConfig[] }>(hostId, 'providers.list', {});
        const existing = (listRes?.configs ?? []).find((c) => c.id === prov);
        if (existing) {
          const nextDaemonCustom = (existing.customModels ?? []).filter((c) => c.slug !== target);
          const payload = buildProviderUpsertPayload(existing, {
            id: prov,
            customModels: nextDaemonCustom,
          });
          await callHost(hostId, 'providers.upsert', payload);
        }
        await loadProviderModels(hostId, prov, false);
      } catch (e) {
        pushToast({ text: `Remove custom model failed: ${(e as Error).message}`, level: 'error' });
      }
    },
    [callHost, loadProviderModels, pushToast],
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
          if (activeHostRef.current === hostId && activeSessionRef.current === id) {
            setActiveSessionId(null);
            activeSessionRef.current = null;
            setViewMode((m) => (m === 'thread' ? 'draft' : m));
          }
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
        case 'host.url': {
          const payload = data as { url?: string };
          const nextUrl = payload?.url?.trim();
          const sup = supervisorsRef.current.get(hostId);
          if (sup && nextUrl && nextUrl !== sup.creds.url) {
            const updatedCreds = { ...sup.creds, url: nextUrl };
            sup.updateCredentials(updatedCreds);
            setHosts((prev) =>
              prev[hostId] ? { ...prev, [hostId]: { ...prev[hostId], creds: updatedCreds } } : prev,
            );
            const list = Array.from(supervisorsRef.current.values()).map((s) => s.creds);
            void persistHosts(list);
          }
          break;
        }
        case 'notify': {
          const n = data as Notification;
          if (n.sessionId) {
            lastNotifiedRef.current = { hostId, sessionId: n.sessionId };
          }
          const isCurrent = activeHostRef.current === hostId && activeSessionRef.current === n.sessionId && document.hasFocus();
          if (allowsDesktopNotification(n.kind, settingsRef.current) && !isCurrent) {
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
              const nextHost: HostRuntime = {
                ...cur,
                info,
                agents: info.agents ?? [],
                projects: info.projects ?? cur.projects,
                sessions: info.sessions ?? cur.sessions,
              };
              const nextMap = { ...prev, [hostId]: nextHost };
              hostsRef.current = nextMap;
              return nextMap;
            });

            // Warm draft defaults if draft is bound to this host with empty cwd
            setDraft((prev) => {
              if (prev.hostId !== hostId || prev.cwd.trim()) return prev;
              return computeDraftForHostSwitch(prev, hostId, hostsRef.current);
            });

            // Warm Codex dynamic catalog eagerly when available
            if ((info.agents ?? []).some((a) => a.id === 'codex')) {
              void loadProviderModels(hostId, 'codex', false);
            }

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
          appVersion: window.agentcDesktop?.version ?? '0.6.1',
        },
      );
      supervisorsRef.current.set(creds.id, sup);
      sup.start();
    },
    [handleServerEvent, loadProviderModels, saveAllCreds, subscribeSession],
  );

  const selectSession = useCallback(
    (hostId: string, sessionId: string) => {
      if (
        activeHostRef.current !== hostId ||
        activeSessionRef.current !== sessionId ||
        viewModeRef.current !== 'thread'
      ) {
        applyWorkspaceTabReset(hostId);
      }
      recentThreadsRef.current = [
        { hostId, sessionId },
        ...recentThreadsRef.current.filter((x) => !(x.hostId === hostId && x.sessionId === sessionId)),
      ].slice(0, 24);
      setActiveHostIdState(hostId);
      activeHostRef.current = hostId;
      setActiveSessionId(sessionId);
      activeSessionRef.current = sessionId;
      setViewMode('thread');
      setDraft((prev) => computeDraftForHostSwitch(prev, hostId, hostsRef.current));
      void subscribeSession(hostId, sessionId);
    },
    [applyWorkspaceTabReset, subscribeSession],
  );

  const setSessionNote = useCallback((hostId: string, sessionId: string, text: string) => {
    if (!hostId || !sessionId) return;
    const key = threadKey(hostId, sessionId);
    setSessionNotes((prev) => {
      const next = { ...prev };
      if (!text.trim()) {
        delete next[key];
      } else {
        next[key] = { text, updatedAt: Date.now() };
      }
      void persistNotes(next);
      return next;
    });
  }, []);

  const switchToPreviousThread = useCallback(() => {
    const curHost = activeHostRef.current;
    const curSess = activeSessionRef.current;
    const candidate = recentThreadsRef.current.find(
      (item) =>
        !(item.hostId === curHost && item.sessionId === curSess) &&
        hostsRef.current[item.hostId]?.sessions.some((s) => s.id === item.sessionId && !s.archived),
    );
    if (candidate) {
      selectSession(candidate.hostId, candidate.sessionId);
    }
  }, [selectSession]);

  const jumpToLastNotifiedThread = useCallback(() => {
    const target = lastNotifiedRef.current;
    if (target && hostsRef.current[target.hostId]?.sessions.some((s) => s.id === target.sessionId)) {
      selectSession(target.hostId, target.sessionId);
      return;
    }
    for (const h of Object.values(hostsRef.current)) {
      const attn = h.sessions.find(
        (s) => !s.archived && (s.pendingPermissions > 0 || Boolean(s.attention)),
      );
      if (attn) {
        selectSession(h.creds.id, attn.id);
        return;
      }
    }
  }, [selectSession]);

  const cycleTerminal = useCallback((delta: number) => {
    setTerminalDrawerOpen(true);
    setTerminalCycleSignal((prev) => ({ delta, seq: prev.seq + 1 }));
  }, []);

  const openDraft = useCallback(
    (partial?: Partial<DraftConfig>) => {
      const targetHostId =
        partial?.hostId ||
        activeHostRef.current ||
        draftRef.current.hostId ||
        Object.keys(hostsRef.current)[0] ||
        '';
      if (targetHostId && targetHostId !== activeHostRef.current) {
        setActiveHostIdState(targetHostId);
        activeHostRef.current = targetHostId;
      }
      applyWorkspaceTabReset(targetHostId);
      setViewMode('draft');
      setActiveSessionId(null);
      activeSessionRef.current = null;
      setDraft((prev) => {
        const hostId = targetHostId || prev.hostId;
        const sameHost = !hostId || prev.hostId === hostId;
        const h = hostsRef.current[hostId];
        const matchedProj = partial?.projectId
          ? h?.projects.find((p) => p.id === partial.projectId)
          : undefined;
        const keepPrevAgent =
          sameHost &&
          Boolean(prev.agent) &&
          (!h?.agents.length || h.agents.some((a) => a.id === prev.agent && a.available));
        const defaultAgent =
          partial?.agent ||
          (keepPrevAgent ? prev.agent : '') ||
          matchedProj?.defaultAgent ||
          h?.agents.find((a) => a.available && a.id === 'claude')?.id ||
          h?.agents.find((a) => a.available && a.id === 'codex')?.id ||
          h?.agents.find((a) => a.available && a.id === 'agy')?.id ||
          h?.agents.find((a) => a.available)?.id ||
          'claude';
        const defaultCwd =
          partial?.cwd ||
          matchedProj?.path ||
          (sameHost ? prev.cwd : '') ||
          h?.projects[0]?.path ||
          h?.info?.home ||
          '~';
        const nextProjectId =
          partial && 'projectId' in partial
            ? partial.projectId
            : sameHost
              ? prev.projectId
              : h?.projects[0]?.id;
        return {
          ...prev,
          ...(sameHost ? {} : { baseBranch: '', branchName: '', useWorktree: false, model: '' }),
          ...partial,
          hostId,
          projectId: nextProjectId,
          agent: defaultAgent,
          cwd: defaultCwd,
        };
      });
    },
    [applyWorkspaceTabReset],
  );

  const pairHostByLink = useCallback(
    async (rawLink: string, isLocal = false): Promise<PairedHostCredentials> => {
      const { credentials } = await pairWithLink(rawLink, {
        deviceName: window.agentcDesktop?.hostname
          ? `Desktop (${window.agentcDesktop.hostname})`
          : 'Agent Companion Web',
        platform: window.agentcDesktop ? 'linux-desktop' : 'web',
        appVersion: window.agentcDesktop?.version ?? '0.6.1',
        isLocal,
      });
      mountSupervisor(credentials);
      saveAllCreds();
      setActiveHostIdState(credentials.id);
      activeHostRef.current = credentials.id;
      if (!activeSessionRef.current) {
        setDraft((prev) => computeDraftForHostSwitch(prev, credentials.id, hostsRef.current));
      } else {
        setDraft((prev) => (prev.hostId ? prev : computeDraftForHostSwitch(prev, credentials.id, hostsRef.current)));
      }
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
      for (const k of Array.from(catalogInFlightRef.current.keys())) {
        if (k.startsWith(`${hostId}:`)) catalogInFlightRef.current.delete(k);
      }
      setHosts((prev) => {
        const copy = { ...prev };
        delete copy[hostId];
        hostsRef.current = copy;
        return copy;
      });
      setDetails((prev) => pruneHostPrefixMap(prev, hostId));
      setTranscripts((prev) => pruneHostPrefixMap(prev, hostId));
      setAsidesMap((prev) => pruneHostPrefixMap(prev, hostId));
      setProviderCatalogs((prev) => pruneHostPrefixMap(prev, hostId));
      setSessionNotes((prev) => {
        const next = pruneHostPrefixMap(prev, hostId);
        if (next !== prev) void persistNotes(next);
        return next;
      });
      setSettings((prev) => {
        const nextScoped = pruneHostPrefixMap(prev.customModelsByScope ?? {}, hostId);
        if (nextScoped === prev.customModelsByScope) return prev;
        const next = { ...prev, customModelsByScope: nextScoped };
        void persistSettings(next);
        return next;
      });

      const nextId = Array.from(supervisorsRef.current.keys())[0] ?? '';
      applyWorkspaceTabReset(nextId);
      if (activeHostRef.current === hostId) {
        setActiveHostIdState(nextId);
        activeHostRef.current = nextId;
        setActiveSessionId(null);
        activeSessionRef.current = null;
        setViewMode((m) => (m === 'thread' ? 'draft' : m));
      }
      setDraft((prev) =>
        prev.hostId === hostId ? computeDraftForHostSwitch(prev, nextId, hostsRef.current) : prev,
      );
      saveAllCreds();
    },
    [applyWorkspaceTabReset, saveAllCreds],
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

  const upsertHostTerminal = useCallback((hostId: string, term: TerminalInfo) => {
    if (!hostId || !term?.id) return;
    setHosts((prev) => {
      const cur = prev[hostId];
      if (!cur) return prev;
      const exists = cur.terminals.some((t) => t.id === term.id);
      const nextTerms = exists
        ? cur.terminals.map((t) => (t.id === term.id ? term : t))
        : [...cur.terminals, term];
      const nextMap = { ...prev, [hostId]: { ...cur, terminals: nextTerms } };
      hostsRef.current = nextMap;
      return nextMap;
    });
  }, []);

  const refreshHostTerminals = useCallback(
    async (hostId: string): Promise<TerminalInfo[]> => {
      if (!hostId) return [];
      try {
        const list = await callHost<TerminalInfo[]>(hostId, 'term.list');
        const safe = Array.isArray(list) ? list : [];
        setHosts((prev) => {
          const cur = prev[hostId];
          if (!cur) return prev;
          const nextMap = { ...prev, [hostId]: { ...cur, terminals: safe } };
          hostsRef.current = nextMap;
          return nextMap;
        });
        return safe;
      } catch {
        return hostsRef.current[hostId]?.terminals ?? [];
      }
    },
    [callHost],
  );

  // Right-panel tab management with pure updaters and Ctrl+Shift+T reopen stack (up to 20 tabs)
  const openRightTab = useCallback((tab: Omit<RightTab, 'id'> & { id?: string }) => {
    const { tabs: nextTabs, activeId: targetId } = upsertRightTab(
      rightTabsRef.current,
      tab,
      activeHostRef.current,
    );
    rightTabsRef.current = nextTabs;
    activeRightTabIdRef.current = targetId;
    setRightTabs(nextTabs);
    if (tab.filePath || tab.openFilePath || tab.line) {
      setRightTabFocusSeq((s) => s + 1);
    }
    setActiveRightTabId(targetId);
    setRightPanelOpen(true);
  }, []);

  const closeRightTab = useCallback((id: string) => {
    const prev = rightTabsRef.current;
    const target = prev.find((t) => t.id === id);
    if (target) {
      closedTabsRef.current = [target, ...closedTabsRef.current].slice(0, 20);
    }
    const next = prev.filter((t) => t.id !== id);
    rightTabsRef.current = next;
    setRightTabs(next);

    if (activeRightTabIdRef.current === id) {
      const nextActive = next[next.length - 1]?.id ?? null;
      activeRightTabIdRef.current = nextActive;
      setActiveRightTabId(nextActive);
    }
    if (next.length === 0) {
      setRightPanelOpen(false);
      setRightPanelMaximized(false);
    }
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
      const [savedHosts, savedSettings, savedNotes] = await Promise.all([
        loadSavedHosts(),
        loadSavedSettings(),
        loadSavedNotes(),
      ]);
      if (!mounted) return;
      setSessionNotes(savedNotes);
      const rawScoped = (savedSettings as Partial<ClientSettings>).customModelsByScope;
      const cleanScoped: Record<string, CustomModel[]> = {};
      if (rawScoped && typeof rawScoped === 'object' && !Array.isArray(rawScoped)) {
        for (const [k, v] of Object.entries(rawScoped)) {
          if (k.includes(':') && Array.isArray(v)) {
            cleanScoped[k] = v;
          }
        }
      }
      const { customModels: _legacyUnscoped, ...restSaved } = savedSettings as Partial<ClientSettings> & {
        customModels?: unknown;
      };
      setSettings((prev) => ({
        ...prev,
        ...restSaved,
        customModelsByScope: Object.keys(cleanScoped).length > 0 ? cleanScoped : prev.customModelsByScope,
      }));
      for (const h of savedHosts) {
        mountSupervisor(h);
      }
      if (savedHosts.length > 0) {
        setActiveHostIdState(savedHosts[0].id);
        activeHostRef.current = savedHosts[0].id;
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
          const cur = activeRightTabIdRef.current;
          if (cur) {
            closeRightTab(cur);
          } else {
            setTerminalDrawerOpen(false);
          }
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
    providerCatalogs,
    loadProviderModels,
    refreshHostInfo,
    saveCustomModelSlug,
    removeCustomModelSlug,
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
    rightTabFocusSeq,
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
    sessionNotes,
    setSessionNote,
    switchToPreviousThread,
    jumpToLastNotifiedThread,
    terminalCycleSignal,
    cycleTerminal,
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
    upsertHostTerminal,
    refreshHostTerminals,
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
