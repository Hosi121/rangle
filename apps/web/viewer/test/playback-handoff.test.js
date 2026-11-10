import test from 'node:test';
import assert from 'node:assert/strict';
import { handoffTimeline } from '../src/playback-handoff.js';
import { SceneSession } from '../src/session.js';

const manifest = { frameRate: 25, frameCount: 20, temporal: {} };
const options = { source: 'https://media.example.test/assets/game/r1/full/scene.json',
  previewUrl: 'https://media.example.test/assets/game/r1/full/preview.webm' };
test('source anchors are explicit and bound to the exact preview, never title-derived', () => {
  assert.equal(handoffTimeline({ ...manifest, title: '10–20 seconds' }, options).mapping, null);
  const mapping = { previewStartSeconds: 2, sceneStartSeconds: 0 };
  assert.deepEqual(handoffTimeline(manifest, { ...options, mapping }).mapping, mapping);
  const explicit = { ...manifest, timeline: { ...mapping, previewUrl: 'preview.webm' } };
  assert.deepEqual(handoffTimeline(explicit, options).mapping, mapping);
  assert.equal(handoffTimeline(explicit, { ...options, previewUrl: 'https://other.test/preview.webm', mapping }).mapping, null);
  assert.throws(() => handoffTimeline({ ...explicit, timeline: { ...explicit.timeline, sceneStartSeconds: -1 } }, options));
});

test('capture reports the last drawn scene time and logical intent without advancing or rebuilding it', () => {
  const active = { temporal: { position: .56, clock: { playing: false, value() { throw new Error('desired time must not replace drawn time'); } } } };
  assert.deepEqual(SceneSession.prototype.capturePlayback.call(active), { positionSeconds: .56, playing: false });
  assert.deepEqual(SceneSession.prototype.capturePlayback.call({ loadedFrame: 19, manifest: { frameRate: 25 } }),
    { positionSeconds: .76, playing: false });
});
