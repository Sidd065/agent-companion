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

    // Remove item
    s = applyOps(s, [{ seq: 4, op: 'remove', id: 'u1' }]);
    expect(s.items).toHaveLength(1);
    expect(s.items[0].id).toBe('a1');

    // Snapshot replaces cleanly
    s = applySnapshot([{ id: 'x1', kind: 'notice', seq: 10, ts: 10, text: 'fresh' }], 10);
    expect(s.seq).toBe(10);
    expect(s.items[0].text).toBe('fresh');
  });
});
