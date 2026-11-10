#!/usr/bin/env node
// Build artifacts only: no runtime compression, media rewrites or external I/O.
import { constants } from 'node:fs';
import { lstat, open, readdir, realpath, rename, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { createGzip } from 'node:zlib';
import { pathToFileURL } from 'node:url';

const extensions = new Set(['.html', '.js', '.mjs', '.css', '.json', '.wasm', '.svg', '.txt']);
const maxBytes = 32 * 1024 * 1024;
const minBytes = 1024;

function allowed(relative) {
  const parts = relative.split('/');
  return !/[\\%?#:]/.test(relative) && parts.every(part => part && !part.startsWith('.')) &&
    !relative.endsWith('.local.md') && !relative.endsWith('.private.md') &&
    !['samples', 'healthz'].includes(parts[0]);
}

function sameVersion(a, b) {
  return a.isFile() && b.isFile() && a.dev === b.dev && a.ino === b.ino &&
    a.size === b.size && a.mtimeNs === b.mtimeNs && a.ctimeNs === b.ctimeNs;
}

export async function precompress(rootName) {
  const root = await realpath(rootName);
  const totals = { files: 0, originalBytes: 0, gzipBytes: 0 };
  let visited = 0;
  async function walk(relative = '') {
    const directory = path.join(root, relative);
    // Do not follow a directory symlink introduced into a reused build tree.
    if (await realpath(directory) !== directory) throw new Error('UI build directory changed');
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const child = relative ? `${relative}/${entry.name}` : entry.name;
      if (!allowed(child) || entry.isSymbolicLink() || child.toLowerCase().endsWith('.gz')) continue;
      if (++visited > 4096) throw new Error('UI build exceeds traversal limit');
      if (entry.isDirectory()) {
        await walk(child);
      } else if (entry.isFile() && extensions.has(path.extname(child).toLowerCase())) {
        const filename = path.join(root, child);
        const sidecar = filename + '.gz';
        const info = await lstat(filename, { bigint: true });
        if (!info.isFile()) continue;
        if (info.size < minBytes || info.size > maxBytes) {
          await rm(sidecar, { force: true });
          continue;
        }
        const source = await open(filename, constants.O_RDONLY | constants.O_NOFOLLOW);
        const temporary = path.join(directory, `.${entry.name}.gzip-${process.pid}-${visited}`);
        let output;
        let created = false;
        try {
          const before = await source.stat({ bigint: true });
          if (!sameVersion(info, before)) throw new Error('UI asset changed before compression');
          output = await open(temporary, 'wx', 0o644);
          created = true;
          await pipeline(source.createReadStream(), createGzip({ level: 6 }),
            output.createWriteStream());
          await output.close();
          output = undefined;
          if (!sameVersion(before, await lstat(filename, { bigint: true }))) {
            throw new Error('UI asset changed during compression');
          }
          const packed = await stat(temporary);
          if (packed.size < Number(before.size)) {
            await rename(temporary, sidecar);
            totals.files++;
            totals.originalBytes += Number(before.size);
            totals.gzipBytes += packed.size;
          } else {
            await rm(sidecar, { force: true });
          }
        } finally {
          await output?.close();
          await source.close();
          if (created) await rm(temporary, { force: true });
        }
      }
    }
  }
  await walk();
  return totals;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  if (process.argv.length !== 3) throw new Error('Usage: node scripts/precompress-web.mjs <web-build-root>');
  const result = await precompress(process.argv[2]);
  console.log(`Precompressed ${result.files} UI files: ${result.originalBytes} -> ${result.gzipBytes} bytes (gzip).`);
}
