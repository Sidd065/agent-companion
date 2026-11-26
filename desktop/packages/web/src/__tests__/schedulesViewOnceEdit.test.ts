import { describe, expect, it } from 'vitest';
import type { ScheduledTask } from '@agentc/protocol';
import {
  extractScheduleEditFormInit,
  resolveOnceScheduleRunAt,
} from '../components/views/SchedulesView.js';

describe('SchedulesView once-schedule edit runAt preservation (D-04)', () => {
  it('preserves existing t.runAt when editing an existing once schedule instead of resetting to now + 60s', () => {
    const fixedRunAt = 1_800_000_123_456;
    const task: ScheduledTask = {
      id: 'sched-once-1',
      name: 'Deploy window',
      enabled: true,
      kind: 'once',
      runAt: fixedRunAt,
      cwd: '/home/dev/app',
      projectId: 'proj-1',
      agent: 'codex',
      model: 'google/gemini-3-flash-preview',
      mode: 'auto',
      prompt: 'Run pre-deploy checks',
      useWorktree: false,
      nextRunAt: fixedRunAt,
      lastRunAt: 0,
      createdAt: 1_700_000_000_000,
      updatedAt: 1_700_000_000_000,
    };

    const init = extractScheduleEditFormInit(task);
    expect(init.editingRunAt).toBe(fixedRunAt);
    expect(resolveOnceScheduleRunAt(init.editId, init.editingRunAt, 1_900_000_000_000)).toBe(fixedRunAt);
  });

  it('uses now + 60_000 for brand-new once schedules or when switching a cron task to once', () => {
    const now = 1_750_000_000_000;
    expect(resolveOnceScheduleRunAt('', undefined, now)).toBe(now + 60_000);

    const cronTask: ScheduledTask = {
      id: 'sched-cron-1',
      name: 'Morning triage',
      enabled: true,
      kind: 'cron',
      cron: '0 9 * * 1-5',
      cwd: '~',
      agent: 'codex',
      prompt: 'Check CI',
      useWorktree: false,
      nextRunAt: 0,
      lastRunAt: 0,
      createdAt: 1_700_000_000_000,
      updatedAt: 1_700_000_000_000,
    };
    const initCron = extractScheduleEditFormInit(cronTask);
    expect(initCron.editingRunAt).toBeUndefined();
    expect(resolveOnceScheduleRunAt(initCron.editId, initCron.editingRunAt, now)).toBe(now + 60_000);
  });
});
