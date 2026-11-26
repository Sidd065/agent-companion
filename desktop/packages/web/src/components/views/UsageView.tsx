import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Activity, Coins, Cpu, RefreshCw } from 'lucide-react';
import type { UsageBucket, UsageSummary } from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';
import { formatHostDisplayName } from '../../utils/multiHost.js';

interface UsageRangeSpec {
  id: string;
  label: string;
  params: { days?: number; hours?: number; allTime?: boolean };
}

const RANGES: UsageRangeSpec[] = [
  { id: '5h', label: '5h', params: { hours: 5 } },
  { id: '12h', label: '12h', params: { hours: 12 } },
  { id: '24h', label: '24h', params: { hours: 24 } },
  { id: '7d', label: '7d', params: { days: 7 } },
  { id: '30d', label: '30d', params: { days: 30 } },
  { id: '90d', label: '90d', params: { days: 90 } },
  { id: '180d', label: '180d', params: { days: 180 } },
  { id: 'all', label: 'All time', params: { allTime: true } },
];

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

export const UsageView: React.FC = () => {
  const { activeHost, setActiveHostId, hostList, callHost, pushToast } = useApp();
  const [hostId, setHostId] = useState<string>('all');
  const [rangeId, setRangeId] = useState('30d');
  const activeRange = RANGES.find((r) => r.id === rangeId) ?? RANGES[4];
  const [metric, setMetric] = useState<'cost' | 'tokens'>('tokens');
  const [summary, setSummary] = useState<UsageSummary | null>(null);
  const [claudeLimits, setClaudeLimits] = useState<Record<string, unknown> | null>(null);
  const [agyQuota, setAgyQuota] = useState<Record<string, unknown> | null>(null);
  const [loading, setLoading] = useState(false);
  const reqSeqRef = useRef(0);

  useEffect(() => {
    if (hostId !== 'all' && hostId && !hostList.some((h) => h.creds.id === hostId)) {
      setHostId('all');
    }
  }, [hostId, hostList]);

  const load = useCallback(async () => {
    if (!hostId) return;
    const seq = ++reqSeqRef.current;
    setLoading(true);
    try {
      const targetHosts =
        hostId === 'all'
          ? hostList.filter((h) => h.state === 'connected').map((h) => h.creds.id)
          : [hostId];
      if (targetHosts.length === 0) {
        setLoading(false);
        return;
      }
      const primaryHost = targetHosts[0]!;
      const [summaries, cl, aq] = await Promise.all([
        Promise.all(
          targetHosts.map((hid) => callHost<UsageSummary>(hid, 'usage.summary', activeRange.params).catch(() => null)),
        ),
        callHost<Record<string, unknown>>(primaryHost, 'claude.usage', {}).catch(() => null),
        callHost<Record<string, unknown>>(primaryHost, 'agy.quota', {}).catch(() => null),
      ]);
      const mergeBuckets = (lists: UsageBucket[][]): UsageBucket[] => {
        const map = new Map<string, UsageBucket>();
        for (const arr of lists) {
          for (const b of arr || []) {
            const cur = map.get(b.key);
            if (!cur) map.set(b.key, { ...b });
            else {
              cur.turns += b.turns;
              cur.inputTokens += b.inputTokens;
              cur.outputTokens += b.outputTokens;
              cur.cachedTokens += b.cachedTokens;
              cur.totalTokens += b.totalTokens;
              cur.costUsd += b.costUsd;
            }
          }
        }
        return Array.from(map.values()).sort((a, b) => b.totalTokens - a.totalTokens);
      };
      const valid = summaries.filter((x): x is UsageSummary => Boolean(x));
      const u: UsageSummary = valid.length === 1
        ? valid[0]!
        : {
            days: valid[0]?.days ?? 30,
            turns: valid.reduce((s, x) => s + (x.turns || 0), 0),
            inputTokens: valid.reduce((s, x) => s + (x.inputTokens || 0), 0),
            outputTokens: valid.reduce((s, x) => s + (x.outputTokens || 0), 0),
            cachedTokens: valid.reduce((s, x) => s + (x.cachedTokens || 0), 0),
            totalTokens: valid.reduce((s, x) => s + (x.totalTokens || 0), 0),
            costUsd: valid.reduce((s, x) => s + (x.costUsd || 0), 0),
            byDay: mergeBuckets(valid.map((x) => x.byDay)).sort((a, b) => a.key.localeCompare(b.key)),
            byAgent: mergeBuckets(valid.map((x) => x.byAgent)),
            byModel: mergeBuckets(valid.map((x) => x.byModel)),
            byProject: mergeBuckets(valid.map((x) => x.byProject)),
          };
      if (seq !== reqSeqRef.current) return;
      setSummary(u);
      setClaudeLimits(cl);
      setAgyQuota(aq);
    } catch (e) {
      if (seq !== reqSeqRef.current) return;
      pushToast({ text: `Usage load failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      if (seq === reqSeqRef.current) {
        setLoading(false);
      }
    }
  }, [hostId, activeRange, callHost, pushToast]);

  useEffect(() => {
    reqSeqRef.current++;
    setSummary(null);
    setClaudeLimits(null);
    setAgyQuota(null);
    void load();
  }, [hostId, load]);

  const renderBucketList = (title: string, buckets: UsageBucket[]) => {
    const maxVal = Math.max(
      1,
      ...buckets.map((b) => (metric === 'cost' ? b.costUsd : b.totalTokens)),
    );
    return (
      <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-2.5">
        <div className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">{title}</div>
        {buckets.length === 0 ? (
          <div className="text-xs text-[var(--text-muted)] py-4 text-center">No activity in this window.</div>
        ) : (
          <div className="space-y-2">
            {buckets.slice(0, 8).map((b) => {
              const val = metric === 'cost' ? b.costUsd : b.totalTokens;
              const pct = Math.max(3, Math.round((val / maxVal) * 100));
              return (
                <div key={b.key} className="space-y-1 text-xs">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono truncate text-[var(--text-primary)]">{b.key || '(unknown)'}</span>
                    <span className="shrink-0 font-mono text-[11px] text-[var(--text-secondary)]">
                      {b.turns} turns · {fmtTokens(b.totalTokens)} tok · ${b.costUsd.toFixed(3)}
                    </span>
                  </div>
                  <div className="h-1.5 w-full rounded-full bg-[var(--bg-base)] overflow-hidden">
                    <div className="h-full rounded-full bg-[var(--accent)]" style={{ width: `${pct}%` }} />
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    );
  };

  const byDay = summary?.byDay ?? [];
  const maxDayVal = Math.max(1, ...byDay.map((d) => (metric === 'cost' ? d.costUsd : d.totalTokens)));

  return (
    <div data-testid="usage-view" className="flex-1 overflow-y-auto p-6">
      <div className="mx-auto max-w-5xl space-y-5">
        {/* Header */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-lg font-semibold flex items-center gap-2">
              <Activity className="w-5 h-5 text-[var(--accent)]" />
              Usage &amp; Cost Dashboard
            </h1>
            <p className="text-xs text-[var(--text-muted)]">
              Local 92-day token, turn, and cost telemetry captured directly by your daemon.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {hostList.length > 1 && (
              <select
                data-testid="usage-host-select"
                value={hostId}
                onChange={(e) => {
                  const v = e.target.value;
                  setHostId(v);
                  if (v !== 'all') setActiveHostId(v);
                }}
                className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1 text-xs"
              >
                <option value="all">All Connected Hosts ({hostList.filter((h) => h.state === 'connected').length})</option>
                {hostList.map((h) => (
                  <option key={h.creds.id} value={h.creds.id}>
                    {formatHostDisplayName(h, hostList)}
                  </option>
                ))}
              </select>
            )}
            <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-0.5 text-xs">
              {RANGES.map((r) => (
                <button
                  key={r.id}
                  type="button"
                  onClick={() => setRangeId(r.id)}
                  className={`rounded-md px-2 py-1 cursor-pointer ${
                    rangeId === r.id ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium' : 'text-[var(--text-muted)]'
                  }`}
                >
                  {r.label}
                </button>
              ))}
            </div>
            <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-0.5 text-xs">
              <button
                type="button"
                onClick={() => setMetric('tokens')}
                className={`rounded-md px-2.5 py-1 cursor-pointer ${
                  metric === 'tokens' ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium' : 'text-[var(--text-muted)]'
                }`}
              >
                Tokens
              </button>
              <button
                type="button"
                onClick={() => setMetric('cost')}
                className={`rounded-md px-2.5 py-1 cursor-pointer ${
                  metric === 'cost' ? 'bg-[var(--accent-soft)] text-[var(--accent)] font-medium' : 'text-[var(--text-muted)]'
                }`}
              >
                Cost ($)
              </button>
            </div>
            <button
              type="button"
              onClick={() => void load()}
              className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-1.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
            </button>
          </div>
        </div>

        {/* Summary KPI Cards */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
            <div className="text-[11px] text-[var(--text-muted)] flex items-center gap-1.5">
              <Coins className="w-3.5 h-3.5 text-[var(--accent)]" />
              Total Cost
            </div>
            <div className="mt-1 text-2xl font-semibold font-mono">${(summary?.costUsd ?? 0).toFixed(3)}</div>
          </div>
          <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
            <div className="text-[11px] text-[var(--text-muted)] flex items-center gap-1.5">
              <Cpu className="w-3.5 h-3.5 text-[var(--accent)]" />
              Total Tokens
            </div>
            <div className="mt-1 text-2xl font-semibold font-mono">{fmtTokens(summary?.totalTokens ?? 0)}</div>
          </div>
          <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
            <div className="text-[11px] text-[var(--text-muted)]">Cached Tokens</div>
            <div className="mt-1 text-2xl font-semibold font-mono">{fmtTokens(summary?.cachedTokens ?? 0)}</div>
          </div>
          <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
            <div className="text-[11px] text-[var(--text-muted)]">Completed Turns</div>
            <div className="mt-1 text-2xl font-semibold font-mono">{summary?.turns ?? 0}</div>
          </div>
        </div>

        {/* Daily Bar Chart */}
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
          <div className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)] mb-3">
            Daily Activity ({metric === 'cost' ? 'USD' : 'Tokens'})
          </div>
          {byDay.length === 0 ? (
            <div className="h-28 flex items-center justify-center text-xs text-[var(--text-muted)]">
              No daily records yet.
            </div>
          ) : (
            <div className="flex items-end gap-1.5 h-32 pt-2">
              {byDay.map((d) => {
                const v = metric === 'cost' ? d.costUsd : d.totalTokens;
                const h = Math.max(6, Math.round((v / maxDayVal) * 100));
                return (
                  <div key={d.key} className="flex-1 flex flex-col items-center gap-1 h-full justify-end min-w-[16px]">
                    <div
                      title={`${d.key}: ${d.turns} turns, ${fmtTokens(d.totalTokens)} tokens, $${d.costUsd.toFixed(3)}`}
                      style={{ height: `${h}%` }}
                      className="w-full max-w-[32px] rounded-t-md bg-[var(--accent)]/85 hover:bg-[var(--accent)] transition"
                    />
                    <span className="text-[9px] font-mono text-[var(--text-muted)] truncate max-w-full">
                      {d.key.slice(5)}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Breakdown Grid */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {renderBucketList('By Provider', summary?.byAgent ?? [])}
          {renderBucketList('By Model', summary?.byModel ?? [])}
          {renderBucketList('By Project / Folder', summary?.byProject ?? [])}
        </div>

        {/* Provider Live Limits / Quota Cards */}
        {(claudeLimits || agyQuota) && (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {claudeLimits && (
              <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-2">
                <div className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                  Claude Code Plan Limits
                </div>
                <pre className="max-h-40 overflow-auto rounded-lg bg-[var(--bg-base)] p-2.5 font-mono text-[11px] text-[var(--text-secondary)]">
                  {JSON.stringify(claudeLimits, null, 2)}
                </pre>
              </div>
            )}
            {agyQuota && (
              <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-2">
                <div className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                  Antigravity Quota
                </div>
                <pre className="max-h-40 overflow-auto rounded-lg bg-[var(--bg-base)] p-2.5 font-mono text-[11px] text-[var(--text-secondary)]">
                  {JSON.stringify(agyQuota, null, 2)}
                </pre>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
};
