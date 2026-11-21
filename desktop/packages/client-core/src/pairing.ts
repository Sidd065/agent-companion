import { sha256 } from '@noble/hashes/sha2';
import { DH_LEN } from './noise.js';

export interface PairLink {
  base: string;
  pairId: string;
  hostKey: string;
  secret: string;
  name: string;
  hostId: string;
  fingerprint: string;
}

export function bytesToBase64(bytes: Uint8Array): string {
  if (typeof Buffer !== 'undefined') {
    return Buffer.from(bytes).toString('base64');
  }
  let s = '';
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s);
}

export function base64ToBytes(b64: string): Uint8Array {
  const norm = b64.replace(/-/g, '+').replace(/_/g, '/');
  const padded = norm + '='.repeat((4 - (norm.length % 4)) % 4);
  if (typeof Buffer !== 'undefined') {
    return new Uint8Array(Buffer.from(padded, 'base64'));
  }
  const bin = atob(padded);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function bytesToBase64Url(bytes: Uint8Array): string {
  return bytesToBase64(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function keyId(pub: Uint8Array): string {
  return bytesToBase64Url(sha256(pub).slice(0, 12));
}

export function fingerprint(pub: Uint8Array): string {
  const digest = sha256(pub).slice(0, 8);
  let hex = '';
  for (const b of digest) hex += b.toString(16).padStart(2, '0');
  return `${hex.slice(0, 4)}-${hex.slice(4, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}`;
}

export function parsePairLink(raw: string): PairLink | null {
  try {
    const trimmed = raw.trim();
    if (!trimmed) return null;
    const u = new URL(trimmed);
    let base = '';
    let pairId = '';
    let params: URLSearchParams;

    if (u.protocol === 'agentc:') {
      params = u.searchParams;
      base = params.get('u') ?? '';
      pairId = params.get('p') ?? '';
      if (!base || !pairId) return null;
    } else if (u.protocol === 'http:' || u.protocol === 'https:') {
      const segs = u.pathname.split('/').filter(Boolean);
      if (segs.length !== 2 || segs[0] !== 'p') return null;
      base = u.origin;
      pairId = segs[1];
      const frag = u.hash.replace(/^#/, '');
      if (!frag) return null;
      params = new URLSearchParams(frag);
    } else {
      return null;
    }

    const k = params.get('k') ?? '';
    const s = params.get('s') ?? '';
    if (!k || !s) return null;
    const keyBytes = base64ToBytes(k);
    const pskBytes = base64ToBytes(s);
    if (keyBytes.length !== DH_LEN || pskBytes.length !== 32) return null;

    return {
      base: base.replace(/\/+$/, ''),
      pairId,
      hostKey: k,
      secret: s,
      name: params.get('n') ?? '',
      hostId: keyId(keyBytes),
      fingerprint: fingerprint(keyBytes),
    };
  } catch {
    return null;
  }
}

export function buildWsUrl(base: string, mode: 'conn' | 'pair', pairId = ''): string {
  const u = new URL(base.replace(/\/+$/, ''));
  if (u.protocol === 'https:' || u.protocol === 'wss:') {
    u.protocol = 'wss:';
  } else if (u.protocol === 'http:' || u.protocol === 'ws:') {
    u.protocol = 'ws:';
  } else {
    throw new Error(`Unsupported URL protocol: ${u.protocol}`);
  }
  u.pathname = u.pathname.replace(/\/+$/, '') + '/v1/ws';
  u.searchParams.set('m', mode);
  if (mode === 'pair') {
    u.searchParams.set('p', pairId);
  }
  return u.toString();
}
