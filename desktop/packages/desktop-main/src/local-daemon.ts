import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';

export interface LocalDaemonStatus {
  available: boolean;
  socketPath?: string;
  host?: string;
  hostId?: string;
  fingerprint?: string;
  version?: string;
  listenUrl?: string;
  publicUrl?: string;
  devices?: number;
  error?: string;
}

export function findLocalSocket(): string | null {
  const candidates: string[] = [];
  if (process.env.AGENTC_LOCAL_SOCK) {
    candidates.push(process.env.AGENTC_LOCAL_SOCK);
  }
  if (process.env.XDG_RUNTIME_DIR) {
    candidates.push(path.join(process.env.XDG_RUNTIME_DIR, 'agentc', 'agentc.sock'));
  }
  const uid = typeof process.getuid === 'function' ? process.getuid() : 1000;
  candidates.push(path.join(os.tmpdir(), `agentc-${uid}`, 'agentc', 'agentc.sock'));
  candidates.push(path.join('/run/user', String(uid), 'agentc', 'agentc.sock'));

  for (const c of candidates) {
    try {
      const st = fs.statSync(c);
      if (st.isSocket()) return c;
    } catch {
      // keep searching
    }
  }
  return null;
}

function unixRequest<T>(socketPath: string, method: 'GET' | 'POST', reqPath: string, body?: unknown): Promise<T> {
  return new Promise((resolve, reject) => {
    const payload = body !== undefined ? JSON.stringify(body) : undefined;
    const req = http.request(
      {
        socketPath,
        path: reqPath,
        method,
        headers: payload
          ? {
              'Content-Type': 'application/json',
              'Content-Length': Buffer.byteLength(payload),
            }
          : undefined,
        timeout: 4_000,
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (c: Buffer) => chunks.push(c));
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          if ((res.statusCode ?? 500) >= 400) {
            reject(new Error(text || `HTTP ${res.statusCode}`));
            return;
          }
          try {
            resolve(JSON.parse(text) as T);
          } catch (err) {
            reject(err);
          }
        });
      },
    );
    req.on('error', reject);
    req.on('timeout', () => {
      req.destroy(new Error('local daemon socket timed out'));
    });
    if (payload) req.write(payload);
    req.end();
  });
}

export async function detectLocalDaemon(): Promise<LocalDaemonStatus> {
  const sock = findLocalSocket();
  if (!sock) return { available: false };
  try {
    const info = await unixRequest<{
      host?: string;
      hostId?: string;
      fingerprint?: string;
      version?: string;
      publicUrl?: string;
      devices?: number;
      listen?: string[];
    }>(sock, 'GET', '/local/status');
    let listenUrl = 'http://127.0.0.1:9473';
    if (Array.isArray(info.listen) && info.listen.length > 0) {
      const addr = info.listen[0]
        .replace(/^0\.0\.0\.0:/, '127.0.0.1:')
        .replace(/^\[::\]:/, '127.0.0.1:');
      listenUrl = addr.startsWith('http') ? addr : `http://${addr}`;
    }
    return {
      available: true,
      socketPath: sock,
      host: info.host,
      hostId: info.hostId,
      fingerprint: info.fingerprint,
      version: info.version,
      listenUrl,
      publicUrl: info.publicUrl,
      devices: info.devices,
    };
  } catch (err) {
    return { available: false, socketPath: sock, error: (err as Error).message };
  }
}

export async function requestLocalPairLink(customUrl?: string): Promise<{ url: string; host: string; fingerprint: string }> {
  const status = await detectLocalDaemon();
  if (!status.available || !status.socketPath) {
    throw new Error('Local agentc daemon is not running on this machine.');
  }
  const targetUrl = customUrl || status.listenUrl || 'http://127.0.0.1:9473';
  const res = await unixRequest<{ url: string; host: string; fingerprint: string; error?: string }>(
    status.socketPath,
    'POST',
    '/local/pair',
    { ttlSeconds: 180, url: targetUrl },
  );
  if (!res.url) {
    throw new Error(res.error || 'Failed to generate local pairing link');
  }
  return res;
}
