import { build } from 'electron-builder';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const desktopRoot = path.resolve(__dirname, '..');
const stageDir = path.join(desktopRoot, '.stage-app');
const releaseDir = path.join(desktopRoot, 'release');

// Clean and stage minimal Electron app directory (zero devDependencies bloated into ASAR)
fs.rmSync(stageDir, { recursive: true, force: true });
fs.mkdirSync(stageDir, { recursive: true });
fs.mkdirSync(releaseDir, { recursive: true });

const mainDist = path.join(desktopRoot, 'packages/desktop-main/dist');
const webDist = path.join(desktopRoot, 'packages/web/dist');
if (!fs.existsSync(path.join(mainDist, 'main.cjs')) || !fs.existsSync(path.join(webDist, 'index.html'))) {
  throw new Error('Build web and desktop-main first (`npm run build`).');
}

fs.copyFileSync(path.join(mainDist, 'main.cjs'), path.join(stageDir, 'main.cjs'));
fs.copyFileSync(path.join(mainDist, 'preload.cjs'), path.join(stageDir, 'preload.cjs'));

const rootPkg = JSON.parse(fs.readFileSync(path.join(desktopRoot, 'package.json'), 'utf8'));
fs.writeFileSync(
  path.join(stageDir, 'package.json'),
  JSON.stringify(
    {
      name: 'agent-companion',
      productName: 'Agent Companion',
      version: rootPkg.version || '0.6.0',
      description: 'End-to-end encrypted Linux desktop workbench for agentc coding agents',
      desktopName: 'agent-companion.desktop',
      author: { name: 'Sidd065', email: '78248734+Sidd065@users.noreply.github.com' },
      homepage: 'https://github.com/Sidd065/agent-companion',
      main: 'main.cjs',
    },
    null,
    2,
  ),
);

const electronPkg = JSON.parse(
  fs.readFileSync(path.join(desktopRoot, 'node_modules/electron/package.json'), 'utf8'),
);

try {
  await build({
    projectDir: desktopRoot,
    targets: undefined,
    linux: ['deb', 'AppImage'],
    config: {
      appId: 'dev.agentc.desktop',
      productName: 'Agent Companion',
      electronVersion: electronPkg.version,
      asar: true,
      directories: {
        app: stageDir,
        output: releaseDir,
      },
      extraResources: [
        {
          from: webDist,
          to: 'web',
        },
      ],
      protocols: [
        {
          name: 'Agent Companion Pairing',
          schemes: ['agentc'],
        },
      ],
      afterPack: path.join(__dirname, 'linux-after-pack.mjs'),
      linux: {
        executableName: 'agent-companion',
        icon: path.join(__dirname, 'icon.png'),
        category: 'Development',
        desktop: {
          entry: {
            Name: 'Agent Companion',
            Comment: 'End-to-end encrypted workbench for Claude Code, Antigravity, Codex, and ACP agents',
            StartupWMClass: 'agent-companion',
            Categories: 'Development;IDE;',
          },
        },
      },
      deb: {
        packageName: 'agent-companion',
        depends: [
          'libgtk-3-0t64 | libgtk-3-0',
          'libnotify4',
          'libnss3',
          'libxss1',
          'libxtst6',
          'xdg-utils',
          'libatspi2.0-0t64 | libatspi2.0-0',
          'libuuid1',
          'libsecret-1-0',
          'libasound2t64 | libasound2',
        ],
      },
      appImage: {
        artifactName: 'Agent-Companion-${version}-${arch}.AppImage',
      },
    },
  });
  console.log('Linux packaging complete ->', releaseDir);
} finally {
  fs.rmSync(stageDir, { recursive: true, force: true });
}
