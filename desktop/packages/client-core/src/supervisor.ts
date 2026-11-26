import type { DaemonInfo, HostConnectionState, PairedHostCredentials } from '@agentc/protocol';
import { base64ToBytes } from './pairing.js';
import { RpcClient, RpcError } from './rpc.js';

export interface HostSupervisorEvents {
  onStateChange(hostId: string, state: HostConnectionState, error?: string): void;
  onDaemonInfo(hostId: string, info: DaemonInfo): void;
  onEvent(hostId: string, event: string, data: unknown): void;
  onCredentialsUpdated?(creds: PairedHostCredentials): void;
}

export interface HostSupervisorOptions {
  deviceName: string;
  appVersion?: string;
  platform?: string;
  WebSocketImpl?: typeof WebSocket;
  minBackoffMs?: number;
  maxBackoffMs?: number;
}

/**
 * Single retry owner for one paired agentc host.
 *
 * Implements resilient reconnection:
 * - Jittered exponential backoff capped at 5 minutes, reset only once a
 *   connection stays healthy for 10+ seconds.
 * - Stops retrying immediately when the daemon reports `unpaired`.
 * - Foreground / online wakeups probe the live socket via `ping` (3s timeout
 *   on loopback, 10s remote) and replace dead sockets immediately.
 * - Automatically falls back from localUrl to public url when switching networks.
 */
export class HostSupervisor {
  private client: RpcClient | null = null;
  private state: HostConnectionState = 'disconnected';
  private lastError = '';
  private attempt = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private healthyTimer: ReturnType<typeof setTimeout> | null = null;
  private disposed = false;
  private probing = false;
  private useFallbackUrl = false;
  public info: DaemonInfo | null = null;

  constructor(
    public creds: PairedHostCredentials,
    private readonly events: HostSupervisorEvents,
    private readonly opts: HostSupervisorOptions,
  ) {}

  get connectionState(): HostConnectionState {
    return this.state;
  }

  get error(): string {
    return this.lastError;
  }

  get activeUrl(): string {
    if (this.creds.localUrl && !this.useFallbackUrl) {
      return this.creds.localUrl;
    }
    return this.creds.url;
  }

  start(): void {
    if (this.disposed) return;
    if (this.state === 'connected' || this.state === 'connecting') return;
    this.clearRetry();
    this.attemptConnect();
  }

  reconnectNow(): void {
    if (this.disposed) return;
    this.attempt = 0;
    this.clearRetry();
    if (this.client) {
      const old = this.client;
      this.client = null;
      old.close('reconnect requested');
    }
    this.attemptConnect();
  }

  /** Called on window focus / network online wakeup. Probes live connection or wakes pending retry. */
  async wakeup(): Promise<void> {
    if (this.disposed) return;
    if (this.state === 'unpaired') return;
    if (this.state === 'reconnecting' || this.state === 'error' || this.state === 'disconnected') {
      this.reconnectNow();
      return;
    }
    if (this.state === 'connected' && this.client && !this.probing) {
      this.probing = true;
      const isLoopback = /^https?:\/\/(127\.0\.0\.1|localhost)(:|\/|$)/.test(this.activeUrl);
      const timeout = isLoopback ? 3_000 : 10_000;
      try {
        await this.client.call('ping', {}, timeout);
      } catch {
        if (!this.disposed) {
          this.reconnectNow();
        }
      } finally {
        this.probing = false;
      }
    }
  }

  async call<T = unknown>(method: string, params?: unknown, timeoutMs = 45_000): Promise<T> {
    if (!this.client?.isOpen) {
      throw new RpcError(`Host ${this.creds.name} is not connected`);
    }
    return this.client.call<T>(method, params, timeoutMs);
  }

  updateCredentials(next: PairedHostCredentials): void {
    const urlChanged = next.url !== this.creds.url || next.localUrl !== this.creds.localUrl;
    const authChanged = next.devicePriv !== this.creds.devicePriv || next.hostKey !== this.creds.hostKey;
    const inactiveState =
      this.state === 'unpaired' ||
      this.state === 'disconnected' ||
      (this.state as string) === 'closed';
    this.creds = next;
    if ((urlChanged || authChanged || inactiveState) && !this.disposed) {
      this.useFallbackUrl = false;
      this.reconnectNow();
    }
  }

  dispose(): void {
    this.disposed = true;
    this.clearRetry();
    if (this.healthyTimer) {
      clearTimeout(this.healthyTimer);
      this.healthyTimer = null;
    }
    if (this.client) {
      const c = this.client;
      this.client = null;
      c.close('host removed');
    }
    this.setState('disconnected');
  }

  private setState(next: HostConnectionState, err = ''): void {
    if (this.state === next && this.lastError === err) return;
    this.state = next;
    this.lastError = err;
    this.events.onStateChange(this.creds.id, next, err || undefined);
  }

  private clearRetry(): void {
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
  }

  private attemptConnect(): void {
    if (this.disposed) return;
    this.setState(this.attempt === 0 ? 'connecting' : 'reconnecting', this.lastError);

    const url = this.activeUrl;
    let client: RpcClient;
    try {
      client = new RpcClient({
        baseUrl: url,
        daemonKey: base64ToBytes(this.creds.hostKey),
        devicePriv: base64ToBytes(this.creds.devicePriv),
        deviceName: this.opts.deviceName,
        platform: this.opts.platform ?? 'linux-desktop',
        appVersion: this.opts.appVersion ?? '0.6.0',
        WebSocketImpl: this.opts.WebSocketImpl,
        onEvent: (ev, data) => {
          if (this.client !== client) return;
          if (ev === 'host.url' && data && typeof (data as { url?: string }).url === 'string') {
            const newUrl = (data as { url: string }).url;
            if (newUrl && newUrl !== this.creds.url) {
              this.creds = { ...this.creds, url: newUrl };
              this.events.onCredentialsUpdated?.(this.creds);
            }
          }
          this.events.onEvent(this.creds.id, ev, data);
        },
        onClose: (reason) => {
          if (this.client !== client) return;
          this.client = null;
          if (this.healthyTimer) {
            clearTimeout(this.healthyTimer);
            this.healthyTimer = null;
          }
          if (this.disposed) return;
          if (reason === 'unpaired') {
            this.setState('unpaired', 'This device was unpaired from the host. Re-pair with `agentc pair`.');
            return;
          }
          this.scheduleRetry(reason);
        },
      });
    } catch (err) {
      this.scheduleRetry((err as Error).message);
      return;
    }

    this.client = client;
    client
      .connect()
      .then(async (reply) => {
        if (this.client !== client || this.disposed) {
          client.close('superseded');
          return;
        }
        if (reply.publicUrl && reply.publicUrl !== this.creds.url) {
          this.creds = { ...this.creds, url: reply.publicUrl, name: this.creds.name || reply.host };
          this.events.onCredentialsUpdated?.(this.creds);
        }
        const info = await client.call<DaemonInfo>(
          'hello',
          { device: this.opts.deviceName, app: this.opts.appVersion ?? '0.6.0' },
          20_000,
        );
        if (this.client !== client || this.disposed) return;
        this.info = info;
        this.setState('connected');
        this.events.onDaemonInfo(this.creds.id, info);

        // Only reset backoff after staying stably connected for 10s.
        this.healthyTimer = setTimeout(() => {
          this.attempt = 0;
        }, 10_000);
      })
      .catch((err: Error) => {
        if (this.client !== client || this.disposed) return;
        this.client = null;
        if (err.message === 'unpaired') {
          this.setState('unpaired', 'This device was unpaired from the host.');
          return;
        }
        // If localUrl failed and we have a distinct public url, alternate immediately!
        if (this.creds.localUrl && this.creds.url && this.creds.localUrl !== this.creds.url) {
          this.useFallbackUrl = !this.useFallbackUrl;
        }
        this.scheduleRetry(err.message);
      });
  }

  private scheduleRetry(reason: string): void {
    if (this.disposed) return;
    this.attempt++;
    const minMs = this.opts.minBackoffMs ?? 800;
    const maxMs = this.opts.maxBackoffMs ?? 300_000; // 5 min cap
    const base = Math.min(maxMs, minMs * Math.pow(1.8, Math.min(this.attempt - 1, 14)));
    // Full-ish jitter (±25%) so clients of a restarted daemon don't stampede.
    const jitter = base * (0.75 + Math.random() * 0.5);
    const delay = Math.round(Math.min(maxMs, jitter));
    this.setState('reconnecting', reason);
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.attemptConnect();
    }, delay);
  }
}
