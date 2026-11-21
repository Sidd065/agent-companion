import fs from 'node:fs';
import path from 'node:path';

/**
 * Strip world-writable (0o002) and group-writable (0o020) bits from unpacked Linux artifacts
 * so `.deb` installs pass strict permission checks without 0777 directories/files.
 */
function sanitizePermissions(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isSymbolicLink()) continue;
    const st = fs.statSync(full);
    const cleanMode = st.mode & ~0o022;
    if (cleanMode !== st.mode) {
      fs.chmodSync(full, cleanMode);
    }
    if (entry.isDirectory()) {
      sanitizePermissions(full);
    }
  }
}

export default async function afterPack(context) {
  if (context.electronPlatformName !== 'linux') return;
  if (context.appOutDir && fs.existsSync(context.appOutDir)) {
    sanitizePermissions(context.appOutDir);
  }
}
