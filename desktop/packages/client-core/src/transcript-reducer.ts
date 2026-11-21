import type { TranscriptItem, TranscriptOp } from '@agentc/protocol';

export interface TranscriptState {
  seq: number;
  items: TranscriptItem[];
  byId: Record<string, number>;
}

export function emptyTranscript(): TranscriptState {
  return { seq: 0, items: [], byId: {} };
}

function rebuildIndex(items: TranscriptItem[]): Record<string, number> {
  const byId: Record<string, number> = {};
  for (let i = 0; i < items.length; i++) {
    byId[items[i].id] = i;
  }
  return byId;
}

export function applySnapshot(items: TranscriptItem[], seq: number): TranscriptState {
  const copy = items.map((it) => ({ ...it }));
  return { seq, items: copy, byId: rebuildIndex(copy) };
}

export function applyOps(state: TranscriptState, ops: TranscriptOp[], explicitSeq?: number): TranscriptState {
  if (!ops || ops.length === 0) {
    return explicitSeq && explicitSeq > state.seq ? { ...state, seq: explicitSeq } : state;
  }
  let items = state.items.slice();
  let byId = { ...state.byId };
  let seq = state.seq;

  for (const op of ops) {
    if (op.seq && op.seq <= seq && op.op !== 'reset') {
      continue;
    }
    if (op.seq && op.seq > seq) {
      seq = op.seq;
    }
    switch (op.op) {
      case 'reset': {
        items = (op.items ?? []).map((it) => ({ ...it }));
        byId = rebuildIndex(items);
        break;
      }
      case 'add': {
        if (!op.item) break;
        const existingIdx = byId[op.item.id];
        if (existingIdx !== undefined) {
          items[existingIdx] = { ...op.item };
        } else {
          byId[op.item.id] = items.length;
          items.push({ ...op.item });
        }
        break;
      }
      case 'set': {
        if (!op.item) break;
        const idx = byId[op.item.id];
        if (idx !== undefined) {
          items[idx] = { ...op.item };
        } else {
          byId[op.item.id] = items.length;
          items.push({ ...op.item });
        }
        break;
      }
      case 'append': {
        const id = op.id;
        if (!id || !op.text) break;
        const idx = byId[id];
        if (idx !== undefined) {
          const cur = items[idx];
          items[idx] = { ...cur, text: (cur.text ?? '') + op.text, seq: op.seq || cur.seq };
        }
        break;
      }
      case 'remove': {
        const id = op.id;
        if (!id) break;
        const idx = byId[id];
        if (idx !== undefined) {
          items.splice(idx, 1);
          byId = rebuildIndex(items);
        }
        break;
      }
    }
  }

  if (explicitSeq && explicitSeq > seq) {
    seq = explicitSeq;
  }
  return { seq, items, byId };
}
