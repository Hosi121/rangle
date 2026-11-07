import test from 'node:test';
import assert from 'node:assert/strict';
import { Matrix4, PerspectiveCamera } from 'three';
import { SplatBuffer } from '@mkkellogg/gaussian-splats-3d';
import { TemporalSession } from '../src/temporal-session.js';

const manifest = { frameCount: 20, frameRate: 25, temporal: { url: 'https://example.test/model.rgs' },
  camera: {}, attribution: { label: 'Test', source: 'https://example.test', license: 'Test' } };
const flush = () => new Promise(resolve => setImmediate(resolve));

function harness() {
  const messages = [], commands = [], renders = [], counts = { builds: 0, starts: 0, stops: 0, closes: 0 };
  const worker = new EventTarget();
  worker.postMessage = message => commands.push(message);
  worker.terminate = () => { worker.terminated = true; };
  worker.emit = data => {
    const event = new Event('message');
    Object.defineProperty(event, 'data', { value: data });
    worker.dispatchEvent(event);
  };
  const viewer = {
    camera: new PerspectiveCamera(50, 960 / 803),
    renderer: { domElement: new EventTarget() },
    splatMesh: {
      matrixWorld: new Matrix4(), updateMatrixWorld() {},
      material: { vertexShader: 'vec3 splatCenter = uintBitsToFloat(uvec3(sampledCenterColor.gba));\nvColor = uintToRGBAVec(sampledCenterColor.r);', uniforms: {} },
      updateRenderIndexes(ids, count) { counts.active = count; counts.ids = Array.from(ids); },
    },
    async addSplatBuffers() { counts.builds++; }, disposeSortWorker() {},
    updateForRendererSizeChanges() {}, updateSplatMesh() {}, forceRenderNextFrame() {},
    render() { renders.push(this.camera.position.toArray()); },
    start() { counts.starts++; }, stop() { counts.stops++; },
    async dispose() { counts.closes++; },
  };
  viewer.camera.position.z = 3;
  const session = new TemporalSession({ createViewer: () => viewer, createWorker: () => worker,
    notify: message => messages.push({ ...message, renders: renders.length }) });
  const model = { type: 'model', frameCount: 20, frameRate: 25,
    buffer: SplatBuffer.preallocateUncompressed(1, 0).splatBuffer.bufferData,
    textureHeight: 1, texture: new Float32Array(2048 * 4) };
  const respond = command => worker.emit({ type: 'sorted', id: command.id,
    time: command.time, indexes: new Uint32Array([0]), active: 1 });
  const load = async (timeline = manifest) => {
    const promise = session.load(timeline);
    worker.emit(model); await flush();
    respond(commands.at(-1)); await promise;
  };
  return { session, worker, model, viewer, counts, commands, messages, renders, respond, load };
}

test('hidden preparation draws a real first frame before ready, then waits for visibility', async () => {
  const h = harness();
  h.session.pause(true);
  await h.load();
  assert.equal(h.counts.starts, 0);
  const ready = h.messages.find(message => message.type === 'ready');
  assert.equal(ready.canPlay, true);
  assert.ok(ready.renders > 0);
  h.session.pause(false); h.session.pause(false);
  assert.equal(h.counts.starts, 1);
  await h.session.dispose();
  assert.equal(h.worker.terminated, true);
});

test('fractional source rates match their float32 binary representation', async () => {
  const h = harness();
  h.model.frameRate = Math.fround(29.97);
  await h.load({ ...manifest, frameRate: 29.97 });
  assert.equal(h.messages.find(message => message.type === 'ready').frameRate, 29.97);
  await h.session.dispose();
});

test('seeks reuse one mesh and commit the matching camera snapshot', async () => {
  const h = harness(); await h.load();
  h.viewer.camera.position.x = 4;
  const sought = h.session.seek(10), command = h.commands.at(-1);
  h.viewer.camera.position.x = 100;
  h.respond(command); await sought;
  h.viewer.render();
  assert.equal(h.renders.at(-1)[0], 4, 'render uses the camera whose depth order was computed');
  assert.equal(h.viewer.camera.position.x, 100, 'controls retain their desired camera');
  assert.equal(h.session.position, .4);
  assert.equal(h.counts.builds, 1);
  assert.equal(h.commands.filter(command => command.type === 'load').length, 1);
  await h.session.dispose();
});

test('superseded sort results cannot display or acknowledge an obsolete seek', async () => {
  const h = harness(); await h.load();
  const old = h.session.seek(5), command = h.commands.at(-1);
  const latest = h.session.seek(19);
  h.respond(command); await old;
  h.respond(h.commands.at(-1)); await latest;
  assert.deepEqual(h.messages.filter(m => m.type === 'ready').map(m => m.frame), [0, 19]);
  assert.equal(h.counts.builds, 1);
  await h.session.dispose();
});

test('leaving during loading releases the worker and ignores late completion', async () => {
  const h = harness();
  const loading = h.session.load(manifest);
  const rejected = assert.rejects(loading, { name: 'AbortError' });
  const release = h.session.dispose();
  assert.equal(h.session.dispose(), release);
  await release; await rejected;
  h.worker.emit(h.model); await flush();
  assert.equal(h.counts.builds, 0);
  assert.equal(h.messages.some(message => message.type === 'ready'), false);
  assert.equal(h.worker.terminated, true);
});
