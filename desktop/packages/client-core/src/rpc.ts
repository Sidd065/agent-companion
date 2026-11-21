import type { HelloReply, PairedHostCredentials, RpcResponseEnvelope } from '@agentc/protocol';
import { generateKeyPair, keyPairFromPrivate, MODE_PAIR } from './noise.js';
import { base64ToBytes, bytesToBase64, parsePairLink, type PairLink } from './pairing.js';
import { SecureSocket } from './socket.js';

export class RpcError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RpcError';
  }
}

export type EventHandler = (event: string, data: unknown) => void;

export interface RpcClientOptions {
  baseUrl: string;
  daemonKey: Uint8Array;
  devicePriv: Uint8Array;
  deviceName: string;
  platform?: string;
  appVersion?: string;
  WebSocketImpl?: typeof WebSocket;
  onEvent?: EventHandler;
  onClose?: (reason: string, code: number) => void;
}

export class RpcClient {
  private socket: SecureSocket | null = null;
  private nextId = 1;
  private pending = new Map<
    number,
    { resolve: (v: unknown) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout> }
  >();
  public reply: HelloReply | null = null;

  constructor(private readonly opts: RpcClientOptions) {}

  connect(): Promise<HelloReply> {
    return new Promise((resolve, reject) => {
      let settled = false;
      const keys = keyPairFromPrivate(this.opts.devicePriv);
      this.socket = new SecureSocket(
        {
          baseUrl: this.opts.baseUrl,
          daemonKey: this.opts.daemonKey,
          deviceKeys: keys,
          hello: {
            v: 1,
            purpose: 'rpc',
            name: this.opts.deviceName,
            platform: this.opts.platform ?? 'linux-desktop',
            app: this.opts.appVersion ?? '0.4.0',
          },
          WebSocketImpl: this.opts.WebSocketImpl,
        },
        {
          onOpen: (reply) => {
            this.reply = reply;
            if (!settled) {
              settled = true;
              resolve(reply);
            }
          },
          onText: (text) => this.handleMessage(text),
          onClose: (reason, code) => {
            for (const p of this.pending.values()) {
              clearTimeout(p.timer);
              p.reject(new RpcError(reason || 'connection closed'));
            }
            this.pending.clear();
            if (!settled) {
              settled = true;
              reject(new RpcError(reason || `closed (${code})`));
            }
            this.opts.onClose?.(reason, code);
          },
        },
      );
      this.socket.connect();
    });
  }

  private handleMessage(raw: string): void {
    let msg: RpcResponseEnvelope;
    try {
      msg = JSON.parse(raw) as RpcResponseEnvelope;
    } catch {
      return;
    }
    if (msg.event !== undefined) {
      this.opts.onEvent?.(msg.event, msg.data);
      return;
    }
    if (typeof msg.id === 'number') {
      const p = this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      clearTimeout(p.timer);
      if (msg.error) {
        p.reject(new RpcError(msg.error.message || 'RPC error'));
      } else {
        p.resolve(msg.result);
      }
    }
  }

  call<T = unknown>(method: string, params?: unknown, timeoutMs = 45_000): Promise<T> {
    if (!this.socket?.isOpen) {
      return Promise.reject(new RpcError('not connected'));
    }
    const id = this.nextId++;
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new RpcError(`RPC ${method} timed out`));
      }, timeoutMs);
      this.pending.set(id, {
        resolve: (v) => resolve(v as T),
        reject,
        timer,
      });
      try {
        this.socket!.sendText(JSON.stringify({ id, method, params: params ?? {} }));
      } catch (err) {
        this.pending.delete(id);
        clearTimeout(timer);
        reject(err as Error);
      }
    });
  }

  get isOpen(): boolean {
    return Boolean(this.socket?.isOpen);
  }

  close(reason = 'client closed'): void {
    this.socket?.close(reason);
    this.socket = null;
  }
}

export async function pairWithLink(
  rawLink: string,
  opts: {
    deviceName: string;
    platform?: string;
    appVersion?: string;
    isLocal?: boolean;
    WebSocketImpl?: typeof WebSocket;
  },
): Promise<{ credentials: PairedHostCredentials; reply: HelloReply; link: PairLink }> {
  const link = parsePairLink(rawLink);
  if (!link) {
    throw new RpcError('Invalid pairing link. Paste the full link printed by `agentc pair`.');
  }
  const devKeys = generateKeyPair();
  const daemonKey = base64ToBytes(link.hostKey);
  const psk = base64ToBytes(link.secret);

  const reply = await new Promise<HelloReply>((resolve, reject) => {
    let done = false;
    const sock = new SecureSocket(
      {
        baseUrl: link.base,
        daemonKey,
        deviceKeys: devKeys,
        mode: MODE_PAIR,
        pairId: link.pairId,
        psk,
        hello: {
          v: 1,
          purpose: 'rpc',
          name: opts.deviceName,
          platform: opts.platform ?? 'linux-desktop',
          app: opts.appVersion ?? '0.4.0',
        },
        handshakeTimeoutMs: 30_000,
        WebSocketImpl: opts.WebSocketImpl,
      },
      {
        onOpen: (rep) => {
          if (!done) {
            done = true;
            sock.close('paired');
            resolve(rep);
          }
        },
        onText: () => {},
        onClose: (reason) => {
          if (!done) {
            done = true;
            if (reason === 'pairing-invalid') {
              reject(new RpcError('Pairing code is invalid, already used, or expired. Run `agentc pair` again.'));
            } else {
              reject(new RpcError(reason || 'Pairing failed'));
            }
          }
        },
      },
    );
    sock.connect();
  });

  const credentials: PairedHostCredentials = {
    id: reply.hostId || link.hostId,
    name: reply.host || link.name || 'agentc host',
    url: reply.publicUrl || link.base,
    localUrl: opts.isLocal ? link.base : undefined,
    hostKey: link.hostKey,
    devicePriv: bytesToBase64(devKeys.privateKey),
    devicePub: bytesToBase64(devKeys.publicKey),
    deviceId: reply.deviceId,
    fingerprint: link.fingerprint,
    pairedAt: Date.now(),
    isLocal: opts.isLocal,
  };

  return { credentials, reply, link };
}
