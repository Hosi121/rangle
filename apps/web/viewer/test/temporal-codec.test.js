import test from 'node:test';
import assert from 'node:assert/strict';
import { gzipSync } from 'node:zlib';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { decodeTemporal, packTemporal, temporalHeader } from '../src/temporal-codec.js';

function fixture(count = 4099) {
  const data = new ArrayBuffer(32 + count * 56), view = new DataView(data), bytes = new Uint8Array(data);
  bytes.set(new TextEncoder().encode('RANGL4D1'));
  view.setUint32(8, count, true); view.setUint32(12, 56, true); view.setUint32(16, 20, true);
  view.setFloat32(20, 25, true); view.setFloat32(24, .76, true);
  // Distinct rows and columns exercise both canonical order and gzip chunk boundaries.
  for (let i = 32; i < bytes.length; i++) bytes[i] = ((i * 17) ^ (i >>> 5) ^ (i >>> 13)) & 255;
  return data;
}
const pack = data => packTemporal(data, bytes => gzipSync(bytes, { level: 6 }));

test('compressed temporal bytes recover every original byte and canonical ID across chunks', async () => {
  for (const count of [1, 4099]) {
    const raw = fixture(count), packed = pack(raw);
    assert.equal(temporalHeader(packed).compressed, true);
    assert.deepEqual(await decodeTemporal(packed), raw);
    assert.equal(await decodeTemporal(raw), raw, 'existing models are passed through without a copy');
    assert.throws(() => pack(packed), /already compressed/);
  }
});

test('corrupt, truncated and extended gzip streams never reach the model parser', async () => {
  const corrupt = pack(fixture());
  new Uint8Array(corrupt)[corrupt.byteLength - 8] ^= 1;
  await assert.rejects(decodeTemporal(corrupt));
  const valid = pack(fixture());
  await assert.rejects(decodeTemporal(valid.slice(0, -4)));
  const extended = new Uint8Array(valid.byteLength + 1);
  extended.set(new Uint8Array(valid)); extended[extended.length - 1] = 1;
  await assert.rejects(decodeTemporal(extended.buffer));
});

test('declared expansion limits and exact decoded lengths are enforced', async () => {
  const tooLarge = pack(fixture(2));
  new DataView(tooLarge).setUint32(8, 10000000, true);
  await assert.rejects(decodeTemporal(tooLarge), /header/);
  const short = pack(fixture(2));
  new DataView(short).setUint32(8, 3, true);
  await assert.rejects(decodeTemporal(short), /Truncated/);
  const long = pack(fixture(2));
  new DataView(long).setUint32(8, 1, true);
  await assert.rejects(decodeTemporal(long), /exceeds declared size/);
});

test('malformed timeline and unknown formats are rejected before decompression', async () => {
  for (const mutate of [
    data => new Uint8Array(data)[0] = 0,
    data => new DataView(data).setFloat32(20, NaN, true),
    data => new DataView(data).setUint32(12, 55, true),
  ]) {
    const data = pack(fixture(2)); mutate(data);
    await assert.rejects(decodeTemporal(data));
  }
});

test('offline packer verifies a round trip and refuses to overwrite a published asset', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'rangle-pack-'));
  try {
    const input = join(directory, 'input.rgs'), output = join(directory, 'output.rgs');
    const raw = fixture();
    await writeFile(input, new Uint8Array(raw));
    const run = () => spawnSync(process.execPath, [new URL('../pack-temporal.mjs', import.meta.url).pathname, input, output], { encoding: 'utf8' });
    const first = run(); assert.equal(first.status, 0, first.stderr);
    const summary = JSON.parse(first.stdout);
    assert.ok(summary.outputBytes < summary.inputBytes);
    const encoded = await readFile(output);
    const data = encoded.buffer.slice(encoded.byteOffset, encoded.byteOffset + encoded.byteLength);
    assert.deepEqual(await decodeTemporal(data), raw);
    assert.notEqual(run().status, 0);
    assert.deepEqual(await readFile(output), encoded);
  } finally { await rm(directory, { recursive: true, force: true }); }
});
