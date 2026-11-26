import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test';
import {
  createHermeticDaemon,
  ensureGoBinaries,
  makeE2ETempDir,
  type HermeticDaemon,
} from './daemon-harness';

const DESKTOP_ROOT = path.resolve(__dirname, '..');
const MAIN_ENTRY = path.join(DESKTOP_ROOT, 'packages/desktop-main/dist/main.cjs');
const OUT_DIR = process.env.VISUAL_OUT_DIR || '/tmp/visual-desktop';

function writeLargePng(filePath: string, width: number, height: number, accent: [number, number, number]): void {
  const crcTable = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) {
      c = (c & 1) ? (0xedb88320 ^ (c >>> 1)) : (c >>> 1);
    }
    crcTable[n] = c >>> 0;
  }
  const crc32 = (buf: Buffer): number => {
    let c = 0xffffffff;
    for (let i = 0; i < buf.length; i++) {
      c = crcTable[(c ^ buf[i]!) & 0xff]! ^ (c >>> 8);
    }
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type: string, data: Buffer): Buffer => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length, 0);
    const typeBuf = Buffer.from(type, 'ascii');
    const crcBuf = Buffer.alloc(4);
    crcBuf.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])), 0);
    return Buffer.concat([len, typeBuf, data, crcBuf]);
  };

  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  ihdr[10] = 0;
  ihdr[11] = 0;
  ihdr[12] = 0;

  const rowBytes = 1 + width * 3;
  const raw = Buffer.alloc(rowBytes * height);
  let seed = 0x12345678;
  for (let y = 0; y < height; y++) {
    const rowOff = y * rowBytes;
    raw[rowOff] = 0;
    for (let x = 0; x < width; x++) {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      const noise = seed & 0x3f;
      const grid = (x % 64 < 3 || y % 64 < 3) ? 60 : 0;
      const p = rowOff + 1 + x * 3;
      raw[p] = Math.min(255, ((accent[0] * x) / width + noise + grid) | 0);
      raw[p + 1] = Math.min(255, ((accent[1] * y) / height + ((seed >>> 8) & 0x3f) + grid) | 0);
      raw[p + 2] = Math.min(255, (accent[2] + ((seed >>> 16) & 0x3f)) | 0);
    }
  }
  const compressed = zlib.deflateSync(raw, { level: 0 });
  const png = Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk('IHDR', ihdr),
    chunk('IDAT', compressed),
    chunk('IEND', Buffer.alloc(0)),
  ]);
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  fs.writeFileSync(filePath, png);
}

interface PageMetrics {
  shot: string;
  viewport: { w: number; h: number };
  docScrollWidth: number;
  overflowingElements: Array<{ tag: string; cls: string; text: string; scrollW: number; clientW: number }>;
  clippedAbsoluteMenus: Array<{ cls: string; text: string; rectH: number; parentOverflow: string; visibleH: number }>;
  brokenImages: Array<{ src: string; alt: string; naturalWidth: number }>;
  emptyOptionButtons: Array<{ text: string; cls: string }>;
}

async function captureAndInspect(win: Page, name: string): Promise<PageMetrics> {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  const shotPath = path.join(OUT_DIR, name);
  await win.waitForTimeout(350);
  await win.screenshot({ path: shotPath });

  return await win.evaluate((shotName) => {
    const vp = { w: window.innerWidth, h: window.innerHeight };
    const docScrollWidth = document.documentElement.scrollWidth;
    const overflowingElements: Array<{ tag: string; cls: string; text: string; scrollW: number; clientW: number }> = [];
    for (const el of Array.from(document.querySelectorAll<HTMLElement>('*'))) {
      const style = window.getComputedStyle(el);
      if (
        style.overflowX === 'visible' &&
        el.clientWidth > 0 &&
        el.scrollWidth > el.clientWidth + 6 &&
        el.tagName !== 'HTML' &&
        el.tagName !== 'BODY'
      ) {
        overflowingElements.push({
          tag: el.tagName.toLowerCase(),
          cls: (el.className || '').toString().slice(0, 80),
          text: (el.innerText || '').slice(0, 60).replace(/\s+/g, ' '),
          scrollW: el.scrollWidth,
          clientW: el.clientWidth,
        });
      }
    }

    const clippedAbsoluteMenus: Array<{ cls: string; text: string; rectH: number; parentOverflow: string; visibleH: number }> = [];
    for (const el of Array.from(document.querySelectorAll<HTMLElement>('div.absolute'))) {
      const rect = el.getBoundingClientRect();
      if (rect.height < 10 || rect.width < 10) continue;
      let p: HTMLElement | null = el.parentElement;
      while (p) {
        const ps = window.getComputedStyle(p);
        if (ps.overflowX !== 'visible' || ps.overflowY !== 'visible') {
          const pr = p.getBoundingClientRect();
          const visTop = Math.max(rect.top, pr.top);
          const visBot = Math.min(rect.bottom, pr.bottom);
          const visibleH = Math.max(0, visBot - visTop);
          if (visibleH < rect.height * 0.5) {
            clippedAbsoluteMenus.push({
              cls: (el.className || '').toString().slice(0, 80),
              text: (el.innerText || '').slice(0, 60).replace(/\s+/g, ' '),
              rectH: Math.round(rect.height),
              parentOverflow: `${ps.overflowX}/${ps.overflowY}`,
              visibleH: Math.round(visibleH),
            });
          }
          break;
        }
        p = p.parentElement;
      }
    }

    const brokenImages: Array<{ src: string; alt: string; naturalWidth: number }> = [];
    for (const img of Array.from(document.querySelectorAll<HTMLImageElement>('img'))) {
      if (!img.complete || img.naturalWidth === 0) {
        brokenImages.push({
          src: (img.getAttribute('src') || '').slice(0, 80),
          alt: img.alt || '',
          naturalWidth: img.naturalWidth,
        });
      }
    }

    const emptyOptionButtons: Array<{ text: string; cls: string }> = [];
    for (const btn of Array.from(document.querySelectorAll<HTMLButtonElement>('button'))) {
      const txt = (btn.innerText || '').trim();
      if (/^[1-9]$/.test(txt)) {
        emptyOptionButtons.push({
          text: txt,
          cls: (btn.className || '').toString().slice(0, 80),
        });
      }
    }

    return {
      shot: shotName,
      viewport: vp,
      docScrollWidth,
      overflowingElements: overflowingElements.slice(0, 12),
      clippedAbsoluteMenus,
      brokenImages,
      emptyOptionButtons,
    };
  }, name);
}

test.describe('Desktop Visual & UX Audit', () => {
  test.setTimeout(180_000);

  let hostLocal: HermeticDaemon;
  let hostRemote: HermeticDaemon;
  let app: ElectronApplication | undefined;
  let configDir: string;

  test.beforeAll(async () => {
    ensureGoBinaries();
    hostLocal = await createHermeticDaemon('rpi-local', { enableCodex: true });
    hostRemote = await createHermeticDaemon('dev-box', { enableCodex: true });
  });

  test.afterAll(async () => {
    if (app) await app.close().catch(() => {});
    await hostLocal?.cleanup();
    await hostRemote?.cleanup();
    if (configDir) fs.rmSync(configDir, { recursive: true, force: true });
  });

  test('captures all views, themes, compact sizes, media, and interactive states', async () => {
    configDir = makeE2ETempDir('vis-cfg');
    const repoDir = path.join(hostLocal.homeDir, 'duckvideo-pipeline');
    fs.mkdirSync(path.join(repoDir, 'src'), { recursive: true });
    fs.mkdirSync(path.join(repoDir, 'docs'), { recursive: true });

    const largePngPath = path.join(repoDir, 'assets', 'storyboard 2k.png');
    writeLargePng(largePngPath, 800, 720, [70, 160, 230]); // ~1.73 MiB (> 1 MiB limit!)
    const relPngPath = path.join(repoDir, 'docs', 'arch.png');
    writeLargePng(relPngPath, 320, 180, [90, 200, 140]);
    const mp4Path = path.join(repoDir, 'assets', 'blockout.mp4');
    try {
      execFileSync(
        'ffmpeg',
        ['-y', '-f', 'lavfi', '-i', 'testsrc=size=320x180:rate=12', '-t', '1', '-pix_fmt', 'yuv420p', mp4Path],
        { stdio: 'ignore' },
      );
    } catch {
      fs.writeFileSync(mp4Path, Buffer.alloc(1024));
    }

    fs.writeFileSync(
      path.join(repoDir, 'src', 'pipeline.ts'),
      [
        'export interface ModuleSpec {',
        '  id: string;',
        '  frames: number;',
        '  fps: number;',
        '}',
        '',
        'export function validateGrid(frames: number): boolean {',
        '  return (frames - 5) % 17 === 0;',
        '}',
        '',
        'export const DEFAULT_FPS = 24;',
      ].join('\n'),
    );

    fs.writeFileSync(
      path.join(repoDir, 'docs', 'GUIDE.md'),
      [
        '# Duckvideo Architecture Guide',
        '',
        '> [!TIP]',
        '> Relative images and KaTeX math should render cleanly in FilesPanel preview.',
        '',
        '![System Architecture](./arch.png)',
        '',
        '$$\\text{Error} = \\sqrt{\\frac{1}{N}\\sum_{i=1}^{N} (x_i - \\mu)^2}$$',
      ].join('\n'),
    );

    execFileSync('git', ['init', '-b', 'main'], { cwd: repoDir, stdio: 'pipe' });
    execFileSync('git', ['config', 'user.name', 'Audit Bot'], { cwd: repoDir, stdio: 'pipe' });
    execFileSync('git', ['config', 'user.email', 'audit@agentc.local'], { cwd: repoDir, stdio: 'pipe' });
    execFileSync('git', ['add', '.'], { cwd: repoDir, stdio: 'pipe' });
    execFileSync('git', ['commit', '-m', 'Initial pipeline commit'], { cwd: repoDir, stdio: 'pipe' });
    execFileSync('git', ['branch', 'feat/static-tunnels'], { cwd: repoDir, stdio: 'pipe' });

    fs.writeFileSync(path.join(repoDir, 'src', 'staged.ts'), 'export const STAGED_TUNNEL_DOMAIN = "jdlf.cc";\n');
    execFileSync('git', ['add', 'src/staged.ts'], { cwd: repoDir, stdio: 'pipe' });
    fs.writeFileSync(
      path.join(repoDir, 'src', 'pipeline.ts'),
      [
        'export interface ModuleSpec {',
        '  id: string;',
        '  frames: number;',
        '  fps: number;',
        '  aspect: "16:9" | "9:16";',
        '}',
        '',
        'export function validateGrid(frames: number): boolean {',
        '  if (frames < 22) return false;',
        '  return (frames - 5) % 17 === 0;',
        '}',
        '',
        'export const DEFAULT_FPS = 24;',
      ].join('\n'),
    );

    const stateAgentcDir = path.join(hostLocal.rootDir, 'st', 'agentc');
    fs.mkdirSync(stateAgentcDir, { recursive: true });
    const now = Date.now();
    fs.writeFileSync(
      path.join(stateAgentcDir, 'usage.jsonl'),
      [
        JSON.stringify({ ts: now - 3600_000, sessionId: 's1', agent: 'agy', model: 'gemini-3-pro', inputTokens: 48200, outputTokens: 6400, totalTokens: 54600, costUsd: 0.184, projectPath: repoDir }),
        JSON.stringify({ ts: now - 1800_000, sessionId: 's2', agent: 'claude', model: 'claude-sonnet-4-5', inputTokens: 31500, outputTokens: 4100, totalTokens: 35600, costUsd: 0.142, projectPath: repoDir }),
        JSON.stringify({ ts: now - 600_000, sessionId: 's3', agent: 'codex', model: 'openai/gpt-5-codex', inputTokens: 19200, outputTokens: 2800, totalTokens: 22000, costUsd: 0.089, projectPath: repoDir }),
      ].join('\n') + '\n',
    );

    await hostLocal.start();
    await hostRemote.start();

    app = await electron.launch({
      args: [MAIN_ENTRY, '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
      env: {
        ...process.env,
        AGENTC_DESKTOP_TEST: '1',
        AGENTC_DESKTOP_CONFIG_DIR: configDir,
        AGENTC_LOCAL_SOCK: hostLocal.socketPath,
        ELECTRON_ENABLE_LOGGING: '1',
      },
    });
    const win = await app.firstWindow();
    const consoleErrors: string[] = [];
    win.on('pageerror', (err) => consoleErrors.push(`PAGEERROR: ${err.message}`));
    win.on('console', (msg) => {
      if (msg.type() === 'error') consoleErrors.push(`CONSOLE: ${msg.text()}`);
    });

    await win.waitForLoadState('domcontentloaded');
    await win.setViewportSize({ width: 1440, height: 900 });

    const allMetrics: PageMetrics[] = [];

    // 1. Pair local daemon + remote dev-box daemon via exact data-testids
    await win.getByTestId('empty-pair-btn').click();
    await expect(win.getByTestId('pair-modal')).toBeVisible();
    await win.getByTestId('one-click-local-pair-btn').click();
    await expect(win.getByTestId('pair-modal')).toBeHidden();

    const pairLink = await hostRemote.createPairLink();
    await win.getByTestId('pair-host-btn').click();
    await expect(win.getByTestId('pair-modal')).toBeVisible();
    await win.getByTestId('pair-link-input').fill(pairLink);
    allMetrics.push(await captureAndInspect(win, '01-onboarding-hosts.png'));
    await win.getByTestId('pair-submit-btn').click();
    await expect(win.getByTestId('pair-modal')).toBeHidden();
    await expect(win.locator('[data-testid^="host-chip-"]')).toHaveCount(2);
    await win.locator('[data-testid^="host-chip-"]').first().click();

    // Add duckvideo-pipeline existing folder project
    await win.getByTestId('add-project-btn').click();
    await expect(win.getByTestId('add-project-modal')).toBeVisible();
    const folderInput = win.getByTestId('add-project-modal').locator('input').first();
    await folderInput.fill(repoDir);
    await win.getByTestId('add-project-submit').click();
    await expect(win.getByTestId('add-project-modal')).toBeHidden();

    // 2. DraftView + ModelPicker open (dark theme 1440x900)
    await win.getByTestId('new-thread-btn').click();
    await expect(win.getByTestId('draft-view')).toBeVisible();
    await win.getByTestId('draft-project-select').selectOption({ label: 'duckvideo-pipeline' });
    await win.getByTestId('model-picker-btn').click();
    await win.getByRole('button', { name: 'Fake ACP', exact: true }).click();
    allMetrics.push(await captureAndInspect(win, '02-draft-model-picker-dark.png'));
    await win.getByRole('button', { name: /Provider Default/i }).click();

    // Create thread with rich Gate 1 Markdown, tool calls, subagent, and file edit
    await win.getByTestId('composer-textarea').fill(
      `[bash:git status -sb] [subagent] [edit:src/notes.md:# Audit Notes\\nVerified] [gate1:${repoDir}]`,
    );
    await win.getByTestId('composer-send-btn').click();
    await expect(win.getByTestId('thread-view')).toBeVisible({ timeout: 20_000 });
    await expect(win.getByText(/End-to-End 3D-Guided Hub-and-Spoke Architecture/i)).toBeVisible({ timeout: 20_000 });
    await win.waitForTimeout(1200);

    // Seed gate1_review.md artifact into brain/<sessionId>/ for ArtifactsPanel
    const sessionsDir = path.join(stateAgentcDir, 'sessions');
    const sessionFiles = fs.existsSync(sessionsDir) ? fs.readdirSync(sessionsDir).filter((f) => f.endsWith('.json')) : [];
    if (sessionFiles.length > 0) {
      const activeSessionId = sessionFiles[0]!.replace(/\.json$/, '');
      const brainDir = path.join(hostLocal.homeDir, '.gemini', 'antigravity-cli', 'brain', activeSessionId);
      fs.mkdirSync(brainDir, { recursive: true });
      fs.copyFileSync(relPngPath, path.join(brainDir, 'preview.png'));
      fs.writeFileSync(
        path.join(brainDir, 'gate1_review.md'),
        [
          '# Gate 1 Review & Engineering Architecture',
          '',
          '> [!IMPORTANT]',
          '> **GPU Budget & Hygiene Status**: Vast.ai RTX 3060 (`$0.04` spent out of `$100.00` budget; `$0.00/hr` burning while awaiting your Gate 1 review).',
          '',
          '![Relative Artifact Preview](./preview.png)',
          '',
          '```mermaid',
          'flowchart LR',
          '  Ref["ref.mp4 (360x640)"] --> Clean["Curated Clean Frames"]',
          '  Clean --> Anchor["Master Anchor 16:9 (2560x1440)"]',
          '  Anchor --> Blender["Headless Blender 4.2 (362f @ 24fps)"]',
          '  Blender --> H3["MiniMax H3 ControlNet"]',
          '```',
          '',
          '| Module | Aspect | Frames | QA Check |',
          '|---|---|---|---|',
          '| `01_dive` | `16:9` + `9:16` | `362f @ 24fps` | Seam SSIM `0.994`, 0 drift |',
          '| `02_huddle` | `16:9` + `9:16` | `362f @ 24fps` | Repulsion verified |',
        ].join('\n'),
      );
      fs.writeFileSync(
        path.join(brainDir, 'gate1_review.md.metadata.json'),
        JSON.stringify({
          summary: 'Gate 1 Review & Engineering Architecture',
          requestFeedback: true,
          userFacing: true,
          updatedAt: new Date(Date.now() + 60_000).toISOString(),
        }),
      );
    }

    // 3. Rich Gate 1 thread in Dark Theme (bottom: table+carousel, top: alerts+Mermaid)
    allMetrics.push(await captureAndInspect(win, '03-thread-gate1-dark.png'));
    await win.getByText(/Gate 1 Review & Engineering Architecture/i).first().scrollIntoViewIfNeeded();
    allMetrics.push(await captureAndInspect(win, '03b-thread-mermaid-dark.png'));

    // 16 & 4. SettingsModal search ("notification") + switch to Light Theme ("paper")
    await win.getByTestId('open-settings-btn').click();
    await expect(win.getByTestId('settings-modal')).toBeVisible();
    const settingsSearch = win.getByTestId('settings-modal').getByPlaceholder(/Search settings/i);
    await settingsSearch.fill('notification');
    allMetrics.push(await captureAndInspect(win, '16-settings-search.png'));
    await settingsSearch.fill('');
    await win.getByTestId('settings-tab-appearance').click();
    await win.getByTestId('theme-btn-paper').click();
    await win.getByTestId('settings-close-btn').click();
    await win.waitForTimeout(800);
    allMetrics.push(await captureAndInspect(win, '04b-thread-mermaid-light.png'));
    await win.getByTestId('checkpoint-card').last().scrollIntoViewIfNeeded();
    allMetrics.push(await captureAndInspect(win, '04-thread-gate1-light.png'));

    // Switch back to Dark Theme ("obsidian")
    await win.getByTestId('open-settings-btn').click();
    await win.getByTestId('settings-tab-appearance').click();
    await win.getByTestId('theme-btn-obsidian').click();
    await win.getByTestId('settings-close-btn').click();

    // 11. Open turn-scoped DiffPanel via checkpoint card -> Split view
    const cpCard = win.getByTestId('checkpoint-card').last();
    await cpCard.getByTestId('checkpoint-open-diff-btn').click();
    await expect(win.getByTestId('diff-panel')).toBeVisible();
    await win.getByTestId('diff-view-toggle').click();
    allMetrics.push(await captureAndInspect(win, '11-diff-panel-split.png'));

    // 6. Click '+' in RightPanel to capture Add-Tab Dropdown (clipped inside overflow-x-auto!)
    await win.getByTestId('right-panel-add-tab').click();
    allMetrics.push(await captureAndInspect(win, '06-rightpanel-add-tab-dropdown.png'));

    // 7 & 8. Open FilesPanel -> >1.7 MiB PNG ('storyboard 2k.png') & relative Markdown ('docs/GUIDE.md')
    await win.getByRole('button', { name: /^Files$/ }).click({ force: true });
    await expect(win.getByTestId('files-panel')).toBeVisible();
    await win.getByTestId('file-tree-entry').filter({ hasText: 'assets' }).first().click();
    await win.getByTestId('file-tree-entry').filter({ hasText: 'storyboard 2k.png' }).first().click();
    await win.waitForTimeout(700);
    allMetrics.push(await captureAndInspect(win, '07-files-panel-large-png.png'));

    await win.getByRole('button', { name: 'Up directory' }).click();
    await win.getByTestId('file-tree-entry').filter({ hasText: 'docs' }).first().click();
    await win.getByTestId('file-tree-entry').filter({ hasText: 'GUIDE.md' }).first().click();
    await win.waitForTimeout(700);
    allMetrics.push(await captureAndInspect(win, '08-files-panel-relative-md.png'));

    // 10. SourceControlPanel
    await win.getByTestId('right-panel-add-tab').click();
    await win.getByRole('button', { name: /^Source Control$/ }).click({ force: true });
    await expect(win.getByTestId('source-control-panel')).toBeVisible();
    allMetrics.push(await captureAndInspect(win, '10-source-control-panel.png'));

    // 12. NotesPanel
    await win.getByTestId('open-notes-btn').click();
    const notesBox = win.locator('textarea').last();
    await notesBox.fill('- [ ] Configure static Cloudflare tunnel for jdlf.cc\n- [x] Verify Noise IKpsk1 pairing\n- [ ] Polish mobile & desktop UI');
    allMetrics.push(await captureAndInspect(win, '12-notes-panel.png'));

    // 9. ArtifactsPanel
    await win.getByTestId('right-panel-add-tab').click();
    await win.getByRole('button', { name: /^Artifacts$/ }).click({ force: true });
    await win.waitForTimeout(800);
    allMetrics.push(await captureAndInspect(win, '09-artifacts-panel-review.png'));

    // 5. Switch approval mode to Supervised & send [agyquestion] (string-array options!)
    await win.getByTestId('model-picker-btn').click();
    await win.getByRole('button', { name: /Supervised/i }).click();
    await win.keyboard.press('Escape');
    await win.getByTestId('composer-textarea').fill('[agyquestion]');
    await win.getByTestId('composer-send-btn').click();
    await expect(win.getByTestId('perm-question-card')).toBeVisible({ timeout: 15_000 });
    allMetrics.push(await captureAndInspect(win, '05-composer-agy-question.png'));

    // 13. Compact 1024x700 window on active thread + RightPanel open!
    await win.setViewportSize({ width: 1024, height: 700 });
    await win.waitForTimeout(400);
    allMetrics.push(await captureAndInspect(win, '13-compact-1024x700-thread.png'));

    // 14. UsageView at compact 1024x700
    await win.getByTestId('usage-view-btn').click();
    await expect(win.getByTestId('usage-view')).toBeVisible();
    allMetrics.push(await captureAndInspect(win, '14-usage-view-compact.png'));

    // 15. SchedulesView + New Schedule Modal at compact 1024x700
    await win.getByTestId('schedules-view-btn').click();
    await expect(win.getByTestId('schedules-view')).toBeVisible();
    await win.getByTestId('schedule-new-btn').click();
    allMetrics.push(await captureAndInspect(win, '15-schedules-modal-compact.png'));
    await win.keyboard.press('Escape');

    fs.writeFileSync(
      path.join(OUT_DIR, 'metrics.json'),
      JSON.stringify({ consoleErrors, shots: allMetrics }, null, 2),
    );
  });
});
