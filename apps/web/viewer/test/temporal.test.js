import test from 'node:test';
import assert from 'node:assert/strict';
import { Vector3 } from 'three';
import { SplatBuffer } from '@mkkellogg/gaussian-splats-3d';
import { prepareTemporal } from '../src/temporal-format.js';
import { opacityByte, TemporalSorter } from '../src/temporal-sort.js';
import { PlaybackClock } from '../src/playback-clock.js';
import { attachPlaybackGestures } from '../src/playback-gestures.js';
import { parseManifest } from '../src/session.js';

function fixture() {
  const data = new ArrayBuffer(32 + 3 * 56), h = new DataView(data);
  new Uint8Array(data).set(new TextEncoder().encode('RANGL4D1'));
  h.setUint32(8, 3, true); h.setUint32(12, 56, true); h.setUint32(16, 20, true);
  h.setFloat32(20, 25, true); h.setFloat32(24, .76, true);
  [9, -9, 1].forEach((x, i) => {
    const offset = 32 + i * 56;
    new Float32Array(data, offset, 6).set([x, 0, -2, .01, .01, .01]);
    new Uint8Array(data, offset + 24, 8).set([200, 100, 50, 255, 255, 128, 128, 128]);
    new Float32Array(data, offset + 32, 6).set([i, 0, 0, i / 2, .2, .5]);
  });
  return data;
}

test('canonical IDs survive preparation even when their opacity at t=0 is zero', () => {
  const model = prepareTemporal(fixture()), buffer = new SplatBuffer(model.buffer), point = new Vector3();
  assert.equal(buffer.getSplatCount(), 3);
  for (const [i, x] of [9, -9, 1].entries()) {
    buffer.getSplatCenter(i, point);
    assert.equal(point.x, x);
    assert.equal(model.motion[i * 9], x);
    assert.equal(model.texture[i * 8], i);
  }
  assert.equal(opacityByte(model.motion[26], 0, model.motion[24], model.motion[25]), 0);
  assert.equal(opacityByte(model.motion[26], 1, model.motion[24], model.motion[25]), 128);
});

test('model size, timeline and finite primitive checks happen before use', () => {
  for (const mutate of [
    b => new DataView(b).setUint32(8, 1000000, true),
    b => new DataView(b).setFloat32(24, 5, true),
    b => new DataView(b).setFloat32(32, NaN, true),
    b => new DataView(b).setFloat32(32 + 48, 0, true),
    b => new DataView(b).setFloat32(32 + 52, 2, true),
  ]) {
    const bytes = fixture(); mutate(bytes);
    assert.throws(() => prepareTemporal(bytes));
  }
});

test('depth order follows moving centres and primitives can appear after frame zero', () => {
  const sorter = new TemporalSorter(new Float32Array([
    0, 0, -2, 0, 0, 2, .5, 1, 1,
    0, 0, -2, 0, 0, -2, .5, 1, 1,
    0, 0, -5, 0, 0, 0, 1, .02, 1,
  ]));
  const view = [1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1];
  let result = sorter.sort(0, view);
  assert.deepEqual(Array.from(result.indexes.slice(0, result.active)), [0, 1]);
  result = sorter.sort(1, view);
  assert.deepEqual(Array.from(result.indexes.slice(0, result.active)), [2, 1, 0]);
  result = sorter.sort(.5, view);
  assert.deepEqual(Array.from(result.indexes.slice(0, result.active)), [0, 1]);
  assert.equal(opacityByte(Math.fround(.5 / 255), 0, 0, 1), 0);
  assert.equal(opacityByte(Math.fround(1.5 / 255), 0, 0, 1), 2);
});

test('playback loops at source cadence, seeks precisely, and excludes hidden time', () => {
  let now = 0;
  const clock = new PlaybackClock(20, 25, () => now);
  clock.toggle(); now = 400;
  assert.equal(clock.value(), .4);
  clock.suspend(true); now = 10000;
  assert.equal(clock.value(), .4);
  clock.suspend(false); now += 100;
  assert.equal(clock.value(), .5);
  clock.seek(.76); now += 20;
  assert.equal(clock.value(), .76);
  now += 40;
  assert.ok(Math.abs(clock.value() - .02) < 1e-9);
  clock.toggle(); now += 1000;
  assert.ok(Math.abs(clock.value() - .02) < 1e-9);
});

test('double click and touch double tap toggle once; dragging and pinch do not', () => {
  const element = new EventTarget(); let count = 0, now = 0;
  const release = attachPlaybackGestures(element, () => count++, () => now);
  const emit = (type, extra = {}) => {
    const event = new Event(type, { cancelable: true });
    Object.assign(event, { pointerId: 1, pointerType: 'touch', clientX: 10, clientY: 10, ...extra });
    element.dispatchEvent(event);
  };
  emit('dblclick'); assert.equal(count, 1);
  emit('pointerdown'); emit('pointerup'); now = 100;
  emit('pointerdown'); emit('pointerup'); emit('dblclick'); assert.equal(count, 2);
  now = 1000; emit('pointerdown'); emit('pointerup', { clientX: 100 });
  now += 100; emit('pointerdown'); emit('pointerup'); assert.equal(count, 2);
  emit('pointerdown'); emit('pointerdown', { pointerId: 2 });
  emit('pointerup', { pointerId: 2 }); emit('pointerup'); assert.equal(count, 2);
  release(); emit('dblclick'); assert.equal(count, 2);
});

test('temporal manifest resolves its model independently of legacy snapshot URLs', () => {
  const input = { version: 1, frameRate: 25, frameCount: 20, temporal: { url: 'model.rgs' },
    camera: { position: [0, 0, 2], lookAt: [0, 0, 0], up: [0, 1, 0], fovYDegrees: 50 },
    attribution: { label: 'Example', source: 'https://example.test', license: 'Test' } };
  const source = 'https://example.test/clip/scene.json';
  assert.equal(parseManifest(input, source).temporal.url, 'https://example.test/clip/model.rgs');
  for (const change of [{ frameCount: 1 }, { temporal: { url: 'file:///x.rgs' } },
    { temporal: { url: 'frame.splat' } }]) assert.throws(() => parseManifest({ ...input, ...change }, source));
});
