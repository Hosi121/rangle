import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { patchTextureSizing } from '../texture-sizing.mjs';

test('the sizing workaround applies once to the pinned upstream and fails on drift', async () => {
  const source = await readFile(new URL('../node_modules/@mkkellogg/gaussian-splats-3d/build/gaussian-splats-3d.module.js', import.meta.url), 'utf8');
  const patched = patchTextureSizing(source, '0.4.7');
  assert.equal(patched.includes('const texSize = new THREE.Vector2(4096, 1024);'), false);
  assert.equal(patched.split('const texSize = new THREE.Vector2(4096, 1);').length, 2);
  assert.throws(() => patchTextureSizing(source, '0.4.8'), /Review texture sizing/);
  assert.throws(() => patchTextureSizing(patched, '0.4.7'), /source changed/);
});
