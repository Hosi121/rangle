import test from 'node:test';
import assert from 'node:assert/strict';
import { privateSceneSource, snapshotChoices, SnapshotLoader } from '../src/private-preview-model.js';

test('private scene selection stays on the authenticated origin and rejects ambiguous paths', () => {
  assert.equal(privateSceneSource('?scene=sample-one', 'https://app.test'), 'https://app.test/api/v1/private-scenes/sample-one/scene.json');
  for (const query of ['?scene=../other', '?scene=one&scene=two', '?scene=https://evil.test', '?scene=', '?scene=.hidden']) {
    assert.throws(() => privateSceneSource(query, 'https://app.test'));
  }
});

test('snapshot labels retain captured source times instead of inventing consecutive frames', () => {
  const source = 'https://app.test/api/v1/private-scenes/sample/scene.json';
  assert.deepEqual(snapshotChoices({ frames: ['frame-000.splat', 'frame-010.splat', 'frame-019.splat'], frameRate: 30 }, source),
    [{ index: 0, sourceFrame: 0, seconds: 0 }, { index: 1, sourceFrame: 10, seconds: 1 / 3 }, { index: 2, sourceFrame: 19, seconds: 19 / 30 }]);
  for (const frame of ['https://other.test/frame-000.splat', '../frame-000.splat', 'frame-000.splat?token=x']) {
    assert.throws(() => snapshotChoices({ frames: [frame], frameRate: 30 }, source));
  }
});

test('private snapshot cache is bounded, reuses prepared data, and clears on disposal', async () => {
  const calls = [];
  const loader = new SnapshotLoader({ load: async url => { calls.push(url); return { url }; }, cancel() {}, dispose() {} }, 2);
  const first = await loader.load('one');
  await loader.load('two');
  assert.equal(await loader.load('one'), first);
  await loader.load('three');
  await loader.load('two');
  assert.deepEqual(calls, ['one', 'two', 'three', 'two']);
  loader.dispose();
  assert.equal(loader.cache.size, 0);
  await assert.rejects(loader.load('one'));
});

test('disposing during a preparation never retains the completed private buffer', async () => {
  let finish;
  const loader = new SnapshotLoader({ load: () => new Promise(resolve => { finish = resolve; }), cancel() {}, dispose() {} });
  const pending = loader.load('one');
  loader.dispose();
  finish({});
  await assert.rejects(pending, { name: 'AbortError' });
  assert.equal(loader.cache.size, 0);
});
