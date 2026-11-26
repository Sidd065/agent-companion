import DOMPurify from 'dompurify';
import hljs from 'highlight.js';
import katex from 'katex';
import { marked } from 'marked';
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Quote, X } from 'lucide-react';
import { useApp } from '../../state/appStore.js';
import { BUILTIN_THEMES } from '../../state/themes.js';
import {
  isFenceClose,
  parseFenceOpen,
  parseGitHubAlertBlockquote,
  parseLocalFileLinkTarget,
  splitCarouselSlides,
} from '../../utils/sessionHelpers.js';

const VIDEO_EXT_RE = /\.(mp4|webm|mov|mkv|m4v)(?:[?#].*)?$/i;

// Allow standard web schemes PLUS file:// so Antigravity/Claude clickable file links are preserved!
const SAFE_URI_REGEXP = /^(?:(?:https?|mailto|tel|file):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i;

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function safeDecodeURIComponent(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

function normalizePosixPath(p: string): string {
  const isAbs = p.startsWith('/');
  const parts = p.split('/');
  const stack: string[] = [];
  for (const seg of parts) {
    if (!seg || seg === '.') continue;
    if (seg === '..') {
      stack.pop();
    } else {
      stack.push(seg);
    }
  }
  return (isAbs ? '/' : '') + stack.join('/');
}

function resolveLocalMediaOrLink(rawHref: string, baseDir?: string): string | null {
  const src = (rawHref || '').trim();
  if (!src || /^(https?:|data:|blob:|mailto:|tel:|#)/i.test(src)) {
    return null;
  }
  if (src.startsWith('file://')) {
    const withoutScheme = src.slice('file://'.length).split('#')[0] || '';
    return safeDecodeURIComponent(withoutScheme);
  }
  if (src.startsWith('/')) {
    return safeDecodeURIComponent(src.split('#')[0] || src);
  }
  if (baseDir && baseDir.startsWith('/')) {
    const cleanRel = safeDecodeURIComponent(src.split('#')[0] || src);
    return normalizePosixPath(`${baseDir}/${cleanRel}`);
  }
  return null;
}

function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function renderMathInText(src: string, enabled: boolean): string {
  if (!enabled) return src;
  const placeholders: string[] = [];
  const hold = (s: string) => {
    const idx = placeholders.length;
    placeholders.push(s);
    return `@@CODEHOLD_${idx}@@`;
  };
  let out = src
    .replace(/`{3,}[\s\S]*?`{3,}/g, hold)
    .replace(/~{3,}[\s\S]*?~{3,}/g, hold)
    .replace(/`[^`\n]+`/g, hold);

  out = out.replace(/\$\$([\s\S]+?)\$\$/g, (_m, expr: string) => {
    try {
      return hold(
        `<div class="my-2 overflow-x-auto">${katex.renderToString(expr.trim(), { displayMode: true, throwOnError: false })}</div>`,
      );
    } catch {
      return _m;
    }
  });

  out = out.replace(/(^|[^\\$])\$([^\s$][^$\n]*?[^\s$]|[^\s$])\$/g, (_m, pre: string, expr: string) => {
    try {
      return pre + hold(katex.renderToString(expr.trim(), { displayMode: false, throwOnError: false }));
    } catch {
      return _m;
    }
  });

  return out.replace(/@@CODEHOLD_(\d+)@@/g, (_m, idx) => placeholders[Number(idx)] ?? '');
}

function createScopedRenderer(baseDir?: string) {
  const mdRenderer = new marked.Renderer();

  mdRenderer.code = ({ text, lang }) => {
    const safeLang = (lang || '').trim().toLowerCase();
    if (safeLang === 'mermaid') {
      const esc = escapeHtml(text);
      const encodedSrc = encodeURIComponent(text);
      return `<div class="mermaid-block my-2 rounded-lg border border-[var(--border)] bg-[var(--bg-sidebar)] p-3 overflow-x-auto"><pre class="mermaid" data-mermaid-src="${encodedSrc}">${esc}</pre></div>`;
    }
    if (safeLang === 'carousel') {
      return renderCarouselHtml(text, true, baseDir);
    }
    let highlighted: string;
    try {
      if (safeLang && hljs.getLanguage(safeLang)) {
        highlighted = hljs.highlight(text, { language: safeLang }).value;
      } else {
        highlighted = hljs.highlightAuto(text).value;
      }
    } catch {
      highlighted = escapeHtml(text);
    }
    const encodedRaw = encodeURIComponent(text);
    return `<div class="code-wrap relative group my-2 rounded-lg border border-[var(--border)] bg-[var(--bg-sidebar)] overflow-hidden">
      <div class="flex items-center justify-between px-3 py-1 text-[11px] text-[var(--text-muted)] border-b border-[var(--border)] bg-[var(--bg-surface)]">
        <span>${escapeHtml(safeLang || 'code')}</span>
        <button type="button" data-copy-code="${encodedRaw}" class="copy-code-btn px-2 py-0.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)] cursor-pointer">Copy</button>
      </div>
      <pre class="!m-0 !border-0 !rounded-none"><code class="hljs">${highlighted}</code></pre>
    </div>`;
  };

  mdRenderer.image = ({ href, title, text }) => {
    const src = (href || '').trim();
    const alt = escapeHtml(text || '');
    const titleAttr = title ? ` title="${escapeHtml(title)}"` : '';
    const localPath = resolveLocalMediaOrLink(src, baseDir);

    if (localPath) {
      const encPath = escapeHtml(localPath);
      if (VIDEO_EXT_RE.test(localPath)) {
        return `<div class="my-2 rounded-xl border border-[var(--border)] bg-[var(--bg-sidebar)] p-2.5 space-y-1.5">
          <video controls loop preload="metadata" data-remote-path="${encPath}" class="w-full max-h-[420px] rounded-lg bg-black"></video>
          ${alt ? `<div class="text-[11px] text-[var(--text-muted)]">${alt}</div>` : ''}
        </div>`;
      }
      return `<figure class="my-2 space-y-1">
        <div data-media-slot="1" class="inline-block max-w-full rounded-lg border border-[var(--border)] bg-[var(--bg-sidebar)]/60 overflow-hidden">
          <img alt="${alt}"${titleAttr} data-remote-path="${encPath}" data-zoomable="1" class="max-w-full max-h-[460px] object-contain cursor-zoom-in block min-h-[48px] min-w-[120px] animate-pulse bg-[var(--bg-elevated)]" />
        </div>
        ${alt ? `<figcaption class="text-[11px] text-[var(--text-muted)]">${alt}</figcaption>` : ''}
      </figure>`;
    }

    return `<img src="${escapeHtml(src)}" alt="${alt}"${titleAttr} data-zoomable="1" class="max-w-full rounded-lg my-2 cursor-zoom-in" />`;
  };

  return mdRenderer;
}

function renderMarkdownFragment(src: string, renderMath: boolean, baseDir?: string): string {
  const withMath = renderMathInText(src || '', renderMath);
  const renderer = createScopedRenderer(baseDir);
  return marked.parse(withMath, { gfm: true, breaks: true, renderer }) as string;
}

function renderAlertHtml(
  kind: 'NOTE' | 'TIP' | 'IMPORTANT' | 'WARNING' | 'CAUTION',
  body: string,
  renderMath: boolean,
  baseDir?: string,
): string {
  const palette: Record<string, { border: string; badge: string; label: string }> = {
    NOTE: { border: 'border-[var(--accent)]', badge: 'text-[var(--accent)]', label: 'Note' },
    TIP: { border: 'border-[var(--success)]', badge: 'text-[var(--success)]', label: 'Tip' },
    IMPORTANT: { border: 'border-violet-400', badge: 'text-violet-400', label: 'Important' },
    WARNING: { border: 'border-[var(--warning)]', badge: 'text-[var(--warning)]', label: 'Warning' },
    CAUTION: { border: 'border-[var(--danger)]', badge: 'text-[var(--danger)]', label: 'Caution' },
  };
  const st = palette[kind] ?? palette.NOTE;
  const inner = body ? renderMarkdownFragment(body, renderMath, baseDir) : '';
  return `<div data-testid="md-github-alert" class="my-2.5 rounded-xl border border-[var(--border)] border-l-4 ${st.border} bg-[var(--bg-surface)]/85 px-3.5 py-2.5 space-y-1">
    <div class="text-[11px] font-semibold uppercase tracking-wider ${st.badge}">${st.label}</div>
    <div class="text-xs leading-relaxed">${inner}</div>
  </div>`;
}

function renderCarouselHtml(body: string, renderMath: boolean, baseDir?: string): string {
  const slides = splitCarouselSlides(body);
  if (slides.length === 0) return '';
  const slidesHtml = slides
    .map(
      (s, idx) =>
        `<div data-carousel-slide="${idx}" class="${idx === 0 ? 'block' : 'hidden'}">${renderMarkdownFragment(s, renderMath, baseDir)}</div>`,
    )
    .join('');

  return `<div data-testid="md-carousel" data-carousel-index="0" data-carousel-total="${slides.length}" class="agentc-carousel my-2.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-surface)]/80 p-3 space-y-2">
    <div class="flex items-center justify-between gap-2 border-b border-[var(--border)] pb-1.5 text-[11px] text-[var(--text-muted)]">
      <span class="font-semibold uppercase tracking-wider">Carousel · <span data-carousel-status="1">1 / ${slides.length}</span></span>
      ${
        slides.length > 1
          ? `<div class="flex items-center gap-1">
              <button type="button" data-carousel-nav="-1" class="rounded border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[11px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer">Prev</button>
              <button type="button" data-carousel-nav="1" class="rounded border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-0.5 text-[11px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer">Next</button>
            </div>`
          : ''
      }
    </div>
    <div class="agentc-carousel-slides">${slidesHtml}</div>
  </div>`;
}

function buildRichMarkdownHtml(raw: string, renderMath: boolean, baseDir?: string): string {
  const lines = (raw || '').replace(/\r\n/g, '\n').split('\n');
  const chunks: string[] = [];
  const mdBuf: string[] = [];
  const flushMd = () => {
    const joined = mdBuf.join('\n');
    if (joined.trim()) {
      chunks.push(renderMarkdownFragment(joined, renderMath, baseDir));
    }
    mdBuf.length = 0;
  };

  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    const t = line.trimStart();
    const indent = line.length - t.length;
    if (indent <= 3) {
      const open = parseFenceOpen(t);
      if (open) {
        let j = i + 1;
        while (j < lines.length && !isFenceClose(lines[j]!.trimStart(), open)) j++;
        if (open.lang === 'carousel') {
          flushMd();
          const inner = lines.slice(i + 1, Math.min(lines.length, j)).join('\n');
          chunks.push(renderCarouselHtml(inner, renderMath, baseDir));
          i = j + 1;
          continue;
        }
        for (let k = i; k <= Math.min(lines.length - 1, j); k++) mdBuf.push(lines[k]!);
        i = j + 1;
        continue;
      }
      if (t.startsWith('>')) {
        let j = i;
        while (j + 1 < lines.length && lines[j + 1]!.trimStart().startsWith('>')) j++;
        const rawQuote = lines.slice(i, j + 1).join('\n');
        const alert = parseGitHubAlertBlockquote(rawQuote);
        if (alert) {
          flushMd();
          chunks.push(renderAlertHtml(alert.kind, alert.body, renderMath, baseDir));
          i = j + 1;
          continue;
        }
      }
    }
    mdBuf.push(line);
    i++;
  }
  flushMd();
  return chunks.join('\n');
}

export interface MarkdownRendererProps {
  text: string;
  citable?: boolean;
  className?: string;
  baseDir?: string;
  hostId?: string;
}

export const MarkdownRenderer: React.FC<MarkdownRendererProps> = React.memo(
  ({ text, citable = true, className = '', baseDir, hostId }) => {
    const {
      settings,
      activeHostId,
      activeSessionId,
      callHost,
      openRightTab,
      addComposerChip,
      pushToast,
    } = useApp();
    const effectiveHostId = hostId || activeHostId;
    const containerRef = useRef<HTMLDivElement>(null);
    const [citeBtn, setCiteBtn] = useState<{ text: string; top: number; left: number } | null>(null);
    const [lightbox, setLightbox] = useState<{ src: string; alt: string } | null>(null);
    const [carouselSlideTick, setCarouselSlideTick] = useState(0);

    const isLightTheme = useMemo(() => {
      const found = BUILTIN_THEMES.find((t) => t.id === settings.themeId);
      if (found) return !found.isDark;
      return typeof document !== 'undefined' && document.documentElement.dataset.theme === 'light';
    }, [settings.themeId]);

    const html = useMemo(() => {
      const built = buildRichMarkdownHtml(text || '', settings.renderMath, baseDir);
      return DOMPurify.sanitize(built, {
        ALLOWED_URI_REGEXP: SAFE_URI_REGEXP,
        ADD_ATTR: [
          'data-copy-code',
          'data-remote-path',
          'data-mermaid-src',
          'data-zoomable',
          'data-media-slot',
          'data-carousel-slide',
          'data-carousel-index',
          'data-carousel-total',
          'data-carousel-status',
          'data-carousel-nav',
          'data-testid',
          'target',
          'rel',
          'controls',
          'loop',
          'preload',
          'viewBox',
          'd',
          'xmlns',
          'preserveAspectRatio',
          'stroke',
          'fill',
          'stroke-width',
        ],
        ADD_TAGS: [
          'video',
          'figure',
          'figcaption',
          'svg',
          'path',
          'line',
          'rect',
          'g',
          'math',
          'semantics',
          'mrow',
          'mi',
          'mo',
          'mn',
          'msup',
          'msub',
          'msubsup',
          'mfrac',
          'msqrt',
          'mroot',
          'munderover',
          'munder',
          'mover',
          'mtable',
          'mtr',
          'mtd',
          'mtext',
          'mspace',
          'annotation',
        ],
      });
    }, [text, settings.renderMath, baseDir]);

    // Lazy-render mermaid blocks and re-render cleanly whenever light/dark theme changes!
    useEffect(() => {
      const el = containerRef.current;
      if (!el || !el.querySelector('.mermaid')) return;
      let cancelled = false;
      void import('mermaid').then(async ({ default: mermaid }) => {
        if (cancelled) return;
        const mmdTheme = isLightTheme ? 'default' : 'dark';
        mermaid.initialize({
          startOnLoad: false,
          theme: mmdTheme,
          securityLevel: 'strict',
        });
        const nodes = el.querySelectorAll<HTMLElement>('.mermaid');
        for (const n of Array.from(nodes)) {
          if (n.dataset.renderedTheme === mmdTheme) continue;
          const rawCode = n.dataset.mermaidSrc
            ? safeDecodeURIComponent(n.dataset.mermaidSrc)
            : n.textContent || '';
          if (!rawCode.trim()) continue;
          n.dataset.renderedTheme = mmdTheme;
          try {
            const { svg } = await mermaid.render(
              `mmd-${Math.random().toString(36).slice(2, 9)}`,
              rawCode,
            );
            if (!cancelled) n.innerHTML = svg;
          } catch {
            n.textContent = rawCode;
          }
        }
      });
      return () => {
        cancelled = true;
      };
    }, [html, isLightTheme]);

    // Hydrate visible remote host images & videos over encrypted Noise RPC
    const blobUrlsRef = useRef<string[]>([]);
    useEffect(() => {
      return () => {
        for (const u of blobUrlsRef.current) URL.revokeObjectURL(u);
        blobUrlsRef.current = [];
      };
    }, [html, effectiveHostId]);

    useEffect(() => {
      const el = containerRef.current;
      if (!el || !effectiveHostId) return;
      const mediaNodes = el.querySelectorAll<HTMLImageElement | HTMLVideoElement>('[data-remote-path]');
      if (mediaNodes.length === 0) return;
      let cancelled = false;

      const loadMedia = async (node: HTMLImageElement | HTMLVideoElement) => {
        // Lazy-load hidden carousel slides only when they become visible
        const parentSlide = node.closest<HTMLElement>('[data-carousel-slide]');
        if (parentSlide && parentSlide.classList.contains('hidden')) return;

        const remotePath = node.dataset.remotePath;
        if (!remotePath || node.dataset.loadedHost === effectiveHostId) return;
        node.dataset.loadedHost = effectiveHostId;
        try {
          const chunks: Uint8Array[] = [];
          let offset = 0;
          let mime = '';
          while (!cancelled && offset < 32 * 1024 * 1024) {
            const res = await callHost<{
              data: string;
              eof: boolean;
              mime?: string;
            }>(effectiveHostId, 'fs.download', {
              path: remotePath,
              offset,
              length: 256 * 1024,
            });
            const bytes = res.data ? base64ToBytes(res.data) : new Uint8Array(0);
            if (bytes.length > 0) chunks.push(bytes);
            offset += bytes.length;
            if (res.mime) mime = res.mime;
            if (res.eof || bytes.length === 0) break;
          }
          if (cancelled) return;
          if (chunks.length === 0) throw new Error('Empty file');
          const total = chunks.reduce((acc, c) => acc + c.length, 0);
          const merged = new Uint8Array(total);
          let pos = 0;
          for (const c of chunks) {
            merged.set(c, pos);
            pos += c.length;
          }
          const blob = new Blob([merged], { type: mime || 'application/octet-stream' });
          const url = URL.createObjectURL(blob);
          blobUrlsRef.current.push(url);
          node.classList.remove('animate-pulse', 'min-h-[48px]', 'min-w-[120px]', 'bg-[var(--bg-elevated)]');
          node.src = url;
        } catch {
          if (cancelled) return;
          node.classList.remove('animate-pulse');
          const slot = node.closest<HTMLElement>('[data-media-slot]');
          const label = node.getAttribute('alt') || remotePath.split('/').pop() || remotePath;
          if (slot) {
            slot.innerHTML = `<div class="px-3 py-2 text-[11px] text-[var(--text-muted)] font-mono flex items-center gap-2"><span>Media unavailable:</span><span class="truncate">${escapeHtml(label)}</span></div>`;
          }
        }
      };

      for (const node of Array.from(mediaNodes)) {
        void loadMedia(node);
      }

      return () => {
        cancelled = true;
      };
    }, [html, effectiveHostId, carouselSlideTick, callHost]);

    useEffect(() => {
      if (!lightbox) return;
      const onKey = (e: KeyboardEvent) => {
        if (e.key === 'Escape') setLightbox(null);
      };
      window.addEventListener('keydown', onKey);
      return () => window.removeEventListener('keydown', onKey);
    }, [lightbox]);

    const onClick = useCallback(
      (e: React.MouseEvent<HTMLDivElement>) => {
        const target = e.target as HTMLElement;
        const copyBtn = target.closest<HTMLButtonElement>('[data-copy-code]');
        if (copyBtn) {
          const raw = safeDecodeURIComponent(copyBtn.dataset.copyCode || '');
          void navigator.clipboard.writeText(raw);
          copyBtn.textContent = 'Copied!';
          setTimeout(() => {
            copyBtn.textContent = 'Copy';
          }, 1200);
          return;
        }

        const navBtn = target.closest<HTMLButtonElement>('[data-carousel-nav]');
        if (navBtn) {
          const carousel = navBtn.closest<HTMLElement>('.agentc-carousel');
          if (carousel) {
            const total = Number(carousel.dataset.carouselTotal || '1');
            const cur = Number(carousel.dataset.carouselIndex || '0');
            const delta = Number(navBtn.dataset.carouselNav || '0');
            const next = (cur + delta + total) % total;
            carousel.dataset.carouselIndex = String(next);
            const statusEl = carousel.querySelector<HTMLElement>('[data-carousel-status]');
            if (statusEl) statusEl.textContent = `${next + 1} / ${total}`;
            carousel.querySelectorAll<HTMLElement>('[data-carousel-slide]').forEach((sl) => {
              const isMatch = Number(sl.dataset.carouselSlide) === next;
              sl.classList.toggle('hidden', !isMatch);
              sl.classList.toggle('block', isMatch);
            });
            setCarouselSlideTick((t) => t + 1);
          }
          return;
        }

        const zoomImg = target.closest<HTMLImageElement>('img[data-zoomable]');
        if (zoomImg && zoomImg.src && !zoomImg.closest('a')) {
          setLightbox({ src: zoomImg.src, alt: zoomImg.alt || '' });
          return;
        }

        const anchor = target.closest<HTMLAnchorElement>('a');
        const rawHref = anchor?.getAttribute('href') || '';
        if (anchor && rawHref) {
          const resolvedRel =
            !rawHref.startsWith('file://') && !rawHref.startsWith('/') && baseDir
              ? (() => {
                  const p = resolveLocalMediaOrLink(rawHref, baseDir);
                  const frag = rawHref.includes('#') ? `#${rawHref.split('#')[1]}` : '';
                  return p ? `file://${p}${frag}` : rawHref;
                })()
              : rawHref;

          const localTarget = parseLocalFileLinkTarget(resolvedRel);
          if (localTarget) {
            e.preventDefault();
            const isArtifact =
              localTarget.line === 0 &&
              activeSessionId &&
              (localTarget.path.includes(`/.gemini/antigravity-cli/brain/${activeSessionId}/`) ||
                localTarget.path.includes('/.claude/plans/'));
            if (isArtifact) {
              openRightTab({
                id: 'artifacts',
                kind: 'artifacts',
                title: 'Artifacts',
                hostId: effectiveHostId,
                openFilePath: localTarget.path,
              });
            } else {
              openRightTab({
                id: 'files',
                kind: 'files',
                title: 'Files',
                hostId: effectiveHostId,
                filePath: localTarget.dirPath,
                openFilePath: localTarget.path,
                line: localTarget.line,
              });
            }
            return;
          }

          if (rawHref.startsWith('http://') || rawHref.startsWith('https://')) {
            e.preventDefault();
            if (window.agentcDesktop) {
              void window.agentcDesktop.openExternal(rawHref);
            } else {
              window.open(rawHref, '_blank', 'noopener,noreferrer');
            }
          }
        }
      },
      [effectiveHostId, activeSessionId, baseDir, openRightTab],
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
        {lightbox && (
          <div
            onClick={() => setLightbox(null)}
            className="fixed inset-0 z-[80] flex flex-col items-center justify-center bg-black/85 backdrop-blur-xs p-6 cursor-zoom-out"
          >
            <button
              type="button"
              onClick={() => setLightbox(null)}
              className="absolute right-5 top-5 rounded-full bg-white/10 p-2 text-white hover:bg-white/20 cursor-pointer"
              title="Close (Esc)"
            >
              <X className="w-5 h-5" />
            </button>
            <img
              src={lightbox.src}
              alt={lightbox.alt}
              onClick={(e) => e.stopPropagation()}
              className="max-w-[92vw] max-h-[86vh] rounded-xl object-contain shadow-2xl cursor-default"
            />
            {lightbox.alt && <div className="mt-3 text-xs text-white/80 text-center max-w-xl">{lightbox.alt}</div>}
          </div>
        )}
      </div>
    );
  },
);
