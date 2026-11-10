import test from 'node:test';
import assert from 'node:assert/strict';
import { SnapshotClock } from '../src/snapshot-clock.js';

test('ten seconds stays on the source clock when installs are slow; pause releases scheduled work', async () => {
  let now = 0, release;
  const shown = [], scheduled = new Map(); let sequence = 0;
  const player = new SnapshotClock({ frames: 250, fps: 25, now: () => now,
    show: async frame => { shown.push(frame); await new Promise(resolve => { release = resolve; }); },
    schedule: callback => { scheduled.set(++sequence, callback); return sequence; },
    cancel: id => scheduled.delete(id) });
  player.play();
  now = 400;
  const callback = scheduled.values().next().value; scheduled.clear();
  const pending = callback();
  assert.deepEqual(shown, [10]);
  now = 1200; release(); await pending;
  const next = scheduled.values().next().value; scheduled.clear();
  const later = next();
  assert.deepEqual(shown, [10, 30]);
  player.pause(); release(); await later;
  assert.equal(scheduled.size, 0);
  assert.equal(player.clock.playing, false);
});

test('source loops at 250/25 seconds and explicit seek clamps to the last sample', async () => {
  let now = 0;
  const frames = [];
  const player = new SnapshotClock({ frames: 250, fps: 25, now: () => now,
    show: async frame => frames.push(frame), schedule: () => 1, cancel() {} });
  player.play();
  now = 9999; await player.tick();
  now = 10000; await player.tick();
  assert.deepEqual(frames, [249, 0]);
  await player.seek(999);
  assert.equal(frames.at(-1), 249);
  assert.equal(player.clock.playing, false);
  assert.equal(player.clock.value(), 9.96);
  await assert.rejects(player.seek(NaN));
  player.dispose();
});
