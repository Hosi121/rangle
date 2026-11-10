import test from 'node:test';
import assert from 'node:assert/strict';
import { parseManifest, SceneSession } from '../src/session.js';

const manifest = () => ({ version: 1, frameRate: 25,
  camera: { position: [0, 1, 2], lookAt: [0, 0, 0], up: [0, 1, 0], fovYDegrees: 50 },
  frames: Array.from({ length: 20 }, (_, index) => `frames/${index}.splat`),
  attribution: { label: 'Example', source: 'https://example.test/data', license: 'CC BY 4.0' } });
const source = 'https://example.test/scenes/basketball/scene.json';
const vector = (value) => ({ value, clone() { return vector(this.value); }, copy(other) { this.value = other.value; } });
const deferred = () => { let resolve; let reject; const promise = new Promise((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; };
function harness() {
  const messages = [];
  const viewer = {
    camera: { position: vector(1), quaternion: vector(2), up: vector(3), fov: 50, updateProjectionMatrix() {} },
    controls: { target: vector(4), update() {} }, calls: [], disposed: 0,
    scenes: [], removed: 0,
    async removeSplatScene(index) {
      assert.equal(index, 0);
      this.scenes.splice(index, 1);
      this.removed++;
    },
    async addSplatBuffers([buffer], options, finalBuild, showLoading, showProgress, replaceExisting = false) {
      assert.equal(replaceExisting, false, 'avoid the SDK replace path');
      assert.equal(this.scenes.length, 0, 'release the previous scene before adding');
      this.scenes.push(buffer);
      this.calls.push(buffer.url);
    },
    start() {}, stop() {}, async dispose() { this.disposed++; },
  };
  const frameLoader = { async load(url) { return { url }; }, cancel() {}, dispose() {} };
  const session = new SceneSession({ createViewer: () => viewer, frameLoader,
    fetchManifest: async () => ({ ok: true, url: source, text: async () => JSON.stringify(manifest()) }),
    notify: (message) => messages.push(message) });
  return { session, viewer, messages, frameLoader };
}

test('default manifest fetch retains the global receiver required by browser fetch', async () => {
  const originalFetch = globalThis.fetch;
  const { viewer, messages, frameLoader } = harness();
  let requestedUrl;
  globalThis.fetch = async function (url, { signal }) {
    assert.equal(this, globalThis, 'Illegal invocation: fetch requires its global receiver');
    assert.ok(signal instanceof AbortSignal);
    requestedUrl = url;
    return { ok: true, url: source, text: async () => JSON.stringify(manifest()) };
  };
  const session = new SceneSession({ createViewer: () => viewer, frameLoader, notify: (message) => messages.push(message) });
  try {
    await session.load(source);
    assert.equal(requestedUrl, source);
    assert.equal(messages.at(-1)?.type, 'ready');
  } finally {
    await session.dispose();
    globalThis.fetch = originalFetch;
  }
});

test('manifest resolves frames relative to its final URL and rejects invalid input', () => {
  assert.equal(parseManifest(manifest(), source).frames[0], 'https://example.test/scenes/basketball/frames/0.splat');
  for (const change of [ { version: 2 }, { frameRate: 0 }, { frames: [] }, { frames: ['blob:invalid'] }, { frames: Array(20).fill('file:///tmp/a.splat') }, { camera: { ...manifest().camera, up: [0, 0, 0] } } ]) {
    assert.throws(() => parseManifest({ ...manifest(), ...change }, source));
  }
});

test('manifest frame count and fractional frame rate determine the seek range', async () => {
  const { session, messages } = harness();
  session.fetchManifest = async () => ({ ok: true, url: source, text: async () => JSON.stringify({ ...manifest(), frameRate: 12.5, frames: ['0.splat', '1.splat', '2.splat'] }) });
  await session.load(source);
  await session.seek(100);
  assert.equal(messages.at(-1).frame, 2);
  assert.equal(messages.at(-1).frameCount, 3);
  assert.equal(messages.at(-1).frameRate, 12.5);
  await session.dispose();
});

test('optional reference raster height is bounded and survives manifest parsing', () => {
  const input = manifest();
  input.camera.referenceImageHeight = 270;
  assert.equal(parseManifest(input, source).camera.referenceImageHeight, 270);
  for (const height of [null, '270', 0, 63, 2161, 270.5, Infinity]) {
    input.camera.referenceImageHeight = height;
    assert.throws(() => parseManifest(input, source), /Invalid scene manifest/);
  }
});

test('optional calibrated intrinsics survive parsing and reject unstable projection values', () => {
  const input = manifest();
  const intrinsics = { width: 960, height: 803, fx: 915, fy: 916, cx: 476, cy: 407 };
  input.camera.intrinsics = intrinsics;
  assert.deepEqual(parseManifest(input, source).camera.intrinsics, intrinsics);
  for (const change of [null, { ...intrinsics, fy: Number.MIN_VALUE }, { ...intrinsics, fx: Infinity }]) {
    input.camera.intrinsics = change;
    assert.throws(() => parseManifest(input, source), /Invalid scene manifest/);
  }
});

test('seeks are serialized, skip queued intermediate frames, and preserve the orbit camera', async () => {
  const { session, viewer, messages } = harness();
  await session.load(source);
  viewer.camera.position.value = 42;
  viewer.controls.target.value = 17;
  const pending = deferred();
  const building = deferred();
  const add = viewer.addSplatBuffers.bind(viewer);
  viewer.addSplatBuffers = async (buffers) => {
    await add(buffers);
    if (buffers[0].url.endsWith('/1.splat')) { building.resolve(); await pending.promise; }
  };
  const first = session.seek(1);
  await building.promise;
  session.seek(2);
  session.seek(19);
  pending.resolve();
  await first;
  assert.deepEqual(viewer.calls.map((url) => url.split('/').at(-1)), ['0.splat', '1.splat', '19.splat']);
  assert.deepEqual(messages.filter((message) => message.type === 'ready').map((message) => message.frame), [0, 19]);
  assert.equal(viewer.camera.position.value, 42);
  assert.equal(viewer.controls.target.value, 17);
  assert.equal(viewer.scenes.length, 1);
  assert.equal(viewer.removed, 2);
  await session.dispose();
});

test('failed scene load releases the viewer and never reports ready', async () => {
  const { session, viewer, messages, frameLoader } = harness();
  frameLoader.load = async () => { throw new Error('private failure detail'); };
  await session.load(source);
  assert.equal(viewer.disposed, 1);
  assert.equal(messages.some((message) => message.type === 'ready'), false);
  assert.equal(messages.at(-1).type, 'error');
  assert.equal(JSON.stringify(messages).includes('private failure detail'), false);
});

test('repeated lifecycle notifications keep a single render loop', async () => {
  const { session, viewer } = harness();
  let starts = 0;
  let stops = 0;
  viewer.start = () => { starts++; };
  viewer.stop = () => { stops++; };
  await session.load(source);
  session.pause(false);
  session.pause(false);
  assert.equal(starts, 1);
  const beforePause = stops;
  session.pause(true);
  session.pause(true);
  assert.equal(stops, beforePause + 1);
  session.pause(false);
  session.pause(false);
  assert.equal(starts, 2);
  await session.dispose();
});

test('leaving during an in-flight load aborts work, disposes once, and ignores late completion', async () => {
  const { session, viewer, messages } = harness();
  const pending = deferred();
  viewer.addSplatBuffers = () => pending.promise;
  const loading = session.load(source);
  await new Promise((resolve) => setImmediate(resolve));
  await session.dispose();
  await session.dispose();
  pending.resolve();
  await loading;
  assert.equal(viewer.disposed, 1);
  assert.equal(session.abort.signal.aborted, true);
  assert.equal(messages.some((message) => message.type === 'ready'), false);
});

test('superseded preparation is canceled before building a mesh and keeps the latest camera', async () => {
  const { session, viewer, messages, frameLoader } = harness();
  await session.load(source);
  const pending = deferred();
  let canceled = 0;
  const load = frameLoader.load;
  frameLoader.load = (url) => url.endsWith('/1.splat') ? pending.promise : load(url);
  frameLoader.cancel = () => { canceled++; pending.reject(new DOMException('superseded', 'AbortError')); };
  const stopped = [];
  viewer.stop = () => stopped.push(true);
  const first = session.seek(1);
  viewer.camera.position.value = 73;
  assert.equal(stopped.length, 0, 'previous view remains interactive during preparation');
  session.seek(19);
  await first;
  assert.equal(canceled, 1);
  assert.deepEqual(viewer.calls.map((url) => url.split('/').at(-1)), ['0.splat', '19.splat']);
  assert.equal(viewer.camera.position.value, 73);
  assert.equal(messages.at(-1).frame, 19);
  await session.seek(19);
  assert.equal(viewer.calls.length, 2, 'already displayed frame is reused');
  await session.dispose();
});
