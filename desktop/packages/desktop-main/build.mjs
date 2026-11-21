import esbuild from 'esbuild';
import fs from 'node:fs';

fs.mkdirSync('dist', { recursive: true });

await esbuild.build({
  entryPoints: ['src/main.ts'],
  outfile: 'dist/main.cjs',
  bundle: true,
  platform: 'node',
  target: 'node22',
  format: 'cjs',
  external: ['electron'],
  sourcemap: true,
});

// Sandboxed preload MUST be a single self-contained CJS bundle.
await esbuild.build({
  entryPoints: ['src/preload.ts'],
  outfile: 'dist/preload.cjs',
  bundle: true,
  platform: 'node',
  target: 'node22',
  format: 'cjs',
  external: ['electron'],
  sourcemap: true,
});

console.log('desktop-main built: dist/main.cjs, dist/preload.cjs');
