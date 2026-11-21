/**
 * Pure TypeScript implementation of Noise_IK_25519_ChaChaPoly_SHA256 and
 * Noise_IKpsk1_25519_ChaChaPoly_SHA256 (matching daemon/internal/secure and
 * android/app/.../net/Noise.kt byte-for-byte).
 *
 * Browser, Electron renderer, and Node compatible (zero native dependencies).
 */
import { chacha20poly1305 } from '@noble/ciphers/chacha';
import { x25519 } from '@noble/curves/ed25519';
import { hmac } from '@noble/hashes/hmac';
import { sha256 } from '@noble/hashes/sha2';

export const DH_LEN = 32;
export const TAG_LEN = 16;
export const MAX_NOISE_MESSAGE = 65535;
export const MAX_FRAGMENT = MAX_NOISE_MESSAGE - TAG_LEN - 1;
export const MAX_LOGICAL_MESSAGE = 64 * 1024 * 1024;
export const FLAG_FINAL = 1;
export const FLAG_BINARY = 2;

export const PROLOGUE_PREFIX = 'agentc/1 ';
export const MODE_CONNECT = 'conn';
export const MODE_PAIR = 'pair';

export interface NoiseKeyPair {
  privateKey: Uint8Array;
  publicKey: Uint8Array;
}

const EMPTY = new Uint8Array(0);
const encoder = new TextEncoder();

export function prologue(mode: 'conn' | 'pair', pairId = ''): Uint8Array {
  if (mode === MODE_PAIR) {
    return encoder.encode(`${PROLOGUE_PREFIX}${MODE_PAIR}\n${pairId}`);
  }
  return encoder.encode(`${PROLOGUE_PREFIX}${MODE_CONNECT}\n`);
}

export function generateKeyPair(): NoiseKeyPair {
  const privateKey = x25519.utils.randomPrivateKey();
  const publicKey = x25519.getPublicKey(privateKey);
  return { privateKey, publicKey };
}

export function keyPairFromPrivate(privateKey: Uint8Array): NoiseKeyPair {
  if (privateKey.length !== DH_LEN) {
    throw new Error(`noise: expected ${DH_LEN}-byte private key, got ${privateKey.length}`);
  }
  return { privateKey: new Uint8Array(privateKey), publicKey: x25519.getPublicKey(privateKey) };
}

function concatBytes(...chunks: Uint8Array[]): Uint8Array {
  let total = 0;
  for (const c of chunks) total += c.length;
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}

function hashConcat(...chunks: Uint8Array[]): Uint8Array {
  return sha256(concatBytes(...chunks));
}

function hkdf(chainingKey: Uint8Array, ikm: Uint8Array, outputs: 2 | 3): Uint8Array[] {
  const tempKey = hmac(sha256, chainingKey, ikm);
  const out1 = hmac(sha256, tempKey, new Uint8Array([1]));
  const out2 = hmac(sha256, tempKey, concatBytes(out1, new Uint8Array([2])));
  if (outputs === 2) return [out1, out2];
  const out3 = hmac(sha256, tempKey, concatBytes(out2, new Uint8Array([3])));
  return [out1, out2, out3];
}

function noiseNonce(n: bigint): Uint8Array {
  const nonce = new Uint8Array(12);
  let v = n;
  for (let i = 4; i < 12; i++) {
    nonce[i] = Number(v & 0xffn);
    v >>= 8n;
  }
  return nonce;
}

function aeadEncrypt(key: Uint8Array, n: bigint, ad: Uint8Array, plaintext: Uint8Array): Uint8Array {
  return chacha20poly1305(key, noiseNonce(n), ad).encrypt(plaintext);
}

function aeadDecrypt(key: Uint8Array, n: bigint, ad: Uint8Array, ciphertext: Uint8Array): Uint8Array {
  return chacha20poly1305(key, noiseNonce(n), ad).decrypt(ciphertext);
}

const MAX_NONCE = 0xffffffffffffffffn;

export class CipherState {
  private n = 0n;
  constructor(private readonly key: Uint8Array | null) {}

  encrypt(plaintext: Uint8Array, ad: Uint8Array = EMPTY): Uint8Array {
    if (!this.key) return plaintext;
    if (this.n === MAX_NONCE) throw new Error('noise: nonce exhausted');
    return aeadEncrypt(this.key, this.n++, ad, plaintext);
  }

  decrypt(ciphertext: Uint8Array, ad: Uint8Array = EMPTY): Uint8Array {
    if (!this.key) return ciphertext;
    if (this.n === MAX_NONCE) throw new Error('noise: nonce exhausted');
    const pt = aeadDecrypt(this.key, this.n, ad, ciphertext);
    this.n++;
    return pt;
  }
}

export interface NoiseSession {
  send: CipherState;
  recv: CipherState;
  responderPayload: Uint8Array;
  handshakeHash: Uint8Array;
}

export class NoiseInitiator {
  private h: Uint8Array;
  private ck: Uint8Array;
  private k: Uint8Array | null = null;
  private n = 0n;
  private e: NoiseKeyPair | null = null;

  constructor(
    private readonly s: NoiseKeyPair,
    private readonly rs: Uint8Array,
    prologueBytes: Uint8Array,
    private readonly psk: Uint8Array | null = null,
    private readonly fixedEphemeral: NoiseKeyPair | null = null,
  ) {
    if (rs.length !== DH_LEN) {
      throw new Error('noise: invalid responder static key length');
    }
    if (psk && psk.length !== 32) {
      throw new Error('noise: invalid PSK length');
    }
    const protoName = encoder.encode(`Noise_IK${psk ? 'psk1' : ''}_25519_ChaChaPoly_SHA256`);
    if (protoName.length <= 32) {
      this.h = new Uint8Array(32);
      this.h.set(protoName);
    } else {
      this.h = sha256(protoName);
    }
    this.ck = new Uint8Array(this.h);
    this.mixHash(prologueBytes);
    this.mixHash(rs);
  }

  private mixHash(data: Uint8Array): void {
    this.h = hashConcat(this.h, data);
  }

  private mixKey(ikm: Uint8Array): void {
    const [newCk, tempK] = hkdf(this.ck, ikm, 2);
    this.ck = newCk;
    this.k = tempK;
    this.n = 0n;
  }

  private mixKeyAndHash(ikm: Uint8Array): void {
    const [newCk, tempH, tempK] = hkdf(this.ck, ikm, 3);
    this.ck = newCk;
    this.mixHash(tempH);
    this.k = tempK;
    this.n = 0n;
  }

  private encryptAndHash(pt: Uint8Array): Uint8Array {
    const ct = this.k ? aeadEncrypt(this.k, this.n++, this.h, pt) : pt;
    this.mixHash(ct);
    return ct;
  }

  private decryptAndHash(ct: Uint8Array): Uint8Array {
    const pt = this.k ? aeadDecrypt(this.k, this.n++, this.h, ct) : ct;
    this.mixHash(ct);
    return pt;
  }

  writeMessage1(payload: Uint8Array): Uint8Array {
    const eph = this.fixedEphemeral ?? generateKeyPair();
    this.e = eph;
    this.mixHash(eph.publicKey);
    if (this.psk) this.mixKey(eph.publicKey);
    this.mixKey(x25519.getSharedSecret(eph.privateKey, this.rs));
    const encS = this.encryptAndHash(this.s.publicKey);
    this.mixKey(x25519.getSharedSecret(this.s.privateKey, this.rs));
    if (this.psk) this.mixKeyAndHash(this.psk);
    const encPayload = this.encryptAndHash(payload);
    const msg = concatBytes(eph.publicKey, encS, encPayload);
    if (msg.length > MAX_NOISE_MESSAGE) {
      throw new Error('noise: message 1 exceeds maximum size');
    }
    return msg;
  }

  readMessage2(msg: Uint8Array): NoiseSession {
    if (!this.e) throw new Error('noise: message 1 not written');
    if (msg.length < DH_LEN + TAG_LEN) {
      throw new Error('noise: short handshake message 2');
    }
    const re = msg.slice(0, DH_LEN);
    this.mixHash(re);
    if (this.psk) this.mixKey(re);
    this.mixKey(x25519.getSharedSecret(this.e.privateKey, re));
    this.mixKey(x25519.getSharedSecret(this.s.privateKey, re));
    const payload = this.decryptAndHash(msg.slice(DH_LEN));
    const [k1, k2] = hkdf(this.ck, EMPTY, 2);
    return {
      send: new CipherState(k1),
      recv: new CipherState(k2),
      responderPayload: payload,
      handshakeHash: new Uint8Array(this.h),
    };
  }
}
