import test from 'node:test';
import assert from 'node:assert/strict';
import { SplatLoader } from '@mkkellogg/gaussian-splats-3d';
import { FrameLoader } from '../src/frame-loader.js';
import { MAX_FRAME_BYTES, prepareFrame } from '../src/frame-format.js';

function rawFrame() {
  const data = new ArrayBuffer(32 * 3);
  const floats = new Float32Array(data);
  const bytes = new Uint8Array(data);
  for (let i = 0; i < 3; i++) {
    floats.set([i, 1, 2, 0.1, 0.2, 0.3], i * 8);
    bytes.set([10, 20, 30, i === 1 ? 0 : 255, 255, 128, 128, 128], i * 32 + 24);
  }
  return data;
}

class FakeWorker extends EventTarget {
  messages = [];
  terminated = 0;
  postMessage(message) { this.messages.push(message); }
  terminate() { this.terminated++; }
  reply(id, buffer) { this.dispatchEvent(new MessageEvent('message', { data: { id, buffer } })); }
}

test('worker conversion matches the pinned viewer conversion byte for byte', async () => {
  const previous = globalThis.window;
  globalThis.window = { setTimeout };
  try {
    const expected = await SplatLoader.loadFromFileData(rawFrame(), 1, 0, true);
    const actual = prepareFrame(rawFrame());
    assert.deepEqual(new Uint8Array(actual), new Uint8Array(expected.bufferData));
    for (const bad of [new ArrayBuffer(0), new ArrayBuffer(33), new ArrayBuffer(MAX_FRAME_BYTES + 32)]) {
      assert.throws(() => prepareFrame(bad), /Invalid or oversized/);
    }
  } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
});

test('one worker is reused, superseded work is terminated, and late events are ignored', async () => {
  const workers = [];
  const loader = new FrameLoader(() => { const worker = new FakeWorker(); workers.push(worker); return worker; });
  const buffer = prepareFrame(rawFrame());
  const first = loader.load('https://example.test/0.splat');
  workers[0].reply(1, buffer.slice(0));
  assert.equal((await first).getSplatCount(), 2);
  const superseded = loader.load('https://example.test/1.splat');
  const rejected = assert.rejects(superseded, { name: 'AbortError' });
  const newest = loader.load('https://example.test/19.splat');
  await rejected;
  assert.equal(workers.length, 2);
  assert.equal(workers[0].terminated, 1);
  workers[0].dispatchEvent(new Event('error'));
  workers[0].reply(2, buffer.slice(0));
  assert.equal(workers[1].terminated, 0);
  workers[1].reply(3, buffer.slice(0));
  await newest;
  loader.dispose();
  loader.dispose();
  assert.equal(workers[1].terminated, 1);
  await assert.rejects(loader.load('https://example.test/0.splat'), /disposed/);
});

test('dispose cancels preparation and malformed responses fail without resolving a frame', async () => {
  const worker = new FakeWorker();
  const loader = new FrameLoader(() => worker);
  const failed = loader.load('https://example.test/0.splat');
  worker.reply(1, new ArrayBuffer(0));
  await assert.rejects(failed, /Invalid prepared frame/);
  const active = loader.load('https://example.test/1.splat');
  const rejected = assert.rejects(active, { name: 'AbortError' });
  loader.dispose();
  await rejected;
  assert.equal(worker.terminated, 1);
});
