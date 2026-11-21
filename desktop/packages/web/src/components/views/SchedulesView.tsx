import React, { useCallback, useEffect, useState } from 'react';
import {
  CalendarClock,
  CheckCircle2,
  ExternalLink,
  Play,
  Plus,
  RefreshCw,
  Trash2,
  X,
  XCircle,
} from 'lucide-react';
import type { ScheduledTask, ScheduleRunRecord } from '@agentc/protocol';
import { useApp } from '../../state/appStore.js';

export const SchedulesView: React.FC = () => {
  const { activeHost, hostList, callHost, selectSession, pushToast } = useApp();
  const [hostId, setHostId] = useState(activeHost?.creds.id || hostList[0]?.creds.id || '');
  const [tasks, setTasks] = useState<ScheduledTask[]>([]);
  const [runs, setRuns] = useState<ScheduleRunRecord[]>([]);
  const [loading, setLoading] = useState(false);

  const [editorOpen, setEditorOpen] = useState(false);
  const [editId, setEditId] = useState('');
  const [name, setName] = useState('');
  const [kind, setKind] = useState<'cron' | 'interval' | 'once'>('cron');
  const [cron, setCron] = useState('0 9 * * 1-5');
  const [intervalSec, setIntervalSec] = useState(3600);
  const [cwd, setCwd] = useState('');
  const [agent, setAgent] = useState('');
  const [prompt, setPrompt] = useState('');
  const [useWorktree, setUseWorktree] = useState(true);
  const [enabled, setEnabled] = useState(true);

  const host = hostList.find((h) => h.creds.id === hostId) ?? activeHost ?? null;

  useEffect(() => {
    if (!hostId && activeHost) setHostId(activeHost.creds.id);
  }, [hostId, activeHost]);

  const load = useCallback(async () => {
    if (!hostId) return;
    setLoading(true);
    try {
      const res = await callHost<{ tasks: ScheduledTask[]; runs: ScheduleRunRecord[] }>(hostId, 'schedules.list', {});
      setTasks(Array.isArray(res?.tasks) ? res.tasks : []);
      setRuns(Array.isArray(res?.runs) ? res.runs : []);
    } catch (e) {
      pushToast({ text: `Failed to load schedules: ${(e as Error).message}`, level: 'error' });
    } finally {
      setLoading(false);
    }
  }, [hostId, callHost, pushToast]);

  useEffect(() => {
    void load();
  }, [load]);

  const openNew = () => {
    setEditId('');
    setName('');
    setKind('cron');
    setCron('0 9 * * 1-5');
    setIntervalSec(3600);
    setCwd(host?.projects[0]?.path || '~');
    setAgent(host?.agents.find((a) => a.available)?.id || 'claude');
    setPrompt('');
    setUseWorktree(false);
    setEnabled(true);
    setEditorOpen(true);
  };

  const openEdit = (t: ScheduledTask) => {
    setEditId(t.id);
    setName(t.name);
    setKind((t.kind as 'cron' | 'interval' | 'once') || 'cron');
    setCron(t.cron || '0 9 * * 1-5');
    setIntervalSec(t.intervalSec || 3600);
    setCwd(t.cwd);
    setAgent(t.agent);
    setPrompt(t.prompt);
    setUseWorktree(Boolean(t.useWorktree));
    setEnabled(t.enabled);
    setEditorOpen(true);
  };

  const saveTask = async () => {
    if (!hostId || !name.trim() || !prompt.trim()) return;
    try {
      await callHost(hostId, 'schedules.upsert', {
        id: editId || undefined,
        name: name.trim(),
        enabled,
        kind,
        cron: kind === 'cron' ? cron.trim() : undefined,
        intervalSec: kind === 'interval' ? Number(intervalSec) || 3600 : undefined,
        runAt: kind === 'once' ? Date.now() + 60_000 : undefined,
        cwd: cwd.trim() || '~',
        agent: agent || 'claude',
        prompt: prompt.trim(),
        useWorktree,
      });
      setEditorOpen(false);
      pushToast({ text: `Saved schedule "${name.trim()}"`, level: 'success' });
      await load();
    } catch (e) {
      pushToast({ text: `Save failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const toggleEnabled = async (t: ScheduledTask) => {
    try {
      await callHost(hostId, 'schedules.upsert', { ...t, enabled: !t.enabled });
      await load();
    } catch (e) {
      pushToast({ text: (e as Error).message, level: 'error' });
    }
  };

  const runNow = async (t: ScheduledTask) => {
    try {
      await callHost(hostId, 'schedules.runNow', { id: t.id });
      pushToast({ text: `Triggered "${t.name}"`, level: 'success' });
      setTimeout(() => void load(), 600);
    } catch (e) {
      pushToast({ text: `Run failed: ${(e as Error).message}`, level: 'error' });
    }
  };

  const deleteTask = async (t: ScheduledTask) => {
    if (!window.confirm(`Delete schedule "${t.name}"?`)) return;
    try {
      await callHost(hostId, 'schedules.delete', { id: t.id });
      pushToast({ text: `Deleted "${t.name}"`, level: 'info' });
      await load();
    } catch (e) {
      pushToast({ text: (e as Error).message, level: 'error' });
    }
  };

  return (
    <div data-testid="schedules-view" className="flex-1 overflow-y-auto p-6">
      <div className="mx-auto max-w-5xl space-y-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-lg font-semibold flex items-center gap-2">
              <CalendarClock className="w-5 h-5 text-[var(--accent)]" />
              Scheduled Tasks
            </h1>
            <p className="text-xs text-[var(--text-muted)]">
              Automate recurring agent runs (cron, interval, or one-shot) directly on your daemon host.
            </p>
          </div>
          <div className="flex items-center gap-2">
            {hostList.length > 1 && (
              <select
                value={hostId}
                onChange={(e) => setHostId(e.target.value)}
                className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-1 text-xs"
              >
                {hostList.map((h) => (
                  <option key={h.creds.id} value={h.creds.id}>
                    {h.creds.name}
                  </option>
                ))}
              </select>
            )}
            <button
              type="button"
              data-testid="schedule-new-btn"
              onClick={openNew}
              className="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent)] px-3 py-1.5 text-xs font-semibold text-[var(--accent-fg)] cursor-pointer"
            >
              <Plus className="w-3.5 h-3.5" />
              New Schedule
            </button>
            <button
              type="button"
              onClick={() => void load()}
              className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-1.5 text-[var(--text-muted)] cursor-pointer"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
            </button>
          </div>
        </div>

        {/* Tasks List */}
        <div className="space-y-2.5">
          {tasks.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-[var(--border)] p-10 text-center text-xs text-[var(--text-muted)]">
              No scheduled tasks yet. Create one to run daily code reviews, test sweeps, or maintenance prompts.
            </div>
          ) : (
            tasks.map((t) => (
              <div
                key={t.id}
                data-testid="schedule-task-card"
                className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 flex flex-wrap items-start justify-between gap-3"
              >
                <div className="space-y-1 min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-sm">{t.name}</span>
                    <span className="rounded-md bg-[var(--bg-elevated)] px-2 py-0.5 font-mono text-[11px] text-[var(--accent)]">
                      {t.kind === 'cron' ? t.cron : t.kind === 'interval' ? `every ${t.intervalSec}s` : 'once'}
                    </span>
                    <span className="rounded-md border border-[var(--border)] px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)]">
                      {t.agent}
                    </span>
                    {!t.enabled && (
                      <span className="rounded bg-[var(--warning)]/15 px-1.5 py-0.5 text-[10px] text-[var(--warning)]">
                        Paused
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-[var(--text-secondary)] line-clamp-2">{t.prompt}</div>
                  <div className="text-[11px] font-mono text-[var(--text-muted)]">
                    {t.cwd}
                    {t.nextRunAt ? ` · Next: ${new Date(t.nextRunAt).toLocaleString()}` : ''}
                  </div>
                </div>

                <div className="flex items-center gap-1.5 shrink-0">
                  <button
                    type="button"
                    data-testid="schedule-run-now-btn"
                    onClick={() => void runNow(t)}
                    title="Run now"
                    className="inline-flex items-center gap-1 rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    <Play className="w-3 h-3 text-[var(--success)]" />
                    Run
                  </button>
                  <button
                    type="button"
                    onClick={() => openEdit(t)}
                    className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    Edit
                  </button>
                  <button
                    type="button"
                    onClick={() => void toggleEnabled(t)}
                    className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    {t.enabled ? 'Pause' : 'Resume'}
                  </button>
                  <button
                    type="button"
                    onClick={() => void deleteTask(t)}
                    className="rounded-lg p-1.5 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </div>
              </div>
            ))
          )}
        </div>

        {/* Run History */}
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-2.5">
          <div className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">
            Recent Run History ({runs.length})
          </div>
          {runs.length === 0 ? (
            <div className="py-4 text-center text-xs text-[var(--text-muted)]">No runs recorded yet.</div>
          ) : (
            <div className="space-y-1.5">
              {runs.slice(0, 20).map((r) => (
                <div
                  key={r.id}
                  className="flex items-center justify-between gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2 text-xs"
                >
                  <div className="flex items-center gap-2 min-w-0">
                    {r.status === 'error' ? (
                      <XCircle className="w-3.5 h-3.5 shrink-0 text-[var(--danger)]" />
                    ) : (
                      <CheckCircle2 className="w-3.5 h-3.5 shrink-0 text-[var(--success)]" />
                    )}
                    <span className="font-medium truncate">{r.taskName}</span>
                    <span className="text-[11px] text-[var(--text-muted)]">
                      {new Date(r.startedAt).toLocaleString()}
                    </span>
                    {r.error && <span className="text-[11px] text-[var(--danger)] truncate">{r.error}</span>}
                  </div>
                  {r.sessionId && (
                    <button
                      type="button"
                      onClick={() => selectSession(hostId, r.sessionId!)}
                      className="inline-flex items-center gap-1 text-[11px] text-[var(--accent)] hover:underline shrink-0 cursor-pointer"
                    >
                      Open thread
                      <ExternalLink className="w-3 h-3" />
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Create / Edit Modal */}
      {editorOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-lg rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-5 shadow-2xl space-y-3 text-xs">
            <div className="flex items-center justify-between">
              <div className="text-sm font-semibold">{editId ? 'Edit Schedule' : 'New Scheduled Task'}</div>
              <button type="button" onClick={() => setEditorOpen(false)} className="cursor-pointer">
                <X className="w-4 h-4 text-[var(--text-muted)]" />
              </button>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="col-span-2">
                <label className="block text-[11px] text-[var(--text-muted)] mb-1">Task Name</label>
                <input
                  data-testid="schedule-name-input"
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Nightly test triage"
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 outline-none focus:border-[var(--accent)]"
                />
              </div>
              <div>
                <label className="block text-[11px] text-[var(--text-muted)] mb-1">Schedule Type</label>
                <select
                  value={kind}
                  onChange={(e) => setKind(e.target.value as 'cron' | 'interval' | 'once')}
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5"
                >
                  <option value="cron">Cron (5-field)</option>
                  <option value="interval">Interval (seconds)</option>
                  <option value="once">One-shot (in 1 min)</option>
                </select>
              </div>
              <div>
                <label className="block text-[11px] text-[var(--text-muted)] mb-1">
                  {kind === 'cron' ? 'Cron Expression' : kind === 'interval' ? 'Interval (sec)' : 'Timing'}
                </label>
                {kind === 'cron' ? (
                  <input
                    data-testid="schedule-cron-input"
                    type="text"
                    value={cron}
                    onChange={(e) => setCron(e.target.value)}
                    placeholder="0 9 * * 1-5"
                    className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 font-mono"
                  />
                ) : kind === 'interval' ? (
                  <input
                    type="number"
                    min={60}
                    value={intervalSec}
                    onChange={(e) => setIntervalSec(Number(e.target.value))}
                    className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 font-mono"
                  />
                ) : (
                  <div className="py-1.5 text-[var(--text-muted)]">Runs once in 60 seconds</div>
                )}
              </div>
              <div>
                <label className="block text-[11px] text-[var(--text-muted)] mb-1">Working Directory</label>
                <input
                  data-testid="schedule-cwd-input"
                  type="text"
                  value={cwd}
                  onChange={(e) => setCwd(e.target.value)}
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 font-mono"
                />
              </div>
              <div>
                <label className="block text-[11px] text-[var(--text-muted)] mb-1">Agent Provider</label>
                <select
                  value={agent}
                  onChange={(e) => setAgent(e.target.value)}
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5"
                >
                  {(host?.agents ?? []).map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </select>
              </div>
              <div className="col-span-2">
                <label className="block text-[11px] text-[var(--text-muted)] mb-1">Prompt</label>
                <textarea
                  data-testid="schedule-prompt-input"
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  rows={3}
                  placeholder="Run unit tests and summarize any regressions..."
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-2.5 outline-none focus:border-[var(--accent)]"
                />
              </div>
            </div>
            <div className="flex items-center gap-4">
              <label className="flex items-center gap-1.5">
                <input type="checkbox" checked={useWorktree} onChange={(e) => setUseWorktree(e.target.checked)} />
                Run in isolated worktree
              </label>
              <label className="flex items-center gap-1.5">
                <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
                Enabled
              </label>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={() => setEditorOpen(false)}
                className="rounded-lg border border-[var(--border)] px-3 py-1.5 cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                data-testid="schedule-save-btn"
                disabled={!name.trim() || !prompt.trim()}
                onClick={() => void saveTask()}
                className="rounded-lg bg-[var(--accent)] px-3.5 py-1.5 font-semibold text-[var(--accent-fg)] disabled:opacity-40 cursor-pointer"
              >
                Save Schedule
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
