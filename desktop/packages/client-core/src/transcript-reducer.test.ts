import { describe, expect, it } from 'vitest';
import { applyOps, applySnapshot, emptyTranscript } from './transcript-reducer.js';

describe('Transcript reducer', () => {
  it('applies add, append, set, remove, and deduplicates old seqs', () => {
    let s = emptyTranscript();
    s = applyOps(s, [
      { seq: 1, op: 'add', id: 'u1', item: { id: 'u1', kind: 'user', seq: 1, ts: 1, text: 'hi' } },
      { seq: 2, op: 'add', id: 'a1', item: { id: 'a1', kind: 'agent', seq: 2, ts: 2, text: 'Hel' } },
      { seq: 3, op: 'append', id: 'a1', text: 'lo!' },
    ]);
    expect(s.seq).toBe(3);
    expect(s.items).toHaveLength(2);
    expect(s.items[1].text).toBe('Hello!');

    // Duplicate op seq <= 3 must be ignored
    s = applyOps(s, [{ seq: 3, op: 'append', id: 'a1', text: 'DUP' }]);
    expect(s.items[1].text).toBe('Hello!');

    // Set existing and new item idempotently
    s = applyOps(s, [
      { seq: 4, op: 'set', id: 'a1', item: { id: 'a1', kind: 'agent', seq: 4, ts: 4, text: 'Updated!' } },
      { seq: 5, op: 'set', id: 'n1', item: { id: 'n1', kind: 'notice', seq: 5, ts: 5, text: 'Note' } },
    ]);
    expect(s.items).toHaveLength(3);
    expect(s.items[1].text).toBe('Updated!');
    expect(s.items[2].text).toBe('Note');

    // Remove item
    s = applyOps(s, [{ seq: 6, op: 'remove', id: 'u1' }]);
    expect(s.items).toHaveLength(2);
    expect(s.items[0].id).toBe('a1');

    // Snapshot replaces cleanly
    s = applySnapshot([{ id: 'x1', kind: 'notice', seq: 10, ts: 10, text: 'fresh' }], 10);
    expect(s.seq).toBe(10);
    expect(s.items[0].text).toBe('fresh');
  });

  it('resets seq watermark on reconnect reset (!op.seq or op.seq === 0) so subsequent ops apply', () => {
    let s = applySnapshot(
      [
        { id: 'u1', kind: 'user', seq: 8, ts: 8, text: 'before' },
        { id: 'a1', kind: 'agent', seq: 9, ts: 9, text: 'reply' },
      ],
      9,
    );
    expect(s.seq).toBe(9);

    // Full reconnect reset with omitted seq resets watermark to 0
    s = applyOps(s, [
      {
        op: 'reset',
        items: [{ id: 'u-re', kind: 'user', seq: 0, ts: 10, text: 'reconnected' }],
      } as unknown as Parameters<typeof applyOps>[1][number],
      {
        seq: 1,
        op: 'add',
        id: 'a-re',
        item: { id: 'a-re', kind: 'agent', seq: 1, ts: 11, text: 'post-reconnect' },
      },
      {
        seq: 2,
        op: 'append',
        id: 'a-re',
        text: ' delta',
      },
    ]);

    expect(s.seq).toBe(2);
    expect(s.items).toHaveLength(2);
    expect(s.items[0].text).toBe('reconnected');
    expect(s.items[1].text).toBe('post-reconnect delta');

    // Explicit op.seq === 0 reset also resets watermark cleanly
    s = applyOps(s, [{ seq: 0, op: 'reset', items: [] }]);
    expect(s.seq).toBe(0);
    expect(s.items).toHaveLength(0);
  });

  it('ignores stale duplicate resets (0 < op.seq <= current seq) while applying newer sequenced resets', () => {
    let s = applySnapshot(
      [
        { id: 'u1', kind: 'user', seq: 5, ts: 5, text: 'keep me' },
        { id: 'a1', kind: 'agent', seq: 6, ts: 6, text: 'keep me too' },
      ],
      6,
    );

    // Stale reset with seq 4 (< 6) and duplicate seq 6 (<= 6) must NOT wipe newer items
    s = applyOps(s, [
      { seq: 4, op: 'reset', items: [] },
      { seq: 6, op: 'reset', items: [] },
    ]);
    expect(s.seq).toBe(6);
    expect(s.items).toHaveLength(2);
    expect(s.items[0].text).toBe('keep me');

    // Newer sequenced reset (seq 7 > 6, e.g. rewind/history replay) applies cleanly
    s = applyOps(s, [
      {
        seq: 7,
        op: 'reset',
        items: [{ id: 'u-rewound', kind: 'user', seq: 7, ts: 7, text: 'after rewind' }],
      },
    ]);
    expect(s.seq).toBe(7);
    expect(s.items).toHaveLength(1);
    expect(s.items[0].text).toBe('after rewind');
  });
});
