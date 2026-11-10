import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { SceneSession } from '../src/session.js';
import { decodeTemporal, temporalHeader, checkTemporalTimeline } from '../src/temporal-codec.js';
import { prepareTemporal } from '../src/temporal-format.js';
import { handoffTimeline } from '../src/playback-handoff.js';
import { PlaybackClock } from '../src/playback-clock.js';

const root = new URL('../../../../testdata/contracts/v1/', import.meta.url);
const corpus = JSON.parse(await readFile(new URL('cases.json', root), 'utf8'));
const read = name => readFile(new URL(name, root));
const buffer = bytes => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);

for (const row of corpus.cases) {
  if (!['rgs', 'scene', 'bundle', 'clock'].includes(row.profile)) continue;
  test(`shared contract ${row.id} (${row.profile})`, async () => {
    const run = async () => {
      let bytes = await read(row.file);
      if (row.recipe?.kind === 'pad-to-size') {
        const padded = Buffer.alloc(row.recipe.bytes);
        bytes.copy(padded); bytes = padded;
      }
      if (row.profile === 'rgs') {
        const raw = await decodeTemporal(buffer(bytes));
        prepareTemporal(raw);
        const header = temporalHeader(raw);
        if (row.accept) {
          assert.equal(header.frameRate, row.normalized.fps);
          assert.equal((header.frameCount - 1) / header.frameRate, row.normalized.end);
          assert.equal(createHash('sha256').update(new Uint8Array(raw)).digest('hex'), row.normalized.raw_sha256);
        }
      } else if (row.profile === 'scene') {
        const source = corpus.context.scene_url;
        const session = new SceneSession({ fetchManifest: async () => new Response(bytes) });
        session.liveOptions = { live: true, origins: [corpus.context.origin] };
        const manifest = await session.manifestFrom(source, new AbortController().signal);
        const raw = await decodeTemporal(buffer(await read(`rgs/${row.model}.rgs`)));
        checkTemporalTimeline(prepareTemporal(raw), manifest);
      } else if (row.profile === 'clock') {
        const value = JSON.parse(bytes);
        const manifest = { ...value, temporal: { url: 'model.rgs' } };
        const timeline = handoffTimeline(manifest, value.context);
        assert.equal(timeline.frameRate, value.frameRate);
        assert.equal(timeline.frameCount, value.frameCount);
        assert.deepEqual(timeline.mapping, value.mapping);
        const clock = new PlaybackClock(timeline.frameCount, timeline.frameRate);
        assert.equal(clock.duration, value.end);
        clock.seek(100);
        assert.equal(clock.value(), value.end);
      } else {
        // Real worker entrypoint with synthetic network bytes, no canvas/GPU.
        const index = JSON.parse(bytes), segment = index.sessions[0].segments[0];
        const originalSelf = globalThis.self, originalFetch = globalThis.fetch;
        let message;
        let resolve;
        const ready = new Promise(done => { resolve = done; });
        globalThis.self = { addEventListener: (_type, listener) => { message = listener; }, postMessage: resolve };
        globalThis.fetch = async () => new Response(await read(`rgs/${row.model}.rgs`));
        try {
          await import(`../src/temporal-worker.js?contract=${row.id}`);
          message({ data: { type: 'load', url: 'https://media.example.test/model.rgs', strict: true, sha256: segment.model_sha256 } });
          const response = await ready;
          if (response.type !== 'model') throw new Error('Worker rejected model proof');
        } finally {
          globalThis.self = originalSelf;
          globalThis.fetch = originalFetch;
        }
      }
    };
    if (row.accept) await run();
    else await assert.rejects(run());
  });
}
