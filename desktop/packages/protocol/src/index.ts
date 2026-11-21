export * from './generated.js';
import type {
  AgentProfile,
  AsideItem,
  Device,
  Notification,
  Project,
  ScheduledTask,
  ScheduleRunRecord,
  SessionDetail,
  SessionSummary,
  TerminalInfo,
  TranscriptItem,
  TranscriptOp,
} from './generated.js';

export interface HelloReply {
  v: number;
  protocolVersion?: number;
  capabilities?: string[];
  host: string;
  hostId: string;
  version: string;
  deviceId: string;
  deviceName: string;
  publicUrl?: string;
}

export interface DaemonInfo {
  name: string;
  version: string;
  protocolVersion?: number;
  capabilities?: string[];
  host: string;
  home: string;
  uptimeSec: number;
  agents: AgentProfile[];
  stats?: Record<string, unknown>;
  hostId: string;
  fingerprint: string;
  publicUrl?: string;
  devices: number;
  sessions?: SessionSummary[];
  projects?: Project[];
  client?: { id: string; deviceId: string; deviceName: string };
  listen?: string[];
  tunnel?: { mode?: string; url?: string; error?: string };
  config?: Record<string, unknown>;
}

export interface PairedHostCredentials {
  id: string;
  name: string;
  url: string;
  localUrl?: string;
  hostKey: string;
  devicePriv: string;
  devicePub: string;
  deviceId?: string;
  fingerprint: string;
  pairedAt: number;
  isLocal?: boolean;
}

export type HostConnectionState =
  | 'disconnected'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'unpaired'
  | 'error';

export interface ServerEventMap {
  'session.changed': SessionSummary;
  'session.removed': { id: string };
  'sessions.invalidate': { id?: string };
  'session.snapshot': { id: string; session: SessionDetail; seq: number; items: TranscriptItem[] };
  'session.delta': { id: string; session: SessionDetail; seq: number; ops: TranscriptOp[] };
  'session.ops': { id: string; ops: TranscriptOp[] };
  'session.usage': { id: string; used: number; size: number };
  'session.config': { id: string; session: SessionDetail };
  'session.live': { id: string; live: unknown };
  'session.aside': { id: string; aside: AsideItem };
  'session.asides': { id: string; asides: AsideItem[] };
  'projects.changed': Project[];
  'providers.changed': AgentProfile[];
  'schedules.changed': { tasks: ScheduledTask[]; runs: ScheduleRunRecord[] };
  'terms.changed': TerminalInfo[];
  'devices.changed': Device[];
  'host.url': { url: string };
  'notify': Notification;
  'claude.rateLimit': { rateLimit: unknown; at: number };
}

export interface RpcRequestEnvelope {
  id: number;
  method: string;
  params?: unknown;
}

export interface RpcResponseEnvelope {
  id?: number;
  result?: unknown;
  error?: { message: string };
  event?: string;
  data?: unknown;
}
