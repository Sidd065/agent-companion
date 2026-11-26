import type { PairedHostCredentials } from '@agentc/protocol';

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
  loadNotes(): Promise<Record<string, unknown>>;
  saveNotes(notes: Record<string, unknown>): Promise<void>;
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

declare global {
  interface Window {
    agentcDesktop?: DesktopBridge;
  }
}
