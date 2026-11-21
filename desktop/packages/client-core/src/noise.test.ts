import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { keyPairFromPrivate, NoiseInitiator } from './noise.js';

interface Vector {
  name: string;
  prologue: string;
  psk?: string;
  initStaticPriv: string;
  initEphemeralPriv: string;
  respStaticPub: string;
  msg1Payload: string;
  msg1: string;
  msg2Payload: string;
  msg2: string;
  handshakeHash: string;
  transport: Array<{ dir: 'i2r' | 'r2i'; plain: string; cipher: string }>;
}

function fromHex(h: string): Uint8Array {
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(h.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function toHex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

describe('Noise_IK and Noise_IKpsk1 Go/Kotlin interop vectors', () => {
  const vecPath = path.resolve(__dirname, '../../protocol/test-vectors/noise-vectors.json');
  const vectors = JSON.parse(fs.readFileSync(vecPath, 'utf8')) as Vector[];

  for (const v of vectors) {
    it(`matches byte-for-byte: ${v.name}`, () => {
      const s = keyPairFromPrivate(fromHex(v.initStaticPriv));
      const e = keyPairFromPrivate(fromHex(v.initEphemeralPriv));
      const rs = fromHex(v.respStaticPub);
      const pro = fromHex(v.prologue);
      const psk = v.psk ? fromHex(v.psk) : null;

      const init = new NoiseInitiator(s, rs, pro, psk, e);
      const msg1 = init.writeMessage1(fromHex(v.msg1Payload));
      expect(toHex(msg1)).toBe(v.msg1);

      const sess = init.readMessage2(fromHex(v.msg2));
      expect(toHex(sess.responderPayload)).toBe(v.msg2Payload);
      expect(toHex(sess.handshakeHash)).toBe(v.handshakeHash);

      for (const tr of v.transport) {
        if (tr.dir === 'i2r') {
          expect(toHex(sess.send.encrypt(fromHex(tr.plain)))).toBe(tr.cipher);
        } else {
          expect(toHex(sess.recv.decrypt(fromHex(tr.cipher)))).toBe(tr.plain);
        }
      }
    });
  }
});
