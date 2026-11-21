import fs from 'node:fs';
import path from 'node:path';
import type { PairedHostCredentials } from '@agentc/protocol';

export interface WindowBounds {
  x?: number;
  y?: number;
  width: number;
  height: number;
  maximized?: boolean;
}

export class AtomicJsonStore<T> {
  constructor(
    private readonly filePath: string,
    private readonly fallback: T,
  ) {}

  read(): T {
    try {
      const raw = fs.readFileSync(this.filePath, 'utf8');
      return JSON.parse(raw) as T;
    } catch {
      return structuredClone(this.fallback);
    }
  }

  write(data: T): void {
    const dir = path.dirname(this.filePath);
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    const tmp = `${this.filePath}.${process.pid}.${Date.now()}.tmp`;
    const body = JSON.stringify(data, null, 2) + '\n';
    fs.writeFileSync(tmp, body, { mode: 0o600 });
    fs.renameSync(tmp, this.filePath);
  }
}

export interface DesktopStores {
  hosts: AtomicJsonStore<PairedHostCredentials[]>;
  settings: AtomicJsonStore<Record<string, unknown>>;
  bounds: AtomicJsonStore<WindowBounds>;
}

export function openDesktopStores(baseDir: string): DesktopStores {
  fs.mkdirSync(baseDir, { recursive: true, mode: 0o700 });
  return {
    hosts: new AtomicJsonStore<PairedHostCredentials[]>(path.join(baseDir, 'hosts.json'), []),
    settings: new AtomicJsonStore<Record<string, unknown>>(path.join(baseDir, 'settings.json'), {}),
    bounds: new AtomicJsonStore<WindowBounds>(path.join(baseDir, 'window-bounds.json'), {
      width: 1380,
      height: 860,
    }),
  };
}
