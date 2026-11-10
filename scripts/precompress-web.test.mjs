import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, readdir, rm, stat, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { randomBytes } from 'node:crypto';
import { gunzipSync } from 'node:zlib';
import { test } from 'node:test';
import { precompress } from './precompress-web.mjs';

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'rangle-web-gzip-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  async function write(name, body) {
    const filename = path.join(root, name);
    await mkdir(path.dirname(filename), { recursive: true });
    await writeFile(filename, body);
    return filename;
  }
  return { root, write };
}

async function absent(filename) {
  await assert.rejects(stat(filename), { code: 'ENOENT' });
}

test('gzip output is deterministic and decodes to exact UI JS/Wasm/HTML bytes', async t => {
  const { root, write } = await fixture(t);
  const originals = new Map([
    ['main.dart.js', Buffer.from('console.log("fixture");\n'.repeat(1000))],
    ['canvaskit/canvaskit.wasm', Buffer.concat([Buffer.from('\0asm'), Buffer.alloc(16384, 7)])],
    ['index.html', Buffer.from('<html>fixture UI</html>'.repeat(100))],
  ]);
  for (const [name, body] of originals) await write(name, body);
  const result = await precompress(root);
  assert.equal(result.files, originals.size);
  assert.ok(result.gzipBytes < result.originalBytes);
  const first = new Map();
  for (const [name, body] of originals) {
    const encoded = await readFile(path.join(root, name + '.gz'));
    assert.deepEqual(gunzipSync(encoded), body);
    first.set(name, encoded);
  }
  assert.deepEqual(await precompress(root), result);
  for (const [name, encoded] of first) assert.deepEqual(await readFile(path.join(root, name + '.gz')), encoded);
});

test('only allowed UI originals get sidecars; media, private paths and symlinks stay untouched', async t => {
  const { root, write } = await fixture(t);
  const body = 'compressible fixture\n'.repeat(300);
  for (const name of ['model.rgs', 'model.splat', 'preview.webm', 'font.woff2', '.private/secret.js',
    '.env.js', 'samples/hidden.js', 'healthz/hidden.js', 'bad%path.js', 'notes.local.md', 'notes.private.md']) {
    await write(name, body);
  }
  const outside = await fixture(t);
  const target = await outside.write('outside.js', body);
  await symlink(target, path.join(root, 'linked.js'));
  await symlink(outside.root, path.join(root, 'linked-dir'));
  assert.equal((await precompress(root)).files, 0);
  for (const name of await readdir(root)) assert.ok(!name.endsWith('.gz'));
  assert.equal(await readFile(target, 'utf8'), body);
});

test('rebuild replaces sidecar atomically and removes obsolete small/incompressible variants', async t => {
  const { root, write } = await fixture(t);
  const source = await write('main.dart.js', 'old UI\n'.repeat(1000));
  await precompress(root);
  await writeFile(source, 'new UI\n'.repeat(900));
  await precompress(root);
  assert.equal(gunzipSync(await readFile(source + '.gz')).toString(), 'new UI\n'.repeat(900));
  await writeFile(source, 'small UI');
  await precompress(root);
  await absent(source + '.gz');
  await writeFile(source, randomBytes(4096));
  await writeFile(source + '.gz', 'obsolete');
  await precompress(root);
  await absent(source + '.gz');
  assert.deepEqual(await readdir(root), ['main.dart.js']);
});

test('an existing sidecar symlink is replaced without modifying its destination', async t => {
  const { root, write } = await fixture(t);
  const source = await write('main.dart.js', 'UI bytes\n'.repeat(1000));
  const outside = await fixture(t);
  const target = await outside.write('untouched.txt', 'private');
  await symlink(target, source + '.gz');
  await precompress(root);
  assert.equal(await readFile(target, 'utf8'), 'private');
  assert.equal(gunzipSync(await readFile(source + '.gz')).toString(), await readFile(source, 'utf8'));
});
