import { contextBridge, ipcRenderer } from 'electron';
import type { PairedHostCredentials } from '@agentc/protocol';
import type { LocalDaemonStatus } from './local-daemon.js';

export interface DetectedEditor {
  id: string;
  name: string;
  command: string;
}

export interface DesktopBridge {
  isDesktop: true;
  platform: string;
  version: string;
  hostname: string;
  loadHosts(): Promise<PairedHostCredentials[]>;
  saveHosts(hosts: PairedHostCredentials[]): Promise<void>;
  loadSettings(): Promise<Record<string, unknown>>;
  saveSettings(settings: Record<string, unknown>): Promise<void>;
  detectLocalDaemon(): Promise<LocalDaemonStatus>;
  requestLocalPairLink(customUrl?: string): Promise<{ url: string; host: string; fingerprint: string }>;
  listEditors(): Promise<DetectedEditor[]>;
  openInEditor(editorId: string, dirPath: string): Promise<boolean>;
  openExternal(url: string): Promise<void>;
  showNotification(opts: { title: string; body: string; hostId?: string; sessionId?: string }): Promise<void>;
  setBadgeCount(count: number): Promise<void>;
  signalReady(): Promise<void>;
  setTitleBarTheme(dark: boolean): Promise<void>;
  onDeepLink(cb: (url: string) => void): () => void;
  onNotificationClick(cb: (target: { hostId?: string; sessionId?: string }) => void): () => void;
  onWindowFocus(cb: () => void): () => void;
  onCloseActiveTab(cb: () => void): () => void;
}

const bridge: DesktopBridge = {
  isDesktop: true,
  platform: process.platform,
  version: process.env.AGENTC_DESKTOP_VERSION || '0.5.0',
  hostname: process.env.HOSTNAME || 'linux-desktop',
  loadHosts: () => ipcRenderer.invoke('store:hosts:load'),
  saveHosts: (hosts) => ipcRenderer.invoke('store:hosts:save', hosts),
  loadSettings: () => ipcRenderer.invoke('store:settings:load'),
  saveSettings: (settings) => ipcRenderer.invoke('store:settings:save', settings),
  detectLocalDaemon: () => ipcRenderer.invoke('local:detect'),
  requestLocalPairLink: (customUrl) => ipcRenderer.invoke('local:pair', customUrl),
  listEditors: () => ipcRenderer.invoke('editors:list'),
  openInEditor: (editorId, dirPath) => ipcRenderer.invoke('editors:open', editorId, dirPath),
  openExternal: (url) => ipcRenderer.invoke('shell:openExternal', url),
  showNotification: (opts) => ipcRenderer.invoke('notify:show', opts),
  setBadgeCount: (count) => ipcRenderer.invoke('badge:set', count),
  signalReady: () => ipcRenderer.invoke('app:ready'),
  setTitleBarTheme: (dark) => ipcRenderer.invoke('window:titleBarTheme', dark),
  onDeepLink: (cb) => {
    const listener = (_: unknown, url: string) => cb(url);
    ipcRenderer.on('app:deepLink', listener);
    return () => ipcRenderer.removeListener('app:deepLink', listener);
  },
  onNotificationClick: (cb) => {
    const listener = (_: unknown, target: { hostId?: string; sessionId?: string }) => cb(target);
    ipcRenderer.on('notify:click', listener);
    return () => ipcRenderer.removeListener('notify:click', listener);
  },
  onWindowFocus: (cb) => {
    const listener = () => cb();
    ipcRenderer.on('window:focus', listener);
    return () => ipcRenderer.removeListener('window:focus', listener);
  },
  onCloseActiveTab: (cb) => {
    const listener = () => cb();
    ipcRenderer.on('window:closeTab', listener);
    return () => ipcRenderer.removeListener('window:closeTab', listener);
  },
};

contextBridge.exposeInMainWorld('agentcDesktop', bridge);
