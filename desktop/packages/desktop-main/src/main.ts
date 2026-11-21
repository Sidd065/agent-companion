import { app, BrowserWindow, ipcMain, Menu, nativeImage, Notification, screen, shell, Tray } from 'electron';
import { execFile, spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import type { PairedHostCredentials } from '@agentc/protocol';
import { detectLocalDaemon, requestLocalPairLink } from './local-daemon.js';
import { openDesktopStores, type DesktopStores, type WindowBounds } from './storage.js';

// Pre-ready Linux desktop identity (before app.whenReady!).
app.setName('Agent Companion');
if (process.platform === 'linux') {
  try {
    (app as unknown as { setDesktopName?: (n: string) => void }).setDesktopName?.('agent-companion.desktop');
  } catch {
    // ignore
  }
}

const isTestMode = Boolean(process.env.AGENTC_DESKTOP_TEST || process.env.AGENTC_DESKTOP_CONFIG_DIR);
const gotLock = isTestMode ? true : app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
}

let mainWindow: BrowserWindow | null = null;
let tray: Tray | null = null;
let stores: DesktopStores;
let rendererReady = false;
let quitting = false;
const pendingDeepLinks: string[] = [];

function extractDeepLink(argv: string[]): string | null {
  for (const a of argv) {
    if (a.startsWith('agentc://')) return a;
  }
  return null;
}

const startupLink = extractDeepLink(process.argv);
if (startupLink) pendingDeepLinks.push(startupLink);

function deliverDeepLink(link: string): void {
  if (rendererReady && mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('app:deepLink', link);
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  } else {
    pendingDeepLinks.push(link);
  }
}

app.on('second-instance', (_ev, argv) => {
  const link = extractDeepLink(argv);
  if (link) deliverDeepLink(link);
  if (mainWindow && !mainWindow.isDestroyed()) {
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  }
});

app.on('open-url', (ev, url) => {
  ev.preventDefault();
  if (url.startsWith('agentc://')) deliverDeepLink(url);
});

function clampBounds(b: WindowBounds): WindowBounds {
  const displays = screen.getAllDisplays();
  const width = Math.max(400, Math.min(b.width || 1380, 3840));
  const height = Math.max(360, Math.min(b.height || 860, 2160));
  if (typeof b.x === 'number' && typeof b.y === 'number') {
    const visible = displays.some((d) => {
      const wa = d.workArea;
      return b.x! < wa.x + wa.width - 80 && b.x! + width > wa.x + 80 && b.y! < wa.y + wa.height - 80 && b.y! + height > wa.y + 80;
    });
    if (visible) return { x: b.x, y: b.y, width, height, maximized: b.maximized };
  }
  return { width, height, maximized: b.maximized };
}

function getWebDistIndex(): string {
  // Packaged inside resources/web/index.html or workspace ../web/dist/index.html
  const prodPath = path.join(process.resourcesPath, 'web', 'index.html');
  if (fs.existsSync(prodPath)) return prodPath;
  return path.resolve(__dirname, '../../web/dist/index.html');
}

function createMainWindow(): BrowserWindow {
  const saved = clampBounds(stores.bounds.read());
  const win = new BrowserWindow({
    x: saved.x,
    y: saved.y,
    width: saved.width,
    height: saved.height,
    minWidth: 400,
    minHeight: 360,
    show: false,
    backgroundColor: '#0c0d10',
    autoHideMenuBar: true,
    titleBarStyle: process.platform === 'linux' ? 'hidden' : 'hiddenInset',
    titleBarOverlay: {
      // #01000000 avoids Chromium Linux's #00000000 transparent overlay bug.
      color: '#01000000',
      symbolColor: '#a1a1aa',
      height: 38,
    },
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webviewTag: true,
    },
  });

  if (saved.maximized) win.maximize();

  // Persist window bounds with 500ms debounce, ignoring minimized state.
  let boundsTimer: ReturnType<typeof setTimeout> | null = null;
  const saveBounds = () => {
    if (win.isDestroyed() || win.isMinimized()) return;
    if (boundsTimer) clearTimeout(boundsTimer);
    boundsTimer = setTimeout(() => {
      if (win.isDestroyed() || win.isMinimized()) return;
      const nb = win.getNormalBounds();
      stores.bounds.write({
        x: nb.x,
        y: nb.y,
        width: nb.width,
        height: nb.height,
        maximized: win.isMaximized(),
      });
    }, 500);
  };
  win.on('resize', saveBounds);
  win.on('move', saveBounds);
  win.on('maximize', saveBounds);
  win.on('unmaximize', saveBounds);

  // Ctrl+W single-tab close guard (prevents closing whole window on tab close / auto-repeat).
  win.webContents.on('before-input-event', (event, input) => {
    const isCtrlW = (input.control || input.meta) && !input.alt && !input.shift && input.key.toLowerCase() === 'w';
    if (isCtrlW && input.type === 'keyDown') {
      event.preventDefault();
      if (!input.isAutoRepeat) {
        win.webContents.send('window:closeTab');
      }
    }
  });

  win.on('focus', () => {
    try {
      app.setBadgeCount(0);
    } catch {
      // ignore
    }
    win.webContents.send('window:focus');
  });

  win.on('close', (ev) => {
    const settings = stores.settings.read();
    if (!quitting && settings.closeToTray === true && tray) {
      ev.preventDefault();
      win.hide();
    }
  });

  win.once('ready-to-show', () => {
    win.show();
  });

  const devUrl = process.env.AGENTC_DEV_URL;
  if (devUrl) {
    void win.loadURL(devUrl);
  } else {
    void win.loadFile(getWebDistIndex());
  }

  return win;
}

const KNOWN_EDITORS = [
  { id: 'code', name: 'VS Code', command: 'code' },
  { id: 'cursor', name: 'Cursor', command: 'cursor' },
  { id: 'zed', name: 'Zed', command: 'zed' },
  { id: 'windsurf', name: 'Windsurf', command: 'windsurf' },
  { id: 'codium', name: 'VSCodium', command: 'codium' },
  { id: 'subl', name: 'Sublime Text', command: 'subl' },
  { id: 'files', name: 'Files', command: 'xdg-open' },
];

function commandExists(cmd: string): Promise<boolean> {
  return new Promise((resolve) => {
    execFile('which', [cmd], (err) => resolve(!err));
  });
}

function setupTray(): void {
  if (isTestMode || tray) return;
  try {
    const icon = nativeImage.createEmpty();
    tray = new Tray(icon);
    tray.setToolTip('Agent Companion');
    tray.setContextMenu(
      Menu.buildFromTemplate([
        {
          label: 'Show Agent Companion',
          click: () => {
            mainWindow?.show();
            mainWindow?.focus();
          },
        },
        { type: 'separator' },
        {
          label: 'Quit',
          click: () => {
            quitting = true;
            app.quit();
          },
        },
      ]),
    );
    tray.on('click', () => {
      if (mainWindow?.isVisible()) {
        mainWindow.focus();
      } else {
        mainWindow?.show();
      }
    });
  } catch {
    // Tray optional in headless/minimal environments
  }
}

function registerIpc(): void {
  ipcMain.handle('store:hosts:load', () => stores.hosts.read());
  ipcMain.handle('store:hosts:save', (_ev, hosts: PairedHostCredentials[]) => {
    stores.hosts.write(Array.isArray(hosts) ? hosts : []);
  });
  ipcMain.handle('store:settings:load', () => stores.settings.read());
  ipcMain.handle('store:settings:save', (_ev, settings: Record<string, unknown>) => {
    stores.settings.write(settings && typeof settings === 'object' ? settings : {});
  });
  ipcMain.handle('local:detect', () => detectLocalDaemon());
  ipcMain.handle('local:pair', (_ev, customUrl?: string) => requestLocalPairLink(customUrl));
  ipcMain.handle('editors:list', async () => {
    const found = [];
    for (const ed of KNOWN_EDITORS) {
      if (await commandExists(ed.command)) found.push(ed);
    }
    return found;
  });
  ipcMain.handle('editors:open', async (_ev, editorId: string, dirPath: string) => {
    const ed = KNOWN_EDITORS.find((e) => e.id === editorId) ?? KNOWN_EDITORS[KNOWN_EDITORS.length - 1];
    if (!dirPath) return false;
    try {
      const child = spawn(ed.command, [dirPath], { detached: true, stdio: 'ignore' });
      child.unref();
      return true;
    } catch {
      return false;
    }
  });
  ipcMain.handle('shell:openExternal', async (_ev, rawUrl: string) => {
    try {
      const u = new URL(rawUrl);
      if (u.protocol === 'http:' || u.protocol === 'https:') {
        await shell.openExternal(u.toString());
      }
    } catch {
      // ignore invalid URLs
    }
  });
  ipcMain.handle('notify:show', (_ev, opts: { title: string; body: string; hostId?: string; sessionId?: string }) => {
    if (!Notification.isSupported()) return;
    try {
      const n = new Notification({ title: opts.title, body: opts.body });
      n.on('click', () => {
        if (mainWindow && !mainWindow.isDestroyed()) {
          if (mainWindow.isMinimized()) mainWindow.restore();
          mainWindow.show();
          mainWindow.focus();
          mainWindow.webContents.send('notify:click', { hostId: opts.hostId, sessionId: opts.sessionId });
        }
      });
      n.show();
    } catch {
      // Never block or crash on dead D-Bus notification daemon
    }
  });
  ipcMain.handle('badge:set', (_ev, count: number) => {
    try {
      app.setBadgeCount(Math.max(0, Math.floor(count)));
    } catch {
      // ignore
    }
  });
  ipcMain.handle('window:titleBarTheme', (_ev, dark: boolean) => {
    try {
      mainWindow?.setTitleBarOverlay({
        color: '#01000000',
        symbolColor: dark ? '#a1a1aa' : '#52525b',
        height: 38,
      });
    } catch {
      // ignore
    }
  });
  ipcMain.handle('app:ready', () => {
    rendererReady = true;
    while (pendingDeepLinks.length > 0) {
      const next = pendingDeepLinks.shift();
      if (next && mainWindow && !mainWindow.isDestroyed()) {
        mainWindow.webContents.send('app:deepLink', next);
      }
    }
  });
}

app.whenReady().then(() => {
  process.env.HOSTNAME = os.hostname();
  const configDir = process.env.AGENTC_DESKTOP_CONFIG_DIR || path.join(app.getPath('userData'), 'agentc-desktop');
  stores = openDesktopStores(configDir);
  registerIpc();
  setupTray();
  mainWindow = createMainWindow();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      mainWindow = createMainWindow();
    }
  });
});

app.on('before-quit', () => {
  quitting = true;
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit();
  }
});
