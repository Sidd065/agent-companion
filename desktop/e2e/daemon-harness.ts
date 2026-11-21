import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

export interface HermeticDaemon {
  name: string;
  rootDir: string;
  socketPath: string;
  listenUrl: string;
  port: number;
  proc: ChildProcess | null;
  start(): Promise<void>;
  stop(): Promise<void>;
  createPairLink(): Promise<string>;
  cleanup(): Promise<void>;
}

const REPO_ROOT = path.resolve(__dirname, '..', '..');
const BIN_DIR = path.join(__dirname, '.bin');
const AGENTC_BIN = path.join(BIN_DIR, 'agentc');
const FAKEACP_BIN = path.join(BIN_DIR, 'fakeacp');

let binariesBuilt = false;

export function ensureGoBinaries(): { agentcBin: string; fakeacpBin: string } {
  if (binariesBuilt && fs.existsSync(AGENTC_BIN) && fs.existsSync(FAKEACP_BIN)) {
    return { agentcBin: AGENTC_BIN, fakeacpBin: FAKEACP_BIN };
  }
  fs.mkdirSync(BIN_DIR, { recursive: true });
  const goBin = fs.existsSync(path.join(os.homedir(), '.local/go/bin/go'))
    ? path.join(os.homedir(), '.local/go/bin/go')
    : 'go';
  const env = {
    ...process.env,
    PATH: `${path.join(os.homedir(), '.local/go/bin')}:${path.join(os.homedir(), '.local/bin')}:${process.env.PATH || ''}`,
  };
  execFileSync(goBin, ['build', '-o', AGENTC_BIN, '.'], {
    cwd: path.join(REPO_ROOT, 'daemon'),
    env,
    stdio: 'pipe',
  });
  execFileSync(goBin, ['build', '-o', FAKEACP_BIN, './cmd/fakeacp'], {
    cwd: path.join(REPO_ROOT, 'daemon'),
    env,
    stdio: 'pipe',
  });
  binariesBuilt = true;
  return { agentcBin: AGENTC_BIN, fakeacpBin: FAKEACP_BIN };
}

function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
    srv.on('error', reject);
  });
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
          ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) }
          : undefined,
        timeout: 4_000,
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (c: Buffer) => chunks.push(c));
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          if ((res.statusCode ?? 500) >= 400) {
            reject(new Error(`HTTP ${res.statusCode}: ${text}`));
            return;
          }
          try {
            resolve(JSON.parse(text) as T);
          } catch (e) {
            reject(e);
          }
        });
      },
    );
    req.on('error', reject);
    req.on('timeout', () => req.destroy(new Error('timeout')));
    if (payload) req.write(payload);
    req.end();
  });
}

async function waitForDaemon(socketPath: string, timeoutMs = 10_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      if (fs.existsSync(socketPath)) {
        await unixRequest(socketPath, 'GET', '/local/status');
        return;
      }
    } catch {
      // retry
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`Hermetic daemon failed to start at ${socketPath}`);
}

export async function createHermeticDaemon(name: string): Promise<HermeticDaemon> {
  const { agentcBin, fakeacpBin } = ensureGoBinaries();
  const port = await getFreePort();
  // Short path under /tmp to keep tmux.sock & agentc.sock well below Linux 108-byte sun_path limit.
  const rootDir = fs.mkdtempSync(path.join('/tmp', `agce2e-${name}-`));
  const cfgDir = path.join(rootDir, 'cfg');
  const stateDir = path.join(rootDir, 'st');
  const dataDir = path.join(rootDir, 'dt');
  const rtDir = path.join(rootDir, 'rt');
  const fakeStateDir = path.join(rootDir, 'fake');
  for (const d of [cfgDir, path.join(cfgDir, 'agentc'), stateDir, dataDir, rtDir, fakeStateDir]) {
    fs.mkdirSync(d, { recursive: true });
  }

  const listenUrl = `http://127.0.0.1:${port}`;
  const socketPath = path.join(rtDir, 'agentc', 'agentc.sock');

  fs.writeFileSync(
    path.join(cfgDir, 'agentc', 'config.json'),
    JSON.stringify(
      {
        tunnel: 'none',
        listen: `127.0.0.1:${port}`,
        publicUrl: listenUrl,
        defaultSkipPermissions: true,
        maxLive: 8,
      },
      null,
      2,
    ),
    { mode: 0o600 },
  );

  let proc: ChildProcess | null = null;
  let logs = '';

  const start = async () => {
    if (proc && !proc.killed) return;
    proc = spawn(agentcBin, ['serve', '--listen', `127.0.0.1:${port}`], {
      env: {
        ...process.env,
        HOME: rootDir,
        XDG_CONFIG_HOME: cfgDir,
        XDG_STATE_HOME: stateDir,
        XDG_DATA_HOME: dataDir,
        XDG_RUNTIME_DIR: rtDir,
        AGENTC_FAKE_ACP: fakeacpBin,
        FAKEACP_STATE_DIR: fakeStateDir,
        GIT_AUTHOR_NAME: 'Agent Companion E2E',
        GIT_AUTHOR_EMAIL: 'e2e@agentc.local',
        GIT_COMMITTER_NAME: 'Agent Companion E2E',
        GIT_COMMITTER_EMAIL: 'e2e@agentc.local',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    proc.stdout?.on('data', (d: Buffer) => {
      logs += d.toString();
    });
    proc.stderr?.on('data', (d: Buffer) => {
      logs += d.toString();
    });
    try {
      await waitForDaemon(socketPath, 10_000);
    } catch (err) {
      throw new Error(`${(err as Error).message}\nDaemon logs:\n${logs}`);
    }
  };

  const stop = async () => {
    if (!proc) return;
    const p = proc;
    proc = null;
    await new Promise<void>((resolve) => {
      if (p.exitCode !== null) {
        resolve();
        return;
      }
      const timer = setTimeout(() => {
        try {
          p.kill('SIGKILL');
        } catch {
          // ignore
        }
        resolve();
      }, 4_000);
      p.once('exit', () => {
        clearTimeout(timer);
        resolve();
      });
      try {
        p.kill('SIGTERM');
      } catch {
        clearTimeout(timer);
        resolve();
      }
    });
    // Also clean up any hermetic tmux server on this isolated socket
    const tmuxSock = path.join(rtDir, 'agentc', 'tmux.sock');
    if (fs.existsSync(tmuxSock)) {
      try {
        execFileSync('tmux', ['-S', tmuxSock, 'kill-server'], { stdio: 'ignore' });
      } catch {
        // ignore if already gone
      }
    }
  };

  const createPairLink = async (): Promise<string> => {
    const res = await unixRequest<{ url: string }>(socketPath, 'POST', '/local/pair', {
      ttlSeconds: 300,
      url: listenUrl,
    });
    return res.url;
  };

  const cleanup = async () => {
    await stop();
    fs.rmSync(rootDir, { recursive: true, force: true });
  };

  return {
    name,
    rootDir,
    socketPath,
    listenUrl,
    port,
    get proc() {
      return proc;
    },
    start,
    stop,
    createPairLink,
    cleanup,
  };
}
