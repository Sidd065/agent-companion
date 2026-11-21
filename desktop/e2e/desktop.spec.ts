import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { createHermeticDaemon, type HermeticDaemon } from './daemon-harness';

const DESKTOP_ROOT = path.resolve(__dirname, '..');
const MAIN_ENTRY = path.join(DESKTOP_ROOT, 'packages/desktop-main/dist/main.cjs');

let daemon1: HermeticDaemon;
let daemon2: HermeticDaemon;
let desktopConfigDir: string;
let scratchProjectsDir: string;
let electronApp: ElectronApplication;
let page: Page;

async function launchDesktop(): Promise<{ app: ElectronApplication; win: Page }> {
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
      AGENTC_DESKTOP_CONFIG_DIR: desktopConfigDir,
      AGENTC_LOCAL_SOCK: daemon1.socketPath,
      ELECTRON_ENABLE_LOGGING: '1',
    },
  });
  const win = await app.firstWindow();
  await win.waitForLoadState('domcontentloaded');
  await win.setViewportSize({ width: 1400, height: 900 });
  return { app, win };
}

test.describe.serial('Agent Companion Linux Desktop — Full Real-Electron E2E', () => {
  test.beforeAll(async () => {
    desktopConfigDir = fs.mkdtempSync('/tmp/agce2e-desktop-cfg-');
    scratchProjectsDir = fs.mkdtempSync('/tmp/agce2e-projects-');
    daemon1 = await createHermeticDaemon('d1');
    daemon2 = await createHermeticDaemon('d2');
    await daemon1.start();
    await daemon2.start();
    const launched = await launchDesktop();
    electronApp = launched.app;
    page = launched.win;
  });

  test.afterAll(async () => {
    if (electronApp) {
      await electronApp.close().catch(() => {});
    }
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
    // Click outside to close picker if still open
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
