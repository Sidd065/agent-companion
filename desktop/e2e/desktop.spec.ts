import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test';
import {
  createHermeticDaemon,
  createMockOpenRouterServer,
  makeE2ETempDir,
  type HermeticDaemon,
  type MockOpenRouterServer,
} from './daemon-harness';

const DESKTOP_ROOT = path.resolve(__dirname, '..');
const MAIN_ENTRY = path.join(DESKTOP_ROOT, 'packages/desktop-main/dist/main.cjs');

async function launchDesktopInstance(opts: {
  configDir: string;
  localSocketPath: string;
}): Promise<{ app: ElectronApplication; win: Page }> {
  const app = await electron.launch({
    args: [
      MAIN_ENTRY,
      '--no-sandbox',
      '--disable-gpu',
      '--disable-dev-shm-usage',
    ],
    env: {
      ...process.env,
      AGENTC_DESKTOP_TEST: '1',
      AGENTC_DESKTOP_CONFIG_DIR: opts.configDir,
      AGENTC_LOCAL_SOCK: opts.localSocketPath,
      ELECTRON_ENABLE_LOGGING: '1',
    },
  });
  const win = await app.firstWindow();
  await win.waitForLoadState('domcontentloaded');
  await win.setViewportSize({ width: 1440, height: 920 });
  return { app, win };
}

async function closeDesktopInstance(app: ElectronApplication | undefined): Promise<void> {
  if (!app) return;
  const proc = app.process();
  await Promise.race([
    app.close().catch(() => {}),
    new Promise<void>((resolve) =>
      setTimeout(() => {
        try {
          proc?.kill('SIGKILL');
        } catch {
          // ignore
        }
        resolve();
      }, 4_000),
    ),
  ]);
}

function initGitRepo(repoDir: string, initialFiles: Record<string, string>): void {
  fs.mkdirSync(repoDir, { recursive: true });
  execFileSync('git', ['init'], { cwd: repoDir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.name', 'Agent Companion E2E'], { cwd: repoDir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.email', 'e2e@agentc.local'], { cwd: repoDir, stdio: 'pipe' });
  for (const [rel, content] of Object.entries(initialFiles)) {
    const full = path.join(repoDir, rel);
    fs.mkdirSync(path.dirname(full), { recursive: true });
    fs.writeFileSync(full, content);
  }
  execFileSync('git', ['add', '.'], { cwd: repoDir, stdio: 'pipe' });
  execFileSync('git', ['commit', '-m', 'chore: initial commit'], { cwd: repoDir, stdio: 'pipe' });
}

test.describe.serial('Agent Companion Linux Desktop — Full Real-Electron E2E', () => {
  let daemon1: HermeticDaemon;
  let daemon2: HermeticDaemon;
  let desktopConfigDir: string;
  let scratchProjectsDir: string;
  let electronApp: ElectronApplication;
  let page: Page;

  test.beforeAll(async () => {
    desktopConfigDir = makeE2ETempDir('desktop-cfg');
    scratchProjectsDir = makeE2ETempDir('projects');
    daemon1 = await createHermeticDaemon('d1');
    daemon2 = await createHermeticDaemon('d2');
    await daemon1.start();
    await daemon2.start();
    const launched = await launchDesktopInstance({
      configDir: desktopConfigDir,
      localSocketPath: daemon1.socketPath,
    });
    electronApp = launched.app;
    page = launched.win;
  });

  test.afterAll(async () => {
    await closeDesktopInstance(electronApp);
    await daemon1?.cleanup();
    await daemon2?.cleanup();
    fs.rmSync(desktopConfigDir, { recursive: true, force: true });
    fs.rmSync(scratchProjectsDir, { recursive: true, force: true });
  });

  test('1. One-click local daemon pairing + remote `agentc pair` URL multi-host pairing', async () => {
    // Click Pair a Host on welcome screen -> detects Daemon #1 via Unix socket
    await page.getByTestId('empty-pair-btn').click();
    await expect(page.getByTestId('pair-modal')).toBeVisible();
    await expect(page.getByTestId('local-daemon-card')).toBeVisible();
    await page.getByTestId('one-click-local-pair-btn').click();
    await expect(page.getByTestId('pair-modal')).toBeHidden();

    // Verify Host #1 appears in sidebar and connects
    await expect(page.locator('[data-testid^="host-chip-"]')).toHaveCount(1);

    // Pair Host #2 using a genuine `agentc pair` link over Noise-IKpsk1 -> Noise-IK
    const link2 = await daemon2.createPairLink();
    expect(link2).toContain('/p/');
    await page.getByTestId('pair-host-btn').click();
    await expect(page.getByTestId('pair-modal')).toBeVisible();
    await page.getByTestId('pair-link-input').fill(link2);
    await page.getByTestId('pair-submit-btn').click();
    await expect(page.getByTestId('pair-modal')).toBeHidden();

    // Both hosts are now connected simultaneously in the unified sidebar!
    await expect(page.locator('[data-testid^="host-chip-"]')).toHaveCount(2);

    // Select Host #1 (first chip) so all subsequent project/worktree tests run on Daemon #1
    await page.locator('[data-testid^="host-chip-"]').first().click();
  });

  test('2. Add Project (git init) + Draft thread in New Worktree + Streaming Markdown/KaTeX', async () => {
    await page.getByTestId('add-project-btn').click();
    await expect(page.getByTestId('add-project-modal')).toBeVisible();
    await page.getByTestId('add-project-tab-init').click();
    await page.getByTestId('project-init-name').fill('demo-repo');
    await page.getByTestId('project-init-parent').fill(scratchProjectsDir);
    await page.getByTestId('add-project-submit').click();
    await expect(page.getByTestId('add-project-modal')).toBeHidden();

    await expect(page.getByTestId('project-item-demo-repo')).toBeVisible();

    // Open New Thread draft for demo-repo in a NEW WORKTREE using Fake ACP
    await page.getByTestId('new-thread-btn').click();
    await expect(page.getByTestId('draft-view')).toBeVisible();

    // Select Fake ACP provider via Model Picker
    await page.getByTestId('model-picker-btn').click();
    await page.getByRole('button', { name: 'Fake ACP', exact: true }).click();
    await page.getByRole('button', { name: /Provider Default/i }).click();

    // Select project & enable New Worktree
    await page.getByTestId('draft-project-select').selectOption({ label: 'demo-repo' });
    await page.getByTestId('draft-checkout-worktree').click();
    await page.getByTestId('draft-branch-input').fill('feat-e2e');

    // Send [markdown] prompt -> verifies streaming prose, code blocks, and KaTeX rendering
    await page.getByTestId('composer-textarea').fill('[markdown] Hello worktree!');
    await page.getByTestId('composer-send-btn').click();

    await expect(page.getByTestId('thread-view')).toBeVisible();
    await expect(page.getByTestId('user-message').first()).toContainText('[markdown] Hello worktree!');
    await expect(page.getByTestId('agent-message').first()).toBeVisible();
    // Verify KaTeX math formula rendered inside agent message
    await expect(page.locator('.katex').first()).toBeVisible();
    // Verify branch indicator shows our new worktree branch
    await expect(page.getByTestId('info-branch-btn')).toContainText('feat-e2e');
  });

  test('3. File edit -> Per-turn Checkpoint -> Diff Panel (Split/Review Comment) -> Revert', async () => {
    await page.getByTestId('composer-textarea').fill('[edit:hello.txt:hello world from fakeacp]');
    await page.getByTestId('composer-send-btn').click();

    // Per-turn checkpoint card appears with changed file stats
    const cpCard = page.getByTestId('checkpoint-card').last();
    await expect(cpCard).toBeVisible();
    await expect(cpCard).toContainText('1 changed file');
    await expect(cpCard).toContainText('+1');

    // Open turn-scoped Diff panel
    await cpCard.getByTestId('checkpoint-open-diff-btn').click();
    await expect(page.getByTestId('diff-panel')).toBeVisible();
    await expect(page.getByTestId('diff-file-card').first()).toContainText('hello.txt');

    // Toggle Stacked <-> Split view
    await page.getByTestId('diff-view-toggle').click();
    await expect(page.getByTestId('diff-panel').locator('table')).toBeVisible();

    // Add inline review comment -> verify chip appears in main composer!
    await page.getByTestId('diff-comment-file-btn').first().click();
    await page.getByTestId('diff-comment-input').fill('Please add a newline and unit test');
    await page.getByTestId('diff-comment-submit').click();
    await expect(page.getByTestId('composer-context-chip')).toContainText('hello.txt:1');

    // Revert workspace via checkpoint card
    await cpCard.getByTestId('checkpoint-revert-btn').click();
    await page.getByTestId('confirm-revert-btn').click();
    await expect(page.getByTestId('toast-notice').last()).toContainText(/Reverted workspace/i);
  });

  test('4. Supervised Approvals ([perm]), AskUserQuestion ([question]), and Plan Review ([plan])', async () => {
    // Switch approval mode to Supervised so [perm] pauses for explicit user approval
    await page.getByTestId('model-picker-btn').click();
    await page.getByRole('button', { name: /Supervised/i }).click();
    await page.keyboard.press('Escape');
    await page.getByTestId('composer-textarea').click();

    // Test [perm] approval flow
    await page.getByTestId('composer-textarea').fill('[perm:echo approved_ok]');
    await page.getByTestId('composer-send-btn').click();
    const permCard = page.getByTestId('perm-approval-card');
    await expect(permCard).toBeVisible();
    await permCard.getByRole('button', { name: /Allow/i }).first().click();
    await expect(permCard).toBeHidden();

    // Test [question] AskUserQuestion flow
    await page.getByTestId('composer-textarea').fill('[question]');
    await page.getByTestId('composer-send-btn').click();
    const qCard = page.getByTestId('perm-question-card');
    await expect(qCard).toBeVisible();
    await qCard.getByRole('button', { name: /Fast/i }).first().click();
    await qCard.getByRole('button', { name: /Submit Answer/i }).click();
    await expect(qCard).toBeHidden();

    // Test [plan] Plan Review flow
    await page.getByTestId('composer-textarea').fill('[plan]');
    await page.getByTestId('composer-send-btn').click();
    const pCard = page.getByTestId('perm-plan-card');
    await expect(pCard).toBeVisible();
    await pCard.getByRole('button', { name: /Approve/i }).first().click();
    await expect(pCard).toBeHidden();
  });

  test('5. Inline !command, Side Chat (/btw), Files edit/save, Source Control commit, Terminal, Themes, Usage & Schedules', async () => {
    // Inline !command execution
    await page.getByTestId('composer-textarea').fill('!echo INLINE_E2E_OK');
    await page.getByTestId('composer-send-btn').click();
    const execRow = page.getByTestId('tool-row').last();
    await expect(execRow).toBeVisible();
    await expect(execRow).toContainText('echo INLINE_E2E_OK');

    // Side Chat (/btw)
    await page.getByTestId('composer-textarea').fill('/btw Summarize our session briefly');
    await page.getByTestId('composer-send-btn').click();
    await expect(page.getByTestId('side-chat-panel')).toBeVisible();
    await expect(page.getByTestId('aside-card').first()).toBeVisible();

    // Files explorer -> edit README.md and save via fs.write
    await page.getByTestId('right-panel-add-tab').click();
    await page.getByRole('button', { name: /^Files$/ }).click();
    await expect(page.getByTestId('files-panel')).toBeVisible();
    await page.getByTestId('file-tree-entry').filter({ hasText: 'README.md' }).first().click();
    await page.getByTestId('files-toggle-edit-btn').click();
    await page.getByTestId('files-editor-textarea').fill('# Demo Repo\nEdited via Agent Companion Desktop E2E\n');
    await page.getByTestId('files-save-btn').click();
    await expect(page.getByTestId('toast-notice').last()).toContainText(/Saved README\.md/i);

    // Source Control -> commit our README.md edit
    await page.getByTestId('right-panel-add-tab').click();
    await page.getByRole('button', { name: /^Source Control$/ }).click();
    await expect(page.getByTestId('source-control-panel')).toBeVisible();
    await page.getByTestId('sc-commit-message').fill('docs: update README from desktop');
    await page.getByTestId('sc-commit-btn').click();
    await expect(page.getByTestId('toast-notice').last()).toContainText(/Committed changes/i);

    // Interactive xterm.js Terminal Drawer over encrypted Noise TerminalStream
    await page.getByTestId('toggle-terminal-btn').click();
    await expect(page.getByTestId('terminal-panel')).toBeVisible();
    await page.getByTestId('terminal-empty-create-btn').click();
    await expect(page.getByTestId('xterm-container')).toBeVisible();
    await expect(page.locator('.xterm')).toBeVisible();
    // Close drawer cleanly
    await page.getByTestId('toggle-terminal-btn').click();

    // Command Palette (Ctrl+K)
    await page.keyboard.press('Control+k');
    await expect(page.getByTestId('command-palette')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('command-palette')).toBeHidden();

    // Settings Modal + Theme Switch (Paper Light -> data-theme="light")
    await page.getByTestId('open-settings-btn').click();
    await expect(page.getByTestId('settings-modal')).toBeVisible();
    await page.getByTestId('settings-tab-appearance').click();
    await page.getByTestId('theme-btn-paper').click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    await page.getByTestId('theme-btn-obsidian').click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await page.getByTestId('settings-close-btn').click();

    // Usage Dashboard
    await page.getByTestId('usage-view-btn').click();
    await expect(page.getByTestId('usage-view')).toBeVisible();
    await expect(page.getByTestId('usage-view')).toContainText('Completed Turns');

    // Scheduled Tasks CRUD
    await page.getByTestId('schedules-view-btn').click();
    await expect(page.getByTestId('schedules-view')).toBeVisible();
    await page.getByTestId('schedule-new-btn').click();
    await page.getByTestId('schedule-name-input').fill('E2E Daily Check');
    await page.getByTestId('schedule-prompt-input').fill('Check repository health');
    await page.getByTestId('schedule-save-btn').click();
    await expect(page.getByTestId('schedule-task-card').first()).toContainText('E2E Daily Check');
  });

  test('5b. v0.6.0 Features: Client-side Session Notes checklist, quoted search, Current Thread palette, Usage ranges & Notification/Agy Read settings', async () => {
    // Switch back to active thread view
    const firstRow = page.locator('[data-testid^="thread-row-"]').first();
    await firstRow.click();
    await expect(page.getByTestId('thread-view')).toBeVisible();

    // Open client-side Session Notes panel from TitleBar
    await page.getByTestId('open-notes-btn').click();
    await expect(page.getByTestId('notes-panel')).toBeVisible();
    await page.getByTestId('notes-textarea').fill('- [ ] Verify Cloudflare static tunnel\n- [ ] Ship v0.6.0 release');

    // Toggle first checklist item -> next unchecked summary becomes "Ship v0.6.0 release"
    await page.getByTestId('notes-panel').getByRole('button', { name: /Verify Cloudflare static tunnel/i }).click();
    await expect(firstRow).toContainText('Ship v0.6.0 release');

    // Insert next note item into composer
    await page.getByTestId('notes-insert-composer-btn').click();
    await expect(page.getByTestId('composer-textarea')).toHaveValue(/Ship v0\.6\.0 release/);
    await page.getByTestId('composer-textarea').fill('');

    // Verify quoted exact-phrase search matches client-side note text
    await page.getByTestId('sidebar-search-input').fill('"Ship v0.6.0"');
    await expect(page.locator('[data-testid^="thread-row-"]')).toHaveCount(1);
    await page.getByTestId('sidebar-search-input').fill('');

    // Command Palette shows Current Thread actions
    await page.keyboard.press('Control+k');
    await expect(page.getByTestId('command-palette')).toBeVisible();
    await expect(page.getByTestId('command-palette')).toContainText('Current Thread');
    await expect(page.getByTestId('command-palette')).toContainText('Settle Current Thread');
    await page.keyboard.press('Escape');

    // Settings Modal: Notification categories + Auto-allow Antigravity read requests
    await page.getByTestId('open-settings-btn').click();
    await expect(page.getByTestId('settings-modal')).toBeVisible();
    await expect(page.getByTestId('settings-modal')).toContainText('Notification Categories');
    await expect(page.getByTestId('settings-agy-auto-allow-reads')).toBeVisible();
    await page.getByTestId('settings-agy-auto-allow-reads').click();
    await page.getByTestId('settings-close-btn').click();

    // Usage View: 5h and All time windows
    await page.getByTestId('usage-view-btn').click();
    await expect(page.getByTestId('usage-view')).toBeVisible();
    await page.getByTestId('usage-view').getByRole('button', { name: '5h', exact: true }).click();
    await page.getByTestId('usage-view').getByRole('button', { name: 'All time', exact: true }).click();
    await firstRow.click();
  });

  test('6. Resilience / Chaos: Kill and restart daemon mid-session -> automatic reconnect & transcript continuity', async () => {
    // Return to our active thread in the sidebar
    await page.locator('[data-testid^="thread-row-"]').first().click();
    await expect(page.getByTestId('thread-view')).toBeVisible();
    const beforeAgentCount = await page.getByTestId('agent-message').count();
    expect(beforeAgentCount).toBeGreaterThan(0);

    // KILL Daemon #1 mid-session!
    await daemon1.stop();
    await page.waitForTimeout(800);

    // RESTART Daemon #1 on the exact same port & XDG state dirs
    await daemon1.start();

    // Wait for HostSupervisor's automatic backoff reconnect (trigger online event to speed up wakeup)
    await page.evaluate(() => window.dispatchEvent(new Event('online')));
    await page.waitForTimeout(1200);

    // Verify subsequent prompt works seamlessly over fresh Noise-IK session with zero transcript loss!
    await page.getByTestId('composer-textarea').fill('After daemon restart, confirm alive');
    await page.getByTestId('composer-send-btn').click();
    await expect(page.getByTestId('user-message').last()).toContainText('After daemon restart, confirm alive');
    await expect(page.getByTestId('agent-message')).toHaveCount(beforeAgentCount + 1, { timeout: 20_000 });
  });
});

test.describe.serial('Multi-Host Isolation (Host A + Host B) — FilesPanel, Trash Fallback, Projects & Terminal', () => {
  let hostA: HermeticDaemon;
  let hostB: HermeticDaemon;
  let cfgDir: string;
  let app: ElectronApplication;
  let win: Page;
  let projBDir: string;

  test.beforeAll(async () => {
    cfgDir = makeE2ETempDir('mh-cfg');
    hostA = await createHermeticDaemon('mha');
    hostB = await createHermeticDaemon('mhb', { disableSystemTrash: true });

    fs.writeFileSync(path.join(hostA.homeDir, 'only-on-host-a.txt'), 'HOST_A_EXCLUSIVE_CONTENT\n');
    fs.writeFileSync(path.join(hostB.homeDir, 'only-on-host-b.txt'), 'HOST_B_EXCLUSIVE_CONTENT\n');
    fs.writeFileSync(path.join(hostB.homeDir, 'delete-me-headless.txt'), 'headless delete test\n');
    projBDir = path.join(hostB.homeDir, 'proj-b');
    initGitRepo(projBDir, { 'README.md': '# Project B on Host Beta\n' });

    await hostA.start();
    await hostB.start();

    const launched = await launchDesktopInstance({
      configDir: cfgDir,
      localSocketPath: hostA.socketPath,
    });
    app = launched.app;
    win = launched.win;
  });

  test.afterAll(async () => {
    await closeDesktopInstance(app);
    await hostA?.cleanup();
    await hostB?.cleanup();
    fs.rmSync(cfgDir, { recursive: true, force: true });
  });

  test('7. Multi-host FilesPanel ~ switching, inline New File, TRASH_UNSUPPORTED permanent delete, AddProjectModal, Terminal pwd, Session on Host B & Sidebar Health', async () => {
    // Pair Host A (local) and Host B (remote pair link)
    await win.getByTestId('empty-pair-btn').click();
    await win.getByTestId('one-click-local-pair-btn').click();
    await expect(win.locator('[data-testid^="host-chip-"]')).toHaveCount(1);

    const linkB = await hostB.createPairLink();
    await win.getByTestId('pair-host-btn').click();
    await win.getByTestId('pair-link-input').fill(linkB);
    await win.getByTestId('pair-submit-btn').click();
    await expect(win.getByTestId('pair-modal')).toBeHidden();
    await expect(win.locator('[data-testid^="host-chip-"]')).toHaveCount(2);

    // Rename both hosts inline via Settings -> Hosts & Devices (zero window.prompt!)
    await win.getByTestId('open-settings-btn').click();
    await win.getByTestId('settings-tab-hosts').click();
    const renameBtns = win.locator('[data-testid^="rename-host-btn-"]');
    await expect(renameBtns).toHaveCount(2);

    await renameBtns.nth(0).click();
    await win.locator('[data-testid^="rename-host-input-"]').fill('Host Alpha');
    await win.locator('[data-testid^="rename-host-save-"]').click();

    await renameBtns.nth(1).click();
    await win.locator('[data-testid^="rename-host-input-"]').fill('Host Beta');
    await win.locator('[data-testid^="rename-host-save-"]').click();
    await win.getByTestId('settings-close-btn').click();

    // Open FilesPanel via RightPanel
    await win.getByTestId('toggle-right-panel-btn').click();
    await win.getByTestId('right-panel-add-tab').click();
    await win.getByRole('button', { name: /^Files$/ }).click();
    const filesPanel = win.getByTestId('files-panel');
    await expect(filesPanel).toBeVisible();

    // Switch to Host Alpha via FilesPanel host selector and browse ~
    await win.getByTestId('files-host-select').selectOption({ label: 'Host Alpha (connected)' });
    await win.getByTestId('files-home-btn').click();
    await expect(filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'only-on-host-a.txt' })).toBeVisible();
    await expect(filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'only-on-host-b.txt' })).toHaveCount(0);

    // Open Host Alpha file in viewer
    await filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'only-on-host-a.txt' }).click();
    await expect(filesPanel).toContainText('HOST_A_EXCLUSIVE_CONTENT');

    // Switch to Host Beta via Sidebar chip -> DraftView MUST stay on Host Beta (no snap-back!) AND FilesPanel must reset & load Host Beta's ~!
    await win.locator('[data-testid^="host-chip-"]').filter({ hasText: 'Host Beta' }).click();
    await expect(win.getByTestId('draft-host-select')).toHaveValue(await win.getByTestId('files-host-select').inputValue());
    await expect(filesPanel).not.toContainText('HOST_A_EXCLUSIVE_CONTENT');
    await expect(filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'only-on-host-a.txt' })).toHaveCount(0);
    await expect(filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'only-on-host-b.txt' })).toBeVisible();
    await expect(filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'delete-me-headless.txt' })).toBeVisible();

    // Inline New File creation on Host Beta
    await win.getByTestId('files-new-file-btn').click();
    await win.getByTestId('files-inline-input').fill('created-on-beta.txt');
    await win.getByTestId('files-inline-submit').click();
    await expect(win.getByTestId('files-editor-textarea')).toBeVisible();
    await win.getByTestId('files-editor-textarea').fill('Created on Host Beta!\n');
    await win.getByTestId('files-save-btn').click();
    await expect(win.getByTestId('toast-notice').last()).toContainText(/Saved created-on-beta\.txt/i);
    expect(fs.readFileSync(path.join(hostB.homeDir, 'created-on-beta.txt'), 'utf8')).toBe('Created on Host Beta!\n');
    expect(fs.existsSync(path.join(hostA.homeDir, 'created-on-beta.txt'))).toBe(false);

    // Headless TRASH_UNSUPPORTED -> Permanent Delete confirmation flow on Host Beta
    const delEntryBtn = filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'delete-me-headless.txt' });
    const delRow = delEntryBtn.locator('..');
    await delRow.hover();
    await delRow.getByTestId('files-delete-btn').click({ force: true });
    await expect(win.getByTestId('files-confirm-delete-btn')).toBeVisible();
    await win.getByTestId('files-confirm-delete-btn').click();

    // Must transition cleanly to permanent-delete confirmation
    await expect(win.getByTestId('files-delete-prompt')).toContainText(/System trash is unavailable/i);
    await expect(win.getByTestId('files-confirm-permanent-delete-btn')).toBeVisible();
    await win.getByTestId('files-confirm-permanent-delete-btn').click();
    await expect(delEntryBtn).toHaveCount(0);
    expect(fs.existsSync(path.join(hostB.homeDir, 'delete-me-headless.txt'))).toBe(false);

    // Add Project on Host Beta via AddProjectModal
    await win.getByTestId('add-project-btn').click();
    await expect(win.getByTestId('add-project-modal')).toBeVisible();
    await win.getByTestId('add-project-host-select').selectOption({ label: 'Host Beta (connected)' });
    await win.getByTestId('project-path-input').fill(projBDir);
    await win.getByTestId('add-project-submit').click();
    await expect(win.getByTestId('add-project-modal')).toBeHidden();
    await expect(win.getByTestId('project-item-proj-b')).toBeVisible();
    await expect(win.getByTestId('project-item-proj-b')).toContainText('Host Beta');

    // Open TerminalPanel on Host Beta and run `pwd > pwd-out.txt`
    await win.getByTestId('toggle-terminal-btn').click();
    await expect(win.getByTestId('terminal-panel')).toBeVisible();
    await win.getByTestId('terminal-empty-create-btn').click();
    await expect(win.getByTestId('xterm-container')).toBeVisible();
    await win.locator('.xterm').click();
    await win.keyboard.type('pwd > pwd-out.txt\r');
    await expect
      .poll(() => {
        const outFile = path.join(projBDir, 'pwd-out.txt');
        return fs.existsSync(outFile) ? fs.readFileSync(outFile, 'utf8').trim() : '';
      })
      .toBe(fs.realpathSync(projBDir));
    await win.getByTestId('toggle-terminal-btn').click();

    // Start a session on Host Beta from DraftView and verify Host Alpha remains untouched
    await win.getByTestId('model-picker-btn').click();
    await win.getByRole('button', { name: 'Fake ACP', exact: true }).click();
    await win.getByRole('button', { name: /Provider Default/i }).click();
    await win.getByTestId('composer-textarea').fill('Hello exclusively on Host Beta!');
    await win.getByTestId('composer-send-btn').click();
    await expect(win.getByTestId('thread-view')).toBeVisible();
    await expect(win.getByTestId('agent-message').first()).toBeVisible();

    // Verify Sidebar "Health" button opens SettingsModal directly on Diagnostics & Health (Bug P4 fix)
    await win.getByTestId('sidebar-health-btn').click();
    await expect(win.getByTestId('settings-modal')).toBeVisible();
    await expect(win.getByTestId('settings-modal')).toContainText('Linux Daemon Host Health');
    await win.getByTestId('settings-close-btn').click();
  });

  test('7b. Multi-host regressions: >1 MiB truncated file guard, attachment reset, openRightTab filePath/singleton dedup, SettingsModal customModels scope & unsaved input preservation, and host-stamped TerminalPanel', async () => {
    // 1. [CHAL-6] Create > 1 MiB (~1.2 MiB) text file with tail sentinel on Host Beta & verify truncation guard
    const largeFilePath = path.join(hostB.homeDir, 'large-trunc-guard.txt');
    const tailSentinel = 'TAIL_SENTINEL_DO_NOT_LOSE\n';
    const largeBody = 'L'.repeat(1228 * 1024) + '\n' + tailSentinel;
    fs.writeFileSync(largeFilePath, largeBody, 'utf8');
    const origSize = fs.statSync(largeFilePath).size;
    expect(origSize).toBeGreaterThan(1024 * 1024);

    const filesPanel = win.getByTestId('files-panel');
    await expect(filesPanel).toBeVisible();
    await win.getByTestId('files-host-select').selectOption({ label: 'Host Beta (connected)' });
    await win.getByTestId('files-home-btn').click();
    const largeEntry = filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'large-trunc-guard.txt' });
    await expect(largeEntry).toBeVisible();
    await largeEntry.click();

    await expect(win.getByTestId('files-truncated-banner')).toBeVisible();
    await expect(win.getByTestId('files-truncated-banner')).toContainText('1 MiB');
    await expect(win.getByTestId('files-toggle-edit-btn')).toBeDisabled();
    await expect(win.getByTestId('files-editor-textarea')).toHaveCount(0);
    await expect(win.getByTestId('files-save-btn')).toHaveCount(0);
    await win.keyboard.press('Control+s');
    expect(fs.statSync(largeFilePath).size).toBe(origSize);
    expect(fs.readFileSync(largeFilePath, 'utf8').endsWith(tailSentinel)).toBe(true);

    // 2. [CHAL-5A + Reviewer Finding 2] Stage unsent attachment & pin FilesPanel via ThreadInfoCard cwd on Host Beta, then switch to Host Alpha
    const betaThreadRow = win.locator('[data-testid^="thread-row-"]').first();
    await betaThreadRow.click();
    await expect(win.getByTestId('thread-view')).toBeVisible();

    await win.getByTestId('composer-attach-input').setInputFiles(path.join(hostB.homeDir, 'only-on-host-b.txt'));
    await expect(win.getByTestId('composer-attachment-chip')).toHaveCount(1);
    await expect(win.getByTestId('composer-attachment-chip')).toContainText('only-on-host-b.txt');

    // Pin Files tab to Host Beta's proj-b cwd via ThreadInfoCard cwd button
    await win.getByTitle(projBDir).click();
    await expect(win.getByTestId('files-current-dir')).toContainText('proj-b');

    // Switch to Host Alpha via Sidebar chip -> unsent attachment MUST clear & FilesPanel MUST reset off proj-b!
    await win.locator('[data-testid^="host-chip-"]').filter({ hasText: 'Host Alpha' }).click();
    await expect(win.getByTestId('composer-attachment-chip')).toHaveCount(0);
    await expect(win.getByTestId('files-current-dir')).not.toContainText('proj-b');
    await expect(filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'only-on-host-a.txt' })).toBeVisible();

    // Open Files Explorer via Command Palette (omits filePath) -> NO duplicate Files tab & NO foreign filePath!
    await win.getByTestId('command-palette-btn').click();
    await expect(win.getByTestId('command-palette')).toBeVisible();
    await win.getByTestId('command-palette-input').fill('Open Files Explorer');
    await win.keyboard.press('Enter');
    await expect(win.getByTestId('command-palette')).toBeHidden();
    await expect(win.getByTestId('right-panel').getByRole('button', { name: 'Files', exact: true })).toHaveCount(1);
    await expect(filesPanel.getByTestId('file-tree-entry').filter({ hasText: 'only-on-host-a.txt' })).toBeVisible();

    // 3. [CHAL-7 + CHAL-1-UI-HOST] SettingsModal preserves unsaved inputs on Add Model & scopes customModels per host
    await win.getByTestId('open-settings-btn').click();
    await win.getByTestId('settings-tab-providers').click();
    await win.getByTestId('settings-host-select').selectOption({ label: 'Host Alpha (connected)' });
    await expect(win.getByTestId('codex-save-settings-btn')).toBeEnabled();

    await win.getByTestId('codex-command-input').fill('/opt/custom/unsaved-codex-bin');
    await win.getByTestId('codex-default-model-input').fill('google/gemini-2.5-flash');
    await win.getByTestId('codex-enabled-checkbox').uncheck();

    await win.getByTestId('codex-custom-slug-input').fill('deepseek/deepseek-r1:free');
    await win.getByTestId('codex-add-custom-model-btn').click();
    await expect(win.getByTestId('codex-custom-models-section')).toContainText('deepseek/deepseek-r1:free');

    // Unsaved form inputs MUST NOT be wiped by Add Model!
    await expect(win.getByTestId('codex-command-input')).toHaveValue('/opt/custom/unsaved-codex-bin');
    await expect(win.getByTestId('codex-default-model-input')).toHaveValue('google/gemini-2.5-flash');
    await expect(win.getByTestId('codex-enabled-checkbox')).not.toBeChecked();

    // Switch SettingsModal host selector to Host Beta -> Host Alpha's custom slug MUST NOT leak onto Host Beta!
    await win.getByTestId('settings-host-select').selectOption({ label: 'Host Beta (connected)' });
    await expect(win.getByTestId('codex-save-settings-btn')).toBeEnabled();
    await expect(win.getByTestId('codex-custom-models-section')).toContainText('Saved Custom OpenRouter & Codex Models (0)');
    await expect(win.getByTestId('codex-custom-models-section')).not.toContainText('deepseek/deepseek-r1:free');
    await win.getByTestId('settings-close-btn').click();

    // 4. [CHAL-8] Host-stamped RightPanel Terminal tab stays bound to Host Beta when Sidebar switches to Host Alpha
    await win.locator('[data-testid^="host-chip-"]').filter({ hasText: 'Host Beta' }).click();
    await win.getByTestId('right-panel-add-tab').click();
    await win.getByRole('button', { name: /^Terminal$/ }).click();
    const rightTermPanel = win.getByTestId('right-panel').getByTestId('terminal-panel');
    await expect(rightTermPanel).toBeVisible();
    await expect(rightTermPanel.getByTestId('terminal-host-badge')).toContainText('Host Beta');
    await expect(rightTermPanel.getByTestId('xterm-container')).toBeVisible();

    await win.locator('[data-testid^="host-chip-"]').filter({ hasText: 'Host Alpha' }).click();
    await expect(rightTermPanel.getByTestId('terminal-host-badge')).toContainText('Host Beta');
    await expect(rightTermPanel.getByTestId('xterm-container')).toBeVisible();
  });
});

test.describe.serial('Codex + OpenRouter Full Workbench E2E', () => {
  let mockOR: MockOpenRouterServer;
  let daemonCodex: HermeticDaemon;
  let cfgDir: string;
  let projDir: string;
  let app: ElectronApplication;
  let win: Page;

  test.beforeAll(async () => {
    mockOR = await createMockOpenRouterServer();
    cfgDir = makeE2ETempDir('codex-cfg');
    daemonCodex = await createHermeticDaemon('codexd', {
      enableCodex: true,
      mockOpenRouter: {
        proxyUrl: mockOR.proxyUrl,
        caCertPath: mockOR.caCertPath,
      },
    });
    projDir = path.join(daemonCodex.homeDir, 'codex-repo');
    initGitRepo(projDir, {
      'hello.txt': 'original line\n',
    });

    await daemonCodex.start();
    const launched = await launchDesktopInstance({
      configDir: cfgDir,
      localSocketPath: daemonCodex.socketPath,
    });
    app = launched.app;
    win = launched.win;
  });

  test.afterAll(async () => {
    await closeDesktopInstance(app);
    await daemonCodex?.cleanup();
    await mockOR?.cleanup();
    fs.rmSync(cfgDir, { recursive: true, force: true });
  });

  test('8. Settings OpenRouter key + "***" redaction preservation + Refresh Catalog, Draft catalog & custom -latest:thinking slug, thought_level effort, dynamic modes, multi-file diffs, approvals/questions & openInTerminal', async () => {
    // Pair local Codex-enabled daemon & add project
    await win.getByTestId('empty-pair-btn').click();
    await win.getByTestId('one-click-local-pair-btn').click();
    await expect(win.getByTestId('pair-modal')).toBeHidden();

    await win.getByTestId('add-project-btn').click();
    await win.getByTestId('project-path-input').fill(projDir);
    await win.getByTestId('add-project-submit').click();
    await expect(win.getByTestId('add-project-modal')).toBeHidden();

    // 1. Configure OpenRouter API key via SettingsModal -> Providers & ACP
    await win.getByTestId('open-settings-btn').click();
    await win.getByTestId('settings-tab-providers').click();
    const codexCard = win.getByTestId('codex-provider-card');
    await expect(codexCard).toBeVisible();
    await expect(win.getByTestId('codex-openrouter-badge')).toContainText('OpenRouter Not Configured');

    const keyInput = win.getByTestId('codex-openrouter-key-input');
    await expect(keyInput).toHaveValue('');
    await keyInput.fill('sk-or-e2e-secret-999');
    await win.getByTestId('codex-save-settings-btn').click();

    await expect(win.getByTestId('codex-openrouter-badge')).toContainText('OpenRouter Ready');
    await expect(codexCard).toContainText('Stored (***)');
    // Input MUST remain write-only (never prefilled with "***")
    await expect(keyInput).toHaveValue('');
    expect(mockOR.getLastAuthHeader()).toBe('Bearer sk-or-e2e-secret-999');

    // 2. Save a non-secret setting while key input is blank -> MUST preserve stored secret ("***" invariant)!
    await win.getByTestId('codex-default-model-input').fill('google/gemini-3-flash-preview');
    await win.getByTestId('codex-save-settings-btn').click();
    await expect(win.getByTestId('toast-notice').last()).toContainText(/Saved Codex/i);

    // Add a new upstream OpenRouter model dynamically & click Refresh Catalog
    mockOR.addModel({
      id: 'google/gemini-2.5-pro-exp-09',
      name: 'Google: Gemini 2.5 Pro Exp 09',
      description: 'Live refreshed OpenRouter model',
    });
    await win.getByTestId('codex-refresh-catalog-btn').click();
    await expect(codexCard).toContainText('4 catalog models');
    expect(mockOR.getLastAuthHeader()).toBe('Bearer sk-or-e2e-secret-999');
    await win.getByTestId('settings-close-btn').click();

    // 3. Draft mode: verify dynamic `providers.models` catalog + arbitrary custom `-latest:thinking` slug
    await win.getByTestId('model-picker-btn').click();
    await win.getByRole('button', { name: 'Codex', exact: true }).click();
    await expect(win.getByText('Google: Gemini 3 Flash Preview')).toBeVisible();
    await expect(win.getByText('Google: Gemini 2.5 Pro Exp 09')).toBeVisible();

    const customSlug = 'openrouter/openai/gpt-5.4-nano-latest:thinking';
    await win.getByTestId('model-search-input').fill(customSlug);
    const useCustomBtn = win.getByTestId('use-custom-model-btn');
    await expect(useCustomBtn).toBeVisible();
    await expect(useCustomBtn).toContainText(customSlug);
    await useCustomBtn.click();

    // Reopen Model Picker: verify exact unstripped slug is selected & persisted in customModels,
    // verify Claude Code picker is NOT polluted by Codex OpenRouter custom slugs (CHAL-1-UI-CLAUDE), then set xhigh + Plan mode
    await expect(win.getByTestId('model-picker-btn')).toContainText('gpt-5.4-nano-latest:thinking');
    await win.getByTestId('model-picker-btn').click();
    await expect(win.getByText('Custom', { exact: true }).first()).toBeVisible();

    await win.getByRole('button', { name: 'Claude Code', exact: true }).click();
    await expect(win.getByText('Claude Sonnet 4.5')).toBeVisible();
    await expect(win.getByText('gpt-5.4-nano-latest:thinking')).toHaveCount(0);

    await win.getByRole('button', { name: 'Codex', exact: true }).click();
    await win.getByRole('button', { name: /gpt-5\.4-nano-latest:thinking/i }).first().click();
    await expect(win.getByTestId('model-picker-btn')).toContainText('gpt-5.4-nano-latest:thinking');
    await win.getByTestId('model-picker-btn').click();
    await expect(win.getByText('Custom', { exact: true }).first()).toBeVisible();
    await win.getByTestId('effort-btn-xhigh').click();
    await win.getByTestId('mode-btn-plan').click();
    await expect(win.getByTestId('perm-mode-badge')).toContainText('Plan');

    // Send [bridge-probe] [inspect] -> exercises OpenRouter loopback bridge + inspects exact Codex thread parameters
    await win.getByTestId('composer-textarea').fill('[bridge-probe] [inspect]');
    await win.getByTestId('composer-send-btn').click();

    await expect(win.getByTestId('thread-view')).toBeVisible();
    await expect(win.getByTestId('agent-message').first()).toContainText('INSPECT_JSON:', { timeout: 20_000 });
    const msg1Text = await win.getByTestId('agent-message').first().innerText();
    const inspect1Match = msg1Text.match(/INSPECT_JSON:(\{.*\})/);
    expect(inspect1Match).not.toBeNull();
    const inspect1 = JSON.parse(inspect1Match![1]) as Record<string, unknown>;
    expect(String(inspect1.model)).toContain('openai/gpt-5.4-nano-latest:thinking');
    expect(inspect1.effort).toBe('xhigh');
    expect(inspect1.collaborationMode).toBe('plan');
    expect(['read-only', 'readOnly']).toContain(inspect1.sandbox);
    expect(inspect1.catalogHasModel).toBe(true);
    expect(inspect1.envHasRawOpenRouterKey).toBe(false);

    // Verify OpenRouter `/v1/responses` received exact unstripped slug & preserved API key
    const orReqs = mockOR.getResponsesRequests();
    expect(orReqs.length).toBeGreaterThanOrEqual(1);
    expect(orReqs[0].auth).toBe('Bearer sk-or-e2e-secret-999');
    expect(orReqs[0].body.model).toBe('openai/gpt-5.4-nano-latest:thinking');

    // 4. Mid-session: switch effort to `low` (`configId: 'thought_level'`) and mode to `Full access`
    await win.getByTestId('model-picker-btn').click();
    await win.getByTestId('effort-btn-low').click();
    await win.getByTestId('mode-btn-full-access').click();
    await expect(win.getByTestId('perm-mode-badge')).toContainText('Full access');

    await win.getByTestId('composer-textarea').fill('[inspect]');
    await win.getByTestId('composer-send-btn').click();
    await expect(win.getByTestId('agent-message')).toHaveCount(2, { timeout: 20_000 });
    const msg2Text = await win.getByTestId('agent-message').nth(1).innerText();
    const inspect2Match = msg2Text.match(/INSPECT_JSON:(\{.*\})/);
    expect(inspect2Match).not.toBeNull();
    const inspect2 = JSON.parse(inspect2Match![1]) as Record<string, unknown>;
    expect(inspect2.effort).toBe('low');
    expect(inspect2.approvalPolicy).toBe('never');
    expect(['danger-full-access', 'dangerFullAccess']).toContain(inspect2.sandbox);

    // 5. Switch mode to Build (Supervised), run multi-file edits + command approval + structured question
    await win.getByTestId('model-picker-btn').click();
    await win.getByTestId('mode-btn-default').click();
    await expect(win.getByTestId('perm-mode-badge')).toContainText('Build (Supervised)');

    await win
      .getByTestId('composer-textarea')
      .fill('[edit:hello.txt:codex-updated-alpha] [edit:second.txt:codex-created-beta] [approve-cmd:echo CODEX_CMD_OK] [question]');
    await win.getByTestId('composer-send-btn').click();

    // Command approval card with reason & command preview
    const permCard = win.getByTestId('perm-approval-card');
    await expect(permCard).toBeVisible();
    await expect(permCard).toContainText('Needs shell command approval');
    await permCard.getByRole('button', { name: /Allow/i }).first().click();
    await expect(permCard).toBeHidden();

    // Structured AskUserQuestion card (`q.id === 'q_env'`)
    const qCard = win.getByTestId('perm-question-card');
    await expect(qCard).toBeVisible();
    await qCard.getByRole('button', { name: /Staging/i }).first().click();
    await qCard.getByRole('button', { name: /Submit Answer/i }).click();
    await expect(qCard).toBeHidden();

    await expect(win.getByTestId('agent-message')).toHaveCount(3, { timeout: 20_000 });
    const msg3Text = await win.getByTestId('agent-message').nth(2).innerText();
    expect(msg3Text).toContain('APPROVAL_DECISION:accept');
    expect(msg3Text).toContain('QUESTION_ANSWER:');
    expect(msg3Text).toContain('Staging');

    // Expand completed turn work-log and verify inline edit diffs WITHOUT raw {"type":"diff"} JSON dump
    await win.getByRole('button', { name: /Worked for/i }).last().click();
    const editRows = win.getByTestId('tool-row').filter({ hasText: /Edit|Write/i });
    const editCount = await editRows.count();
    expect(editCount).toBeGreaterThanOrEqual(2);
    for (let i = 0; i < editCount; i++) {
      await editRows.nth(i).locator('button').first().click();
    }
    await expect(win.getByTestId('tool-inline-diff').first()).toBeVisible();
    const timelineText = await win.getByTestId('chat-timeline').innerText();
    expect(timelineText).toContain('+ codex-updated-alpha');
    expect(timelineText).toContain('+ codex-created-beta');
    expect(timelineText).not.toContain('"type": "diff"');

    // 5b. [Reviewer Finding 3 & CHAL-5B] Open non-Codex picker on another session, switch back to Codex thread, and press Ctrl+Shift+M
    const codexThreadRow = win.locator('[data-testid^="thread-row-"]').first();
    const codexRowTestId = await codexThreadRow.getAttribute('data-testid');
    expect(codexRowTestId).toBeTruthy();

    await win.getByTestId('new-thread-btn').click();
    await win.getByTestId('model-picker-btn').click();
    await win.getByRole('button', { name: 'Fake ACP', exact: true }).click();
    await win.getByRole('button', { name: /Provider Default/i }).click();
    await win.getByTestId('composer-textarea').fill('Quick Fake ACP session');
    await win.getByTestId('composer-send-btn').click();
    await expect(win.getByTestId('agent-message').first()).toBeVisible();

    // Open Model Picker on Fake ACP thread (sets pickerAgent='fake'), then close and switch back to Codex thread
    await win.getByTestId('model-picker-btn').click();
    await win.keyboard.press('Escape');
    await win.getByTestId(codexRowTestId!).click();
    await expect(win.getByTestId('model-picker-btn')).toContainText('Codex');

    // Press Ctrl+Shift+M -> MUST open Codex model/mode picker (NOT Claude/Fake ACP, zero mode-btn-bypassPermissions!)
    await win.keyboard.press('Control+Shift+M');
    await expect(win.getByTestId('model-search-input')).toBeVisible();
    await expect(win.getByTestId('mode-btn-bypassPermissions')).toHaveCount(0);
    await expect(win.getByTestId('mode-btn-full-access')).toBeVisible();
    await expect(win.getByTestId('effort-btn-xhigh')).toBeVisible();
    await expect(win.getByText('Google: Gemini 3 Flash Preview')).toBeVisible();
    await win.keyboard.press('Escape');

    // 6. [Reviewer Finding 1] Create a prior shell terminal so terminals[0] is 'shell', then click Open in Terminal (`session.openInTerminal`)
    // -> MUST synchronously upsert & select the newly spawned Codex CLI terminal ('CX'), NOT fall back to terminals[0] ('shell')!
    await win.getByTestId('toggle-terminal-btn').click();
    await win.getByTestId('terminal-empty-create-btn').click();
    await expect(win.getByTestId('xterm-container')).toBeVisible();
    await win.getByTestId('toggle-terminal-btn').click();

    await win.getByTestId('info-open-terminal-btn').click();
    const rightTerm = win.getByTestId('right-panel').getByTestId('terminal-panel');
    await expect(rightTerm).toBeVisible();
    await expect(rightTerm.getByTestId('terminal-tab')).toHaveCount(2);
    await expect(rightTerm.getByTestId('terminal-tab').filter({ hasText: 'CX' })).toBeVisible();
    const activeTermTabChip = rightTerm.locator('div[class*="accent-soft"]').getByTestId('terminal-tab');
    await expect(activeTermTabChip).toContainText('CX');
    await expect(rightTerm.getByTestId('xterm-container')).toBeVisible();
  });
});
