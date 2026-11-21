import React, { useState } from 'react';
import { ExternalLink, Monitor, RefreshCw, Smartphone, Tablet } from 'lucide-react';
import type { RightTab } from '../../state/appStore.js';

type Viewport = 'desktop' | 'tablet' | 'mobile';

export const BrowserPanel: React.FC<{ tab: RightTab }> = ({ tab }) => {
  const [urlInput, setUrlInput] = useState(tab.browserUrl || 'http://127.0.0.1:3000');
  const [loadedUrl, setLoadedUrl] = useState(tab.browserUrl || 'http://127.0.0.1:3000');
  const [reloadNonce, setReloadNonce] = useState(0);
  const [viewport, setViewport] = useState<Viewport>('desktop');

  const navigate = (raw: string) => {
    const trimmed = raw.trim();
    if (!trimmed) return;
    const normalized = /^https?:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
    setUrlInput(normalized);
    setLoadedUrl(normalized);
    setReloadNonce((n) => n + 1);
  };

  const widthStyle =
    viewport === 'mobile' ? '390px' : viewport === 'tablet' ? '768px' : '100%';

  return (
    <div data-testid="browser-panel" className="flex-1 flex flex-col min-h-0 text-xs">
      <div className="flex items-center gap-1.5 border-b border-[var(--border)] bg-[var(--bg-sidebar)] px-2.5 py-1.5">
        <button
          type="button"
          onClick={() => setReloadNonce((n) => n + 1)}
          title="Reload"
          className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
        >
          <RefreshCw className="w-3.5 h-3.5" />
        </button>
        <input
          type="text"
          value={urlInput}
          onChange={(e) => setUrlInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') navigate(urlInput);
          }}
          placeholder="http://127.0.0.1:3000"
          className="flex-1 rounded-md border border-[var(--border)] bg-[var(--bg-base)] px-2.5 py-1 font-mono text-[11px] outline-none focus:border-[var(--accent)]"
        />
        <div className="flex items-center gap-0.5 rounded-md border border-[var(--border)] p-0.5">
          {(
            [
              ['desktop', Monitor],
              ['tablet', Tablet],
              ['mobile', Smartphone],
            ] as const
          ).map(([vp, Icon]) => (
            <button
              key={vp}
              type="button"
              onClick={() => setViewport(vp)}
              className={`rounded p-1 cursor-pointer ${
                viewport === vp ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--text-muted)]'
              }`}
            >
              <Icon className="w-3.5 h-3.5" />
            </button>
          ))}
        </div>
        <button
          type="button"
          onClick={() => {
            if (window.agentcDesktop) void window.agentcDesktop.openExternal(loadedUrl);
            else window.open(loadedUrl, '_blank');
          }}
          title="Open in external browser"
          className="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] cursor-pointer"
        >
          <ExternalLink className="w-3.5 h-3.5" />
        </button>
      </div>

      <div className="flex-1 overflow-auto bg-[var(--bg-base)] flex justify-center p-2">
        <iframe
          key={`${loadedUrl}-${reloadNonce}`}
          src={loadedUrl}
          title="Embedded Preview"
          style={{ width: widthStyle }}
          className="h-full rounded-lg border border-[var(--border)] bg-white"
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
        />
      </div>
    </div>
  );
};
