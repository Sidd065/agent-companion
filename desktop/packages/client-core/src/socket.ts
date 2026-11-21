import type { HelloReply, WireHello } from '@agentc/protocol';
import {
  FLAG_BINARY,
  FLAG_FINAL,
  MAX_FRAGMENT,
  MAX_LOGICAL_MESSAGE,
  MODE_CONNECT,
  NoiseInitiator,
  type NoiseKeyPair,
  type NoiseSession,
  prologue,
} from './noise.js';
import { buildWsUrl } from './pairing.js';

export interface SecureSocketOptions {
  baseUrl: string;
  daemonKey: Uint8Array;
  deviceKeys: NoiseKeyPair;
  hello: WireHello;
  mode?: 'conn' | 'pair';
  pairId?: string;
  psk?: Uint8Array | null;
  handshakeTimeoutMs?: number;
  WebSocketImpl?: typeof WebSocket;
}

export interface SecureSocketListener {
  onOpen(reply: HelloReply): void;
  onText(text: string): void;
  onBinary?(data: Uint8Array): void;
  onClose(reason: string, code: number): void;
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export class SecureSocket {
  private ws: WebSocket | null = null;
  private initiator: NoiseInitiator | null = null;
  private session: NoiseSession | null = null;
  private fragments: Uint8Array[] = [];
  private fragTotal = 0;
  private closed = false;
  private handshakeTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    private readonly opts: SecureSocketOptions,
    private readonly listener: SecureSocketListener,
  ) {}

  connect(): void {
    const mode = this.opts.mode ?? MODE_CONNECT;
    const pairId = this.opts.pairId ?? '';
    const url = buildWsUrl(this.opts.baseUrl, mode, pairId);
    const WS = this.opts.WebSocketImpl ?? globalThis.WebSocket;
    if (!WS) {
      throw new Error('WebSocket implementation not available in this runtime');
    }

    const ws = new WS(url);
    ws.binaryType = 'arraybuffer';
    this.ws = ws;

    this.handshakeTimer = setTimeout(() => {
      if (!this.session && !this.closed) {
        this.fail('handshake timeout', 4000);
      }
    }, this.opts.handshakeTimeoutMs ?? 20_000);

    ws.addEventListener('open', () => {
      if (this.closed) return;
      try {
        const init = new NoiseInitiator(
          this.opts.deviceKeys,
          this.opts.daemonKey,
          prologue(mode, pairId),
          mode === 'pair' ? (this.opts.psk ?? null) : null,
        );
        this.initiator = init;
        const helloPayload = encoder.encode(JSON.stringify({ ...this.opts.hello, v: this.opts.hello.v || 1 }));
        const msg1 = init.writeMessage1(helloPayload);
        ws.send(msg1);
      } catch (err) {
        this.fail(`handshake init failed: ${(err as Error).message}`, 4001);
      }
    });

    ws.addEventListener('message', (ev: MessageEvent) => {
      if (this.closed) return;
      this.handleRawFrame(ev.data).catch((err) => {
        this.fail((err as Error).message || 'decrypt error', 4002);
      });
    });

    ws.addEventListener('error', () => {
      // close event always follows
    });

    ws.addEventListener('close', (ev: CloseEvent) => {
      this.cleanup(ev.reason || (this.session ? 'connection closed' : 'connection failed'), ev.code || 1006);
    });
  }

  private async handleRawFrame(raw: unknown): Promise<void> {
    let bytes: Uint8Array;
    if (raw instanceof ArrayBuffer) {
      bytes = new Uint8Array(raw);
    } else if (ArrayBuffer.isView(raw)) {
      bytes = new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength);
    } else if (typeof Blob !== 'undefined' && raw instanceof Blob) {
      bytes = new Uint8Array(await raw.arrayBuffer());
    } else {
      throw new Error('secure: unexpected plaintext frame');
    }

    if (!this.session) {
      if (!this.initiator) throw new Error('secure: missing initiator');
      const sess = this.initiator.readMessage2(bytes);
      this.session = sess;
      this.initiator = null;
      if (this.handshakeTimer) {
        clearTimeout(this.handshakeTimer);
        this.handshakeTimer = null;
      }
      let reply: HelloReply = { v: 1, host: '', hostId: '', version: '', deviceId: '', deviceName: '' };
      if (sess.responderPayload.length > 0) {
        reply = JSON.parse(decoder.decode(sess.responderPayload)) as HelloReply;
      }
      this.listener.onOpen(reply);
      return;
    }

    const pt = this.session.recv.decrypt(bytes);
    if (pt.length === 0) throw new Error('secure: empty transport frame');
    const flags = pt[0];
    const chunk = pt.subarray(1);
    this.fragments.push(chunk);
    this.fragTotal += chunk.length;
    if (this.fragTotal > MAX_LOGICAL_MESSAGE) {
      throw new Error('secure: message exceeds 64 MiB limit');
    }
    if ((flags & FLAG_FINAL) !== 0) {
      const full = new Uint8Array(this.fragTotal);
      let off = 0;
      for (const f of this.fragments) {
        full.set(f, off);
        off += f.length;
      }
      this.fragments = [];
      this.fragTotal = 0;
      if ((flags & FLAG_BINARY) !== 0) {
        this.listener.onBinary?.(full);
      } else {
        this.listener.onText(decoder.decode(full));
      }
    }
  }

  sendText(text: string): void {
    this.sendMessage(encoder.encode(text), false);
  }

  sendBinary(data: Uint8Array): void {
    this.sendMessage(data, true);
  }

  private sendMessage(data: Uint8Array, binary: boolean): void {
    if (this.closed || !this.session || !this.ws || this.ws.readyState !== 1) {
      throw new Error('secure: socket not open');
    }
    let offset = 0;
    let first = true;
    while (first || offset < data.length) {
      first = false;
      const n = Math.min(data.length - offset, MAX_FRAGMENT);
      const isFinal = offset + n >= data.length;
      let flags = 0;
      if (isFinal) flags |= FLAG_FINAL;
      if (binary) flags |= FLAG_BINARY;
      const pt = new Uint8Array(n + 1);
      pt[0] = flags;
      pt.set(data.subarray(offset, offset + n), 1);
      const ct = this.session.send.encrypt(pt);
      this.ws.send(ct);
      offset += n;
    }
  }

  get isOpen(): boolean {
    return !this.closed && this.session !== null && this.ws?.readyState === 1;
  }

  close(reason = 'client closed'): void {
    if (this.closed) return;
    const ws = this.ws;
    this.cleanup(reason, 1000);
    try {
      ws?.close(1000, reason);
    } catch {
      // ignore
    }
  }

  private fail(reason: string, code: number): void {
    const ws = this.ws;
    this.cleanup(reason, code);
    try {
      ws?.close(1008, reason.slice(0, 120));
    } catch {
      // ignore
    }
  }

  private cleanup(reason: string, code: number): void {
    if (this.closed) return;
    this.closed = true;
    if (this.handshakeTimer) {
      clearTimeout(this.handshakeTimer);
      this.handshakeTimer = null;
    }
    this.ws = null;
    this.session = null;
    this.initiator = null;
    this.fragments = [];
    this.fragTotal = 0;
    this.listener.onClose(reason, code);
  }
}
