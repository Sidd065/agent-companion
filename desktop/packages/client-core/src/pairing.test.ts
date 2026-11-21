import { describe, expect, it } from 'vitest';
import { buildWsUrl, bytesToBase64Url, parsePairLink } from './pairing.js';

describe('Pairing link parser', () => {
  const pub = bytesToBase64Url(new Uint8Array(32).fill(7));
  const psk = bytesToBase64Url(new Uint8Array(32).fill(9));

  it('parses https QR / agentc pair URLs', () => {
    const link = parsePairLink(`https://abc-xyz.trycloudflare.com/p/PAIR123#k=${pub}&s=${psk}&n=dev-box`);
    expect(link).not.toBeNull();
    expect(link!.base).toBe('https://abc-xyz.trycloudflare.com');
    expect(link!.pairId).toBe('PAIR123');
    expect(link!.name).toBe('dev-box');
    expect(link!.fingerprint).toMatch(/^[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}$/);
  });

  it('parses agentc:// deep links', () => {
    const link = parsePairLink(
      `agentc://pair?u=${encodeURIComponent('http://127.0.0.1:9473')}&p=P99&k=${pub}&s=${psk}&n=local`,
    );
    expect(link).not.toBeNull();
    expect(link!.base).toBe('http://127.0.0.1:9473');
    expect(link!.pairId).toBe('P99');
  });

  it('rejects malformed keys', () => {
    expect(parsePairLink('https://example.com/p/id#k=short&s=short')).toBeNull();
    expect(parsePairLink('not-a-url')).toBeNull();
  });

  it('builds ws/wss endpoints', () => {
    expect(buildWsUrl('https://a.trycloudflare.com/', 'pair', 'XYZ')).toBe('wss://a.trycloudflare.com/v1/ws?m=pair&p=XYZ');
    expect(buildWsUrl('http://127.0.0.1:9473', 'conn')).toBe('ws://127.0.0.1:9473/v1/ws?m=conn');
  });
});
