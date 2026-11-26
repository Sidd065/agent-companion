import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { confirmAction } from '../shell/ConfirmDialog.js';
import {
  Activity,
  Archive,
  Check,
  CheckCircle2,
  Cpu,
  FolderGit2,
  HeartPulse,
  Info,
  KeyRound,
  Keyboard,
  Palette,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
  Server,
  Sliders,
  Trash2,
  X,
  XCircle,
} from 'lucide-react';
import type {
  CustomModel,
  HostHealth,
  Project,
  ProviderConfig,
  ProviderHealth,
  ScriptDef,
} from '@agentc/protocol';
import { customModelsScopeKey, providerCatalogKey, useApp } from '../../state/appStore.js';
import { DEFAULT_KEYBINDINGS, displayChord, formatEventChord } from '../../state/keybindings.js';
import { BUILTIN_THEMES } from '../../state/themes.js';
import { buildProviderUpsertPayload } from '../../utils/codexModels.js';
import { plainInlineTitle } from '../../utils/sessionHelpers.js';

const TABS = [
  { id: 'general', label: 'General', icon: Sliders },
  { id: 'appearance', label: 'Appearance', icon: Palette },
  { id: 'hosts', label: 'Hosts & Devices', icon: Server },
  { id: 'providers', label: 'Providers & ACP', icon: Cpu },
  { id: 'chat', label: 'Chat & Diffs', icon: Activity },
  { id: 'keybindings', label: 'Keybindings', icon: Keyboard },
  { id: 'projects', label: 'Projects & Scripts', icon: FolderGit2 },
  { id: 'health', label: 'Diagnostics & Health', icon: HeartPulse },
  { id: 'archive', label: 'Archive', icon: Archive },
  { id: 'about', label: 'About', icon: Info },
];

const HOST_SCOPED_TABS = new Set(['providers', 'projects', 'health', 'about']);

function normalizeSettingsTab(raw?: string): string {
  if (raw && TABS.some((t) => t.id === raw)) return raw;
  return 'general';
}

export interface ProvidersRequestTracker {
  hydrateSeq: number;
  configsSeq: number;
}

export function beginHydrateProvidersSeq(tracker: ProvidersRequestTracker): number {
  tracker.configsSeq += 1;
  tracker.hydrateSeq += 1;
  return tracker.hydrateSeq;
}

export function isCurrentHydrateProvidersSeq(tracker: ProvidersRequestTracker, seq: number): boolean {
  return tracker.hydrateSeq === seq;
}

export function beginRefreshConfigsOnlySeq(tracker: ProvidersRequestTracker): {
  seq: number;
  hydrateSnap: number;
} {
  tracker.configsSeq += 1;
  return { seq: tracker.configsSeq, hydrateSnap: tracker.hydrateSeq };
}

export function isCurrentRefreshConfigsOnlySeq(
  tracker: ProvidersRequestTracker,
  token: { seq: number; hydrateSnap: number },
): boolean {
  return tracker.configsSeq === token.seq && tracker.hydrateSeq === token.hydrateSnap;
}

export function resetProvidersRequestTracker(tracker: ProvidersRequestTracker): void {
  tracker.hydrateSeq += 1;
  tracker.configsSeq += 1;
}

export const SettingsModal: React.FC = () => {
  const {
    settingsModalOpen,
    setSettingsModalOpen,
    settingsInitialTab,
    settings,
    updateSettings,
    resetSettings,
    hosts,
    hostList,
    activeHost,
    viewMode,
    draft,
    providerCatalogs,
    loadProviderModels,
    refreshHostInfo,
    saveCustomModelSlug,
    removeCustomModelSlug,
    allSessions,
    callHost,
    setPairModalOpen,
    reconnectHost,
    removeHost,
    renameHost,
    selectSession,
    pushToast,
  } = useApp();

  const [tab, setTab] = useState(() => normalizeSettingsTab(settingsInitialTab));
  const [search, setSearch] = useState('');
  const [selectedHostId, setSelectedHostId] = useState('');

  // Inline host rename state (replaces native browser prompt dialog)
  const [renamingHostId, setRenamingHostId] = useState<string | null>(null);
  const [renameHostValue, setRenameHostValue] = useState('');

  // Providers state (strictly tagged by hostId to prevent cross-host bleed and form input wipes)
  const [providersState, setProvidersState] = useState<{
    hostId: string;
    configs: ProviderConfig[];
    loaded: boolean;
  }>({ hostId: '', configs: [], loaded: false });
  const [providersFetching, setProvidersFetching] = useState(false);
  const hydratedProvidersHostRef = useRef<string>('');
  const providersTrackerRef = useRef<ProvidersRequestTracker>({ hydrateSeq: 0, configsSeq: 0 });
  const [providerHealth, setProviderHealth] = useState<Record<string, ProviderHealth>>({});
  const [newProvId, setNewProvId] = useState('');
  const [newProvName, setNewProvName] = useState('');
  const [newProvCmd, setNewProvCmd] = useState('');
  const [newProvArgs, setNewProvArgs] = useState('');

  // Codex & OpenRouter dedicated form state
  const [codexEnabled, setCodexEnabled] = useState(true);
  const [codexApiKeyInput, setCodexApiKeyInput] = useState('');
  const [codexCommandInput, setCodexCommandInput] = useState('');
  const [codexDefaultModelInput, setCodexDefaultModelInput] = useState('');
  const [codexSaving, setCodexSaving] = useState(false);
  const [codexRefreshing, setCodexRefreshing] = useState(false);
  const [newCustomSlug, setNewCustomSlug] = useState('');
  const [newCustomName, setNewCustomName] = useState('');

  // Diagnostics / Linux Host Health
  const [health, setHealth] = useState<HostHealth | null>(null);
  const [healthLoading, setHealthLoading] = useState(false);

  // Keybindings live recorder
  const [recordingId, setRecordingId] = useState<string | null>(null);

  // Project scripts editor
  const [selectedProjId, setSelectedProjId] = useState('');
  const [newScriptName, setNewScriptName] = useState('');
  const [newScriptCmd, setNewScriptCmd] = useState('');
  const [newScriptOnWt, setNewScriptOnWt] = useState(false);

  useEffect(() => {
    resetProvidersRequestTracker(providersTrackerRef.current);
    setProvidersFetching(false);
    if (settingsModalOpen) {
      hydratedProvidersHostRef.current = '';
      setProvidersState({ hostId: '', configs: [], loaded: false });
      setTab(normalizeSettingsTab(settingsInitialTab));
      const initialHost =
        (viewMode === 'draft' ? draft.hostId : activeHost?.creds.id) ||
        activeHost?.creds.id ||
        hostList[0]?.creds.id ||
        '';
      setSelectedHostId(initialHost);
      setRenamingHostId(null);
      setCodexApiKeyInput('');
    } else {
      hydratedProvidersHostRef.current = '';
    }
  }, [settingsModalOpen, settingsInitialTab]); // eslint-disable-line react-hooks/exhaustive-deps

  // Keep selectedHostId valid if hosts change while modal is open
  useEffect(() => {
    if (!settingsModalOpen) return;
    if (!selectedHostId || !hosts[selectedHostId]) {
      setSelectedHostId(activeHost?.creds.id ?? hostList[0]?.creds.id ?? '');
    }
  }, [settingsModalOpen, selectedHostId, hosts, activeHost, hostList]);

  const hostId = (selectedHostId && hosts[selectedHostId] ? selectedHostId : activeHost?.creds.id ?? hostList[0]?.creds.id) ?? '';
  const targetHost = (hostId ? hosts[hostId] : null) ?? activeHost ?? null;
  const providerConfigs = providersState.hostId === hostId ? providersState.configs : [];
  const providersReadyForHost = providersState.hostId === hostId && providersState.loaded;
  const providersLoading = Boolean(hostId) && (!providersReadyForHost || providersFetching);
  const codexCatalog = hostId ? providerCatalogs[providerCatalogKey(hostId, 'codex')] : undefined;
  const codexCfg = useMemo(() => providerConfigs.find((c) => c.id === 'codex'), [providerConfigs]);

  const prevSettingsHostRef = useRef(hostId);
  useEffect(() => {
    if (prevSettingsHostRef.current !== hostId) {
      prevSettingsHostRef.current = hostId;
      resetProvidersRequestTracker(providersTrackerRef.current);
      setProvidersFetching(false);
      setCodexApiKeyInput('');
      setProviderHealth({});
      setHealth(null);
    }
  }, [hostId]);

  const hydrateProvidersForHost = useCallback(
    async (targetHostId?: string) => {
      const hId = targetHostId ?? hostId;
      if (!hId) return;
      const seq = beginHydrateProvidersSeq(providersTrackerRef.current);
      setProvidersFetching(true);
      try {
        const res = await callHost<{ configs?: ProviderConfig[] }>(hId, 'providers.list', {});
        if (!isCurrentHydrateProvidersSeq(providersTrackerRef.current, seq)) return;
        const cfgs = Array.isArray(res?.configs) ? res.configs : [];
        setProvidersState({ hostId: hId, configs: cfgs, loaded: true });
        hydratedProvidersHostRef.current = hId;
        const cx = cfgs.find((c) => c.id === 'codex');
        setCodexEnabled(cx?.enabled ?? true);
        setCodexCommandInput(cx?.command ?? '');
        setCodexDefaultModelInput(cx?.defaultModel ?? '');
      } catch {
        if (!isCurrentHydrateProvidersSeq(providersTrackerRef.current, seq)) return;
        setProvidersState({ hostId: hId, configs: [], loaded: true });
        hydratedProvidersHostRef.current = hId;
      } finally {
        if (isCurrentHydrateProvidersSeq(providersTrackerRef.current, seq)) {
          setProvidersFetching(false);
        }
      }
    },
    [hostId, callHost],
  );

  const refreshProviderConfigsOnly = useCallback(
    async (targetHostId?: string) => {
      const hId = targetHostId ?? hostId;
      if (!hId) return;
      const token = beginRefreshConfigsOnlySeq(providersTrackerRef.current);
      try {
        const res = await callHost<{ configs?: ProviderConfig[] }>(hId, 'providers.list', {});
        if (!isCurrentRefreshConfigsOnlySeq(providersTrackerRef.current, token)) return;
        const cfgs = Array.isArray(res?.configs) ? res.configs : [];
        setProvidersState({ hostId: hId, configs: cfgs, loaded: true });
      } catch {
        // preserve existing loaded configs on transient error
      }
    },
    [hostId, callHost],
  );

  const checkHealthAll = useCallback(async () => {
    if (!hostId) return;
    try {
      const res = await callHost<ProviderHealth[]>(hostId, 'providers.health', {});
      const map: Record<string, ProviderHealth> = {};
      for (const h of Array.isArray(res) ? res : []) map[h.id] = h;
      setProviderHealth(map);
    } catch (e) {
      pushToast({ text: `Health check failed: ${(e as Error).message}`, level: 'error' });
    }
  }, [hostId, callHost, pushToast]);

  const loadHostHealth = useCallback(async () => {
    if (!hostId) return;
    setHealthLoading(true);
    try {
      const h = await callHost<HostHealth>(hostId, 'daemon.health', {});
      setHealth(h);
    } catch (e) {
      pushToast({ text: `Host health failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setHealthLoading(false);
    }
  }, [hostId, callHost, pushToast]);

  useEffect(() => {
    if (!settingsModalOpen || !hostId) return;
    if (tab === 'providers') {
      if (hydratedProvidersHostRef.current !== hostId) {
        void hydrateProvidersForHost(hostId);
      } else {
        void refreshProviderConfigsOnly(hostId);
      }
      void checkHealthAll();
      void loadProviderModels(hostId, 'codex', false);
    }
    if (tab === 'health') {
      void loadHostHealth();
    }
  }, [
    settingsModalOpen,
    tab,
    hostId,
    hydrateProvidersForHost,
    refreshProviderConfigsOnly,
    checkHealthAll,
    loadProviderModels,
    loadHostHealth,
  ]);

  // Keybinding chord capture listener
  useEffect(() => {
    if (!recordingId) return;
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'Escape') {
        setRecordingId(null);
        return;
      }
      const chord = formatEventChord(e);
      if (!chord || ['mod', 'shift', 'alt', 'mod+shift', 'mod+alt'].includes(chord)) return;
      updateSettings({ keybindings: { ...settings.keybindings, [recordingId]: chord } });
      setRecordingId(null);
      pushToast({ text: `Bound to ${displayChord(chord)}`, level: 'success' });
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [recordingId, settings.keybindings, updateSettings, pushToast]);

  const scopedLocalCodexCustom = useMemo(
    () => (hostId ? settings.customModelsByScope?.[customModelsScopeKey(hostId, 'codex')] ?? [] : []),
    [hostId, settings.customModelsByScope],
  );

  const mergedCodexCustomModels = useMemo<CustomModel[]>(() => {
    const out: CustomModel[] = [];
    const seen = new Set<string>();
    for (const cm of [
      ...(codexCfg?.customModels ?? []),
      ...(codexCatalog?.customModels ?? []),
      ...scopedLocalCodexCustom,
    ]) {
      const slug = cm.slug?.trim();
      if (!slug || seen.has(slug)) continue;
      seen.add(slug);
      out.push({ slug, name: cm.name?.trim() || slug });
    }
    return out;
  }, [codexCfg?.customModels, codexCatalog?.customModels, scopedLocalCodexCustom]);

  if (!settingsModalOpen) return null;

  const TAB_KEYWORDS: Record<string, string> = {
    general: 'notification notifications desktop tray working shelf group threads shortcut enter follow-up steer queue action required completed halted auto-allow antigravity read requests',
    appearance: 'theme light dark font size obsidian paper tokyo catppuccin solarized custom css chat width math katex',
    hosts: 'pair host remote daemon devices unpair cloudflare static quick tunnel url',
    providers: 'providers acp claude codex antigravity openrouter api key custom model slug',
    chat: 'chat diffs wrap word split unified',
    keybindings: 'keyboard shortcuts keybindings hotkeys ctrl',
    projects: 'projects scripts worktree git init clone',
    health: 'diagnostics health logs memory pid uptime',
    archive: 'archive archived restore settled threads',
    about: 'about version license agent companion',
  };
  const qLower = search.trim().toLowerCase();
  const filteredTabs = TABS.filter(
    (t) => !qLower || t.label.toLowerCase().includes(qLower) || (TAB_KEYWORDS[t.id] || '').includes(qLower),
  );
  const archivedSessions = allSessions.filter((s) => s.archived);
  const hostProjects: Project[] = targetHost?.projects ?? [];
  const selectedProj = hostProjects.find((p) => p.id === selectedProjId) ?? hostProjects[0] ?? null;

  const codexAgentProfile = (targetHost?.agents ?? []).find((a) => a.id === 'codex');
  const codexHealth = providerHealth['codex'];
  const hasStoredRedactedOrKey = codexCfg?.env?.OPENROUTER_API_KEY === '***';
  const openRouterReady = Boolean(
    codexCatalog?.openRouterConfigured || targetHost?.info?.openRouterConfigured || hasStoredRedactedOrKey,
  );
  const nativeAuthReady = Boolean(codexCatalog?.nativeAuthConfigured);

  const handleSaveCodexSettings = async (clearApiKey = false) => {
    if (!hostId || codexSaving || providersLoading) return;
    setCodexSaving(true);
    try {
      const payload = buildProviderUpsertPayload(codexCfg, {
        id: 'codex',
        name: 'Codex',
        driver: 'codex',
        enabled: codexEnabled,
        command: codexCommandInput,
        defaultModel: codexDefaultModelInput,
        apiKeyInput: clearApiKey ? '' : codexApiKeyInput,
        clearApiKey,
      });
      await callHost(hostId, 'providers.upsert', payload);
      setCodexApiKeyInput('');
      await Promise.all([
        refreshHostInfo(hostId),
        hydrateProvidersForHost(hostId),
        checkHealthAll(),
        loadProviderModels(hostId, 'codex', true),
      ]);
      pushToast({
        text: clearApiKey ? 'Cleared stored OpenRouter API key' : 'Saved Codex & OpenRouter settings',
        level: 'success',
      });
    } catch (e) {
      pushToast({ text: `Save failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setCodexSaving(false);
    }
  };

  const handleRefreshCodexCatalog = async () => {
    if (!hostId || codexRefreshing) return;
    setCodexRefreshing(true);
    try {
      await loadProviderModels(hostId, 'codex', true);
      pushToast({ text: 'Refreshed Codex & OpenRouter model catalog', level: 'info' });
    } finally {
      setCodexRefreshing(false);
    }
  };

  const handleAddCodexCustomModel = async () => {
    const slug = newCustomSlug.trim();
    if (!slug || !hostId) return;
    const name = newCustomName.trim() || slug;
    await saveCustomModelSlug(hostId, 'codex', { slug, name });
    setNewCustomSlug('');
    setNewCustomName('');
    await refreshProviderConfigsOnly(hostId);
    pushToast({ text: `Saved custom model ${slug}`, level: 'success' });
  };

  const handleRemoveCodexCustomModel = async (slug: string) => {
    if (!hostId) return;
    await removeCustomModelSlug(hostId, 'codex', slug);
    await refreshProviderConfigsOnly(hostId);
  };

  const addCustomProvider = async () => {
    if (!hostId || !newProvId.trim() || !newProvCmd.trim()) return;
    try {
      await callHost(hostId, 'providers.upsert', {
        id: newProvId.trim().toLowerCase().replace(/[^a-z0-9_-]/g, '-'),
        name: newProvName.trim() || newProvId.trim(),
        driver: 'acp',
        enabled: true,
        command: newProvCmd.trim(),
        args: newProvArgs.trim() ? newProvArgs.trim().split(/\s+/) : [],
      });
      setNewProvId('');
      setNewProvName('');
      setNewProvCmd('');
      setNewProvArgs('');
      pushToast({ text: 'Saved custom ACP provider', level: 'success' });
      await Promise.all([refreshHostInfo(hostId), refreshProviderConfigsOnly(hostId), checkHealthAll()]);
    } catch (e) {
      pushToast({ text: (e as Error).message, level: 'error' });
    }
  };

  const addProjectScript = async () => {
    if (!hostId || !selectedProj || !newScriptName.trim() || !newScriptCmd.trim()) return;
    const nextScripts: ScriptDef[] = [
      ...(selectedProj.scripts ?? []),
      {
        id: `sc-${Date.now().toString(36)}`,
        name: newScriptName.trim(),
        command: newScriptCmd.trim(),
        runOnWorktreeCreate: newScriptOnWt,
      },
    ];
    try {
      await callHost(hostId, 'projects.update', { id: selectedProj.id, scripts: nextScripts });
      setNewScriptName('');
      setNewScriptCmd('');
      setNewScriptOnWt(false);
      pushToast({ text: 'Added project script', level: 'success' });
    } catch (e) {
      pushToast({ text: (e as Error).message, level: 'error' });
    }
  };

  const submitHostRename = async (id: string) => {
    const trimmed = renameHostValue.trim();
    if (trimmed) {
      await renameHost(id, trimmed);
      pushToast({ text: `Renamed host to "${trimmed}"`, level: 'success' });
    }
    setRenamingHostId(null);
    setRenameHostValue('');
  };

  const effectiveTab = filteredTabs.some((t) => t.id === tab) ? tab : (filteredTabs[0]?.id ?? tab);

  return (
    <div
      data-testid="settings-modal"
      onClick={() => setSettingsModalOpen(false)}
      onKeyDown={(e) => {
        if (e.key === 'Escape') setSettingsModalOpen(false);
      }}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="flex w-full max-w-4xl h-[82vh] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] shadow-2xl overflow-hidden text-xs"
      >
        {/* Left Nav */}
        <div className="w-56 shrink-0 border-r border-[var(--border)] bg-[var(--bg-sidebar)] flex flex-col">
          <div className="p-3 border-b border-[var(--border)]">
            <div className="relative">
              <Search className="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search settings..."
                className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] pl-8 pr-2.5 py-1.5 text-xs outline-none focus:border-[var(--accent)]"
              />
            </div>
          </div>
          <div className="flex-1 overflow-y-auto p-2 space-y-0.5">
            {filteredTabs.map((t) => {
              const Icon = t.icon;
              return (
                <button
                  key={t.id}
                  type="button"
                  data-testid={`settings-tab-${t.id}`}
                  onClick={() => setTab(t.id)}
                  className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left transition cursor-pointer ${
                    effectiveTab === t.id
                      ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium'
                      : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]'
                  }`}
                >
                  <Icon className="w-4 h-4 shrink-0" />
                  <span>{t.label}</span>
                </button>
              );
            })}
          </div>
          <div className="p-2 border-t border-[var(--border)]">
            <button
              type="button"
              onClick={() => {
                void confirmAction({ title: 'Reset Settings', message: 'Reset all client settings to their defaults?', confirmLabel: 'Reset' }).then((ok) => { if (ok) resetSettings(); });
              }}
              className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-[var(--border)] py-1.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
            >
              <RotateCcw className="w-3 h-3" />
              Reset Defaults
            </button>
          </div>
        </div>

        {/* Right Content */}
        <div className="flex-1 flex flex-col min-w-0 min-h-0">
          <div className="flex items-center justify-between gap-2 border-b border-[var(--border)] px-5 py-3">
            <div className="text-sm font-semibold">{TABS.find((t) => t.id === effectiveTab)?.label ?? 'Settings'}</div>
            <div className="flex items-center gap-2">
              {HOST_SCOPED_TABS.has(effectiveTab) && hostList.length > 1 && (
                <div className="inline-flex items-center gap-1.5">
                  <Server className="w-3.5 h-3.5 text-[var(--accent)]" />
                  <select
                    data-testid="settings-host-select"
                    value={hostId}
                    onChange={(e) => {
                      setSelectedHostId(e.target.value);
                      setCodexApiKeyInput('');
                    }}
                    className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                  >
                    {hostList.map((h) => (
                      <option key={h.creds.id} value={h.creds.id}>
                        {h.creds.name} ({h.state})
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <button
                type="button"
                data-testid="settings-close-btn"
                onClick={() => setSettingsModalOpen(false)}
                className="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
          </div>

          <div className="flex-1 overflow-y-auto p-5 space-y-4">
            {/* GENERAL */}
            {effectiveTab === 'general' && (
              <div className="space-y-4">
                <div className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                  <div>
                    <div className="font-medium">Send message shortcut</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Choose whether Enter or Ctrl+Enter submits</div>
                  </div>
                  <select
                    value={settings.sendShortcut}
                    onChange={(e) => updateSettings({ sendShortcut: e.target.value as 'enter' | 'mod-enter' })}
                    className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1"
                  >
                    <option value="enter">Enter (Shift+Enter newline)</option>
                    <option value="mod-enter">Ctrl+Enter (Enter newline)</option>
                  </select>
                </div>

                <div className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                  <div>
                    <div className="font-medium">Default follow-up behavior while busy</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Hold Ctrl in composer to invert temporarily</div>
                  </div>
                  <select
                    value={settings.followUpBehavior}
                    onChange={(e) => updateSettings({ followUpBehavior: e.target.value as 'steer' | 'queue' })}
                    className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1"
                  >
                    <option value="queue">Queue until turn finishes</option>
                    <option value="steer">Steer immediately</option>
                  </select>
                </div>

                {[
                  ['notificationsEnabled', 'Desktop notifications', 'Master toggle for system notifications on Linux'],
                  ['closeToTray', 'Close window to system tray', 'Keep Agent Companion running in the tray on Linux'],
                  ['showWorkingShelf', 'Show Working Shelf in sidebar', 'Pin active & pending-approval threads at the top'],
                  ['groupByProject', 'Group threads by Project', 'Organize sidebar threads under their project folders'],
                ].map(([key, title, desc]) => (
                  <label
                    key={key}
                    className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 cursor-pointer"
                  >
                    <div>
                      <div className="font-medium">{title}</div>
                      <div className="text-[11px] text-[var(--text-muted)]">{desc}</div>
                    </div>
                    <input
                      type="checkbox"
                      checked={Boolean(settings[key as keyof typeof settings])}
                      onChange={(e) => updateSettings({ [key]: e.target.checked })}
                    />
                  </label>
                ))}

                {settings.notificationsEnabled && (
                  <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3.5 space-y-2.5">
                    <div>
                      <div className="font-medium">Notification Categories</div>
                      <div className="text-[11px] text-[var(--text-muted)]">
                        Choose which agent events trigger notifications (minor intermediate progress updates are off by default)
                      </div>
                    </div>
                    {(
                      [
                        ['notifyOnInput', 'input', 'Action required', 'Permissions, questions, plan reviews, and blocked terminals'],
                        ['notifyOnDone', 'done', 'Task completed', 'Final turn completion when no background tasks remain'],
                        ['notifyOnHalted', 'halted', 'Halted or errored', 'Agent crashes, API halts, and failed commands'],
                        ['notifyOnProgress', 'progress', 'Minor progress updates', 'Intermediate turns while subagents or timers are still running'],
                      ] as const
                    ).map(([field, _evKey, label, desc]) => (
                      <label key={field} className="flex items-center justify-between gap-3 py-1 cursor-pointer">
                        <div>
                          <div className="font-medium text-[11.5px]">{label}</div>
                          <div className="text-[10.5px] text-[var(--text-muted)]">{desc}</div>
                        </div>
                        <input
                          type="checkbox"
                          checked={Boolean(settings[field])}
                          onChange={(e) => {
                            const nextPatch = { [field]: e.target.checked } as Partial<typeof settings>;
                            const merged = { ...settings, ...nextPatch };
                            updateSettings(nextPatch);
                            const events: string[] = [];
                            if (merged.notifyOnInput) events.push('input');
                            if (merged.notifyOnDone) events.push('done');
                            if (merged.notifyOnHalted) events.push('halted');
                            if (merged.notifyOnProgress) events.push('progress');
                            for (const h of hostList) {
                              if (h.state === 'connected') {
                                void callHost(h.creds.id, 'daemon.config.set', { notifyEvents: events }).catch(() => {});
                              }
                            }
                          }}
                        />
                      </label>
                    ))}
                  </div>
                )}

                <label
                  data-testid="settings-agy-auto-allow-reads"
                  className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 cursor-pointer"
                >
                  <div>
                    <div className="font-medium">Auto-allow Antigravity (agy) read requests</div>
                    <div className="text-[11px] text-[var(--text-muted)]">
                      Automatically approve read &amp; search prompts outside the workspace without notification spam
                    </div>
                  </div>
                  <input
                    type="checkbox"
                    checked={Boolean(settings.agyAutoAllowReads)}
                    onChange={(e) => {
                      const next = e.target.checked;
                      updateSettings({ agyAutoAllowReads: next });
                      for (const h of hostList) {
                        if (h.state === 'connected') {
                          void callHost(h.creds.id, 'daemon.config.set', { agyAutoAllowReads: next }).catch(() => {});
                        }
                      }
                    }}
                  />
                </label>
              </div>
            )}

            {/* APPEARANCE */}
            {effectiveTab === 'appearance' && (
              <div className="space-y-4">
                <div>
                  <div className="font-medium mb-2">Theme Palette</div>
                  <div className="grid grid-cols-2 sm:grid-cols-3 gap-2.5">
                    {BUILTIN_THEMES.map((th) => {
                      const active = settings.themeId === th.id;
                      return (
                        <button
                          key={th.id}
                          type="button"
                          data-testid={`theme-btn-${th.id}`}
                          onClick={() => updateSettings({ themeId: th.id })}
                          className={`rounded-xl border p-3 text-left transition cursor-pointer ${
                            active
                              ? 'border-[var(--accent)] ring-2 ring-[var(--accent)]/30'
                              : 'border-[var(--border)] hover:border-[var(--border-strong)]'
                          }`}
                          style={{ background: th.vars['--bg-base'], color: th.vars['--text-primary'] }}
                        >
                          <div className="flex items-center justify-between mb-2">
                            <span className="font-medium text-xs">{th.name}</span>
                            <span
                              className="w-3.5 h-3.5 rounded-full"
                              style={{ background: th.vars['--accent'] }}
                            />
                          </div>
                          <div className="flex gap-1">
                            <span className="h-2 flex-1 rounded" style={{ background: th.vars['--bg-sidebar'] }} />
                            <span className="h-2 flex-1 rounded" style={{ background: th.vars['--bg-elevated'] }} />
                            <span className="h-2 flex-1 rounded" style={{ background: th.vars['--success'] }} />
                          </div>
                        </button>
                      );
                    })}
                  </div>
                </div>

                <div className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                  <div>
                    <div className="font-medium">Diff Color Palette</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Blue/Orange mode improves color-blind contrast</div>
                  </div>
                  <select
                    value={settings.diffPalette}
                    onChange={(e) => updateSettings({ diffPalette: e.target.value as 'red-green' | 'blue-orange' })}
                    className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1"
                  >
                    <option value="red-green">Red / Green (Default)</option>
                    <option value="blue-orange">Blue / Orange (Color-blind safe)</option>
                  </select>
                </div>

                <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 space-y-2">
                  <div className="flex justify-between">
                    <span className="font-medium">Chat Max Width</span>
                    <span className="font-mono text-[var(--text-muted)]">{settings.chatMaxWidth}px</span>
                  </div>
                  <input
                    type="range"
                    min={640}
                    max={1280}
                    step={20}
                    value={settings.chatMaxWidth}
                    onChange={(e) => updateSettings({ chatMaxWidth: Number(e.target.value) })}
                    className="w-full"
                  />
                </div>

                <label className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 cursor-pointer">
                  <div>
                    <div className="font-medium">Render KaTeX Math Formulas</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Typeset $inline$ and $$block$$ LaTeX equations</div>
                  </div>
                  <input
                    type="checkbox"
                    checked={settings.renderMath}
                    onChange={(e) => updateSettings({ renderMath: e.target.checked })}
                  />
                </label>

                <div className="space-y-1.5">
                  <div className="font-medium">Custom CSS (sanitized — external URLs blocked)</div>
                  <textarea
                    value={settings.customCss}
                    onChange={(e) => updateSettings({ customCss: e.target.value })}
                    rows={4}
                    placeholder=":root { --accent: #ff7a59; }"
                    className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-2.5 font-mono text-[11.5px] outline-none focus:border-[var(--accent)]"
                  />
                </div>
              </div>
            )}

            {/* HOSTS & DEVICES */}
            {effectiveTab === 'hosts' && (
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <div className="font-medium">Paired Hosts ({hostList.length})</div>
                  <button
                    type="button"
                    onClick={() => {
                      setSettingsModalOpen(false);
                      setPairModalOpen(true);
                    }}
                    className="inline-flex items-center gap-1 rounded-lg bg-[var(--accent)] px-3 py-1 text-xs font-semibold text-[var(--accent-fg)] cursor-pointer"
                  >
                    <Plus className="w-3.5 h-3.5" />
                    Pair Host
                  </button>
                </div>

                {hostList.map((h) => (
                  <div
                    key={h.creds.id}
                    className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 space-y-2"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="min-w-0 flex-1">
                        {renamingHostId === h.creds.id ? (
                          <div className="flex items-center gap-1.5 max-w-sm">
                            <input
                              type="text"
                              autoFocus
                              data-testid={`rename-host-input-${h.creds.id}`}
                              value={renameHostValue}
                              onChange={(e) => setRenameHostValue(e.target.value)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter') void submitHostRename(h.creds.id);
                                if (e.key === 'Escape') {
                                  setRenamingHostId(null);
                                  setRenameHostValue('');
                                }
                              }}
                              className="flex-1 rounded-md border border-[var(--accent)] bg-[var(--bg-surface)] px-2 py-0.5 text-xs outline-none"
                            />
                            <button
                              type="button"
                              data-testid={`rename-host-save-${h.creds.id}`}
                              onClick={() => void submitHostRename(h.creds.id)}
                              className="rounded bg-[var(--accent)] px-2 py-0.5 text-[11px] font-semibold text-[var(--accent-fg)] cursor-pointer"
                            >
                              Save
                            </button>
                            <button
                              type="button"
                              onClick={() => {
                                setRenamingHostId(null);
                                setRenameHostValue('');
                              }}
                              className="rounded border border-[var(--border)] px-2 py-0.5 text-[11px] text-[var(--text-muted)] cursor-pointer"
                            >
                              Cancel
                            </button>
                          </div>
                        ) : (
                          <div className="font-semibold flex items-center gap-2">
                            <span
                              className={`w-2 h-2 rounded-full ${
                                h.state === 'connected' ? 'bg-[var(--success)]' : 'bg-[var(--warning)]'
                              }`}
                            />
                            {h.creds.name}
                            <span className="text-[10.5px] font-normal text-[var(--text-muted)]">({h.state})</span>
                          </div>
                        )}
                        <div className="font-mono text-[10.5px] text-[var(--text-muted)] mt-0.5">
                          {h.creds.localUrl || h.creds.url} · v{h.info?.version || '?'} · proto v{h.info?.protocolVersion || 1}
                        </div>
                      </div>
                      <div className="flex items-center gap-1.5 shrink-0">
                        <button
                          type="button"
                          data-testid={`rename-host-btn-${h.creds.id}`}
                          onClick={() => {
                            setRenamingHostId(h.creds.id);
                            setRenameHostValue(h.creds.name);
                          }}
                          className="rounded border border-[var(--border)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
                        >
                          Rename
                        </button>
                        <button
                          type="button"
                          onClick={() => reconnectHost(h.creds.id)}
                          className="rounded border border-[var(--border)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
                        >
                          Reconnect
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            if (window.confirm(`Remove host "${h.creds.name}"?`)) void removeHost(h.creds.id);
                          }}
                          className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </div>

                    {/* Connected devices on this host */}
                    {h.devices.length > 0 && (
                      <div className="border-t border-[var(--border)] pt-2 space-y-1">
                        <div className="flex items-center justify-between">
                          <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                            Authorized Devices ({h.devices.length})
                          </div>
                          {h.devices.length > 1 && (
                            <button
                              type="button"
                              data-testid={`revoke-others-btn-${h.creds.id}`}
                              onClick={async () => {
                                if (!window.confirm(`Revoke all other paired devices on "${h.creds.name}"?`)) return;
                                try {
                                  const res = await callHost<{ revoked?: number }>(h.creds.id, 'devices.revokeOthers', {});
                                  pushToast({
                                    text: `Revoked ${res?.revoked ?? 0} other device(s)`,
                                    level: 'info',
                                  });
                                } catch (e) {
                                  pushToast({ text: (e as Error).message, level: 'error' });
                                }
                              }}
                              className="text-[10.5px] font-medium text-[var(--danger)] hover:underline cursor-pointer"
                            >
                              Revoke All Others
                            </button>
                          )}
                        </div>
                        {h.devices.map((d) => (
                          <div key={d.id} className="flex items-center justify-between text-[11px]">
                            <span>
                              {d.name} <span className="text-[var(--text-muted)]">({d.platform || 'client'})</span>
                            </span>
                            <button
                              type="button"
                              onClick={async () => {
                                if (!window.confirm(`Revoke device "${d.name}"?`)) return;
                                try {
                                  await callHost(h.creds.id, 'devices.revoke', { id: d.id });
                                  pushToast({ text: `Revoked ${d.name}`, level: 'info' });
                                } catch (e) {
                                  pushToast({ text: (e as Error).message, level: 'error' });
                                }
                              }}
                              className="text-[var(--danger)] hover:underline cursor-pointer"
                            >
                              Revoke
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}

            {/* PROVIDERS & ACP */}
            {effectiveTab === 'providers' && (
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <div>
                    <div className="font-medium">Agent Providers &amp; Generic ACP</div>
                    <div className="text-[11px] text-[var(--text-muted)]">
                      Configure Codex &amp; OpenRouter BYOK models, inspect built-in drivers, or register any custom ACP CLI.
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => void checkHealthAll()}
                    className="inline-flex items-center gap-1 rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    <RefreshCw className="w-3.5 h-3.5" />
                    Check Health
                  </button>
                </div>

                {/* Dedicated Codex & OpenRouter Integration Card */}
                <div
                  data-testid="codex-provider-card"
                  className="rounded-xl border border-[var(--border-strong)] bg-[var(--bg-base)] p-4 space-y-3.5"
                >
                  <div className="flex flex-wrap items-start justify-between gap-2">
                    <div className="space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        {codexAgentProfile?.available || codexHealth?.available ? (
                          <CheckCircle2 className="w-4 h-4 text-[var(--success)]" />
                        ) : (
                          <XCircle className="w-4 h-4 text-[var(--warning)]" />
                        )}
                        <span className="text-xs font-semibold text-[var(--text-primary)]">
                          Codex &amp; OpenRouter Integration
                        </span>
                        <span className="rounded bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--accent)]">
                          CX · codex app-server
                        </span>
                        {(codexHealth?.version || codexAgentProfile?.version) && (
                          <span className="font-mono text-[10.5px] text-[var(--text-muted)]">
                            {codexHealth?.version || codexAgentProfile?.version}
                          </span>
                        )}
                      </div>
                      <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                        <span
                          data-testid="codex-openrouter-badge"
                          className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10.5px] font-medium ${
                            openRouterReady
                              ? 'border-[var(--success)]/40 bg-[var(--success)]/10 text-[var(--success)]'
                              : 'border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-muted)]'
                          }`}
                        >
                          {openRouterReady ? <Check className="w-3 h-3" /> : <KeyRound className="w-3 h-3" />}
                          {openRouterReady ? 'OpenRouter Ready' : 'OpenRouter Not Configured'}
                        </span>
                        <span
                          data-testid="codex-native-auth-badge"
                          className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10.5px] font-medium ${
                            nativeAuthReady
                              ? 'border-[var(--success)]/40 bg-[var(--success)]/10 text-[var(--success)]'
                              : 'border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-muted)]'
                          }`}
                        >
                          {nativeAuthReady ? 'OpenAI Auth Ready' : 'OpenAI Auth Inactive'}
                        </span>
                        <span className="text-[10.5px] text-[var(--text-muted)] font-mono">
                          {codexCatalog?.models.length ?? 0} catalog models · {mergedCodexCustomModels.length} custom
                        </span>
                      </div>
                      {(codexHealth?.message || codexAgentProfile?.reason) && (
                        <div className="text-[10.5px] text-[var(--text-secondary)]">
                          {codexHealth?.message || codexAgentProfile?.reason}
                        </div>
                      )}
                    </div>

                    <div className="flex items-center gap-2">
                      <label className="inline-flex items-center gap-1.5 text-[11px] text-[var(--text-secondary)] cursor-pointer">
                        <input
                          type="checkbox"
                          data-testid="codex-enabled-checkbox"
                          checked={codexEnabled}
                          onChange={(e) => setCodexEnabled(e.target.checked)}
                        />
                        Enabled
                      </label>
                      <button
                        type="button"
                        data-testid="codex-refresh-catalog-btn"
                        disabled={codexRefreshing || !hostId}
                        onClick={() => void handleRefreshCodexCatalog()}
                        className="inline-flex items-center gap-1 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
                      >
                        <RefreshCw className={`w-3 h-3 ${codexRefreshing ? 'animate-spin' : ''}`} />
                        Refresh Catalog
                      </button>
                    </div>
                  </div>

                  {/* Codex & OpenRouter Configuration Fields */}
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5 pt-2 border-t border-[var(--border)]">
                    <div className="sm:col-span-2">
                      <div className="flex items-center justify-between mb-1">
                        <label className="text-[11px] font-medium text-[var(--text-secondary)]">
                          OpenRouter API Key (write-only · kept in daemon memory / providers.json)
                        </label>
                        {hasStoredRedactedOrKey && (
                          <div className="flex items-center gap-2">
                            <span className="font-mono text-[10.5px] text-[var(--success)]">
                              Stored (***)
                            </span>
                            <button
                              type="button"
                              data-testid="codex-clear-key-btn"
                              disabled={codexSaving || providersLoading}
                              onClick={() => void handleSaveCodexSettings(true)}
                              className="text-[10.5px] text-[var(--danger)] hover:underline cursor-pointer"
                            >
                              Clear Stored Key
                            </button>
                          </div>
                        )}
                      </div>
                      <input
                        type="password"
                        autoComplete="off"
                        data-testid="codex-openrouter-key-input"
                        value={codexApiKeyInput}
                        onChange={(e) => setCodexApiKeyInput(e.target.value)}
                        placeholder={
                          hasStoredRedactedOrKey
                            ? 'Stored in providers.json (*** — leave blank to preserve existing secret)'
                            : openRouterReady
                              ? 'Configured via daemon CLI/env (enter key to override)'
                              : 'sk-or-v1-...'
                        }
                        className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono text-xs outline-none focus:border-[var(--accent)]"
                      />
                    </div>

                    <div>
                      <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                        Codex CLI Binary Path (optional, supports ~/...)
                      </label>
                      <input
                        type="text"
                        data-testid="codex-command-input"
                        value={codexCommandInput}
                        onChange={(e) => setCodexCommandInput(e.target.value)}
                        placeholder={codexAgentProfile?.command || '~/.local/bin/codex'}
                        className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono text-xs outline-none focus:border-[var(--accent)]"
                      />
                    </div>

                    <div>
                      <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">
                        Default Model Slug (optional)
                      </label>
                      <input
                        type="text"
                        list="codex-catalog-models-datalist"
                        data-testid="codex-default-model-input"
                        value={codexDefaultModelInput}
                        onChange={(e) => setCodexDefaultModelInput(e.target.value)}
                        placeholder={codexCatalog?.defaultModelId || 'google/gemini-3-flash-preview'}
                        className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono text-xs outline-none focus:border-[var(--accent)]"
                      />
                      <datalist id="codex-catalog-models-datalist">
                        {(codexCatalog?.models ?? []).slice(0, 120).map((m) => (
                          <option key={m.modelId} value={m.modelId}>
                            {m.name}
                          </option>
                        ))}
                      </datalist>
                    </div>
                  </div>

                  <div className="flex justify-end">
                    <button
                      type="button"
                      data-testid="codex-save-settings-btn"
                      disabled={codexSaving || providersLoading || !hostId}
                      onClick={() => void handleSaveCodexSettings(false)}
                      className="rounded-lg bg-[var(--accent)] px-3.5 py-1.5 font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                    >
                      {codexSaving ? 'Saving...' : 'Save Codex Settings'}
                    </button>
                  </div>

                  {/* Saved Custom OpenRouter Models */}
                  <div
                    data-testid="codex-custom-models-section"
                    className="border-t border-[var(--border)] pt-3 space-y-2"
                  >
                    <div className="text-[11px] font-medium text-[var(--text-secondary)]">
                      Saved Custom OpenRouter &amp; Codex Models ({mergedCodexCustomModels.length})
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <input
                        type="text"
                        data-testid="codex-custom-slug-input"
                        value={newCustomSlug}
                        onChange={(e) => setNewCustomSlug(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            e.preventDefault();
                            void handleAddCodexCustomModel();
                          }
                        }}
                        placeholder="Model slug (e.g. google/gemini-3-flash-preview or vendor/model:thinking)"
                        className="flex-1 min-w-[220px] rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono text-xs outline-none focus:border-[var(--accent)]"
                      />
                      <input
                        type="text"
                        data-testid="codex-custom-name-input"
                        value={newCustomName}
                        onChange={(e) => setNewCustomName(e.target.value)}
                        placeholder="Display name (optional)"
                        className="w-44 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 text-xs outline-none focus:border-[var(--accent)]"
                      />
                      <button
                        type="button"
                        data-testid="codex-add-custom-model-btn"
                        disabled={!newCustomSlug.trim()}
                        onClick={() => void handleAddCodexCustomModel()}
                        className="inline-flex items-center gap-1 rounded-lg border border-[var(--accent)]/50 bg-[var(--accent-soft)] px-2.5 py-1.5 text-xs font-semibold text-[var(--accent)] disabled:opacity-40 cursor-pointer"
                      >
                        <Plus className="w-3.5 h-3.5" />
                        Add Model
                      </button>
                    </div>
                    {mergedCodexCustomModels.length > 0 && (
                      <div className="space-y-1 pt-1">
                        {mergedCodexCustomModels.map((cm) => (
                          <div
                            key={cm.slug}
                            className="flex items-center justify-between gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5"
                          >
                            <div className="min-w-0">
                              <span className="font-mono text-[11px] text-[var(--text-primary)]">{cm.slug}</span>
                              {cm.name && cm.name !== cm.slug && (
                                <span className="ml-2 text-[11px] text-[var(--text-muted)]">({cm.name})</span>
                              )}
                            </div>
                            <button
                              type="button"
                              onClick={() => void handleRemoveCodexCustomModel(cm.slug)}
                              title="Remove custom model"
                              className="p-0.5 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>

                {/* Other Built-in & Registered Providers */}
                <div className="space-y-2">
                  {(targetHost?.agents ?? [])
                    .filter((ag) => ag.id !== 'codex')
                    .map((ag) => {
                      const hl = providerHealth[ag.id];
                      const cfg = providerConfigs.find((c) => c.id === ag.id);
                      return (
                        <div
                          key={ag.id}
                          className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 flex items-center justify-between gap-3"
                        >
                          <div className="min-w-0">
                            <div className="flex items-center gap-2 font-medium">
                              {ag.available ? (
                                <CheckCircle2 className="w-3.5 h-3.5 text-[var(--success)]" />
                              ) : (
                                <XCircle className="w-3.5 h-3.5 text-[var(--text-muted)]" />
                              )}
                              <span>{ag.name}</span>
                              <span className="rounded bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                                {ag.driver || ag.id}
                              </span>
                              {ag.version && <span className="font-mono text-[10.5px] text-[var(--text-muted)]">{ag.version}</span>}
                            </div>
                            <div className="font-mono text-[10.5px] text-[var(--text-muted)] truncate mt-0.5">
                              {ag.command || ag.reason}
                            </div>
                            {hl && (
                              <div className="text-[10.5px] text-[var(--accent)] mt-0.5">
                                Health: {hl.available ? 'OK' : 'Unavailable'} · {hl.latencyMs}ms{' '}
                                {hl.message ? `· ${hl.message}` : ''}
                              </div>
                            )}
                          </div>
                          {cfg && !cfg.builtin && (
                            <button
                              type="button"
                              onClick={async () => {
                                await callHost(hostId, 'providers.remove', { id: cfg.id });
                                await Promise.all([refreshHostInfo(hostId), refreshProviderConfigsOnly(hostId)]);
                                pushToast({ text: `Removed provider ${cfg.name}`, level: 'info' });
                              }}
                              className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </button>
                          )}
                        </div>
                      );
                    })}
                </div>

                {/* Add Custom ACP Provider */}
                <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3.5 space-y-2.5">
                  <div className="font-medium">Add Custom ACP Provider</div>
                  <div className="grid grid-cols-2 gap-2">
                    <input
                      type="text"
                      value={newProvId}
                      onChange={(e) => setNewProvId(e.target.value)}
                      placeholder="id (e.g. gemini-cli)"
                      className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono"
                    />
                    <input
                      type="text"
                      value={newProvName}
                      onChange={(e) => setNewProvName(e.target.value)}
                      placeholder="Display Name"
                      className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5"
                    />
                    <input
                      type="text"
                      value={newProvCmd}
                      onChange={(e) => setNewProvCmd(e.target.value)}
                      placeholder="Command (e.g. /usr/local/bin/my-acp)"
                      className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono"
                    />
                    <input
                      type="text"
                      value={newProvArgs}
                      onChange={(e) => setNewProvArgs(e.target.value)}
                      placeholder="Args (space-separated, e.g. --acp)"
                      className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono"
                    />
                  </div>
                  <button
                    type="button"
                    disabled={!newProvId.trim() || !newProvCmd.trim()}
                    onClick={() => void addCustomProvider()}
                    className="rounded-lg bg-[var(--accent)] px-3 py-1.5 font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                  >
                    Register ACP Provider
                  </button>
                </div>
              </div>
            )}

            {/* CHAT & DIFFS */}
            {effectiveTab === 'chat' && (
              <div className="space-y-4">
                <div className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                  <div>
                    <div className="font-medium">Tool Call Verbosity</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Control how much work-log detail appears in timeline</div>
                  </div>
                  <select
                    value={settings.toolVerbosity}
                    onChange={(e) =>
                      updateSettings({
                        toolVerbosity: e.target.value as 'showAll' | 'hideSuccessful' | 'hideAll' | 'zen',
                      })
                    }
                    className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1"
                  >
                    <option value="showAll">Show all steps</option>
                    <option value="hideSuccessful">Hide successful tool calls</option>
                    <option value="hideAll">Errors only</option>
                    <option value="zen">Zen mode (prose only)</option>
                  </select>
                </div>

                <div className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                  <div>
                    <div className="font-medium">Default Diff View</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Unified stacked or side-by-side split</div>
                  </div>
                  <select
                    value={settings.diffViewMode}
                    onChange={(e) => updateSettings({ diffViewMode: e.target.value as 'stacked' | 'split' })}
                    className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1"
                  >
                    <option value="stacked">Stacked (Unified)</option>
                    <option value="split">Split (Side-by-Side)</option>
                  </select>
                </div>

                <label className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 cursor-pointer">
                  <div>
                    <div className="font-medium">Expand tool call details by default</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Start tool inputs/outputs and completed turn steps open</div>
                  </div>
                  <input
                    type="checkbox"
                    checked={Boolean(settings.expandToolCalls)}
                    onChange={(e) => updateSettings({ expandToolCalls: e.target.checked })}
                  />
                </label>

                <label className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 cursor-pointer">
                  <div>
                    <div className="font-medium">Wrap long diff lines</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Avoid horizontal scrollbars in narrow panels</div>
                  </div>
                  <input
                    type="checkbox"
                    checked={settings.diffWrap}
                    onChange={(e) => updateSettings({ diffWrap: e.target.checked })}
                  />
                </label>

                <label className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 cursor-pointer">
                  <div>
                    <div className="font-medium">Hide test files in diffs by default</div>
                    <div className="text-[11px] text-[var(--text-muted)]">Filter *_test.go, *.test.ts, *.spec.ts files</div>
                  </div>
                  <input
                    type="checkbox"
                    checked={settings.diffHideTests}
                    onChange={(e) => updateSettings({ diffHideTests: e.target.checked })}
                  />
                </label>
              </div>
            )}

            {/* KEYBINDINGS */}
            {effectiveTab === 'keybindings' && (
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <div className="text-[11px] text-[var(--text-muted)]">
                    Click any shortcut to record a new key chord. Press Escape to cancel.
                  </div>
                  <button
                    type="button"
                    onClick={() => updateSettings({ keybindings: {} })}
                    className="text-[11px] text-[var(--accent)] hover:underline cursor-pointer"
                  >
                    Reset all shortcuts
                  </button>
                </div>
                <div className="space-y-1.5">
                  {DEFAULT_KEYBINDINGS.map((kb) => {
                    const current = settings.keybindings[kb.id] || kb.defaultChord;
                    const rec = recordingId === kb.id;
                    return (
                      <div
                        key={kb.id}
                        className="flex items-center justify-between rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2"
                      >
                        <div>
                          <div className="font-medium">{kb.label}</div>
                          <div className="text-[10px] text-[var(--text-muted)]">{kb.group}</div>
                        </div>
                        <button
                          type="button"
                          onClick={() => setRecordingId(rec ? null : kb.id)}
                          className={`rounded-md border px-2.5 py-1 font-mono text-[11px] cursor-pointer ${
                            rec
                              ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)] animate-pulse'
                              : 'border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-primary)] hover:border-[var(--accent)]'
                          }`}
                        >
                          {rec ? 'Press keys...' : displayChord(current)}
                        </button>
                      </div>
                    );
                  })}
                </div>
              </div>
            )}

            {/* PROJECTS & SCRIPTS */}
            {effectiveTab === 'projects' && (
              <div className="space-y-4">
                {hostProjects.length === 0 ? (
                  <div className="py-8 text-center text-[var(--text-muted)]">No projects registered on this host yet.</div>
                ) : (
                  <>
                    <select
                      value={selectedProj?.id || ''}
                      onChange={(e) => setSelectedProjId(e.target.value)}
                      className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-1.5"
                    >
                      {hostProjects.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name} — {p.path}
                        </option>
                      ))}
                    </select>

                    {selectedProj && (
                      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3.5 space-y-3">
                        <div className="font-medium">Custom Scripts for {selectedProj.name}</div>
                        {(selectedProj.scripts ?? []).map((sc) => (
                          <div
                            key={sc.id}
                            className="flex items-center justify-between rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5"
                          >
                            <div>
                              <span className="font-medium">{sc.name}</span>
                              <span className="ml-2 font-mono text-[11px] text-[var(--text-muted)]">{sc.command}</span>
                              {sc.runOnWorktreeCreate && (
                                <span className="ml-2 rounded bg-[var(--accent-soft)] px-1.5 py-0.5 text-[10px] text-[var(--accent)]">
                                  on worktree create
                                </span>
                              )}
                            </div>
                            <button
                              type="button"
                              onClick={async () => {
                                const next = (selectedProj.scripts ?? []).filter((x) => x.id !== sc.id);
                                await callHost(hostId, 'projects.update', { id: selectedProj.id, scripts: next });
                              }}
                              className="text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </button>
                          </div>
                        ))}
                        <div className="grid grid-cols-2 gap-2 pt-2 border-t border-[var(--border)]">
                          <input
                            type="text"
                            value={newScriptName}
                            onChange={(e) => setNewScriptName(e.target.value)}
                            placeholder="Script name (e.g. test)"
                            className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5"
                          />
                          <input
                            type="text"
                            value={newScriptCmd}
                            onChange={(e) => setNewScriptCmd(e.target.value)}
                            placeholder="Shell command (e.g. npm test)"
                            className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1.5 font-mono"
                          />
                        </div>
                        <div className="flex items-center justify-between">
                          <label className="flex items-center gap-1.5 text-[11px]">
                            <input
                              type="checkbox"
                              checked={newScriptOnWt}
                              onChange={(e) => setNewScriptOnWt(e.target.checked)}
                            />
                            Auto-run on worktree creation
                          </label>
                          <button
                            type="button"
                            disabled={!newScriptName.trim() || !newScriptCmd.trim()}
                            onClick={() => void addProjectScript()}
                            className="rounded-lg bg-[var(--accent)] px-3 py-1 font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
                          >
                            Add Script
                          </button>
                        </div>
                      </div>
                    )}
                  </>
                )}
              </div>
            )}

            {/* DIAGNOSTICS & HOST HEALTH (Linux PSI) */}
            {effectiveTab === 'health' && (
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <div className="font-medium">Linux Daemon Host Health &amp; Memory Pressure (PSI)</div>
                  <button
                    type="button"
                    onClick={() => void loadHostHealth()}
                    className="inline-flex items-center gap-1 rounded-lg border border-[var(--border)] px-2.5 py-1 hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    <RefreshCw className={`w-3.5 h-3.5 ${healthLoading ? 'animate-spin' : ''}`} />
                    Refresh
                  </button>
                </div>

                {health && (
                  <>
                    <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5">
                      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                        <div className="text-[10.5px] text-[var(--text-muted)]">Memory Used</div>
                        <div className="text-lg font-mono font-semibold mt-0.5">{health.memUsedPct.toFixed(1)}%</div>
                        <div className="text-[10px] text-[var(--text-muted)]">
                          {Math.round(health.memAvailableKiB / 1024)} MiB free
                        </div>
                      </div>
                      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                        <div className="text-[10.5px] text-[var(--text-muted)]">PSI some avg10</div>
                        <div className="text-lg font-mono font-semibold mt-0.5">{health.psiSomeAvg10.toFixed(2)}%</div>
                      </div>
                      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                        <div className="text-[10.5px] text-[var(--text-muted)]">PSI full avg10</div>
                        <div className="text-lg font-mono font-semibold mt-0.5">{health.psiFullAvg10.toFixed(2)}%</div>
                      </div>
                      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3">
                        <div className="text-[10.5px] text-[var(--text-muted)]">Load Average</div>
                        <div className="text-lg font-mono font-semibold mt-0.5">
                          {health.loadAvg1.toFixed(2)} / {health.loadAvg5.toFixed(2)}
                        </div>
                      </div>
                    </div>

                    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 space-y-2">
                      <div className="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                        Managed Agent Processes ({health.processes?.length ?? 0})
                      </div>
                      {(health.processes ?? []).length === 0 ? (
                        <div className="text-[var(--text-muted)] py-3 text-center">No active agent child processes.</div>
                      ) : (
                        (health.processes ?? []).map((p) => (
                          <div
                            key={p.pid}
                            className="flex items-center justify-between rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-1.5"
                          >
                            <div>
                              <span className="font-medium">{p.title || p.sessionId}</span>
                              <span className="ml-2 font-mono text-[10.5px] text-[var(--text-muted)]">
                                PID {p.pid} · {p.agent} · {Math.round(p.rssKiB / 1024)} MiB RSS
                              </span>
                            </div>
                            <button
                              type="button"
                              onClick={async () => {
                                if (!window.confirm(`Kill agent process PID ${p.pid}?`)) return;
                                await callHost(hostId, 'daemon.killProcess', { pid: p.pid });
                                pushToast({ text: `Killed PID ${p.pid}`, level: 'info' });
                                await loadHostHealth();
                              }}
                              className="rounded border border-[var(--danger)]/40 px-2 py-0.5 text-[11px] text-[var(--danger)] hover:bg-[var(--danger)]/10 cursor-pointer"
                            >
                              Kill
                            </button>
                          </div>
                        ))
                      )}
                    </div>
                  </>
                )}
              </div>
            )}

            {/* ARCHIVE */}
            {effectiveTab === 'archive' && (
              <div className="space-y-2">
                {archivedSessions.length === 0 ? (
                  <div className="py-10 text-center text-[var(--text-muted)]">No archived threads.</div>
                ) : (
                  archivedSessions.map((s) => (
                    <div
                      key={`${s.hostId}:${s.id}`}
                      className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2"
                    >
                      <button
                        type="button"
                        onClick={() => {
                          selectSession(s.hostId, s.id);
                          setSettingsModalOpen(false);
                        }}
                        className="text-left min-w-0 flex-1 cursor-pointer"
                      >
                        <div className="font-medium truncate">{plainInlineTitle(s.title) || s.id}</div>
                        <div className="text-[10.5px] text-[var(--text-muted)] font-mono truncate">
                          {s.hostName} · {s.cwd}
                        </div>
                      </button>
                      <div className="flex items-center gap-1.5">
                        <button
                          type="button"
                          onClick={() => void callHost(s.hostId, 'session.archive', { id: s.id, archived: false })}
                          className="rounded border border-[var(--border)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer"
                        >
                          Restore
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            if (window.confirm('Permanently delete this thread?')) {
                              void callHost(s.hostId, 'session.delete', { id: s.id });
                            }
                          }}
                          className="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            )}

            {/* ABOUT */}
            {effectiveTab === 'about' && (
              <div className="space-y-3 text-xs text-[var(--text-secondary)]">
                <div className="text-base font-semibold text-[var(--text-primary)]">Agent Companion Desktop</div>
                <p>
                  Thin, high-resilience Linux desktop workbench talking end-to-end encrypted Noise_IK / Noise_IKpsk1
                  WebSockets to standalone <code className="font-mono">agentc</code> daemons across local and remote Linux
                  hosts.
                </p>
                <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 font-mono text-[11px] space-y-1">
                  <div>Desktop Version: {window.agentcDesktop?.version || '0.6.1'}</div>
                  <div>Protocol Version: v{targetHost?.info?.protocolVersion ?? 2}</div>
                  <div>Active Daemon: {targetHost?.info?.version ?? 'not connected'}</div>
                  <div>OpenRouter Configured: {targetHost?.info?.openRouterConfigured ? 'yes' : 'no'}</div>
                  <div>
                    Capabilities: {(targetHost?.info?.capabilities ?? []).join(', ') || '—'}
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
};
