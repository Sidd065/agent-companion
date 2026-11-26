import React, { useEffect, useState } from 'react';
import { AlertTriangle } from 'lucide-react';

export interface ConfirmDialogOptions {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
}

interface PendingConfirm extends ConfirmDialogOptions {
  resolve: (ok: boolean) => void;
}

type Listener = (next: PendingConfirm | null) => void;
let currentPending: PendingConfirm | null = null;
const listeners = new Set<Listener>();

function notify(next: PendingConfirm | null): void {
  currentPending = next;
  for (const fn of listeners) fn(next);
}

/**
 * Opens an accessible, themed confirmation modal instead of blocking native `window.confirm()`.
 */
export function confirmAction(opts: ConfirmDialogOptions): Promise<boolean> {
  if (currentPending) {
    currentPending.resolve(false);
  }
  return new Promise<boolean>((resolve) => {
    notify({ ...opts, resolve });
  });
}

export const ConfirmDialog: React.FC = () => {
  const [pending, setPending] = useState<PendingConfirm | null>(currentPending);

  useEffect(() => {
    const fn: Listener = (next) => setPending(next);
    listeners.add(fn);
    return () => {
      listeners.delete(fn);
    };
  }, []);

  useEffect(() => {
    if (!pending) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        const cur = pending;
        notify(null);
        cur.resolve(false);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        const cur = pending;
        notify(null);
        cur.resolve(true);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [pending]);

  if (!pending) return null;

  const close = (result: boolean) => {
    const cur = pending;
    notify(null);
    cur.resolve(result);
  };

  return (
    <div
      data-testid="confirm-dialog"
      onClick={() => close(false)}
      className="fixed inset-0 z-[70] flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
    >
      <div
        role="alertdialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-md rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-5 shadow-2xl space-y-4 text-xs"
      >
        <div className="flex items-start gap-3">
          <div
            className={`mt-0.5 rounded-xl p-2 shrink-0 ${
              pending.danger !== false
                ? 'bg-[var(--danger)]/15 text-[var(--danger)]'
                : 'bg-[var(--accent-soft)] text-[var(--accent)]'
            }`}
          >
            <AlertTriangle className="w-4 h-4" />
          </div>
          <div className="space-y-1 min-w-0">
            <h3 className="text-sm font-semibold text-[var(--text-primary)]">{pending.title}</h3>
            <p className="text-xs leading-relaxed text-[var(--text-secondary)] break-words">{pending.message}</p>
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 pt-1">
          <button
            type="button"
            data-testid="confirm-dialog-cancel"
            onClick={() => close(false)}
            className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-1.5 text-xs font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            {pending.cancelLabel || 'Cancel'}
          </button>
          <button
            type="button"
            autoFocus
            data-testid="confirm-dialog-accept"
            onClick={() => close(true)}
            className={`rounded-lg px-3.5 py-1.5 text-xs font-semibold cursor-pointer ${
              pending.danger !== false
                ? 'bg-[var(--danger)] text-white hover:opacity-90'
                : 'bg-[var(--accent)] text-[var(--accent-fg)] hover:opacity-90'
            }`}
          >
            {pending.confirmLabel || 'Confirm'}
          </button>
        </div>
      </div>
    </div>
  );
};
