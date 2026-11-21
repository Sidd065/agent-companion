import DOMPurify from 'dompurify';
import hljs from 'highlight.js';
import katex from 'katex';
import { marked } from 'marked';
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Quote } from 'lucide-react';
import { useApp } from '../../state/appStore.js';

function renderMathInText(src: string, enabled: boolean): string {
  if (!enabled) return src;
  // Protect fenced code blocks and inline code spans before math substitution.
  const placeholders: string[] = [];
  const hold = (s: string) => {
    const idx = placeholders.length;
    placeholders.push(s);
    return `@@CODEHOLD_${idx}@@`;
  };
  let out = src.replace(/```[\s\S]*?```/g, hold).replace(/`[^`\n]+`/g, hold);

  // Block math $$...$$
  out = out.replace(/\$\$([\s\S]+?)\$\$/g, (_m, expr: string) => {
    try {
      return hold(`<div class="my-2 overflow-x-auto">${katex.renderToString(expr.trim(), { displayMode: true, throwOnError: false })}</div>`);
    } catch {
      return _m;
    }
  });

  // Inline math $...$
  out = out.replace(/(^|[^\\$])\$([^\s$][^$\n]*?[^\s$]|[^\s$])\$/g, (_m, pre: string, expr: string) => {
    try {
      return pre + hold(katex.renderToString(expr.trim(), { displayMode: false, throwOnError: false }));
    } catch {
      return _m;
    }
  });

  // Restore placeholders
  return out.replace(/@@CODEHOLD_(\d+)@@/g, (_m, idx) => placeholders[Number(idx)] ?? '');
}

const renderer = new marked.Renderer();
renderer.code = ({ text, lang }) => {
  const safeLang = (lang || '').trim().toLowerCase();
  if (safeLang === 'mermaid') {
    const esc = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    return `<div class="mermaid-block my-2 rounded-lg border border-[var(--border)] bg-[var(--bg-sidebar)] p-3 overflow-x-auto"><pre class="mermaid">${esc}</pre></div>`;
  }
  let highlighted: string;
  try {
    if (safeLang && hljs.getLanguage(safeLang)) {
      highlighted = hljs.highlight(text, { language: safeLang }).value;
    } else {
      highlighted = hljs.highlightAuto(text).value;
    }
  } catch {
    highlighted = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
  const encodedRaw = encodeURIComponent(text);
  return `<div class="code-wrap relative group my-2 rounded-lg border border-[var(--border)] bg-[var(--bg-sidebar)] overflow-hidden">
    <div class="flex items-center justify-between px-3 py-1 text-[11px] text-[var(--text-muted)] border-b border-[var(--border)] bg-[var(--bg-surface)]">
      <span>${safeLang || 'code'}</span>
      <button type="button" data-copy-code="${encodedRaw}" class="copy-code-btn px-2 py-0.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] cursor-pointer">Copy</button>
    </div>
    <pre class="!m-0 !border-0 !rounded-none"><code class="hljs">${highlighted}</code></pre>
  </div>`;
};

marked.setOptions({ gfm: true, breaks: true, renderer });

export interface MarkdownRendererProps {
  text: string;
  citable?: boolean;
  className?: string;
}

export const MarkdownRenderer: React.FC<MarkdownRendererProps> = React.memo(({ text, citable = true, className = '' }) => {
  const { settings, addComposerChip, pushToast } = useApp();
  const containerRef = useRef<HTMLDivElement>(null);
  const [citeBtn, setCiteBtn] = useState<{ text: string; top: number; left: number } | null>(null);

  const html = useMemo(() => {
    const withMath = renderMathInText(text || '', settings.renderMath);
    const parsed = marked.parse(withMath) as string;
    return DOMPurify.sanitize(parsed, {
      ADD_ATTR: ['data-copy-code', 'target', 'rel'],
      ADD_TAGS: ['math', 'semantics', 'mrow', 'mi', 'mo', 'mn', 'msup', 'msub', 'mfrac', 'annotation'],
    });
  }, [text, settings.renderMath]);

  // Lazy-render mermaid blocks if present
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !el.querySelector('.mermaid')) return;
    let cancelled = false;
    void import('mermaid').then(async ({ default: mermaid }) => {
      if (cancelled) return;
      mermaid.initialize({
        startOnLoad: false,
        theme: document.documentElement.dataset.theme === 'light' ? 'default' : 'dark',
        securityLevel: 'strict',
      });
      const nodes = el.querySelectorAll<HTMLElement>('.mermaid');
      for (const n of Array.from(nodes)) {
        if (n.dataset.rendered) continue;
        n.dataset.rendered = '1';
        try {
          const { svg } = await mermaid.render(`mmd-${Math.random().toString(36).slice(2, 9)}`, n.textContent || '');
          n.innerHTML = svg;
        } catch {
          // leave raw mermaid text readable
        }
      }
    });
    return () => {
      cancelled = true;
    };
  }, [html]);

  const onClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      const target = e.target as HTMLElement;
      const copyBtn = target.closest<HTMLButtonElement>('[data-copy-code]');
      if (copyBtn) {
        const raw = decodeURIComponent(copyBtn.dataset.copyCode || '');
        void navigator.clipboard.writeText(raw);
        copyBtn.textContent = 'Copied!';
        setTimeout(() => {
          copyBtn.textContent = 'Copy';
        }, 1200);
        return;
      }
      const anchor = target.closest<HTMLAnchorElement>('a');
      if (anchor?.href && (anchor.href.startsWith('http://') || anchor.href.startsWith('https://'))) {
        e.preventDefault();
        if (window.agentcDesktop) {
          void window.agentcDesktop.openExternal(anchor.href);
        } else {
          window.open(anchor.href, '_blank', 'noopener,noreferrer');
        }
      }
    },
    [],
  );

  const onMouseUp = useCallback(() => {
    if (!citable) return;
    const sel = window.getSelection();
    const selected = sel?.toString().trim() ?? '';
    if (!selected || selected.length < 3 || !containerRef.current || !sel?.rangeCount) {
      setCiteBtn(null);
      return;
    }
    const range = sel.getRangeAt(0);
    if (!containerRef.current.contains(range.commonAncestorContainer)) {
      setCiteBtn(null);
      return;
    }
    const rect = range.getBoundingClientRect();
    const box = containerRef.current.getBoundingClientRect();
    setCiteBtn({
      text: selected.slice(0, 600),
      top: Math.max(0, rect.top - box.top - 34),
      left: Math.max(0, rect.left - box.left),
    });
  }, [citable]);

  return (
    <div className={`relative ${className}`} ref={containerRef} onClick={onClick} onMouseUp={onMouseUp}>
      {citeBtn && (
        <button
          type="button"
          onMouseDown={(e) => {
            e.preventDefault();
            addComposerChip({
              kind: 'citation',
              label: citeBtn.text.slice(0, 42) + (citeBtn.text.length > 42 ? '…' : ''),
              content: `> ${citeBtn.text.replace(/\n/g, '\n> ')}`,
            });
            setCiteBtn(null);
            window.getSelection()?.removeAllRanges();
            pushToast({ text: 'Citation added to composer', level: 'info' });
          }}
          style={{ top: citeBtn.top, left: citeBtn.left }}
          className="absolute z-20 flex items-center gap-1.5 rounded-md border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-2.5 py-1 text-xs font-medium text-[var(--text-primary)] shadow-lg hover:bg-[var(--bg-hover)] cursor-pointer"
        >
          <Quote className="w-3.5 h-3.5 text-[var(--accent)]" />
          Cite in composer
        </button>
      )}
      <div className="md-prose" dangerouslySetInnerHTML={{ __html: html }} />
    </div>
  );
});
