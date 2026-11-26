import React, { useEffect, useState } from 'react';
import { parsePairLink } from '@agentc/client-core';
import { Check, Laptop, Link2, Loader2, RefreshCw, Server, ShieldCheck, Trash2, X } from 'lucide-react';
import { useApp } from '../../state/appStore.js';
import { formatHostDisplayName } from '../../utils/multiHost.js';
import { confirmAction } from './ConfirmDialog.js';

export const OnboardingModal: React.FC = () => {
  const {
    pairModalOpen,
    setPairModalOpen,
    hostList,
    localDaemon,
    refreshLocalDaemon,
    pairLocalHost,
    pairHostByLink,
    removeHost,
    reconnectHost,
  } = useApp();

  const [linkInput, setLinkInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (pairModalOpen) {
      setError('');
      void refreshLocalDaemon();
    }
  }, [pairModalOpen, refreshLocalDaemon]);

  if (!pairModalOpen) return null;

  const parsed = parsePairLink(linkInput);
  const alreadyPairedLocal =
    localDaemon?.available && localDaemon.hostId && hostList.some((h) => h.creds.id === localDaemon.hostId);

  const handlePairLink = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      await pairHostByLink(linkInput);
      setLinkInput('');
      setPairModalOpen(false);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const handleLocalPair = async () => {
    setError('');
    setBusy(true);
    try {
      await pairLocalHost();
      setPairModalOpen(false);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      data-testid="pair-modal"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
      onClick={() => setPairModalOpen(false)}
    >
      <div
        className="w-full max-w-lg rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] shadow-2xl overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-[var(--border)]">
          <div className="flex items-center gap-2">
            <ShieldCheck className="w-5 h-5 text-[var(--accent)]" />
            <h2 className="text-sm font-semibold">Pair & Manage agentc Hosts</h2>
          </div>
          <button
            type="button"
            onClick={() => setPairModalOpen(false)}
            className="p-1 rounded hover:bg-[var(--bg-hover)] text-[var(--text-muted)] cursor-pointer"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="p-5 space-y-4 max-h-[75vh] overflow-y-auto">
          {/* One-click Local Daemon Pairing */}
          {localDaemon?.available && (
            <div
              data-testid="local-daemon-card"
              className="rounded-lg border border-[var(--accent)]/40 bg-[var(--accent-soft)] p-3.5 flex items-center justify-between gap-3"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-1.5 text-xs font-semibold text-[var(--text-primary)]">
                  <Laptop className="w-4 h-4 text-[var(--accent)]" />
                  Local daemon detected: {localDaemon.host}
                </div>
                <div className="text-[11px] text-[var(--text-secondary)] mt-0.5 font-mono truncate">
                  v{localDaemon.version} · {localDaemon.fingerprint} · {localDaemon.listenUrl}
                </div>
              </div>
              {alreadyPairedLocal ? (
                <span className="shrink-0 inline-flex items-center gap-1 text-xs text-[var(--success)] font-medium">
                  <Check className="w-3.5 h-3.5" /> Paired
                </span>
              ) : (
                <button
                  type="button"
                  data-testid="one-click-local-pair-btn"
                  disabled={busy}
                  onClick={() => void handleLocalPair()}
                  className="shrink-0 px-3 py-1.5 rounded-md bg-[var(--accent)] text-[var(--accent-fg)] text-xs font-semibold hover:opacity-90 disabled:opacity-50 cursor-pointer flex items-center gap-1.5"
                >
                  {busy && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
                  Pair this machine
                </button>
              )}
            </div>
          )}

          {/* Paste `agentc pair` link */}
          <form onSubmit={(e) => void handlePairLink(e)} className="space-y-2.5">
            <label className="block text-xs font-medium text-[var(--text-secondary)]">
              Pair any computer via <code className="font-mono text-[var(--text-primary)]">agentc pair</code>
            </label>
            <div className="flex gap-2">
              <div className="relative flex-1">
                <Link2 className="w-4 h-4 absolute left-2.5 top-2.5 text-[var(--text-muted)]" />
                <input
                  type="text"
                  data-testid="pair-link-input"
                  value={linkInput}
                  onChange={(e) => setLinkInput(e.target.value)}
                  placeholder="https://….trycloudflare.com/p/…#k=…&s=… or agentc://pair?…"
                  className="w-full pl-8 pr-3 py-2 text-xs font-mono rounded-lg bg-[var(--bg-base)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                />
              </div>
              <button
                type="submit"
                data-testid="pair-submit-btn"
                disabled={busy || !parsed}
                className="px-4 py-2 rounded-lg bg-[var(--accent)] text-[var(--accent-fg)] text-xs font-semibold disabled:opacity-40 cursor-pointer"
              >
                {busy ? 'Pairing…' : 'Pair'}
              </button>
            </div>
            {parsed && (
              <div className="text-[11px] text-[var(--success)] flex items-center gap-2 font-mono">
                <ShieldCheck className="w-3.5 h-3.5" />
                Valid Noise-IKpsk1 link · host: {parsed.name || parsed.base} · fingerprint: {parsed.fingerprint}
              </div>
            )}
            {error && (
              <div data-testid="pair-error" className="text-xs text-[var(--danger)] bg-red-500/10 border border-red-500/30 rounded-md px-3 py-2">
                {error}
              </div>
            )}
          </form>

          {/* Existing paired hosts */}
          {hostList.length > 0 && (
            <div className="pt-3 border-t border-[var(--border)] space-y-2">
              <div className="text-xs font-semibold text-[var(--text-secondary)]">Paired Hosts ({hostList.length})</div>
              {hostList.map((h) => (
                <div
                  key={h.creds.id}
                  data-testid={`paired-host-${h.creds.id}`}
                  className="flex items-center justify-between gap-2 p-2.5 rounded-lg border border-[var(--border)] bg-[var(--bg-base)]"
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <Server className="w-3.5 h-3.5 text-[var(--text-muted)]" />
                      <span className="text-xs font-semibold truncate">{formatHostDisplayName(h, hostList)}</span>
                      <span
                        className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${
                          h.state === 'connected'
                            ? 'bg-emerald-500/15 text-emerald-400'
                            : h.state === 'unpaired' || h.state === 'error'
                              ? 'bg-red-500/15 text-red-400'
                              : 'bg-amber-500/15 text-amber-300'
                        }`}
                      >
                        {h.state}
                      </span>
                    </div>
                    <div className="text-[11px] font-mono text-[var(--text-muted)] truncate mt-0.5">
                      {h.creds.localUrl || h.creds.url} · {h.creds.fingerprint}
                    </div>
                    {h.error && <div className="text-[11px] text-[var(--danger)] mt-0.5">{h.error}</div>}
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <button
                      type="button"
                      title="Reconnect"
                      onClick={() => reconnectHost(h.creds.id)}
                      className="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] cursor-pointer"
                    >
                      <RefreshCw className="w-3.5 h-3.5" />
                    </button>
                    <button
                      type="button"
                      title="Remove host"
                      onClick={() => {
                        void confirmAction({
                          title: 'Remove Paired Host',
                          message: `Disconnect and forget "${formatHostDisplayName(h, hostList)}" from this desktop client?`,
                          confirmLabel: 'Remove Host',
                        }).then((ok) => {
                          if (ok) void removeHost(h.creds.id);
                        });
                      }}
                      className="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--danger)] cursor-pointer"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
