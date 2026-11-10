import test from 'node:test';
import assert from 'node:assert/strict';
import { parseTransport } from '../src/transport-manifest.js';
import { TransportABR } from '../src/transport-abr.js';
import { TransportPlayback } from '../src/transport-playback.js';
import { parseManifest } from '../src/session.js';
import { createHash } from 'node:crypto';
import { SegmentCache } from '../src/segment-cache.js';
import { MemoryStorage, MemoryLocks } from './segment-cache-fixture.js';

test('transport reads irregular chunks exactly and rejects short or overflowing bodies', async () => {
  const expected = Uint8Array.from({ length: 256 }, (_, i) => i);
  for (const length of [256, 255, 257]) {
    const body = Uint8Array.from({ length }, (_, i) => i % 256);
    const playback = new TransportPlayback({ createViewer() {}, notify() {}, async fetchAsset() {
      let offset = 0;
      return new Response(new ReadableStream({ pull(controller) {
        if (offset === body.length) { controller.close(); return; }
        const end = Math.min(body.length, offset + (offset % 17) + 1);
        controller.enqueue(body.slice(offset, end)); offset = end;
      } }));
    } });
    const read = playback.read('https://media.test/model.rgs', 256, new AbortController().signal);
    if (length === 256) assert.deepEqual(await read, expected);
    else await assert.rejects(read, /size mismatch/);
  }
});
const source = 'https://media.test/manifests/clip/r1.json';
function manifest(count = 20) {
  return { version: 1, clip_id: 'clip', revision: 'r1', codec: 'rangle-temporal-rgs1-segments-v1',
    representations: ['low', 'mid', 'high'].map((id, i) => ({ id, bitrate_bps: 1000000 * 2 ** i,
      peak_decoded_bytes: 1048576 * 2 ** i, decode_time_ms: 100 * (i + 1),
      segments: Array.from({ length: count }, (_, index) => ({ index, start_seconds: index * 2,
        duration_seconds: 2, url: `/assets/clip/r1/${id}/model-${index}.rgs`, size_bytes: 250000 * 2 ** i,
        independent: true, sha256: '0'.repeat(64) })) })) };
}
test('transport parser binds identity, exact fields, paths, proofs and aligned timeline', () => {
  assert.equal(parseTransport(manifest(), source).representations.length, 3);
  for (const mutate of [m => m.revision = 'r2', m => m.representations[0].segments[1].start_seconds++,
    m => m.representations[0].segments[0].independent = false, m => m.representations[0].segments[0].url += '?token=x',
    m => m.representations[0].segments[0].sha256 = 'bad', m => m.representations[1].segments.pop(),
    m => m.representations[0].segments[0].unexpected = true]) {
    const m = manifest(); mutate(m); assert.throws(() => parseTransport(m, source));
  }
  assert.throws(() => parseTransport(JSON.stringify(manifest()).replace('"version":1', '"version":1,"version":1'), source));
});
test('Go collapse/recovery vector and excluded samples have identical decisions', () => {
  const abr = new TransportABR(parseTransport(manifest(), source), { fast: 1, slow: 4, headroom: 1.1 });
  const trace = [[8000000,1,'low','hold'],[8000000,1,'mid','upshift'],[8000000,1,'mid','hold'],
    [8000000,1,'high','upshift'],[1000000,2,'mid','downshift'],[200000,4,'low','bandwidth-fallback'],
    [8000000,1,'low','hold'],[8000000,1,'mid','upshift'],[8000000,1,'mid','hold'],
    [8000000,1,'mid','hold'],[8000000,1,'high','upshift']];
  let currentId = 'low';
  trace.forEach(([rate,seconds,id,reason], segmentIndex) => {
    abr.observe({ bytes: rate * seconds / 8, seconds });
    assert.equal(abr.observe({ bytes: 99999999, seconds: .01, cacheHit: true }), false);
    const d = abr.select({ segmentIndex, currentId, bufferSeconds: 8 });
    assert.equal(d.representation.id, id); assert.equal(d.reason, reason); currentId = id;
  });
  assert.throws(() => new TransportABR(parseTransport(manifest(), source), { maxDecodedBytes: 1 }).select({ segmentIndex: 0 }));
});
function playbackFixture(tamper = false, legacy = false, segmentCache, animation = {}) {
  const raw = manifest(2); raw.representations = [raw.representations[0]];
  if (legacy) { raw.codec = 'rangle-temporal-rgs1'; raw.representations[0].segments.length = 1; raw.representations[0].segments[0].duration_seconds = 1.5; }
  const r = raw.representations[0], bodies = new Map(), requests = [], events = [], rendered = [];
  const scene = { version: 1, frameRate: 2, frameCount: 4,
    camera: { position:[0,0,3], lookAt:[0,0,0], up:[0,1,0], fovYDegrees:45 },
    attribution: { label:'test', source:'https://source.test/data', license:'CC BY 4.0' }, temporal:{url:'model.rgs'} };
  r.init_url = '/assets/clip/r1/low/scene.json';
  const init = new TextEncoder().encode(JSON.stringify(scene)); r.init_size_bytes = init.length;
  bodies.set(new URL(r.init_url, source).href, init);
  r.segments.forEach((s,i) => { s.url = `/assets/clip/r1/low/${i ? 'model-00001.rgs' : 'model.rgs'}`;
    const body = Uint8Array.of(i,1,2,3); s.size_bytes = body.length; s.sha256 = createHash('sha256').update(body).digest('hex');
    bodies.set(new URL(s.url, source).href, i && tamper ? Uint8Array.of(9,9,9,9) : body); });
  const temporal = { clock:{playing:false}, pause(){}, userPaused:false,
    async load(m,o){rendered.push({m,o});}, async replace(m,o){rendered.push({m,o});return true;},
    async seek(frame){this.frame=frame;}, cancelReplacement(){}, async dispose(){this.disposed=true;} };
  const playback = new TransportPlayback({ createViewer(){}, notify:m=>events.push(m), parseScene:parseManifest,
    requestFrame(){return 1;}, cancelFrame(){}, createTemporal(){return temporal;}, ...animation, ...(segmentCache ? { segmentCache } : {}),
    async fetchAsset(url, options){requests.push({url,options});return new Response(bodies.get(url),{status:200,headers:{'Cache-Control':'public,max-age=600',Age:'0'}});} });
  return { playback, manifest:parseTransport(raw,source), requests, events, rendered, temporal };
}
test('real loader fetches manifest-selected segments, verifies hashes, maps global seek and owns disposal', async () => {
  const f = playbackFixture(); await f.playback.load(f.manifest, { source });
  assert.equal(f.events.find(e=>e.type==='ready').frameCount, 8);
  await f.playback.present(5, true, 7);
  assert.equal(f.temporal.frame, 1); assert.equal(f.playback.position, 2.5);
  assert.equal(f.rendered.length, 2); assert.equal(f.rendered[1].o.bytes.byteLength, 4);
  assert.equal(f.events.find(e=>e.presentationId===7).frame, 5);
  assert.ok(f.requests.every(r=>r.options.credentials==='omit' && r.options.redirect==='error'));
  await f.playback.seek(1); assert.equal(f.temporal.frame,1);
  assert.equal(f.rendered.length,3); // backward seek reacquires bounded data, no unbounded cache
  await f.playback.dispose(); assert.equal(f.temporal.disposed,true);
});
test('tampered segment never reaches replacement/renderer', async () => {
  const f = playbackFixture(true); await f.playback.load(f.manifest,{source});
  await f.playback.seek(5); assert.equal(f.rendered.length,1);
  assert.ok(f.events.some(e=>e.type==='error')); assert.equal(f.temporal.disposed,true);
});

test('legacy manifest preserves the single-model sample endpoint and hash acquisition', async () => {
  const f = playbackFixture(false, true); await f.playback.load(f.manifest,{source});
  assert.equal(f.events.find(e=>e.type==='ready').frameCount,4);
  assert.equal(f.playback.clock.duration,1.5);
  await f.playback.seek(3); assert.equal(f.playback.position,1.5);
  assert.equal(f.rendered.length,1); await f.playback.dispose();
});
test('pause aborts an in-flight seek without publishing an error or stale replacement', async () => {
  const f = playbackFixture(); await f.playback.load(f.manifest,{source});
  let start;
  const entered = new Promise(resolve => { start = resolve; });
  const fetchAsset=f.playback.fetchAsset;
  f.playback.fetchAsset = (url,options) => url.endsWith('model-00001.rgs') ?
    new Promise((resolve,reject)=>{start();options.signal.addEventListener('abort',()=>reject(new DOMException('aborted','AbortError')),{once:true});}) : fetchAsset(url,options);
  const seek=f.playback.seek(5); await entered; f.playback.pause(true); await seek;
  assert.equal(f.rendered.length,1); assert.ok(!f.events.some(e=>e.type==='error'));
  f.playback.pause(false); await f.playback.seek(1); assert.equal(f.temporal.frame,1);
  await f.playback.dispose();
});

test('asset bounds are checked before fetch and body readers cancel on timeout and abort', async () => {
  let calls=0, cancelled=0;
  const playback=new TransportPlayback({transferTimeoutMs:10, fetchAsset:async()=>{
    calls++; return new Response(new ReadableStream({pull(){},cancel(){cancelled++;}}));
  }});
  await assert.rejects(playback.read('https://media.test/model.rgs',33554433,new AbortController().signal));
  assert.equal(calls,0);
  await assert.rejects(playback.read('https://media.test/model.rgs',4,new AbortController().signal),{name:'TimeoutError'});
  assert.equal(cancelled,1);
  const controller=new AbortController();
  const pending=playback.read('https://media.test/model.rgs',4,controller.signal);
  await new Promise(resolve=>setTimeout(resolve,0)); controller.abort();
  await assert.rejects(pending,{name:'AbortError'}); assert.equal(cancelled,2);
});
test('late fetch completions after timeout are cancelled, not used', async () => {
  let finish,cancelled=0;
  const playback=new TransportPlayback({transferTimeoutMs:5,fetchAsset:()=>new Promise(resolve=>{finish=resolve;})});
  const pending=playback.read('https://media.test/model.rgs',4,new AbortController().signal);
  await assert.rejects(pending,{name:'TimeoutError'});
  finish(new Response(new ReadableStream({cancel(){cancelled++;}})));
  await new Promise(resolve=>setTimeout(resolve,0)); assert.equal(cancelled,1);
});

test('outer session rejects transport redirects and binds API identity to requested manifest', async () => {
  const {SceneSession}=await import('../src/session.js');
  const session=new SceneSession({createViewer(){},frameLoader:{dispose(){}},notify(){}});
  session.liveOptions={};
  session.fetchManifest=async(url,options)=>{
    assert.equal(options.redirect,'error'); assert.equal(options.credentials,'omit');
    return {ok:true,url:'https://media.test/manifests/other/r2.json',headers:new Headers(),body:new Response(JSON.stringify(manifest())).body};
  };
  await assert.rejects(session.manifestFrom(source,new AbortController().signal),/redirected/);
  session.fetchManifest=async()=>({ok:true,url:source,headers:new Headers(),body:new Response(JSON.stringify({...manifest(),clip_id:'other'})).body});
  await assert.rejects(session.manifestFrom(source,new AbortController().signal),/identity/);
  session.liveOptions={transportIdentity:{clipId:'clip',revision:'r1',codec:'rangle-temporal-rgs1'}};
  session.fetchManifest=async()=>({ok:true,url:source,headers:new Headers(),body:new Response(JSON.stringify(manifest())).body});
  await assert.rejects(session.manifestFrom(source,new AbortController().signal),/Invalid transport/);
});

test('expected transport never downgrades to an unproved scene and manifest bodies are bounded/cancellable', async () => {
  const {SceneSession}=await import('../src/session.js');
  const session=new SceneSession({createViewer(){},frameLoader:{dispose(){}},notify(){}});
  session.liveOptions={transportIdentity:{clipId:'clip',revision:'r1',codec:'rangle-temporal-rgs1-segments-v1'}};
  const scene={version:1,frameRate:2,frames:['a.splat'],camera:{position:[0,0,3],lookAt:[0,0,0],up:[0,1,0],fovYDegrees:45},attribution:{label:'x',source:'https://source.test/',license:'CC BY 4.0'}};
  session.fetchManifest=async()=>({ok:true,url:source,headers:new Headers(),body:new Response(JSON.stringify(scene)).body});
  await assert.rejects(session.manifestFrom(source,new AbortController().signal),/Invalid transport/);
  let cancelled=0;
  session.fetchManifest=async()=>({ok:true,url:source,headers:new Headers({'Content-Length':'50000000'}),
    body:new ReadableStream({cancel(){cancelled++;}}),text(){throw Error('Unbounded text path used');}});
  await assert.rejects(session.manifestFrom(source,new AbortController().signal),/too large/); assert.equal(cancelled,1);
  session.fetchManifest=async()=>({ok:true,url:source,headers:new Headers(),body:new ReadableStream({
    start(c){c.enqueue(new Uint8Array(1048577));},cancel(){cancelled++;}})});
  await assert.rejects(session.manifestFrom(source,new AbortController().signal),/too large/); assert.equal(cancelled,2);
  let entered;
  const reading=new Promise(resolve=>{entered=resolve;});
  session.fetchManifest=async()=>({ok:true,url:source,headers:new Headers(),body:new ReadableStream({pull(){entered();},cancel(){cancelled++;}})});
  const controller=new AbortController(), pending=session.manifestFrom(source,controller.signal);
  await reading; controller.abort(); await assert.rejects(pending,{name:'AbortError'}); assert.equal(cancelled,3);
});

test('explicit cancellation invalidates an awaited prefetch and retains the displayed global position', async () => {
  const f=playbackFixture(); await f.playback.load(f.manifest,{source});
  let start,finish;
  const entered=new Promise(resolve=>{start=resolve;});
  const fetchAsset=f.playback.fetchAsset;
  f.playback.fetchAsset=(url,options)=>url.endsWith('model-00001.rgs')?
    new Promise(resolve=>{start();finish=()=>resolve(new Response(Uint8Array.of(1,1,2,3)));}):fetchAsset(url,options);
  f.playback.prefetch(); await entered;
  const seek=f.playback.seek(5); f.playback.cancelReplacement(); finish(); await seek;
  assert.equal(f.rendered.length,1); assert.equal(f.playback.index,0);
  assert.equal(f.playback.position,0); assert.equal(f.playback.clock.value(),0);
  assert.ok(!f.events.some(e=>e.presentationId!==undefined || e.type==='error'));
  await f.playback.dispose();
});

test('new viewer reuses verified encoded bytes while keeping cached speed out of ABR', async () => {
  const storage = new MemoryStorage(), locks = new MemoryLocks();
  const first = playbackFixture(false, true, new SegmentCache({ storage, locks }));
  await first.playback.load(first.manifest, { source }); assert.ok(first.playback.abr.fast > 0);
  structuredClone(first.rendered[0].o.bytes, { transfer: [first.rendered[0].o.bytes] });
  await first.playback.dispose();
  const next = playbackFixture(false, true, new SegmentCache({ storage, locks }));
  await next.playback.load(next.manifest, { source });
  assert.equal(next.requests.filter(r => r.url.endsWith('.rgs')).length, 0);
  assert.equal(next.rendered.length, 1); assert.equal(next.playback.abr.fast, undefined); assert.equal(next.playback.abr.slow, undefined);
  await next.playback.dispose();
});
test('corrupt cache bytes are evicted and fetched again before rendering', async () => {
  const storage = new MemoryStorage(), locks = new MemoryLocks();
  const first = playbackFixture(false, true, new SegmentCache({ storage, locks }));
  await first.playback.load(first.manifest, { source }); await first.playback.dispose();
  storage.entries.values().next().value.bytes[0] ^= 255;
  const next = playbackFixture(false, true, new SegmentCache({ storage, locks }));
  await next.playback.load(next.manifest, { source });
  assert.equal(next.requests.filter(r => r.url.endsWith('.rgs')).length, 1);
  assert.equal(next.rendered.length, 1); assert.deepEqual([...new Uint8Array(next.rendered[0].o.bytes)], [0,1,2,3]);
  assert.ok(next.playback.abr.fast > 0); await next.playback.dispose();
});
test('unavailable cache never fails a successful verified model transfer', async () => {
  const storage = new MemoryStorage(); storage.failPut = true;
  const first = playbackFixture(false, true, new SegmentCache({ storage, locks: new MemoryLocks() }));
  await first.playback.load(first.manifest, { source });
  assert.equal(first.rendered.length, 1); assert.ok(first.playback.abr.fast > 0); assert.equal(storage.entries.size, 0);
  assert.ok(!first.events.some(e => e.type === 'error')); await first.playback.dispose();
});

test('an already cancelled acquisition never starts another network request', async () => {
  let calls = 0;
  const playback = new TransportPlayback({ fetchAsset: async () => { calls++; return new Response(new Uint8Array(4)); } });
  const stop = new AbortController(); stop.abort();
  await assert.rejects(playback.read('https://media.test/model.rgs', 4, stop.signal), { name: 'AbortError' });
  assert.equal(calls, 0);
});

test('a model whose public response expires during transfer is displayed but never cached', async t => {
  let now = 1000; t.mock.method(Date, 'now', () => now);
  const storage = new MemoryStorage(), locks = new MemoryLocks();
  const f = playbackFixture(false, true, new SegmentCache({ storage, locks }));
  const fetch = f.playback.fetchAsset;
  f.playback.fetchAsset = async (url, options) => {
    const response = await fetch(url, options);
    if (!url.endsWith('.rgs')) return response;
    return { status: 200, headers: new Headers({ 'Cache-Control': 'public,max-age=1', Age: '0' }), body: { getReader() {
      const reader = response.body.getReader();
      return { async read() { now += 5000; return reader.read(); }, cancel: () => reader.cancel(), releaseLock: () => reader.releaseLock() };
    } } };
  };
  await f.playback.load(f.manifest, { source });
  assert.equal(f.rendered.length, 1); assert.equal(storage.entries.size, 0); assert.ok(f.playback.abr.fast > 0);
  await f.playback.dispose();
});


function controlledAnimation() {
  let next = 0;
  const callbacks = new Map();
  return {
    requestFrame(callback) { callbacks.set(++next, callback); return next; },
    cancelFrame(id) { callbacks.delete(id); },
    get pending() { return callbacks.size; },
    async advance(count = 1) {
      for (let i = 0; i < count; i++) {
        assert.equal(callbacks.size, 1, 'playback owns one pending animation callback');
        const [id, callback] = callbacks.entries().next().value;
        callbacks.delete(id);
        callback();
        await new Promise(resolve => setImmediate(resolve));
      }
    },
  };
}
const playbackMessages = fixture => fixture.events.filter(message => message.type === 'playback');

test('stationary paused RAF callbacks emit one playback message without changing the protocol', async t => {
  const animation = controlledAnimation();
  const f = playbackFixture(false, false, undefined, animation);
  t.after(() => f.playback.dispose());
  await f.playback.load(f.manifest, { source });
  f.playback.clock.now = () => 0;
  f.playback.clock.seek(0);
  await animation.advance(60);
  assert.deepEqual(playbackMessages(f), [
    { type: 'playback', positionSeconds: 0, playing: false, playIntent: false },
  ]);
  assert.equal(f.events.filter(message => message.type === 'ready').length, 1);
  assert.equal(f.temporal.frame, 0);
});

test('dedup retains play/pause and every changed committed playback position', async t => {
  const animation = controlledAnimation();
  const f = playbackFixture(false, false, undefined, animation);
  t.after(() => f.playback.dispose());
  await f.playback.load(f.manifest, { source });
  let now = 0;
  f.playback.clock.now = () => now;
  f.playback.clock.seek(0);
  await animation.advance();
  f.playback.togglePlayback();
  await animation.advance();
  await animation.advance(60);
  now = 500;
  await animation.advance(2); // The same next-RAF commit behavior as before.
  f.playback.togglePlayback();
  await animation.advance(60);
  assert.deepEqual(playbackMessages(f), [
    { type: 'playback', positionSeconds: 0, playing: false, playIntent: false },
    { type: 'playback', positionSeconds: 0, playing: true, playIntent: true },
    { type: 'playback', positionSeconds: .5, playing: true, playIntent: true },
    { type: 'playback', positionSeconds: .5, playing: false, playIntent: false },
  ]);
  assert.equal(f.temporal.frame, 1);
});

test('same-state seeks and presentations still publish loading and every ready acknowledgement', async t => {
  const animation = controlledAnimation();
  const f = playbackFixture(false, false, undefined, animation);
  t.after(() => f.playback.dispose());
  await f.playback.load(f.manifest, { source, deferFirstFrame: true });
  f.playback.clock.now = () => 0;
  f.playback.clock.seek(0);
  assert.equal(f.events.filter(message => message.type === 'prepared').length, 1);
  await f.playback.present(1, true, 7);
  await f.playback.present(1, true, 8);
  await f.playback.seek(1);
  await f.playback.present(2, false, 9);
  assert.equal(f.events.filter(message => message.type === 'loading').length, 4);
  assert.deepEqual(f.events.filter(message => message.type === 'ready').map(message =>
    [message.frame, message.presentationId, message.playing]),
  [[1, 7, true], [1, 8, true], [1, undefined, true], [2, 9, false]]);
  assert.deepEqual(playbackMessages(f), [
    { type: 'playback', positionSeconds: .5, playing: true, playIntent: true },
    { type: 'playback', positionSeconds: 1, playing: false, playIntent: false },
  ]);
});

test('visibility suspension keeps play intent and excludes hidden time after resuming', async t => {
  const animation = controlledAnimation();
  const f = playbackFixture(false, false, undefined, animation);
  t.after(() => f.playback.dispose());
  await f.playback.load(f.manifest, { source });
  let now = 0;
  f.playback.clock.now = () => now;
  f.playback.clock.seek(0);
  await animation.advance();
  f.playback.togglePlayback();
  await animation.advance();
  now = 500;
  await animation.advance(2);
  const beforeHidden = playbackMessages(f).length;
  f.playback.pause(true);
  now = 5500;
  await animation.advance(60);
  assert.equal(f.playback.clock.value(), .5);
  assert.equal(playbackMessages(f).length, beforeHidden);
  f.playback.pause(false);
  await animation.advance();
  assert.equal(f.playback.clock.playing, true);
  assert.equal(playbackMessages(f).length, beforeHidden);
  now = 6000;
  await animation.advance(2);
  assert.deepEqual(playbackMessages(f).at(-1),
    { type: 'playback', positionSeconds: 1, playing: true, playIntent: true });
  assert.equal(playbackMessages(f).length, beforeHidden + 1);
  assert.equal(f.temporal.frame, 2);
  await f.playback.dispose();
  assert.equal(animation.pending, 0);
});
