import type { PairedHostCredentials } from '@agentc/protocol';
import { keyPairFromPrivate } from './noise.js';
import { base64ToBytes } from './pairing.js';
import { SecureSocket } from './socket.js';

export interface TerminalStreamListener {
  onData(data: Uint8Array): void;
  onExit(message?: string): void;
  onError(message: string): void;
  onOpen?(): void;
}

const encoder = new TextEncoder();

/**
 * Opens an encrypted PTY stream (`purpose: "term"`) to an agentc tmux session,
 * automatically falling back from `localUrl` to `url` if LAN direct connect fails.
 */
export class TerminalStream {
  private socket: SecureSocket | null = null;
  private closed = false;
  private opened = false;
  private triedFallback = false;

  constructor(
    private readonly creds: PairedHostCredentials,
    private readonly termId: string,
    private readonly cols: number,
    private readonly rows: number,
    private readonly listener: TerminalStreamListener,
    private readonly deviceName = 'Agent Companion Desktop',
  ) {}

  connect(): void {
    const firstUrl = this.creds.localUrl || this.creds.url;
    this.openSocket(firstUrl);
  }

  private openSocket(baseUrl: string): void {
    const keys = keyPairFromPrivate(base64ToBytes(this.creds.devicePriv));
    const daemonKey = base64ToBytes(this.creds.hostKey);

    const sock = new SecureSocket(
      {
        baseUrl,
        daemonKey,
        deviceKeys: keys,
        hello: {
          v: 1,
          purpose: 'term',
          term: this.termId,
          cols: this.cols,
          rows: this.rows,
          name: this.deviceName,
          platform: 'linux-desktop',
          app: '0.6.1',
        },
      },
      {
        onOpen: () => {
          if (this.socket !== sock || this.closed) return;
          this.opened = true;
          try {
            this.resize(this.cols, this.rows);
          } catch {
            // ignore
          }
          this.listener.onOpen?.();
        },
        onBinary: (data) => {
          if (this.socket !== sock || this.closed) return;
          this.listener.onData(data);
        },
        onText: (text) => {
          if (this.socket !== sock || this.closed) return;
          try {
            const msg = JSON.parse(text) as { type?: string; message?: string };
            if (msg.type === 'exit') {
              this.listener.onExit();
            } else if (msg.type === 'error') {
              this.listener.onError(msg.message || 'terminal error');
            }
          } catch {
            this.listener.onData(encoder.encode(text));
          }
        },
        onClose: (reason) => {
          if (this.socket !== sock || this.closed) return;
          if (
            !this.opened &&
            !this.triedFallback &&
            this.creds.localUrl &&
            this.creds.url &&
            this.creds.localUrl !== this.creds.url
          ) {
            this.triedFallback = true;
            this.openSocket(this.creds.url);
            return;
          }
          this.closed = true;
          this.listener.onExit(reason);
        },
      },
    );
    this.socket = sock;
    sock.connect();
  }

  write(data: string | Uint8Array): void {
    if (this.closed || !this.socket?.isOpen) return;
    if (typeof data === 'string') {
      this.socket.sendBinary(encoder.encode(data));
    } else {
      this.socket.sendBinary(data);
    }
  }

  resize(cols: number, rows: number): void {
    if (this.closed || !this.socket?.isOpen) return;
    this.socket.sendText(JSON.stringify({ type: 'resize', cols: Math.max(2, cols), rows: Math.max(2, rows) }));
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.socket?.close('terminal detached');
    this.socket = null;
  }
}
