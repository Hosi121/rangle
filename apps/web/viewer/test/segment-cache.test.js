import test from 'node:test';
import assert from 'node:assert/strict';
import { SegmentCache, SEGMENT_CACHE_NAME, segmentCacheAge } from '../src/segment-cache.js';
import { MemoryStorage, MemoryLocks } from './segment-cache-fixture.js';

const asset = (id, size = 4) => ({ url: `https://media.test/assets/${id}/r1/full/model.rgs`, size_bytes: size, sha256: 'a'.repeat(64) });
function fixture(options = {}) {
  const storage = new MemoryStorage(), locks = new MemoryLocks();
  return { storage, locks, cache: new SegmentCache({ storage, locks, ...options }) };
}
const sources = storage => [...storage.entries.values()].map(entry => entry.headers.get('X-Rangle-Source')).sort();
const entryFor = (storage, asset) => [...storage.entries.values()].find(entry => entry.headers.get('X-Rangle-Source') === asset.url);
test('cache survives viewer instances, retains at most two encoded entries, and evicts oldest first', async () => {
  const f = fixture(), a = asset('a'), b = asset('b'), c = asset('c'), bytes = Uint8Array.of(1,2,3,4);
  await f.cache.put(a, bytes); await f.cache.put(b, bytes);
  const nextViewer = new SegmentCache({ storage: f.storage, locks: f.locks });
  assert.deepEqual(await nextViewer.get(a), bytes);
  await nextViewer.put(c, bytes);
  assert.equal(f.storage.entries.size, 2); assert.equal(await nextViewer.get(a), null);
  assert.deepEqual(await nextViewer.get(b), bytes); assert.deepEqual(await nextViewer.get(c), bytes);
  // Worker transfer detaches the source; the persistent copy remains readable.
  structuredClone(bytes.buffer, { transfer: [bytes.buffer] });
  assert.deepEqual(await nextViewer.get(c), Uint8Array.of(1,2,3,4));
});
test('fixed slots bound payload to 32 MiB and skip larger cache entries without changing transport limits', async () => {
  const f = fixture(), a = asset('a', 16 << 20), b = asset('b', 16 << 20);
  await f.cache.put(a, new Uint8Array(a.size_bytes));
  await f.cache.put(b, new Uint8Array(b.size_bytes));
  assert.equal(f.storage.entries.size, 2);
  assert.equal([...f.storage.entries.values()].reduce((sum, entry) => sum + entry.bytes.length, 0), 32 << 20);
  const opens = f.storage.opens;
  await f.cache.put(asset('large', (16 << 20) + 1), new Uint8Array(1));
  assert.equal(await f.cache.get(asset('large', (16 << 20) + 1)), null);
  assert.equal(f.storage.opens, opens);
});
test('concurrent viewer writes share the same bounded cache', async () => {
  const f = fixture(), other = new SegmentCache({ storage: f.storage, locks: f.locks });
  await Promise.all([f.cache.put(asset('a'), new Uint8Array(4)), other.put(asset('b'), new Uint8Array(4)), f.cache.put(asset('c'), new Uint8Array(4))]);
  assert.deepEqual(sources(f.storage), [asset('b').url, asset('c').url]);
});
test('expired and mismatched cached metadata is discarded', async () => {
  let now = 1000; const f = fixture({ now: () => now }), a = asset('a');
  for (const mutate of [entry => entry.headers.set('X-Rangle-SHA256', 'b'.repeat(64)),
    entry => entry.headers.set('Content-Length', '999'), entry => entry.headers.set('X-Rangle-Expires', 'bad')]) {
    await f.cache.put(a, new Uint8Array(4)); mutate(entryFor(f.storage, a));
    assert.equal(await f.cache.get(a), null); assert.equal(f.storage.entries.size, 0);
  }
  await f.cache.put(a, new Uint8Array(4), undefined, now + 1000); now += 1001;
  assert.equal(await f.cache.get(a), null); assert.equal(f.storage.entries.size, 0);
});
test('short and overflowing cached bodies are rejected before consumption', async () => {
  const f = fixture(), a = asset('a');
  for (const size of [3,5]) {
    await f.cache.put(a, new Uint8Array(4)); entryFor(f.storage, a).bytes = new Uint8Array(size);
    assert.equal(await f.cache.get(a), null); assert.equal(f.storage.entries.size, 0);
  }
});
test('cache never stores LIVE, private preview, ambiguous URLs, or changed sizes', async () => {
  const f = fixture();
  for (const url of ['https://media.test/live/session/model.rgs', 'https://app.test/api/private/model.rgs',
    'https://media.test/generated/session/model.rgs', asset('a').url+'?token=x', asset('a').url+'#hash']) {
    await f.cache.put({ ...asset('a'), url }, new Uint8Array(4));
  }
  await f.cache.put(asset('a'), new Uint8Array(3));
  assert.equal(f.storage.opens, 0);
});
test('storage denial, quota, absent locks and lookup timeouts preserve network fallback', async () => {
  const f = fixture({ timeoutMs: 10 }), a = asset('a'); f.storage.failOpen = true;
  assert.equal(await f.cache.get(a), null); await f.cache.put(a, new Uint8Array(4));
  f.storage.failOpen = false; f.storage.failPut = true; await f.cache.put(a, new Uint8Array(4));
  assert.equal(f.storage.entries.size, 0);
  const unsupported = new SegmentCache({ storage: f.storage, locks: {} });
  const opens = f.storage.opens; assert.equal(await unsupported.get(a), null); assert.equal(f.storage.opens, opens);
  const hung = new SegmentCache({ storage: { open: () => new Promise(() => {}) }, locks: new MemoryLocks(), timeoutMs: 5 });
  assert.equal(await hung.get(a), null);
});
test('parent abort interrupts lookup and pending late writes roll back under their lock', async () => {
  const f = fixture({ timeoutMs: 5 }), a = asset('a'), b = asset('b');
  const stopped = new AbortController(); stopped.abort();
  await assert.rejects(f.cache.get(a, stopped.signal), { name: 'AbortError' });
  let started, finish;
  const entered = new Promise(resolve => { started = resolve; }), gate = new Promise(resolve => { finish = resolve; });
  const put = f.storage.put.bind(f.storage);
  f.storage.put = async (key, response) => { if (response.headers.get('X-Rangle-Source') === a.url) { started(); await gate; } await put(key, response); };
  const first = f.cache.put(a, new Uint8Array(4)); await entered; await first;
  const other = new SegmentCache({ storage: f.storage, locks: f.locks, timeoutMs: 1000 });
  const second = other.put(b, new Uint8Array(4));
  await Promise.resolve(); assert.equal(f.storage.opens, 1);
  finish(); await second;
  assert.deepEqual(sources(f.storage), [b.url]);
});
test('public response freshness controls retention with a ten minute ceiling', () => {
  const response = (value, age = '0') => new Response('', { headers: { 'Cache-Control': value, Age: age } });
  assert.equal(segmentCacheAge(response('public,max-age=86400')), 600);
  assert.equal(segmentCacheAge(response('public,max-age=30', '10')), 20);
  for (const value of ['', 'private,max-age=600', 'public,no-store,max-age=600', 'public,no-cache,max-age=600',
    'public,max-age=0', 'public,max-age=30,max-age=60', 'public,max-age=invalid']) assert.equal(segmentCacheAge(response(value)), 0);
  assert.equal(segmentCacheAge(response('public,max-age=30', '40')), 0);
  assert.equal(segmentCacheAge(response('public,max-age=30', '10'), 2), 18);
  assert.equal(segmentCacheAge(new Response('', { headers: { 'Cache-Control': 'public,max-age=600' } })), 0);
  const dated = new Response('', { headers: { 'Cache-Control': 'public,max-age=30', Date: new Date(1000).toUTCString() } });
  assert.equal(segmentCacheAge(dated, 1, 11000), 20);
  assert.equal(SEGMENT_CACHE_NAME, 'rangle-vod-segments-v2');
});

test('removing a corrupt source preserves a different source in the other slot', async () => {
  const f = fixture(), a = asset('a'), b = asset('b');
  await f.cache.put(a, Uint8Array.of(1,2,3,4)); await f.cache.put(b, Uint8Array.of(5,6,7,8));
  await f.cache.remove(a);
  assert.deepEqual(await f.cache.get(b), Uint8Array.of(5,6,7,8)); assert.equal(await f.cache.get(a), null);
});

test('header-time expiry is not extended by cache lock waits or storage writes', async () => {
  let now = 1000; const f = fixture({ now: () => now }), a = asset('a');
  const expires = now + 1000;
  now += 1500; await f.cache.put(a, new Uint8Array(4), undefined, expires);
  assert.equal(f.storage.entries.size, 0);
  const put = f.storage.put.bind(f.storage);
  f.storage.put = async (key, response) => { await put(key, response); now += 1500; };
  await f.cache.put(a, new Uint8Array(4), undefined, now + 1000);
  assert.equal(f.storage.entries.size, 0);
});
