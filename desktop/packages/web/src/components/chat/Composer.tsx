import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  ArrowUp,
  Check,
  ChevronDown,
  CornerDownLeft,
  Cpu,
  FileCode2,
  FileText,
  Image as ImageIcon,
  Layers,
  ListChecks,
  Paperclip,
  Play,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Sparkles,
  Square,
  Star,
  Terminal,
  Trash2,
  X,
  Zap,
} from 'lucide-react';
import type {
  AgentProfile,
  ModelInfo,
  PermCard,
  SessionDetail,
  SessionSummary,
  SkillEntry,
  SlashCommand,
  TranscriptItem,
} from '@agentc/protocol';
import { useApp, type DraftConfig } from '../../state/appStore.js';
import { MarkdownRenderer } from '../markdown/MarkdownRenderer.js';

export interface PendingAttachment {
  id: string;
  type: 'image' | 'file' | 'path';
  name: string;
  mime: string;
  data?: string;
  path?: string;
  previewUrl?: string;
  sizeBytes?: number;
}

interface BuiltinSlash {
  name: string;
  description: string;
  builtin: true;
}

const BUILTIN_SLASH: BuiltinSlash[] = [
  { name: 'btw', description: 'Ask a side question without polluting main context', builtin: true },
  { name: 'side', description: 'Open the Side Chat panel', builtin: true },
  { name: 'diff', description: 'Open the Diff Review panel', builtin: true },
  { name: 'files', description: 'Open the Files explorer panel', builtin: true },
  { name: 'git', description: 'Open Source Control panel', builtin: true },
  { name: 'pr', description: 'Open Pull Requests & Issues panel', builtin: true },
  { name: 'terminal', description: 'Toggle the integrated terminal drawer', builtin: true },
  { name: 'fork', description: 'Fork this thread into a new session', builtin: true },
  { name: 'settle', description: 'Mark this thread as settled', builtin: true },
];

const EFFORT_LEVELS = [
  { id: 'low', label: 'Low' },
  { id: 'medium', label: 'Medium' },
  { id: 'high', label: 'High' },
  { id: 'max', label: 'Max' },
];

const PERM_MODES = [
  { id: 'supervised', label: 'Supervised', desc: 'Ask before running edits or commands', skip: false },
  { id: 'acceptEdits', label: 'Auto-accept edits', desc: 'Auto-approve file edits, ask for shell', skip: false },
  { id: 'auto', label: 'Full access', desc: 'Auto-approve all tool calls (skip permissions)', skip: true },
];

const LARGE_PASTE_BYTES = 32 * 1024;

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const res = String(reader.result || '');
      const comma = res.indexOf(',');
      resolve(comma >= 0 ? res.slice(comma + 1) : res);
    };
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'));
    reader.readAsDataURL(file);
  });
}

function utf8ToBase64(str: string): string {
  const bytes = new TextEncoder().encode(str);
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

interface QuestionSpec {
  question: string;
  header?: string;
  multiSelect?: boolean;
  options?: Array<{ label: string; description?: string }>;
}

function extractQuestions(perm: PermCard): QuestionSpec[] {
  const inp = perm.input as { questions?: QuestionSpec[] } | undefined;
  if (inp && Array.isArray(inp.questions) && inp.questions.length > 0) {
    return inp.questions;
  }
  return [];
}

function isQuestionPerm(perm: PermCard): boolean {
  return perm.kind === 'question' || perm.tool === 'AskUserQuestion' || extractQuestions(perm).length > 0;
}

function isPlanPerm(perm: PermCard): boolean {
  return perm.kind === 'plan' || perm.tool === 'ExitPlanMode';
}

export interface ComposerProps {
  mode: 'thread' | 'draft';
  hostId: string;
  cwd: string;
  session?: SessionSummary | null;
  detail?: SessionDetail | null;
  transcriptItems?: TranscriptItem[];
  draftConfig?: DraftConfig;
  onDraftChange?: (patch: Partial<DraftConfig>) => void;
  onCreateDraft?: (text: string, attachments: PendingAttachment[]) => Promise<void>;
  openModelPickerSignal?: number;
}

export const Composer: React.FC<ComposerProps> = ({
  mode,
  hostId,
  cwd,
  session,
  detail,
  transcriptItems = [],
  draftConfig,
  onDraftChange,
  onCreateDraft,
  openModelPickerSignal = 0,
}) => {
  const {
    hosts,
    settings,
    updateSettings,
    composerChips,
    removeComposerChip,
    clearComposerChips,
    composerPrefill,
    setComposerPrefill,
    callHost,
    pushToast,
    openRightTab,
    setTerminalDrawerOpen,
    selectSession,
    openSettingsTab,
  } = useApp();

  const host = hosts[hostId] ?? null;
  const agents: AgentProfile[] = host?.agents ?? [];

  const [text, setText] = useState('');
  const [attachments, setAttachments] = useState<PendingAttachment[]>([]);
  const [sending, setSending] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [ctrlHeld, setCtrlHeld] = useState(false);
  const [stopCooldownUntil, setStopCooldownUntil] = useState(0);

  // Model picker popover state
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  const [modelSearch, setModelSearch] = useState('');
  const [pickerAgent, setPickerAgent] = useState('');

  // Autocomplete popover state
  const [acKind, setAcKind] = useState<'file' | 'slash' | 'skill' | null>(null);
  const [acQuery, setAcQuery] = useState('');
  const [acIndex, setAcIndex] = useState(0);
  const [fileMatches, setFileMatches] = useState<Array<{ path: string; dir?: boolean }>>([]);
  const [skills, setSkills] = useState<SkillEntry[]>([]);

  // Question card interactive state keyed by reqId
  const [questionSelections, setQuestionSelections] = useState<Record<string, Record<string, string[]>>>({});
  const [questionCustom, setQuestionCustom] = useState<Record<string, Record<string, string>>>({});
  const [permBusy, setPermBusy] = useState<string | null>(null);

  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const modelPickerRef = useRef<HTMLDivElement | null>(null);
  const draftSaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Restore saved draft when switching sessions
  useEffect(() => {
    if (mode === 'thread' && session) {
      setText(session.draft ?? '');
    }
  }, [mode, session?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // Consume external composer prefill (e.g. from fork/rewind/starter chips)
  useEffect(() => {
    if (composerPrefill) {
      setText((prev) => (prev ? `${prev}\n${composerPrefill}` : composerPrefill));
      setComposerPrefill('');
      setTimeout(() => textareaRef.current?.focus(), 20);
    }
  }, [composerPrefill, setComposerPrefill]);

  // Open model picker via keyboard shortcut
  useEffect(() => {
    if (openModelPickerSignal > 0) {
      setModelPickerOpen((v) => !v);
    }
  }, [openModelPickerSignal]);

  // Close model picker on outside click or Escape
  useEffect(() => {
    if (!modelPickerOpen) return;
    const onDown = (e: MouseEvent) => {
      if (modelPickerRef.current && !modelPickerRef.current.contains(e.target as Node)) {
        setModelPickerOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setModelPickerOpen(false);
    };
    window.addEventListener('mousedown', onDown);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('mousedown', onDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [modelPickerOpen]);

  // Track Ctrl key for live Steer/Queue flip when busy
  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if (e.key === 'Control') setCtrlHeld(true);
    };
    const up = (e: KeyboardEvent) => {
      if (e.key === 'Control') setCtrlHeld(false);
    };
    const blur = () => setCtrlHeld(false);
    window.addEventListener('keydown', down);
    window.addEventListener('keyup', up);
    window.addEventListener('blur', blur);
    return () => {
      window.removeEventListener('keydown', down);
      window.removeEventListener('keyup', up);
      window.removeEventListener('blur', blur);
    };
  }, []);

  // Auto-resize textarea
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(220, Math.max(44, el.scrollHeight))}px`;
  }, [text]);

  // Persist thread draft debounced
  const scheduleDraftSave = useCallback(
    (nextText: string) => {
      if (mode !== 'thread' || !session || !hostId) return;
      if (draftSaveTimer.current) clearTimeout(draftSaveTimer.current);
      draftSaveTimer.current = setTimeout(() => {
        void callHost(hostId, 'session.draft.set', { id: session.id, draft: nextText }).catch(() => {});
      }, 650);
    },
    [mode, session, hostId, callHost],
  );

  // Pending approvals/questions/plans from transcript
  const pendingPerms = useMemo(() => {
    if (mode !== 'thread') return [];
    return transcriptItems
      .filter((it) => it.kind === 'perm' && it.perm && !it.perm.chosen)
      .map((it) => it.perm as PermCard);
  }, [mode, transcriptItems]);

  // Queued follow-up messages from transcript
  const queuedItems = useMemo(() => {
    if (mode !== 'thread') return [];
    return transcriptItems.filter((it) => it.kind === 'user' && it.state === 'queued');
  }, [mode, transcriptItems]);

  const isBusy = mode === 'thread' && (session?.state === 'running' || session?.state === 'waiting');
  const isInlineShell = mode === 'thread' && text.startsWith('!') && text.trim().length > 1;

  // Effective delivery mode while busy (Ctrl flips queue <-> steer live)
  const effectiveDelivery = useMemo(() => {
    const base = settings.followUpBehavior;
    if (!isBusy) return '';
    if (!ctrlHeld) return base;
    return base === 'steer' ? 'queue' : 'steer';
  }, [settings.followUpBehavior, isBusy, ctrlHeld]);

  // Trigger @, /, $ autocomplete based on cursor word
  const updateAutocomplete = useCallback((value: string, caret: number) => {
    const before = value.slice(0, caret);
    // Slash command only at start of input
    if (/^\/[a-zA-Z0-9_-]*$/.test(before)) {
      setAcKind('slash');
      setAcQuery(before.slice(1).toLowerCase());
      setAcIndex(0);
      return;
    }
    const atMatch = before.match(/(?:^|\s)@([^\s@]*)$/);
    if (atMatch) {
      setAcKind('file');
      setAcQuery(atMatch[1]);
      setAcIndex(0);
      return;
    }
    const skillMatch = before.match(/(?:^|\s)\$([a-zA-Z0-9_-]*)$/);
    if (skillMatch) {
      setAcKind('skill');
      setAcQuery(skillMatch[1].toLowerCase());
      setAcIndex(0);
      return;
    }
    setAcKind(null);
  }, []);

  // Fetch file completions when '@' active
  useEffect(() => {
    if (acKind !== 'file' || !hostId || !cwd) return;
    let alive = true;
    const t = setTimeout(() => {
      void callHost<Array<{ path: string; dir?: boolean }>>(hostId, 'fs.complete', {
        cwd,
        query: acQuery,
        max: 20,
      })
        .then((res) => {
          if (alive) setFileMatches(Array.isArray(res) ? res : []);
        })
        .catch(() => {
          if (alive) setFileMatches([]);
        });
    }, 120);
    return () => {
      alive = false;
      clearTimeout(t);
    };
  }, [acKind, acQuery, hostId, cwd, callHost]);

  // Fetch skills when '$' active
  useEffect(() => {
    if (acKind !== 'skill' || !hostId) return;
    let alive = true;
    void callHost<SkillEntry[]>(hostId, 'skills.list', { cwd })
      .then((res) => {
        if (alive) setSkills(Array.isArray(res) ? res : []);
      })
      .catch(() => {
        if (alive) setSkills([]);
      });
    return () => {
      alive = false;
    };
  }, [acKind, hostId, cwd, callHost]);

  const slashMatches = useMemo(() => {
    if (acKind !== 'slash') return [];
    const agentCmds: Array<{ name: string; description: string; builtin?: boolean }> = (detail?.commands ?? []).map(
      (c: SlashCommand) => ({ name: c.name.replace(/^\//, ''), description: c.description }),
    );
    const merged = [...BUILTIN_SLASH, ...agentCmds.filter((c) => !BUILTIN_SLASH.some((b) => b.name === c.name))];
    return merged.filter(
      (c) => !acQuery || c.name.toLowerCase().includes(acQuery) || c.description.toLowerCase().includes(acQuery),
    );
  }, [acKind, acQuery, detail?.commands]);

  const skillMatches = useMemo(() => {
    if (acKind !== 'skill') return [];
    return skills.filter(
      (s) => !acQuery || s.name.toLowerCase().includes(acQuery) || (s.description ?? '').toLowerCase().includes(acQuery),
    );
  }, [acKind, acQuery, skills]);

  const acCount = acKind === 'file' ? fileMatches.length : acKind === 'slash' ? slashMatches.length : acKind === 'skill' ? skillMatches.length : 0;

  const acceptAutocomplete = useCallback(
    (idx: number) => {
      const el = textareaRef.current;
      const caret = el?.selectionStart ?? text.length;
      const before = text.slice(0, caret);
      const after = text.slice(caret);
      if (acKind === 'file' && fileMatches[idx]) {
        const replaced = before.replace(/@([^\s@]*)$/, `@${fileMatches[idx].path} `);
        setText(replaced + after);
        setAcKind(null);
        setTimeout(() => textareaRef.current?.focus(), 0);
      } else if (acKind === 'slash' && slashMatches[idx]) {
        setText(`/${slashMatches[idx].name} ${after.trimStart()}`);
        setAcKind(null);
        setTimeout(() => textareaRef.current?.focus(), 0);
      } else if (acKind === 'skill' && skillMatches[idx]) {
        const replaced = before.replace(/\$([a-zA-Z0-9_-]*)$/, `$${skillMatches[idx].name} `);
        setText(replaced + after);
        setAcKind(null);
        setTimeout(() => textareaRef.current?.focus(), 0);
      }
    },
    [acKind, fileMatches, slashMatches, skillMatches, text],
  );

  // Add files from input, paste, or drop (preserves PNG transparency by reading raw DataURL)
  const addFiles = useCallback(async (files: FileList | File[]) => {
    const arr = Array.from(files);
    const added: PendingAttachment[] = [];
    for (const f of arr) {
      try {
        const b64 = await readFileAsBase64(f);
        const isImg = f.type.startsWith('image/');
        added.push({
          id: `att-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
          type: isImg ? 'image' : 'file',
          name: f.name || (isImg ? 'pasted-image.png' : 'attachment.txt'),
          mime: f.type || (isImg ? 'image/png' : 'text/plain'),
          data: b64,
          previewUrl: isImg ? `data:${f.type || 'image/png'};base64,${b64}` : undefined,
          sizeBytes: f.size,
        });
      } catch {
        // ignore unreadable file
      }
    }
    if (added.length > 0) setAttachments((prev) => [...prev, ...added]);
  }, []);

  const onPaste = useCallback(
    (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
      const files = e.clipboardData?.files;
      if (files && files.length > 0) {
        e.preventDefault();
        void addFiles(files);
        return;
      }
      const pasted = e.clipboardData?.getData('text/plain') ?? '';
      if (new TextEncoder().encode(pasted).length >= LARGE_PASTE_BYTES) {
        e.preventDefault();
        const bytes = new TextEncoder().encode(pasted).length;
        setAttachments((prev) => [
          ...prev,
          {
            id: `paste-${Date.now()}`,
            type: 'file',
            name: `pasted-${Math.round(bytes / 1024)}kb.txt`,
            mime: 'text/plain',
            data: utf8ToBase64(pasted),
            sizeBytes: bytes,
          },
        ]);
        pushToast({ text: `Large paste (${Math.round(bytes / 1024)} KiB) attached as file`, level: 'info' });
      }
    },
    [addFiles, pushToast],
  );

  // Permission / Question / Plan actions
  const respondPermission = useCallback(
    async (perm: PermCard, optionId: string, answers?: Record<string, unknown>, message?: string) => {
      if (!session || !hostId) return;
      setPermBusy(perm.reqId);
      try {
        await callHost(hostId, 'session.permission', {
          id: session.id,
          reqId: perm.reqId,
          optionId,
          ...(answers ? { answers } : {}),
          ...(message ? { message } : {}),
        });
        if (message) {
          setText('');
          scheduleDraftSave('');
        }
      } catch (e) {
        pushToast({ text: `Permission response failed: ${(e as Error).message}`, level: 'error' });
      } finally {
        setPermBusy(null);
      }
    },
    [session, hostId, callHost, pushToast, scheduleDraftSave],
  );

  // Queue actions
  const handleQueueAction = useCallback(
    async (itemId: string, action: 'cancel' | 'steer', pullText?: string) => {
      if (!session || !hostId) return;
      try {
        await callHost(hostId, 'session.queue', { id: session.id, itemId, action });
        if (pullText) {
          setText(pullText);
          setTimeout(() => textareaRef.current?.focus(), 0);
        }
      } catch (e) {
        pushToast({ text: `Queue action failed: ${(e as Error).message}`, level: 'error' });
      }
    },
    [session, hostId, callHost, pushToast],
  );

  // Stop / cancel running turn with 500ms post-stop click guard
  const handleCancel = useCallback(async () => {
    if (!session || !hostId) return;
    setStopCooldownUntil(Date.now() + 500);
    try {
      await callHost(hostId, 'session.cancel', { id: session.id });
    } catch (e) {
      pushToast({ text: `Cancel failed: ${(e as Error).message}`, level: 'error' });
    }
  }, [session, hostId, callHost, pushToast]);

  // Main send handler
  const handleSend = useCallback(async () => {
    if (Date.now() < stopCooldownUntil) return;
    const rawText = text.trim();

    // Quick-approve first pending simple permission if user presses Enter with empty composer
    if (!rawText && attachments.length === 0 && composerChips.length === 0 && pendingPerms.length > 0) {
      const first = pendingPerms[0];
      if (!isQuestionPerm(first) && !isPlanPerm(first)) {
        const allowOpt = first.options.find((o) => o.kind === 'allow_once' || o.kind === 'allow') ?? first.options[0];
        if (allowOpt) {
          await respondPermission(first, allowOpt.optionId);
          return;
        }
      }
    }

    if (!rawText && attachments.length === 0 && composerChips.length === 0) return;

    // Handle built-in slash commands in thread mode
    if (mode === 'thread' && session && rawText.startsWith('/')) {
      const [cmdRaw, ...restParts] = rawText.slice(1).split(/\s+/);
      const cmd = cmdRaw.toLowerCase();
      const arg = restParts.join(' ').trim();
      if (cmd === 'btw') {
        setText('');
        scheduleDraftSave('');
        openRightTab({ id: 'side', kind: 'side', title: 'Side Chat' });
        if (arg) {
          try {
            await callHost(hostId, 'session.aside', { id: session.id, question: arg });
          } catch (e) {
            pushToast({ text: `Side chat failed: ${(e as Error).message}`, level: 'error' });
          }
        }
        return;
      }
      if (cmd === 'side') {
        setText('');
        openRightTab({ id: 'side', kind: 'side', title: 'Side Chat' });
        return;
      }
      if (cmd === 'diff') {
        setText('');
        openRightTab({ id: 'diff', kind: 'diff', title: 'Diff', diffScope: 'uncommitted' });
        return;
      }
      if (cmd === 'files') {
        setText('');
        openRightTab({ id: 'files', kind: 'files', title: 'Files' });
        return;
      }
      if (cmd === 'git') {
        setText('');
        openRightTab({ id: 'git', kind: 'git', title: 'Source Control' });
        return;
      }
      if (cmd === 'pr') {
        setText('');
        openRightTab({ id: 'pr', kind: 'pr', title: 'Pull Requests' });
        return;
      }
      if (cmd === 'terminal') {
        setText('');
        setTerminalDrawerOpen(true);
        return;
      }
      if (cmd === 'settle') {
        setText('');
        void callHost(hostId, 'session.settle', { id: session.id, settled: !session.settled });
        return;
      }
      if (cmd === 'fork') {
        setText('');
        try {
          const res = await callHost<{ session?: SessionSummary }>(hostId, 'session.fork', { id: session.id });
          if (res?.session) selectSession(hostId, res.session.id);
        } catch (e) {
          pushToast({ text: `Fork failed: ${(e as Error).message}`, level: 'error' });
        }
        return;
      }
    }

    // Handle inline `!command` execution in thread mode
    if (mode === 'thread' && session && rawText.startsWith('!') && rawText.length > 1) {
      const command = rawText.slice(1).trim();
      setSending(true);
      setText('');
      scheduleDraftSave('');
      try {
        await callHost(hostId, 'session.exec', { id: session.id, command });
      } catch (e) {
        pushToast({ text: `Inline command failed: ${(e as Error).message}`, level: 'error' });
      } finally {
        setSending(false);
      }
      return;
    }

    // Combine composer context chips into prompt text
    const chipBlock =
      composerChips.length > 0
        ? composerChips.map((c) => `[${c.kind}: ${c.label}]\n${c.content}`).join('\n\n') + '\n\n'
        : '';
    const finalPrompt = `${chipBlock}${rawText}`.trim();

    setSending(true);
    try {
      if (mode === 'draft') {
        if (!onCreateDraft) return;
        await onCreateDraft(finalPrompt, attachments);
        setText('');
        setAttachments([]);
        clearComposerChips();
      } else if (session) {
        await callHost(hostId, 'session.prompt', {
          id: session.id,
          text: finalPrompt,
          attachments: attachments.map((a) => ({
            type: a.type,
            name: a.name,
            mime: a.mime,
            data: a.data,
            path: a.path,
          })),
          ...(effectiveDelivery ? { delivery: effectiveDelivery } : {}),
        });
        setText('');
        setAttachments([]);
        clearComposerChips();
        scheduleDraftSave('');
      }
    } catch (e) {
      pushToast({ text: `Send failed: ${(e as Error).message}`, level: 'error' });
    } finally {
      setSending(false);
    }
  }, [
    stopCooldownUntil,
    text,
    attachments,
    composerChips,
    pendingPerms,
    respondPermission,
    mode,
    session,
    hostId,
    callHost,
    openRightTab,
    setTerminalDrawerOpen,
    selectSession,
    pushToast,
    onCreateDraft,
    clearComposerChips,
    effectiveDelivery,
    scheduleDraftSave,
  ]);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      // Autocomplete navigation
      if (acKind && acCount > 0) {
        if (e.key === 'ArrowDown') {
          e.preventDefault();
          setAcIndex((i) => (i + 1) % acCount);
          return;
        }
        if (e.key === 'ArrowUp') {
          e.preventDefault();
          setAcIndex((i) => (i - 1 + acCount) % acCount);
          return;
        }
        if (e.key === 'Tab' || e.key === 'Enter') {
          e.preventDefault();
          acceptAutocomplete(acIndex);
          return;
        }
        if (e.key === 'Escape') {
          e.preventDefault();
          setAcKind(null);
          return;
        }
      }

      // Alt+Up pulls last queued follow-up back into composer
      if (e.altKey && e.key === 'ArrowUp' && queuedItems.length > 0 && !text.trim()) {
        e.preventDefault();
        const last = queuedItems[queuedItems.length - 1];
        void handleQueueAction(last.id, 'cancel', last.text || '');
        return;
      }

      // Send shortcut
      const isModEnter = (e.ctrlKey || e.metaKey) && e.key === 'Enter';
      const isPlainEnter = e.key === 'Enter' && !e.shiftKey && !e.altKey && !e.ctrlKey && !e.metaKey;
      if (settings.sendShortcut === 'enter') {
        if (isPlainEnter || isModEnter) {
          e.preventDefault();
          void handleSend();
        }
      } else if (isModEnter) {
        e.preventDefault();
        void handleSend();
      }
    },
    [acKind, acCount, acIndex, acceptAutocomplete, queuedItems, text, handleQueueAction, settings.sendShortcut, handleSend],
  );

  // Model & agent info for picker button
  const currentAgentId = mode === 'thread' ? session?.agent || '' : draftConfig?.agent || agents.find((a) => a.available)?.id || '';
  const currentModelId = mode === 'thread' ? session?.model || detail?.models?.currentModelId || '' : draftConfig?.model || '';
  const currentModelLabel =
    (mode === 'thread'
      ? detail?.models?.availableModels.find((m) => m.modelId === currentModelId)?.name || session?.modelName || currentModelId
      : draftConfig?.model) || 'Default model';
  const currentAgentObj = agents.find((a) => a.id === currentAgentId);
  const currentEffort = mode === 'thread' ? session?.thought || 'medium' : draftConfig?.thought || 'medium';
  const currentSkipPerms = mode === 'thread' ? Boolean(session?.skipPermissions) : Boolean(draftConfig?.skipPermissions);
  const currentPermMode = currentSkipPerms ? 'auto' : (mode === 'thread' ? session?.mode : draftConfig?.mode) || 'supervised';

  const activePickerAgent = pickerAgent || currentAgentId || agents[0]?.id || '';
  const availableModels: ModelInfo[] = useMemo(() => {
    if (mode === 'thread' && activePickerAgent === currentAgentId && detail?.models?.availableModels?.length) {
      return detail.models.availableModels;
    }
    if (activePickerAgent === 'claude') {
      return [
        { modelId: 'claude-sonnet-4-5', name: 'Claude Sonnet 4.5', description: 'Balanced speed and coding intelligence' },
        { modelId: 'claude-opus-4-1', name: 'Claude Opus 4.1', description: 'Deep reasoning for hard architecture' },
        { modelId: 'claude-haiku-4-5', name: 'Claude Haiku 4.5', description: 'Ultra-fast lightweight edits' },
      ];
    }
    if (activePickerAgent === 'codex') {
      return [
        { modelId: 'gpt-5-codex', name: 'GPT-5 Codex', description: 'OpenAI Codex default model' },
        { modelId: 'o3', name: 'o3', description: 'Reasoning model' },
      ];
    }
    return [{ modelId: '', name: 'Provider Default', description: 'Use provider default model' }];
  }, [mode, activePickerAgent, currentAgentId, detail?.models]);

  const filteredModels = useMemo(() => {
    const q = modelSearch.trim().toLowerCase();
    const list = availableModels.filter(
      (m) => !q || m.name.toLowerCase().includes(q) || m.modelId.toLowerCase().includes(q),
    );
    const favs = new Set(settings.favoriteModels);
    return [...list].sort((a, b) => Number(favs.has(b.modelId)) - Number(favs.has(a.modelId)));
  }, [availableModels, modelSearch, settings.favoriteModels]);

  const toggleFavoriteModel = (modelId: string) => {
    if (!modelId) return;
    const set = new Set(settings.favoriteModels);
    if (set.has(modelId)) set.delete(modelId);
    else set.add(modelId);
    updateSettings({ favoriteModels: Array.from(set) });
  };

  const selectModel = async (agentId: string, modelId: string) => {
    if (mode === 'draft') {
      onDraftChange?.({ agent: agentId, model: modelId });
      setModelPickerOpen(false);
      return;
    }
    if (session && hostId && modelId) {
      try {
        await callHost(hostId, 'session.setModel', { id: session.id, model: modelId });
        setModelPickerOpen(false);
      } catch (e) {
        pushToast({ text: `Set model failed: ${(e as Error).message}`, level: 'error' });
      }
    }
  };

  const selectEffort = async (level: string) => {
    if (mode === 'draft') {
      onDraftChange?.({ thought: level });
      return;
    }
    if (session && hostId) {
      try {
        await callHost(hostId, 'session.setConfig', { id: session.id, configId: 'thought', value: level });
      } catch (e) {
        pushToast({ text: `Set effort failed: ${(e as Error).message}`, level: 'error' });
      }
    }
  };

  const selectPermMode = async (m: (typeof PERM_MODES)[number]) => {
    setModelPickerOpen(false);
    if (mode === 'draft') {
      onDraftChange?.({ mode: m.id, skipPermissions: m.skip });
      return;
    }
    if (session && hostId) {
      try {
        await callHost(hostId, 'session.setMode', { id: session.id, mode: m.id });
      } catch (e) {
        pushToast({ text: `Set mode failed: ${(e as Error).message}`, level: 'error' });
      }
    }
  };

  return (
    <div
      className="w-full mx-auto px-4 pb-3 pt-1"
      style={{ maxWidth: settings.chatMaxWidth + 48 }}
      onDragOver={(e) => {
        e.preventDefault();
        setDragOver(true);
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragOver(false);
        if (e.dataTransfer.files?.length) void addFiles(e.dataTransfer.files);
      }}
    >
      {/* Composer-Docked Action Deck: Pending Permissions / Questions / Plans */}
      {pendingPerms.map((perm) => {
        const isQ = isQuestionPerm(perm);
        const isP = isPlanPerm(perm);
        const busy = permBusy === perm.reqId;

        if (isQ) {
          const questions = extractQuestions(perm);
          const selForReq = questionSelections[perm.reqId] ?? {};
          const customForReq = questionCustom[perm.reqId] ?? {};
          return (
            <div
              key={perm.reqId}
              data-testid="perm-question-card"
              className="mb-2 rounded-xl border border-[var(--accent)]/50 bg-[var(--bg-surface)] p-3 shadow-md"
            >
              <div className="flex items-center gap-2 text-xs font-semibold text-[var(--accent)] mb-2">
                <Sparkles className="w-3.5 h-3.5" />
                <span>Agent Question</span>
                {perm.title && <span className="text-[var(--text-secondary)] font-normal">· {perm.title}</span>}
              </div>
              <div className="space-y-3">
                {questions.map((q, qi) => {
                  const key = q.header || q.question || `q${qi}`;
                  const chosen = selForReq[key] ?? [];
                  return (
                    <div key={key} className="space-y-1.5">
                      <div className="text-xs font-medium text-[var(--text-primary)]">{q.question}</div>
                      {q.options && q.options.length > 0 && (
                        <div className="flex flex-wrap gap-1.5">
                          {q.options.map((opt, oi) => {
                            const active = chosen.includes(opt.label);
                            return (
                              <button
                                key={opt.label}
                                type="button"
                                onClick={() => {
                                  setQuestionSelections((prev) => {
                                    const cur = prev[perm.reqId]?.[key] ?? [];
                                    const next = q.multiSelect
                                      ? cur.includes(opt.label)
                                        ? cur.filter((x) => x !== opt.label)
                                        : [...cur, opt.label]
                                      : [opt.label];
                                    return { ...prev, [perm.reqId]: { ...(prev[perm.reqId] ?? {}), [key]: next } };
                                  });
                                }}
                                className={`rounded-lg border px-2.5 py-1.5 text-left text-xs transition cursor-pointer ${
                                  active
                                    ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--text-primary)]'
                                    : 'border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                                }`}
                              >
                                <span className="font-mono text-[10px] opacity-60 mr-1.5">{oi + 1}</span>
                                <span className="font-medium">{opt.label}</span>
                                {opt.description && (
                                  <span className="block text-[11px] text-[var(--text-muted)] mt-0.5">{opt.description}</span>
                                )}
                              </button>
                            );
                          })}
                        </div>
                      )}
                      <input
                        type="text"
                        value={customForReq[key] ?? ''}
                        onChange={(e) =>
                          setQuestionCustom((prev) => ({
                            ...prev,
                            [perm.reqId]: { ...(prev[perm.reqId] ?? {}), [key]: e.target.value },
                          }))
                        }
                        placeholder="Or type a custom answer..."
                        className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1.5 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                      />
                    </div>
                  );
                })}
              </div>
              <div className="flex items-center justify-end gap-2 mt-3">
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    const rejectOpt = perm.options.find((o) => o.kind.startsWith('reject')) ?? perm.options[perm.options.length - 1];
                    if (rejectOpt) void respondPermission(perm, rejectOpt.optionId);
                  }}
                  className="rounded-lg border border-[var(--border)] px-3 py-1 text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  Dismiss
                </button>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    const answers: Record<string, string> = {};
                    questions.forEach((q, qi) => {
                      const key = q.header || q.question || `q${qi}`;
                      const parts = [...(selForReq[key] ?? [])];
                      if (customForReq[key]?.trim()) parts.push(customForReq[key].trim());
                      if (parts.length > 0) answers[q.question || key] = parts.join(', ');
                    });
                    const allowOpt = perm.options.find((o) => o.kind.startsWith('allow')) ?? perm.options[0];
                    if (allowOpt) void respondPermission(perm, allowOpt.optionId, answers);
                  }}
                  className="rounded-lg bg-[var(--accent)] px-3 py-1 text-xs font-semibold text-[var(--accent-fg)] hover:opacity-90 cursor-pointer"
                >
                  Submit Answer
                </button>
              </div>
            </div>
          );
        }

        if (isP) {
          const planText =
            typeof (perm.input as { plan?: string } | undefined)?.plan === 'string'
              ? (perm.input as { plan: string }).plan
              : perm.reason || '';
          return (
            <div
              key={perm.reqId}
              data-testid="perm-plan-card"
              className="mb-2 rounded-xl border border-[var(--warning)]/50 bg-[var(--bg-surface)] p-3 shadow-md"
            >
              <div className="flex items-center gap-2 text-xs font-semibold text-[var(--warning)] mb-2">
                <ListChecks className="w-4 h-4" />
                <span>Plan Review Ready</span>
              </div>
              {planText && (
                <div className="max-h-52 overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-2.5 text-xs mb-2.5">
                  <MarkdownRenderer text={planText} citable={false} />
                </div>
              )}
              <div className="flex items-center justify-end gap-2">
                {perm.options.map((opt) => {
                  const isReject = opt.kind.startsWith('reject');
                  return (
                    <button
                      key={opt.optionId}
                      type="button"
                      disabled={busy}
                      onClick={() => void respondPermission(perm, opt.optionId, undefined, isReject ? text.trim() : undefined)}
                      className={`rounded-lg px-3 py-1.5 text-xs font-semibold transition cursor-pointer ${
                        isReject
                          ? 'border border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                          : 'bg-[var(--accent)] text-[var(--accent-fg)] hover:opacity-90'
                      }`}
                    >
                      {isReject && text.trim() ? 'Request changes with feedback' : opt.name}
                    </button>
                  );
                })}
              </div>
            </div>
          );
        }

        // Standard Tool Approval Card
        const inpPreview =
          typeof perm.input === 'string'
            ? perm.input
            : perm.input
              ? JSON.stringify(perm.input, null, 2).slice(0, 400)
              : '';
        return (
          <div
            key={perm.reqId}
            data-testid="perm-approval-card"
            className="mb-2 rounded-xl border border-[var(--warning)]/50 bg-[var(--bg-surface)] p-3 shadow-md"
          >
            <div className="flex items-center justify-between gap-2 mb-1.5">
              <div className="flex items-center gap-2 text-xs font-semibold text-[var(--warning)]">
                <ShieldAlert className="w-4 h-4 shrink-0" />
                <span>Approval Required · {perm.tool || perm.toolKind || 'Tool'}</span>
                {perm.subagent && (
                  <span className="rounded bg-[var(--bg-elevated)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
                    subagent
                  </span>
                )}
              </div>
              {!text.trim() && (
                <span className="text-[10px] text-[var(--text-muted)]">Press Enter to allow · type to deny with feedback</span>
              )}
            </div>
            {perm.title && <div className="text-xs text-[var(--text-primary)] mb-1">{perm.title}</div>}
            {perm.blockedPath && (
              <div className="text-[11px] font-mono text-[var(--text-muted)] mb-1">Path: {perm.blockedPath}</div>
            )}
            {inpPreview && (
              <pre className="max-h-28 overflow-auto rounded-lg bg-[var(--bg-base)] border border-[var(--border)] p-2 text-[11px] font-mono text-[var(--text-secondary)] mb-2">
                {inpPreview}
              </pre>
            )}
            <div className="flex flex-wrap items-center justify-end gap-1.5">
              {perm.options.map((opt) => {
                const isReject = opt.kind.startsWith('reject');
                const isAlways = opt.kind === 'allow_always';
                return (
                  <button
                    key={opt.optionId}
                    type="button"
                    disabled={busy}
                    onClick={() => void respondPermission(perm, opt.optionId, undefined, isReject ? text.trim() : undefined)}
                    className={`rounded-lg px-3 py-1 text-xs font-medium transition cursor-pointer ${
                      isReject
                        ? 'border border-[var(--danger)]/40 text-[var(--danger)] hover:bg-[var(--danger)]/10'
                        : isAlways
                          ? 'border border-[var(--accent)]/50 bg-[var(--accent-soft)] text-[var(--accent)] hover:opacity-90'
                          : 'bg-[var(--success)] text-[#07150d] font-semibold hover:opacity-90'
                    }`}
                  >
                    {isReject && text.trim() ? 'Deny with feedback' : opt.name}
                  </button>
                );
              })}
            </div>
          </div>
        );
      })}

      {/* Queued Follow-ups Bar */}
      {queuedItems.length > 0 && (
        <div
          data-testid="queued-followups"
          className="mb-2 rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 space-y-1.5"
        >
          <div className="flex items-center justify-between text-[11px] text-[var(--text-muted)]">
            <span className="font-medium text-[var(--text-secondary)]">
              Queued follow-ups ({queuedItems.length})
            </span>
            <span>Alt+↑ to edit latest</span>
          </div>
          {queuedItems.map((q) => (
            <div
              key={q.id}
              className="flex items-center justify-between gap-2 rounded-lg bg-[var(--bg-elevated)] px-2.5 py-1.5 text-xs"
            >
              <span className="truncate text-[var(--text-primary)]">{q.text}</span>
              <div className="flex items-center gap-1 shrink-0">
                <button
                  type="button"
                  onClick={() => void handleQueueAction(q.id, 'steer')}
                  title="Steer now"
                  className="rounded px-1.5 py-0.5 text-[11px] text-[var(--accent)] hover:bg-[var(--accent-soft)] cursor-pointer"
                >
                  Steer
                </button>
                <button
                  type="button"
                  onClick={() => void handleQueueAction(q.id, 'cancel', q.text || '')}
                  title="Pull back to edit"
                  className="rounded px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  Edit
                </button>
                <button
                  type="button"
                  onClick={() => void handleQueueAction(q.id, 'cancel')}
                  title="Cancel queued message"
                  className="rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Main Composer Box */}
      <div
        className={`relative rounded-2xl border transition-colors bg-[var(--bg-surface)] shadow-sm ${
          dragOver
            ? 'border-[var(--accent)] ring-2 ring-[var(--accent)]/30'
            : isInlineShell
              ? 'border-[var(--warning)]/60'
              : 'border-[var(--border-strong)] focus-within:border-[var(--accent)]'
        }`}
      >
        {/* Autocomplete Popover */}
        {acKind && acCount > 0 && (
          <div className="absolute bottom-full left-2 right-2 mb-1.5 z-30 max-h-60 overflow-y-auto rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1 shadow-xl">
            <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
              {acKind === 'file' ? 'Mention file (@)' : acKind === 'slash' ? 'Commands (/)' : 'Skills ($)'}
            </div>
            {acKind === 'file' &&
              fileMatches.map((m, i) => (
                <button
                  key={m.path}
                  type="button"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    acceptAutocomplete(i);
                  }}
                  className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-xs cursor-pointer ${
                    i === acIndex ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                  }`}
                >
                  <FileCode2 className="w-3.5 h-3.5 shrink-0 text-[var(--accent)]" />
                  <span className="font-mono truncate">{m.path}</span>
                </button>
              ))}
            {acKind === 'slash' &&
              slashMatches.map((c, i) => (
                <button
                  key={c.name}
                  type="button"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    acceptAutocomplete(i);
                  }}
                  className={`flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-xs cursor-pointer ${
                    i === acIndex ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                  }`}
                >
                  <span className="font-mono font-medium text-[var(--accent)]">/{c.name}</span>
                  <span className="truncate text-[11px] text-[var(--text-muted)]">{c.description}</span>
                </button>
              ))}
            {acKind === 'skill' &&
              skillMatches.map((s, i) => (
                <button
                  key={s.path}
                  type="button"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    acceptAutocomplete(i);
                  }}
                  className={`flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-xs cursor-pointer ${
                    i === acIndex ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                  }`}
                >
                  <span className="font-medium text-[var(--accent)]">${s.name}</span>
                  <span className="truncate text-[11px] text-[var(--text-muted)]">{s.description || s.scope}</span>
                </button>
              ))}
          </div>
        )}

        {/* Inline !command indicator */}
        {isInlineShell && (
          <div className="flex items-center gap-1.5 px-3 pt-2 text-[11px] text-[var(--warning)]">
            <Terminal className="w-3.5 h-3.5" />
            <span>Inline shell mode: runs directly in <code className="font-mono">{cwd}</code></span>
          </div>
        )}

        {/* Context Chips + Attachments Strip */}
        {(composerChips.length > 0 || attachments.length > 0) && (
          <div className="flex flex-wrap items-center gap-1.5 px-3 pt-2.5">
            {composerChips.map((chip) => (
              <span
                key={chip.id}
                data-testid="composer-context-chip"
                className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent)]/40 bg-[var(--accent-soft)] px-2 py-0.5 text-[11px] text-[var(--text-primary)] max-w-xs"
              >
                <FileText className="w-3 h-3 shrink-0 text-[var(--accent)]" />
                <span className="truncate">{chip.label}</span>
                <button
                  type="button"
                  onClick={() => removeComposerChip(chip.id)}
                  className="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <X className="w-3 h-3" />
                </button>
              </span>
            ))}
            {attachments.map((att) => (
              <span
                key={att.id}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[11px] text-[var(--text-primary)] max-w-xs"
              >
                {att.previewUrl ? (
                  <img src={att.previewUrl} alt={att.name} className="w-4 h-4 rounded object-cover" />
                ) : att.type === 'image' ? (
                  <ImageIcon className="w-3 h-3 text-[var(--accent)]" />
                ) : (
                  <Paperclip className="w-3 h-3 text-[var(--text-muted)]" />
                )}
                <span className="truncate">{att.name}</span>
                <button
                  type="button"
                  onClick={() => setAttachments((prev) => prev.filter((x) => x.id !== att.id))}
                  className="text-[var(--text-muted)] hover:text-[var(--danger)] cursor-pointer"
                >
                  <X className="w-3 h-3" />
                </button>
              </span>
            ))}
          </div>
        )}

        {/* Textarea */}
        <textarea
          ref={textareaRef}
          data-testid="composer-textarea"
          value={text}
          onChange={(e) => {
            const v = e.target.value;
            setText(v);
            updateAutocomplete(v, e.target.selectionStart ?? v.length);
            scheduleDraftSave(v);
          }}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          rows={1}
          placeholder={
            mode === 'draft'
              ? 'Describe a task... (@ files, / commands, $ skills)'
              : isBusy
                ? `Type a follow-up (${effectiveDelivery === 'steer' ? 'Steer immediately' : 'Queue for next turn'} · hold Ctrl to flip)...`
                : 'Message agent... (@ files, / commands, $ skills, !shell)'
          }
          className="w-full resize-none bg-transparent px-3.5 pt-3 pb-2 text-[13.5px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-muted)] outline-none"
        />

        {/* Bottom Toolbar */}
        <div className="flex items-center justify-between gap-2 px-2.5 pb-2 pt-0.5">
          <div className="flex items-center gap-1.5 flex-wrap">
            {/* Model & Provider Picker */}
            <div className="relative" ref={modelPickerRef}>
              <button
                type="button"
                data-testid="model-picker-btn"
                onClick={() => {
                  setPickerAgent(currentAgentId);
                  setModelPickerOpen((v) => !v);
                }}
                className="flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-1 text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <Cpu className="w-3.5 h-3.5 text-[var(--accent)]" />
                <span className="font-medium text-[var(--text-primary)]">{currentAgentObj?.name || currentAgentId || 'Agent'}</span>
                <span className="text-[var(--text-muted)]">·</span>
                <span className="max-w-[140px] truncate">{currentModelLabel}</span>
                <ChevronDown className="w-3 h-3 opacity-60" />
              </button>

              {modelPickerOpen && (
                <div className="absolute bottom-full left-0 mb-2 z-40 w-[440px] max-w-[92vw] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)] shadow-2xl overflow-hidden">
                  <div className="grid grid-cols-[132px_1fr] min-h-[260px]">
                    {/* Provider Rail */}
                    <div className="border-r border-[var(--border)] bg-[var(--bg-sidebar)] p-2 space-y-1">
                      <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                        Providers
                      </div>
                      {agents.map((ag) => (
                        <button
                          key={ag.id}
                          type="button"
                          disabled={mode === 'thread' && ag.id !== currentAgentId}
                          onClick={() => {
                            setPickerAgent(ag.id);
                            if (mode === 'draft') onDraftChange?.({ agent: ag.id, model: '' });
                          }}
                          className={`flex w-full items-center justify-between gap-1.5 rounded-lg px-2 py-1.5 text-left text-xs transition cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed ${
                            activePickerAgent === ag.id
                              ? 'bg-[var(--accent-soft)] text-[var(--text-primary)] font-medium'
                              : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                          }`}
                        >
                          <span className="truncate">{ag.name}</span>
                          <span
                            className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                              ag.available ? 'bg-[var(--success)]' : 'bg-[var(--text-muted)]'
                            }`}
                          />
                        </button>
                      ))}
                      <button
                        type="button"
                        onClick={() => {
                          setModelPickerOpen(false);
                          openSettingsTab('providers');
                        }}
                        className="w-full mt-2 rounded-lg border border-dashed border-[var(--border)] px-2 py-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:border-[var(--border-strong)] cursor-pointer"
                      >
                        + Manage ACP
                      </button>
                    </div>

                    {/* Models + Effort + Permissions */}
                    <div className="flex flex-col p-2.5 gap-2">
                      <input
                        type="text"
                        value={modelSearch}
                        onChange={(e) => setModelSearch(e.target.value)}
                        placeholder="Search models..."
                        className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1 text-xs text-[var(--text-primary)] outline-none focus:border-[var(--accent)]"
                      />
                      <div className="max-h-36 overflow-y-auto space-y-0.5">
                        {filteredModels.map((m) => {
                          const isFav = settings.favoriteModels.includes(m.modelId);
                          const isSel = m.modelId === currentModelId && activePickerAgent === currentAgentId;
                          return (
                            <div
                              key={m.modelId || 'default'}
                              className={`flex items-center justify-between gap-1 rounded-lg px-2 py-1.5 text-xs ${
                                isSel ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]' : 'hover:bg-[var(--bg-hover)]'
                              }`}
                            >
                              <button
                                type="button"
                                onClick={() => void selectModel(activePickerAgent, m.modelId)}
                                className="flex-1 text-left min-w-0 cursor-pointer"
                              >
                                <div className="font-medium truncate flex items-center gap-1.5">
                                  {isSel && <Check className="w-3 h-3 text-[var(--accent)] shrink-0" />}
                                  <span>{m.name}</span>
                                </div>
                                {m.description && (
                                  <div className="text-[10px] text-[var(--text-muted)] truncate">{m.description}</div>
                                )}
                              </button>
                              {m.modelId && (
                                <button
                                  type="button"
                                  onClick={() => toggleFavoriteModel(m.modelId)}
                                  className="p-1 text-[var(--text-muted)] hover:text-[var(--warning)] cursor-pointer"
                                  title="Favorite model"
                                >
                                  <Star className={`w-3 h-3 ${isFav ? 'fill-[var(--warning)] text-[var(--warning)]' : ''}`} />
                                </button>
                              )}
                            </div>
                          );
                        })}
                      </div>

                      {/* Effort Level */}
                      <div className="border-t border-[var(--border)] pt-2">
                        <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)] mb-1">
                          Reasoning Effort
                        </div>
                        <div className="grid grid-cols-4 gap-1">
                          {EFFORT_LEVELS.map((lv) => (
                            <button
                              key={lv.id}
                              type="button"
                              onClick={() => void selectEffort(lv.id)}
                              className={`rounded-md py-1 text-[11px] font-medium cursor-pointer ${
                                currentEffort === lv.id
                                  ? 'bg-[var(--accent)] text-[var(--accent-fg)]'
                                  : 'bg-[var(--bg-elevated)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                              }`}
                            >
                              {lv.label}
                            </button>
                          ))}
                        </div>
                      </div>

                      {/* Permission Mode */}
                      <div className="border-t border-[var(--border)] pt-2">
                        <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)] mb-1">
                          Approval Mode
                        </div>
                        <div className="space-y-1">
                          {PERM_MODES.map((pm) => {
                            const active = currentPermMode === pm.id || (pm.id === 'auto' && currentSkipPerms);
                            return (
                              <button
                                key={pm.id}
                                type="button"
                                onClick={() => void selectPermMode(pm)}
                                className={`flex w-full items-center justify-between rounded-md px-2 py-1 text-left text-[11px] cursor-pointer ${
                                  active
                                    ? 'bg-[var(--accent-soft)] text-[var(--text-primary)]'
                                    : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                                }`}
                              >
                                <span className="font-medium">{pm.label}</span>
                                <span className="text-[10px] text-[var(--text-muted)]">{pm.desc}</span>
                              </button>
                            );
                          })}
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              )}
            </div>

            {/* Approval Mode Badge */}
            <button
              type="button"
              onClick={() => setModelPickerOpen(true)}
              title="Change permission mode"
              className="flex items-center gap-1 rounded-lg px-2 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {currentSkipPerms ? (
                <ShieldCheck className="w-3.5 h-3.5 text-[var(--warning)]" />
              ) : (
                <Shield className="w-3.5 h-3.5 text-[var(--success)]" />
              )}
              <span>{currentSkipPerms ? 'Full access' : currentPermMode === 'acceptEdits' ? 'Auto edits' : 'Supervised'}</span>
            </button>

            {/* Attach File Button */}
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              title="Attach files or images"
              className="flex items-center gap-1 rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <Paperclip className="w-3.5 h-3.5" />
            </button>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              className="hidden"
              onChange={(e) => {
                if (e.target.files?.length) void addFiles(e.target.files);
                e.target.value = '';
              }}
            />

            {/* Live Steer/Queue toggle while busy */}
            {isBusy && (
              <button
                type="button"
                onClick={() =>
                  updateSettings({ followUpBehavior: settings.followUpBehavior === 'steer' ? 'queue' : 'steer' })
                }
                title="Toggle follow-up delivery (hold Ctrl to invert temporarily)"
                className={`flex items-center gap-1 rounded-lg border px-2 py-0.5 text-[11px] cursor-pointer ${
                  effectiveDelivery === 'steer'
                    ? 'border-[var(--warning)]/50 bg-[var(--warning)]/10 text-[var(--warning)]'
                    : 'border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-secondary)]'
                }`}
              >
                {effectiveDelivery === 'steer' ? <Zap className="w-3 h-3" /> : <Layers className="w-3 h-3" />}
                <span>{effectiveDelivery === 'steer' ? 'Steer' : 'Queue'}</span>
              </button>
            )}
          </div>

          <div className="flex items-center gap-1.5">
            {isBusy && (
              <button
                type="button"
                data-testid="composer-stop-btn"
                onClick={() => void handleCancel()}
                title="Stop current turn"
                className="flex items-center gap-1 rounded-lg border border-[var(--danger)]/50 bg-[var(--danger)]/15 px-2.5 py-1 text-xs font-medium text-[var(--danger)] hover:bg-[var(--danger)]/25 cursor-pointer"
              >
                <Square className="w-3 h-3 fill-current" />
                <span>Stop</span>
              </button>
            )}
            <button
              type="button"
              data-testid="composer-send-btn"
              disabled={sending || (!text.trim() && attachments.length === 0 && composerChips.length === 0)}
              onClick={() => void handleSend()}
              title={settings.sendShortcut === 'enter' ? 'Send (Enter)' : 'Send (Ctrl+Enter)'}
              className="flex items-center gap-1 rounded-lg bg-[var(--accent)] px-2.5 py-1 text-xs font-semibold text-[var(--accent-fg)] hover:opacity-90 disabled:opacity-35 disabled:cursor-not-allowed cursor-pointer"
            >
              {isInlineShell ? <Play className="w-3.5 h-3.5" /> : <ArrowUp className="w-3.5 h-3.5" />}
              <span>{isInlineShell ? 'Run' : mode === 'draft' ? 'Start' : isBusy ? (effectiveDelivery === 'steer' ? 'Steer' : 'Queue') : 'Send'}</span>
            </button>
          </div>
        </div>
      </div>

      {/* Subtle shortcut footer */}
      <div className="mt-1 flex items-center justify-between px-1 text-[10.5px] text-[var(--text-muted)]">
        <span>
          <kbd className="font-mono">@</kbd> files · <kbd className="font-mono">/</kbd> commands · <kbd className="font-mono">$</kbd> skills · <kbd className="font-mono">!cmd</kbd> shell
        </span>
        <span className="flex items-center gap-1">
          <CornerDownLeft className="w-2.5 h-2.5" />
          {settings.sendShortcut === 'enter' ? 'Enter to send · Shift+Enter newline' : 'Ctrl+Enter to send'}
        </span>
      </div>
    </div>
  );
};
