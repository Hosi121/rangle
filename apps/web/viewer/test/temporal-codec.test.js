import test from 'node:test';
import assert from 'node:assert/strict';
import { constants, deflateSync, gzipSync } from 'node:zlib';
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

test('native zlib boundary verification rejects bytes after its checksum', async () => {
  const source = Buffer.from('boundary-check'), encoded = deflateSync(source);
  const inflate = bytes => new Response(new Blob([bytes]).stream()
    .pipeThrough(new DecompressionStream('deflate'))).arrayBuffer();
  assert.deepEqual(Buffer.from(await inflate(encoded)), source);
  for (const tail of [Buffer.from([0]), Buffer.from('garbage'), encoded]) {
    await assert.rejects(inflate(Buffer.concat([encoded, tail])));
  }
});

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

test('native passes cancel failures and always release reader locks', async () => {
  const Native = globalThis.DecompressionStream, streams = [];
  const boundaryFailure = new Error('Injected native boundary failure');
  let failBoundary = false;
  globalThis.DecompressionStream = class {
    constructor(format) {
      const decoder = new Native(format), state = { format, canceled: 0, released: 0 };
      streams.push(state);
      const getReader = decoder.readable.getReader.bind(decoder.readable);
      decoder.readable.getReader = (...args) => {
        const reader = getReader(...args), cancel = reader.cancel.bind(reader), release = reader.releaseLock.bind(reader);
        reader.cancel = (...values) => { state.canceled++; return cancel(...values); };
        reader.releaseLock = () => { state.released++; return release(); };
        if (failBoundary && format === 'deflate') reader.read = async () => { throw boundaryFailure; };
        return reader;
      };
      return decoder;
    }
  };
  try {
    const valid = pack(fixture(1)), corrupt = valid.slice(0);
    new Uint8Array(corrupt)[corrupt.byteLength - 8] ^= 1;
    await assert.rejects(decodeTemporal(corrupt));
    assert.deepEqual(streams.splice(0), [{ format: 'gzip', canceled: 1, released: 1 }]);
    const extra = new Uint8Array(valid.byteLength + gzipSync(new Uint8Array()).length);
    const empty = gzipSync(new Uint8Array());
    extra.set(new Uint8Array(valid, 0, 32));
    extra.set(empty, 32); extra.set(new Uint8Array(valid, 32), 32 + empty.length);
    await assert.rejects(decodeTemporal(extra.buffer));
    // Native gzip may reject additional members itself. Assert exact cleanup
    // of the passes actually opened, without requiring a permissive runtime.
    const rejected = streams.splice(0);
    assert.deepEqual(rejected, rejected.length === 1 ? [
      { format: 'gzip', canceled: 1, released: 1 },
    ] : [
      { format: 'gzip', canceled: 0, released: 1 },
      { format: 'deflate', canceled: 1, released: 1 },
    ]);
    // Valid single-member gzip always reaches the boundary pass. Inject a
    // failure there independently of native multi-member acceptance behavior.
    failBoundary = true;
    await assert.rejects(decodeTemporal(valid), error => error === boundaryFailure);
    assert.deepEqual(streams.splice(0), [
      { format: 'gzip', canceled: 0, released: 1 },
      { format: 'deflate', canceled: 1, released: 1 },
    ]);
    failBoundary = false;
    const expansion = pack(fixture(2));
    new DataView(expansion).setUint32(8, 1, true);
    // A forged ISIZE bypasses the cheap check; stream/native integrity rejects it.
    new DataView(expansion).setUint32(expansion.byteLength - 4, 56, true);
    await assert.rejects(decodeTemporal(expansion));
    assert.deepEqual(streams, [{ format: 'gzip', canceled: 1, released: 1 }]);
  } finally { globalThis.DecompressionStream = Native; }
});

test('abort and timeout signals cancel native reads, including the boundary pass', async () => {
  const packed = pack(fixture(4099));
  await assert.rejects(decodeTemporal(packed, { signal: AbortSignal.abort() }), { name: 'AbortError' });
  await assert.rejects(decodeTemporal(packed, { signal: AbortSignal.timeout(0) }), { name: 'TimeoutError' });
  const Native = globalThis.DecompressionStream, controller = new AbortController();
  let canceled = 0, released = 0;
  globalThis.DecompressionStream = class {
    constructor(format) {
      const decoder = new Native(format);
      if (format === 'deflate') {
        const getReader = decoder.readable.getReader.bind(decoder.readable);
        decoder.readable.getReader = (...args) => {
          const reader = getReader(...args), cancel = reader.cancel.bind(reader), release = reader.releaseLock.bind(reader);
          reader.cancel = (...values) => { canceled++; return cancel(...values); };
          reader.releaseLock = () => { released++; return release(); };
          return reader;
        };
        queueMicrotask(() => controller.abort());
      }
      return decoder;
    }
  };
  try {
    await assert.rejects(decodeTemporal(packed, { signal: controller.signal }), { name: 'AbortError' });
    assert.ok(canceled >= 1); assert.equal(released, 1);
  } finally { globalThis.DecompressionStream = Native; }
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

test('explicit RLE packing keeps the existing codec and falls back to raw when larger', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'rangle-rle-pack-'));
  try {
    for (const compressible of [false, true]) {
      const raw = fixture();
      if (compressible) new Uint8Array(raw, 32).fill(0);
      const input = join(directory, `${compressible}-input.rgs`);
      const output = join(directory, `${compressible}-output.rgs`);
      await writeFile(input, new Uint8Array(raw));
      const run = spawnSync(process.execPath, [
        new URL('../pack-temporal.mjs', import.meta.url).pathname, input, output, '--strategy=rle',
      ], { encoding: 'utf8' });
      assert.equal(run.status, 0, run.stderr);
      const summary = JSON.parse(run.stdout);
      assert.equal(summary.strategy, 'rle');
      assert.equal(summary.encoding, compressible ? 'byte-shuffle-gzip' : 'raw');
      const encoded = await readFile(output);
      const data = encoded.buffer.slice(encoded.byteOffset, encoded.byteOffset + encoded.byteLength);
      assert.deepEqual(await decodeTemporal(data), raw);
      assert.ok(summary.outputBytes <= summary.inputBytes);
    }
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test('auto packing selects the smaller stream and keeps default on an equal-size tie', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'rangle-auto-pack-'));
  try {
    for (const pattern of ['dictionary', 'tie', 'entropy']) {
      const raw = fixture();
      if (pattern === 'tie') new Uint8Array(raw, 32).fill(0);
      if (pattern === 'entropy') {
        const bytes = new Uint8Array(raw, 32);
        let state = 21;
        for (let i = 0; i < bytes.length; i++) {
          state ^= state << 13; state ^= state >>> 17; state ^= state << 5;
          bytes[i] = state & 15;
        }
      }
      const original = packTemporal(raw, bytes => gzipSync(bytes, { level: 9 }));
      const rle = packTemporal(raw, bytes => gzipSync(bytes, { level: 9, strategy: constants.Z_RLE }));
      if (pattern === 'tie') assert.equal(original.byteLength, rle.byteLength, 'exercise a real gzip tie');
      if (pattern === 'entropy') assert.ok(rle.byteLength < original.byteLength, 'exercise the RLE choice');
      const selected = rle.byteLength < original.byteLength ? rle : original;
      const expected = selected.byteLength < raw.byteLength ? selected : raw;
      const input = join(directory, `${pattern}-input.rgs`);
      const output = join(directory, `${pattern}-output.rgs`);
      await writeFile(input, new Uint8Array(raw));
      const run = spawnSync(process.execPath, [
        new URL('../pack-temporal.mjs', import.meta.url).pathname, input, output, '--strategy=auto',
      ], { encoding: 'utf8' });
      assert.equal(run.status, 0, run.stderr);
      const summary = JSON.parse(run.stdout);
      assert.equal(summary.strategy, 'auto');
      assert.equal(summary.selectedStrategy, selected === rle ? 'rle' : 'default');
      assert.deepEqual(await readFile(output), Buffer.from(expected));
      assert.ok(summary.outputBytes <= original.byteLength);
    }
  } finally { await rm(directory, { recursive: true, force: true }); }
});
