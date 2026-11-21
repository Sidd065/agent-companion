import React, { useCallback, useEffect, useState } from 'react';
import {
  Activity,
  Archive,
  CheckCircle2,
  Cpu,
  FolderGit2,
  HeartPulse,
  Info,
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
  HostHealth,
  Project,
  ProviderConfig,
  ProviderHealth,
  ScriptDef,
} from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';
import { DEFAULT_KEYBINDINGS, displayChord, formatEventChord } from '../../state/keybindings.js';
import { BUILTIN_THEMES } from '../../state/themes.js';

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

export const SettingsModal: React.FC = () => {
  const {
    settingsModalOpen,
    setSettingsModalOpen,
    settingsInitialTab,
    settings,
    updateSettings,
    resetSettings,
    hostList,
    activeHost,
    allSessions,
    callHost,
    setPairModalOpen,
    reconnectHost,
    removeHost,
    renameHost,
    selectSession,
    pushToast,
  } = useApp();

  const [tab, setTab] = useState(settingsInitialTab || 'general');
  const [search, setSearch] = useState('');

  // Providers state
  const [providerConfigs, setProviderConfigs] = useState<ProviderConfig[]>([]);
  const [providerHealth, setProviderHealth] = useState<Record<string, ProviderHealth>>({});
  const [newProvId, setNewProvId] = useState('');
  const [newProvName, setNewProvName] = useState('');
  const [newProvCmd, setNewProvCmd] = useState('');
  const [newProvArgs, setNewProvArgs] = useState('');

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
    if (settingsModalOpen && settingsInitialTab) {
      setTab(settingsInitialTab);
    }
  }, [settingsModalOpen, settingsInitialTab]);

  const hostId = activeHost?.creds.id ?? hostList[0]?.creds.id ?? '';

  const loadProviders = useCallback(async () => {
    if (!hostId) return;
    try {
      const res = await callHost<{ configs?: ProviderConfig[] }>(hostId, 'providers.list', {});
      setProviderConfigs(Array.isArray(res?.configs) ? res.configs : []);
    } catch {
      setProviderConfigs([]);
    }
  }, [hostId, callHost]);

  const checkHealthAll = async () => {
    if (!hostId) return;
    try {
      const res = await callHost<ProviderHealth[]>(hostId, 'providers.health', {});
      const map: Record<string, ProviderHealth> = {};
      for (const h of Array.isArray(res) ? res : []) map[h.id] = h;
      setProviderHealth(map);
    } catch (e) {
      pushToast({ text: `Health check failed: ${(e as Error).message}`, level: 'error' });
    }
  };

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
    if (!settingsModalOpen) return;
    if (tab === 'providers') void loadProviders();
    if (tab === 'health') void loadHostHealth();
  }, [settingsModalOpen, tab, loadProviders, loadHostHealth]);

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

  if (!settingsModalOpen) return null;

  const filteredTabs = TABS.filter((t) => !search.trim() || t.label.toLowerCase().includes(search.toLowerCase()));
  const archivedSessions = allSessions.filter((s) => s.archived);
  const hostProjects: Project[] = activeHost?.projects ?? [];
  const selectedProj = hostProjects.find((p) => p.id === selectedProjId) ?? hostProjects[0] ?? null;

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
      await loadProviders();
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

  return (
    <div
      data-testid="settings-modal"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
    >
      <div className="flex w-full max-w-4xl h-[82vh] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] shadow-2xl overflow-hidden text-xs">
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
                    tab === t.id
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
                if (window.confirm('Reset all client settings to defaults?')) resetSettings();
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
          <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
            <div className="text-sm font-semibold">{TABS.find((t) => t.id === tab)?.label ?? 'Settings'}</div>
            <button
              type="button"
              data-testid="settings-close-btn"
              onClick={() => setSettingsModalOpen(false)}
              className="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          <div className="flex-1 overflow-y-auto p-5 space-y-4">
            {/* GENERAL */}
            {tab === 'general' && (
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
                  ['notificationsEnabled', 'Desktop notifications', 'Alert on approvals, questions, and finished turns'],
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
              </div>
            )}

            {/* APPEARANCE */}
            {tab === 'appearance' && (
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
            {tab === 'hosts' && (
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
                      <div>
                        <div className="font-semibold flex items-center gap-2">
                          <span
                            className={`w-2 h-2 rounded-full ${
                              h.state === 'connected' ? 'bg-[var(--success)]' : 'bg-[var(--warning)]'
                            }`}
                          />
                          {h.creds.name}
                          <span className="text-[10.5px] font-normal text-[var(--text-muted)]">({h.state})</span>
                        </div>
                        <div className="font-mono text-[10.5px] text-[var(--text-muted)]">
                          {h.creds.localUrl || h.creds.url} · v{h.info?.version || '?'} · proto v{h.info?.protocolVersion || 1}
                        </div>
                      </div>
                      <div className="flex items-center gap-1.5">
                        <button
                          type="button"
                          onClick={() => {
                            const next = window.prompt('Rename host:', h.creds.name);
                            if (next?.trim()) void renameHost(h.creds.id, next.trim());
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
                        <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                          Authorized Devices ({h.devices.length})
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
            {tab === 'providers' && (
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <div>
                    <div className="font-medium">Agent Providers &amp; Generic ACP</div>
                    <div className="text-[11px] text-[var(--text-muted)]">
                      Inspect built-in drivers (Claude Code, Antigravity, Codex) or register any custom ACP CLI.
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

                <div className="space-y-2">
                  {(activeHost?.agents ?? []).map((ag) => {
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
                              await loadProviders();
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
            {tab === 'chat' && (
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
            {tab === 'keybindings' && (
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
            {tab === 'projects' && (
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
            {tab === 'health' && (
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
            {tab === 'archive' && (
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
                        <div className="font-medium truncate">{s.title || s.id}</div>
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
            {tab === 'about' && (
              <div className="space-y-3 text-xs text-[var(--text-secondary)]">
                <div className="text-base font-semibold text-[var(--text-primary)]">Agent Companion Desktop</div>
                <p>
                  Thin, high-resilience Linux desktop workbench talking end-to-end encrypted Noise_IK / Noise_IKpsk1
                  WebSockets to standalone <code className="font-mono">agentc</code> daemons across local and remote Linux
                  hosts.
                </p>
                <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 font-mono text-[11px] space-y-1">
                  <div>Desktop Version: 0.4.0</div>
                  <div>Protocol Version: v{activeHost?.info?.protocolVersion ?? 2}</div>
                  <div>Active Daemon: {activeHost?.info?.version ?? 'not connected'}</div>
                  <div>
                    Capabilities: {(activeHost?.info?.capabilities ?? []).join(', ') || '—'}
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
