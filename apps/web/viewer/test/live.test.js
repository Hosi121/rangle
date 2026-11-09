import test from 'node:test';
import assert from 'node:assert/strict';
import { Matrix4, PerspectiveCamera, Vector3 } from 'three';
import { liveSceneUrl } from '../src/live-model.js';
import { TemporalSession } from '../src/temporal-session.js';
import { PlaybackClock } from '../src/playback-clock.js';
import { SceneSession } from '../src/session.js';

const origin = 'https://app.example.test';
const scene = n => `${origin}/live-assets/game/${String(n).padStart(6, '0')}-${'a'.repeat(16)}/scene.json`;
const manifest = () => ({ version: 1, frameRate: 25, frameCount: 20,
  temporal: { url: 'model.rgs' }, camera: { position: [0, 0, 2], lookAt: [0, 0, 0], up: [0, 1, 0], fovYDegrees: 50 },
  attribution: { label: 'Example', source: 'https://source.example.test', license: 'Test' } });
class Worker extends EventTarget {
  messages = []; terminated = 0;
  postMessage(message) { this.messages.push(message); }
  terminate() { this.terminated++; }
  emit(data) { this.dispatchEvent(Object.assign(new Event('message'), { data })); }
}
function harness() {
  const workers = [], candidates = [], messages = [];
  const mesh = () => ({ matrixWorld: new Matrix4(), disposed: 0,
    updateMatrixWorld() {}, updateRenderIndexes(indexes) { this.indexes = indexes; }, dispose() { this.disposed++; } });
  const oldMesh = mesh(), camera = new PerspectiveCamera(), target = new Vector3(1, 2, 3);
  const viewer = { camera, controls: { target }, splatMesh: oldMesh,
    updateSplatMesh() {}, render() {}, forceRenderNextFrame() {}, stop() {}, start() {}, dispose() {} };
  const session = new TemporalSession({ createViewer() { throw new Error('must reuse viewer'); },
    notify: m => messages.push(m), createWorker: () => { const worker = new Worker(); workers.push(worker); return worker; },
    prepareReplacement: () => {
      const candidate = { mesh: mesh(), material: { setTime() {}, dispose() {} }, disposed: false,
        dispose() { this.disposed = true; this.mesh.dispose(); } };
      candidates.push(candidate); return candidate;
    } });
  session.initialized = true; session.viewer = viewer; session.displayCamera = camera.clone();
  session.material = { dispose() {} }; session.worker = new Worker();
  session.clock = new PlaybackClock(20, 25); session.clock.toggle();
  return { session, viewer, workers, candidates, messages, oldMesh, camera, target };
}
const model = { type: 'model', frameCount: 20, frameRate: 25 };
function sorted(worker) {
  const job = worker.messages.at(-1);
  worker.emit({ type: 'sorted', id: job.id, indexes: new Uint32Array([0]), active: 1 });
}

test('hot model swap keeps the same viewer, controls and latest orbit camera until a draw succeeds', async () => {
  const h = harness(), oldWorker = h.session.worker;
  const replacing = h.session.replace({ ...manifest(), temporal: { url: 'https://app.example.test/model.rgs' } });
  h.camera.position.set(9, 8, 7);
  h.target.set(6, 5, 4);
  h.workers[0].emit(model);
  assert.equal(h.viewer.splatMesh, h.oldMesh, 'old model remains active while candidate sorts');
  assert.equal(h.oldMesh.disposed, 0);
  sorted(h.workers[0]);
  assert.equal(await replacing, true);
  assert.equal(h.session.viewer, h.viewer);
  assert.equal(h.viewer.camera, h.camera);
  assert.equal(h.viewer.controls.target, h.target);
  assert.deepEqual(h.camera.position.toArray(), [9, 8, 7]);
  assert.deepEqual(h.target.toArray(), [6, 5, 4]);
  assert.equal(h.oldMesh.disposed, 1);
  assert.equal(oldWorker.terminated, 1);
  assert.equal(h.session.clock.playing, true);
  await h.session.dispose();
});

test('replacement failure preserves the interactive previous mesh and manual pause', async () => {
  const h = harness(); h.session.clock.toggle();
  const oldWorker = h.session.worker;
  h.viewer.render = () => { throw new Error('GPU preparation failed'); };
  const replacement = h.session.replace(manifest());
  h.workers[0].emit(model); sorted(h.workers[0]);
  assert.equal(await replacement, false);
  assert.equal(h.viewer.splatMesh, h.oldMesh);
  assert.equal(h.session.worker, oldWorker);
  assert.equal(oldWorker.terminated, 0);
  assert.equal(h.oldMesh.disposed, 0);
  assert.equal(h.session.clock.playing, false);
  assert.equal(h.candidates[0].disposed, true);
  await h.session.dispose();
});

test('newest replacement cancels only the candidate; failure and late completion cannot evict the current model', async () => {
  const h = harness();
  const first = h.session.replace(manifest()); h.workers[0].emit(model);
  const second = h.session.replace(manifest());
  assert.equal(await first, false);
  assert.equal(h.workers[0].terminated, 1);
  assert.equal(h.candidates[0].disposed, true);
  sorted(h.workers[0]);
  h.workers[1].emit({ type: 'error' });
  assert.equal(await second, false);
  assert.equal(h.viewer.splatMesh, h.oldMesh);
  assert.equal(h.oldMesh.disposed, 0);
  await h.session.dispose();
});

test('live paths require configured origin, exact session scope and immutable revision', () => {
  assert.equal(liveSceneUrl(scene(1), [origin]).session, 'game');
  for (const url of [scene(1).replace(origin, 'https://evil.example.test'), `${scene(1)}?x=1`,
    scene(1).replace('/game/', '/other/'), `${origin}/scene.json`, `https://user:pass@app.example.test${new URL(scene(1)).pathname}`]) {
    assert.throws(() => liveSceneUrl(url, [origin], 'game'));
  }
});

test('scene replacement rejects a manifest that points its model at another origin', async () => {
  let requests = 0, mutations = 0;
  const session = new SceneSession({ createViewer() {}, frameLoader: { dispose() {} }, notify() {},
    fetchManifest: async () => { requests++; return new Response(JSON.stringify({ ...manifest(), temporal: { url: 'https://evil.example.test/model.rgs' } })); } });
  session.liveOptions = { live: true, origins: [origin] };
  session.liveSession = 'game'; session.initialLoad = Promise.resolve();
  session.temporal = { replace() { mutations++; }, cancelReplacement() {}, dispose() {} };
  assert.equal(await session.replace(scene(2), 'a'.repeat(64)), false);
  assert.equal(requests, 1); assert.equal(mutations, 0);
  assert.equal(session.disposed, false);
  await session.dispose();
});

test('live clock holds its last source frame without looping while VOD still loops', () => {
  let now = 0;
  const live = new PlaybackClock(20, 25, () => now, { loop: false });
  const vod = new PlaybackClock(20, 25, () => now);
  live.toggle(); vod.toggle(); now = 10000;
  assert.equal(live.value(), .76);
  assert.equal(live.ended, true);
  assert.ok(vod.value() < .76);
  live.suspend(true); now += 10000;
  assert.equal(live.value(), .76);
});

test('a held live window advances on a replacement but explicit user pause persists', async () => {
  const h = harness(); h.session.live = true; h.session.userPaused = false;
  h.session.clock = new PlaybackClock(20, 25, () => 10000, { loop: false });
  h.session.clock.toggle(); h.session.clock.seek(.76);
  assert.equal(h.session.clock.ended, true);
  let update = h.session.replace(manifest()); h.workers[0].emit(model); sorted(h.workers[0]);
  assert.equal(await update, true); assert.equal(h.session.clock.playing, true);
  h.session.togglePlayback(); assert.equal(h.session.userPaused, true);
  update = h.session.replace(manifest()); h.workers[1].emit(model); sorted(h.workers[1]);
  assert.equal(await update, true); assert.equal(h.session.clock.playing, false);
  await h.session.dispose();
});

test('pinned SDK builds a candidate mesh without modifying displayed geometry (CPU only)', async () => {
  const { Viewer, SplatBuffer } = await import('@mkkellogg/gaussian-splats-3d');
  const { prepareLiveMesh } = await import('../src/live-model.js');
  const viewer = { createSplatMesh: Viewer.prototype.createSplatMesh };
  viewer.createSplatMesh();
  const old = viewer.splatMesh;
  const candidate = prepareLiveMesh(viewer, {
    buffer: SplatBuffer.preallocateUncompressed(1, 0).splatBuffer.bufferData,
    textureHeight: 1, texture: new Float32Array(2048 * 4),
  });
  assert.equal(viewer.splatMesh, old);
  assert.equal(old.getSplatCount(), 0);
  assert.equal(candidate.mesh.getSplatCount(), 1);
  assert.notEqual(candidate.mesh.geometry, old.geometry);
  candidate.dispose(); old.dispose();
});
