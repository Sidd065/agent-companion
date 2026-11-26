import { chacha20poly1305 } from '@noble/ciphers/chacha';
import { x25519 } from '@noble/curves/ed25519';
import { hmac } from '@noble/hashes/hmac';
import { sha256 } from '@noble/hashes/sha2';
import { describe, expect, it } from 'vitest';
import type { DaemonInfo, HostConnectionState, PairedHostCredentials } from '@agentc/protocol';
import { CipherState, DH_LEN, FLAG_FINAL, generateKeyPair, MODE_CONNECT, prologue, TAG_LEN } from './noise.js';
import { bytesToBase64, fingerprint, keyId } from './pairing.js';
import { HostSupervisor } from './supervisor.js';

const encoder = new TextEncoder();
const decoder = new TextDecoder();

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

function hkdf2(ck: Uint8Array, ikm: Uint8Array): [Uint8Array, Uint8Array] {
  const temp = hmac(sha256, ck, ikm);
  const out1 = hmac(sha256, temp, new Uint8Array([1]));
  const out2 = hmac(sha256, temp, concatBytes(out1, new Uint8Array([2])));
  return [out1, out2];
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

interface MockDaemonConfig {
  hostName: string;
  hostKeyPair: { privateKey: Uint8Array; publicKey: Uint8Array };
  allowedDevicePub: Set<string>;
  publicUrl?: string;
  openRouterConfigured?: boolean;
  onRpc?: (method: string, params: unknown) => unknown;
}

function createMockWebSocketClass(daemonsByOrigin: Map<string, MockDaemonConfig>): typeof WebSocket {
  class MockSocket {
    public binaryType: BinaryType = 'arraybuffer';
    public readyState = 0;
    private listeners = new Map<string, Array<(ev: unknown) => void>>();
    private recv: CipherState | null = null;
    private sendCipher: CipherState | null = null;
    private daemon: MockDaemonConfig | undefined;

    constructor(public readonly url: string) {
      const parsed = new URL(url);
      const httpOrigin = `${parsed.protocol === 'wss:' ? 'https:' : 'http:'}//${parsed.host}`;
      this.daemon = daemonsByOrigin.get(httpOrigin);
      queueMicrotask(() => {
        if (this.readyState !== 0) return;
        if (!this.daemon) {
          this.readyState = 3;
          this.emit('close', { code: 1006, reason: 'connection refused' });
          return;
        }
        this.readyState = 1;
        this.emit('open', {});
      });
    }

    addEventListener(type: string, cb: (ev: unknown) => void): void {
      const arr = this.listeners.get(type) ?? [];
      arr.push(cb);
      this.listeners.set(type, arr);
    }

    private emit(type: string, ev: unknown): void {
      for (const cb of this.listeners.get(type) ?? []) {
        cb(ev);
      }
    }

    send(data: Uint8Array): void {
      if (this.readyState !== 1 || !this.daemon) return;
      const daemon = this.daemon;

      if (!this.recv || !this.sendCipher) {
        // Noise_IK_25519_ChaChaPoly_SHA256 responder handshake
        const protoName = encoder.encode('Noise_IK_25519_ChaChaPoly_SHA256');
        let h = new Uint8Array(32);
        h.set(protoName);
        let ck: Uint8Array = new Uint8Array(h);
        const mixHash = (d: Uint8Array) => {
          h = new Uint8Array(sha256(concatBytes(h, d)));
        };
        let k: Uint8Array | null = null;
        let n = 0n;
        const mixKey = (ikm: Uint8Array) => {
          const [nck, tk] = hkdf2(ck, ikm);
          ck = nck;
          k = tk;
          n = 0n;
        };
        const decryptAndHash = (ct: Uint8Array): Uint8Array => {
          const pt = k ? chacha20poly1305(k, noiseNonce(n++), h).decrypt(ct) : ct;
          mixHash(ct);
          return pt;
        };
        const encryptAndHash = (pt: Uint8Array): Uint8Array => {
          const ct = k ? chacha20poly1305(k, noiseNonce(n++), h).encrypt(pt) : pt;
          mixHash(ct);
          return ct;
        };

        try {
          mixHash(prologue(MODE_CONNECT, ''));
          mixHash(daemon.hostKeyPair.publicKey);

          const ie = data.slice(0, DH_LEN);
          mixHash(ie);
          mixKey(x25519.getSharedSecret(daemon.hostKeyPair.privateKey, ie));

          const encS = data.slice(DH_LEN, DH_LEN + DH_LEN + TAG_LEN);
          const isPub = decryptAndHash(encS);
          mixKey(x25519.getSharedSecret(daemon.hostKeyPair.privateKey, isPub));

          decryptAndHash(data.slice(DH_LEN + DH_LEN + TAG_LEN));

          const clientPubB64 = bytesToBase64(isPub);
          if (!daemon.allowedDevicePub.has(clientPubB64)) {
            queueMicrotask(() => {
              this.readyState = 3;
              this.emit('close', { code: 4401, reason: 'unpaired' });
            });
            return;
          }

          const re = generateKeyPair();
          mixHash(re.publicKey);
          mixKey(x25519.getSharedSecret(re.privateKey, ie));
          mixKey(x25519.getSharedSecret(re.privateKey, isPub));

          const replyPayload = encoder.encode(
            JSON.stringify({
              v: 1,
              protocolVersion: 2,
              host: daemon.hostName,
              hostId: keyId(daemon.hostKeyPair.publicKey),
              version: '0.4.1',
              deviceId: 'dev-1',
              deviceName: 'Desktop',
              ...(daemon.publicUrl ? { publicUrl: daemon.publicUrl } : {}),
            }),
          );
          const encReply = encryptAndHash(replyPayload);
          const msg2 = concatBytes(re.publicKey, encReply);

          const [k1, k2] = hkdf2(ck, new Uint8Array(0));
          // Initiator sends with k1, receives with k2 -> Responder receives k1, sends k2
          this.recv = new CipherState(k1);
          this.sendCipher = new CipherState(k2);

          queueMicrotask(() => {
            if (this.readyState === 1) {
              this.emit('message', { data: msg2 });
            }
          });
        } catch {
          queueMicrotask(() => {
            this.readyState = 3;
            this.emit('close', { code: 4002, reason: 'handshake failed' });
          });
        }
        return;
      }

      const pt = this.recv.decrypt(data);
      const reqJson = JSON.parse(decoder.decode(pt.subarray(1))) as {
        id: number;
        method: string;
        params?: unknown;
      };
      let result: unknown = {};
      if (reqJson.method === 'hello') {
        const info: DaemonInfo = {
          name: 'agentc',
          version: '0.4.1',
          protocolVersion: 2,
          openRouterConfigured: Boolean(daemon.openRouterConfigured),
          host: daemon.hostName,
          home: `/home/${daemon.hostName}`,
          uptimeSec: 42,
          agents: [],
          hostId: keyId(daemon.hostKeyPair.publicKey),
          fingerprint: fingerprint(daemon.hostKeyPair.publicKey),
          devices: 1,
        };
        result = info;
      } else if (daemon.onRpc) {
        result = daemon.onRpc(reqJson.method, reqJson.params);
      }

      const respBytes = encoder.encode(JSON.stringify({ id: reqJson.id, result }));
      const frame = new Uint8Array(respBytes.length + 1);
      frame[0] = FLAG_FINAL;
      frame.set(respBytes, 1);
      const ct = this.sendCipher.encrypt(frame);
      queueMicrotask(() => {
        if (this.readyState === 1) {
          this.emit('message', { data: ct });
        }
      });
    }

    close(code = 1000, reason = ''): void {
      if (this.readyState === 3) return;
      this.readyState = 3;
      this.emit('close', { code, reason });
    }
  }

  return MockSocket as unknown as typeof WebSocket;
}

async function waitForCondition(pred: () => boolean, timeoutMs = 2_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (pred()) return;
    await new Promise((r) => setTimeout(r, 10));
  }
  throw new Error('Timed out waiting for condition');
}

describe('HostSupervisor multi-host & credential lifecycle', () => {
  it('preserves user-customized host name when publicUrl updates during handshake', async () => {
    const hostKeys = generateKeyPair();
    const devKeys = generateKeyPair();
    const devPubB64 = bytesToBase64(devKeys.publicKey);

    const daemons = new Map<string, MockDaemonConfig>();
    daemons.set('http://127.0.0.1:19001', {
      hostName: 'raw-daemon-hostname',
      hostKeyPair: hostKeys,
      allowedDevicePub: new Set([devPubB64]),
      publicUrl: 'https://tunnel.example.test',
      openRouterConfigured: true,
    });

    const WS = createMockWebSocketClass(daemons);
    const updatedCredsList: PairedHostCredentials[] = [];
    const connectedInfos: DaemonInfo[] = [];

    const creds: PairedHostCredentials = {
      id: keyId(hostKeys.publicKey),
      name: 'My Custom Workstation',
      url: 'http://127.0.0.1:19001',
      localUrl: 'http://127.0.0.1:19001',
      hostKey: bytesToBase64(hostKeys.publicKey),
      devicePriv: bytesToBase64(devKeys.privateKey),
      devicePub: devPubB64,
      fingerprint: fingerprint(hostKeys.publicKey),
      pairedAt: Date.now(),
    };

    const sup = new HostSupervisor(
      creds,
      {
        onStateChange: () => {},
        onDaemonInfo: (_id, info) => {
          connectedInfos.push(info);
        },
        onEvent: () => {},
        onCredentialsUpdated: (next) => {
          updatedCredsList.push(next);
        },
      },
      { deviceName: 'Test Desktop', WebSocketImpl: WS, minBackoffMs: 20, maxBackoffMs: 100 },
    );

    sup.start();
    await waitForCondition(() => sup.connectionState === 'connected');
    expect(sup.creds.name).toBe('My Custom Workstation');
    expect(updatedCredsList[0]?.name).toBe('My Custom Workstation');
    expect(updatedCredsList[0]?.url).toBe('https://tunnel.example.test');
    expect(connectedInfos[0]?.openRouterConfigured).toBe(true);
    sup.dispose();
  });

  it('reconnects out of unpaired state when updateCredentials rotates device keys at the same URL', async () => {
    const hostKeys = generateKeyPair();
    const oldDevKeys = generateKeyPair();
    const newDevKeys = generateKeyPair();

    const allowed = new Set<string>([bytesToBase64(newDevKeys.publicKey)]);
    const daemons = new Map<string, MockDaemonConfig>();
    daemons.set('http://127.0.0.1:19002', {
      hostName: 'dev-box',
      hostKeyPair: hostKeys,
      allowedDevicePub: allowed,
    });

    const WS = createMockWebSocketClass(daemons);
    const states: HostConnectionState[] = [];

    const initialCreds: PairedHostCredentials = {
      id: keyId(hostKeys.publicKey),
      name: 'dev-box',
      url: 'http://127.0.0.1:19002',
      hostKey: bytesToBase64(hostKeys.publicKey),
      devicePriv: bytesToBase64(oldDevKeys.privateKey),
      devicePub: bytesToBase64(oldDevKeys.publicKey),
      fingerprint: fingerprint(hostKeys.publicKey),
      pairedAt: Date.now(),
    };

    const sup = new HostSupervisor(
      initialCreds,
      {
        onStateChange: (_id, st) => states.push(st),
        onDaemonInfo: () => {},
        onEvent: () => {},
      },
      { deviceName: 'Test Desktop', WebSocketImpl: WS, minBackoffMs: 20, maxBackoffMs: 100 },
    );

    sup.start();
    await waitForCondition(() => sup.connectionState === 'unpaired');

    // Rotate credentials at the SAME URL -> must immediately reconnect to 'connected'
    sup.updateCredentials({
      ...initialCreds,
      devicePriv: bytesToBase64(newDevKeys.privateKey),
      devicePub: bytesToBase64(newDevKeys.publicKey),
    });

    await waitForCondition(() => sup.connectionState === 'connected');
    expect(states).toContain('unpaired');
    expect(sup.connectionState).toBe('connected');
    sup.dispose();
  });

  it('isolates simultaneous Host A and Host B supervisors and RPC calls', async () => {
    const hostAKeys = generateKeyPair();
    const hostBKeys = generateKeyPair();
    const devAKeys = generateKeyPair();
    const devBKeys = generateKeyPair();

    const daemons = new Map<string, MockDaemonConfig>();
    daemons.set('http://127.0.0.1:19011', {
      hostName: 'host-a',
      hostKeyPair: hostAKeys,
      allowedDevicePub: new Set([bytesToBase64(devAKeys.publicKey)]),
      onRpc: (method) => ({ servedBy: 'host-a', method }),
    });
    daemons.set('http://127.0.0.1:19012', {
      hostName: 'host-b',
      hostKeyPair: hostBKeys,
      allowedDevicePub: new Set([bytesToBase64(devBKeys.publicKey)]),
      onRpc: (method) => ({ servedBy: 'host-b', method }),
    });

    const WS = createMockWebSocketClass(daemons);

    const supA = new HostSupervisor(
      {
        id: keyId(hostAKeys.publicKey),
        name: 'Host A',
        url: 'http://127.0.0.1:19011',
        hostKey: bytesToBase64(hostAKeys.publicKey),
        devicePriv: bytesToBase64(devAKeys.privateKey),
        devicePub: bytesToBase64(devAKeys.publicKey),
        fingerprint: fingerprint(hostAKeys.publicKey),
        pairedAt: Date.now(),
      },
      { onStateChange: () => {}, onDaemonInfo: () => {}, onEvent: () => {} },
      { deviceName: 'Desktop', WebSocketImpl: WS },
    );

    const supB = new HostSupervisor(
      {
        id: keyId(hostBKeys.publicKey),
        name: 'Host B',
        url: 'http://127.0.0.1:19012',
        hostKey: bytesToBase64(hostBKeys.publicKey),
        devicePriv: bytesToBase64(devBKeys.privateKey),
        devicePub: bytesToBase64(devBKeys.publicKey),
        fingerprint: fingerprint(hostBKeys.publicKey),
        pairedAt: Date.now(),
      },
      { onStateChange: () => {}, onDaemonInfo: () => {}, onEvent: () => {} },
      { deviceName: 'Desktop', WebSocketImpl: WS },
    );

    supA.start();
    supB.start();
    await waitForCondition(() => supA.connectionState === 'connected' && supB.connectionState === 'connected');

    const [resA, resB] = await Promise.all([
      supA.call<{ servedBy: string; method: string }>('fs.list'),
      supB.call<{ servedBy: string; method: string }>('fs.list'),
    ]);

    expect(resA).toEqual({ servedBy: 'host-a', method: 'fs.list' });
    expect(resB).toEqual({ servedBy: 'host-b', method: 'fs.list' });
    expect(supA.info?.home).toBe('/home/host-a');
    expect(supB.info?.home).toBe('/home/host-b');

    supA.dispose();
    supB.dispose();
  });
});
