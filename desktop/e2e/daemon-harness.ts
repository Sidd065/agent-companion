import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

export interface CreateDaemonOptions {
  enableCodex?: boolean;
  disableSystemTrash?: boolean;
  mockOpenRouter?: {
    proxyUrl: string;
    caCertPath: string;
    apiKey?: string;
  };
}

export interface HermeticDaemon {
  name: string;
  rootDir: string;
  homeDir: string;
  socketPath: string;
  listenUrl: string;
  port: number;
  proc: ChildProcess | null;
  start(): Promise<void>;
  stop(): Promise<void>;
  createPairLink(): Promise<string>;
  cleanup(): Promise<void>;
}

export interface MockOpenRouterModel {
  id: string;
  name: string;
  description?: string;
  context_length?: number;
}

export interface RecordedResponsesRequest {
  auth: string;
  body: Record<string, unknown>;
}

export interface MockOpenRouterServer {
  proxyUrl: string;
  caCertPath: string;
  addModel(model: MockOpenRouterModel): void;
  getLastAuthHeader(): string;
  getAuthHeaders(): string[];
  getResponsesRequests(): RecordedResponsesRequest[];
  cleanup(): Promise<void>;
}

const REPO_ROOT = path.resolve(__dirname, '..', '..');
const BIN_DIR = path.join(__dirname, '.bin');
const AGENTC_BIN = path.join(BIN_DIR, 'agentc');
const FAKEACP_BIN = path.join(BIN_DIR, 'fakeacp');
const FAKECODEX_BIN = path.join(BIN_DIR, 'fakecodex');

// Keep all E2E scratch directories outside /tmp (~66-byte sun_path, well under 108-byte limit)
export const E2E_BASE_DIR = path.join(os.homedir(), '.cache', 'agcdesk-e2e');

export function makeE2ETempDir(prefix: string): string {
  fs.mkdirSync(E2E_BASE_DIR, { recursive: true, mode: 0o700 });
  return fs.mkdtempSync(path.join(E2E_BASE_DIR, `${prefix}-`));
}

let binariesBuilt = false;

export function ensureGoBinaries(): { agentcBin: string; fakeacpBin: string; fakecodexBin: string } {
  if (
    binariesBuilt &&
    fs.existsSync(AGENTC_BIN) &&
    fs.existsSync(FAKEACP_BIN) &&
    fs.existsSync(FAKECODEX_BIN)
  ) {
    return { agentcBin: AGENTC_BIN, fakeacpBin: FAKEACP_BIN, fakecodexBin: FAKECODEX_BIN };
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
  execFileSync(goBin, ['build', '-o', FAKECODEX_BIN, './cmd/fakecodex'], {
    cwd: path.join(REPO_ROOT, 'e2e'),
    env,
    stdio: 'pipe',
  });
  binariesBuilt = true;
  return { agentcBin: AGENTC_BIN, fakeacpBin: FAKEACP_BIN, fakecodexBin: FAKECODEX_BIN };
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

export async function createMockOpenRouterServer(): Promise<MockOpenRouterServer> {
  const certDir = makeE2ETempDir('or-certs');
  const caKeyPath = path.join(certDir, 'ca.key');
  const caCertPath = path.join(certDir, 'ca.crt');
  const leafKeyPath = path.join(certDir, 'leaf.key');
  const leafCsrPath = path.join(certDir, 'leaf.csr');
  const leafCertPath = path.join(certDir, 'leaf.crt');
  const extFilePath = path.join(certDir, 'leaf.ext');

  execFileSync(
    'openssl',
    [
      'req',
      '-x509',
      '-newkey',
      'rsa:2048',
      '-nodes',
      '-keyout',
      caKeyPath,
      '-out',
      caCertPath,
      '-days',
      '2',
      '-subj',
      '/CN=AgentcE2ERootCA',
      '-addext',
      'basicConstraints=critical,CA:TRUE',
      '-addext',
      'keyUsage=critical,keyCertSign,cRLSign',
    ],
    { stdio: 'pipe' },
  );

  execFileSync(
    'openssl',
    [
      'req',
      '-newkey',
      'rsa:2048',
      '-nodes',
      '-keyout',
      leafKeyPath,
      '-out',
      leafCsrPath,
      '-subj',
      '/CN=openrouter.ai',
    ],
    { stdio: 'pipe' },
  );

  fs.writeFileSync(
    extFilePath,
    'subjectAltName=DNS:openrouter.ai\nbasicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth\n',
  );

  execFileSync(
    'openssl',
    [
      'x509',
      '-req',
      '-in',
      leafCsrPath,
      '-CA',
      caCertPath,
      '-CAkey',
      caKeyPath,
      '-CAcreateserial',
      '-out',
      leafCertPath,
      '-days',
      '2',
      '-extfile',
      extFilePath,
    ],
    { stdio: 'pipe' },
  );

  const models: Array<Record<string, unknown>> = [
    {
      id: 'google/gemini-3-flash-preview',
      name: 'Google: Gemini 3 Flash Preview',
      description: 'Fast frontier reasoning & coding model',
      context_length: 1048576,
      architecture: { input_modalities: ['text', 'image'] },
      supported_parameters: ['tools', 'reasoning'],
    },
    {
      id: 'openai/gpt-5.4-mini',
      name: 'OpenAI: GPT-5.4 Mini',
      description: 'Compact coding model on OpenRouter',
      context_length: 200000,
      architecture: { input_modalities: ['text', 'image'] },
      supported_parameters: ['tools', 'reasoning'],
    },
    {
      id: 'anthropic/claude-sonnet-4.6',
      name: 'Anthropic: Claude Sonnet 4.6',
      description: 'Balanced coding model on OpenRouter',
      context_length: 200000,
      architecture: { input_modalities: ['text', 'image'] },
      supported_parameters: ['tools', 'reasoning'],
    },
  ];

  const authHeaders: string[] = [];
  const responsesRequests: RecordedResponsesRequest[] = [];

  const httpsServer = https.createServer(
    {
      key: fs.readFileSync(leafKeyPath),
      cert: fs.readFileSync(leafCertPath),
    },
    (req, res) => {
      const auth = String(req.headers['authorization'] || '');
      if (auth) authHeaders.push(auth);
      const urlPath = (req.url || '').split('?')[0];

      if (req.method === 'GET' && (urlPath === '/api/v1/models' || urlPath === '/api/v1/models/user')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ data: models }));
        return;
      }

      if (req.method === 'POST' && urlPath === '/api/v1/responses') {
        const chunks: Buffer[] = [];
        req.on('data', (c: Buffer) => chunks.push(c));
        req.on('end', () => {
          let parsed: Record<string, unknown> = {};
          try {
            parsed = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>;
          } catch {
            parsed = {};
          }
          responsesRequests.push({ auth, body: parsed });
          const modelStr = String(parsed.model || 'unknown');
          const outText = `MOCK_OR_OK model=${modelStr}`;
          res.writeHead(200, {
            'Content-Type': 'text/event-stream',
            'Cache-Control': 'no-cache',
            Connection: 'keep-alive',
          });
          res.write(`data: ${JSON.stringify({ type: 'response.output_text.delta', delta: outText })}\n\n`);
          res.write(
            `data: ${JSON.stringify({
              type: 'response.completed',
              response: {
                id: `resp_${responsesRequests.length}`,
                output: [
                  {
                    id: `msg_${responsesRequests.length}`,
                    type: 'message',
                    role: 'assistant',
                    content: [{ type: 'output_text', text: outText }],
                  },
                ],
              },
            })}\n\n`,
          );
          res.write('data: [DONE]\n\n');
          res.end();
        });
        return;
      }

      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message: `Unhandled mock path ${urlPath}` } }));
    },
  );

  const openSockets = new Set<net.Socket>();
  const trackSocket = (s: net.Socket) => {
    openSockets.add(s);
    s.once('close', () => openSockets.delete(s));
  };

  httpsServer.on('connection', trackSocket);

  const httpsPort = await new Promise<number>((resolve, reject) => {
    httpsServer.listen(0, '127.0.0.1', () => {
      const addr = httpsServer.address();
      resolve(typeof addr === 'object' && addr ? addr.port : 0);
    });
    httpsServer.on('error', reject);
  });

  // HTTP CONNECT tunnel forwarding openrouter.ai:443 -> local httpsServer
  const proxyServer = http.createServer((_req, res) => {
    res.writeHead(400);
    res.end('CONNECT only');
  });
  proxyServer.on('connection', trackSocket);

  proxyServer.on('connect', (_req, clientSocket, head) => {
    trackSocket(clientSocket as net.Socket);
    const upstream = net.connect(httpsPort, '127.0.0.1', () => {
      clientSocket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      if (head && head.length > 0) upstream.write(head);
      upstream.pipe(clientSocket);
      clientSocket.pipe(upstream);
    });
    trackSocket(upstream);
    upstream.on('error', () => clientSocket.destroy());
    clientSocket.on('error', () => upstream.destroy());
  });

  const proxyPort = await new Promise<number>((resolve, reject) => {
    proxyServer.listen(0, '127.0.0.1', () => {
      const addr = proxyServer.address();
      resolve(typeof addr === 'object' && addr ? addr.port : 0);
    });
    proxyServer.on('error', reject);
  });

  return {
    proxyUrl: `http://127.0.0.1:${proxyPort}`,
    caCertPath,
    addModel(m: MockOpenRouterModel) {
      models.unshift({
        id: m.id,
        name: m.name,
        description: m.description || 'Added OpenRouter model',
        context_length: m.context_length ?? 256000,
        architecture: { input_modalities: ['text', 'image'] },
        supported_parameters: ['tools', 'reasoning'],
      });
    },
    getLastAuthHeader() {
      return authHeaders[authHeaders.length - 1] || '';
    },
    getAuthHeaders() {
      return authHeaders.slice();
    },
    getResponsesRequests() {
      return responsesRequests.slice();
    },
    async cleanup() {
      for (const s of openSockets) {
        try {
          s.destroy();
        } catch {
          // ignore
        }
      }
      openSockets.clear();
      proxyServer.closeAllConnections?.();
      httpsServer.closeAllConnections?.();
      await new Promise<void>((r) => proxyServer.close(() => r()));
      await new Promise<void>((r) => httpsServer.close(() => r()));
      fs.rmSync(certDir, { recursive: true, force: true });
    },
  };
}

export async function createHermeticDaemon(
  name: string,
  opts: CreateDaemonOptions = {},
): Promise<HermeticDaemon> {
  const { agentcBin, fakeacpBin, fakecodexBin } = ensureGoBinaries();
  const port = await getFreePort();
  const rootDir = makeE2ETempDir(name);
  const homeDir = path.join(rootDir, 'home');
  const cfgDir = path.join(rootDir, 'cfg');
  const stateDir = path.join(rootDir, 'st');
  const dataDir = path.join(rootDir, 'dt');
  const rtDir = path.join(rootDir, 'rt');
  const fakeStateDir = path.join(rootDir, 'fake');
  const fakeCodexStateDir = path.join(rootDir, 'fakecodex');
  for (const d of [
    homeDir,
    cfgDir,
    path.join(cfgDir, 'agentc'),
    stateDir,
    dataDir,
    rtDir,
    fakeStateDir,
    fakeCodexStateDir,
  ]) {
    fs.mkdirSync(d, { recursive: true });
  }

  let customPath = process.env.PATH || '';
  if (opts.disableSystemTrash) {
    const noTrashBin = path.join(rootDir, 'notrash-bin');
    fs.mkdirSync(noTrashBin, { recursive: true });
    for (const tool of ['gio', 'kioclient5']) {
      const p = path.join(noTrashBin, tool);
      fs.writeFileSync(
        p,
        '#!/bin/sh\necho "gio: Trashing on system mounts is not supported (unable to find or create trash directory)" >&2\nexit 1\n',
        {
          mode: 0o755,
        },
      );
    }
    customPath = `${noTrashBin}:${customPath}`;
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
    const childEnv: NodeJS.ProcessEnv = {
      ...process.env,
      PATH: customPath,
      HOME: homeDir,
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
    };

    if (opts.enableCodex) {
      childEnv.AGENTC_CODEX_PATH = fakecodexBin;
      childEnv.FAKECODEX_STATE_DIR = fakeCodexStateDir;
    } else {
      childEnv.AGENTC_CODEX_PATH = path.join(rootDir, 'codex-disabled');
    }

    // Isolate from any outer host OpenRouter/OpenAI keys unless explicitly configured
    delete childEnv.OPENROUTER_API_KEY;
    delete childEnv.OPENROUTER_API_KEY_FILE;
    delete childEnv.OPENAI_API_KEY;

    if (opts.mockOpenRouter) {
      childEnv.HTTPS_PROXY = opts.mockOpenRouter.proxyUrl;
      childEnv.HTTP_PROXY = opts.mockOpenRouter.proxyUrl;
      childEnv.NO_PROXY = '127.0.0.1,localhost';
      childEnv.SSL_CERT_FILE = opts.mockOpenRouter.caCertPath;
      if (opts.mockOpenRouter.apiKey) {
        childEnv.OPENROUTER_API_KEY = opts.mockOpenRouter.apiKey;
      }
    }

    proc = spawn(agentcBin, ['serve', '--listen', `127.0.0.1:${port}`], {
      env: childEnv,
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
    homeDir,
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
