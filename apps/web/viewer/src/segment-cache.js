export const SEGMENT_CACHE_NAME = 'rangle-vod-segments-v2';
const MAX_ENTRY_BYTES = 16 * 1024 * 1024, MAX_AGE_SECONDS = 600;

function keyFor(asset) {
  if (!Number.isSafeInteger(asset?.size_bytes) || asset.size_bytes <= 0 || asset.size_bytes > MAX_ENTRY_BYTES ||
      !/^[a-f0-9]{64}$/.test(asset.sha256)) return null;
  try {
    const url = new URL(asset.url);
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash ||
        !/^\/assets\/[A-Za-z0-9_-]{1,64}\/[A-Za-z0-9_-]{1,64}\/[A-Za-z0-9_-]{1,64}\/[A-Za-z0-9_.-]+\.rgs$/.test(url.pathname)) return null;
    return url.href;
  } catch { return null; }
}

function aborted(signal) { signal?.throwIfAborted(); }
function waitFor(promise, signal) {
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, { once: true });
    Promise.resolve(promise).then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
  });
}

/** Only explicitly public, fresh responses can enter this optional cache. */
export function segmentCacheAge(response, responseDelaySeconds = 0, receivedAt = Date.now()) {
  const directives = (response.headers.get('Cache-Control') ?? '').toLowerCase().split(',').map(x => x.trim());
  if (!directives.includes('public') || directives.some(x => /^(private|no-store|no-cache)(\s|=|$)/.test(x))) return 0;
  const ages = directives.filter(x => /^max-age\s*=/.test(x));
  if (ages.length !== 1 || !/^max-age\s*=\s*\d+$/.test(ages[0])) return 0;
  const age = Number(ages[0].split('=')[1]);
  const ageHeader = response.headers.get('Age'), residentAge = Number(ageHeader ?? 0);
  const date = Date.parse(response.headers.get('Date') ?? '');
  if (ageHeader === null && !Number.isFinite(date)) return 0;
  if (!Number.isSafeInteger(age) || !Number.isSafeInteger(residentAge) || residentAge < 0 ||
      !Number.isFinite(responseDelaySeconds) || responseDelaySeconds < 0 || !Number.isFinite(receivedAt)) return 0;
  const apparentAge = Number.isFinite(date) ? Math.max(0, (receivedAt - date) / 1000) : 0;
  const currentAge = Math.max(apparentAge, residentAge + responseDelaySeconds);
  return Math.max(0, Math.min(MAX_AGE_SECONDS, age - currentAge));
}

/** Two fixed slots of at most 16 MiB each, for at most ten minutes.
 * Callers verify SHA before put and again after get. No decoded/GPU data is retained.
 * Fixed slots preserve the payload bound even if an iframe disappears during
 * a non-cancellable write and its Web Lock is released before the write settles.
 * Larger (up to 32 MiB) transport models still use the ordinary network path.
 */
export class SegmentCache {
  constructor(options = {}) {
    try {
      this.storage = options.storage ?? globalThis.caches;
      this.locks = options.locks ?? globalThis.navigator?.locks;
    } catch { /* Blocked storage is an ordinary network-only environment. */ }
    this.timeoutMs = options.timeoutMs ?? 1000;
    this.now = options.now ?? (() => Date.now());
    const origin = options.origin ?? globalThis.location?.origin ?? 'https://cache.invalid';
    this.slots = [0,1].map(index => new URL(`/__rangle_cache__/vod-segment-${index}`, origin).href);
  }
  async use(signal, fallback, action) {
    aborted(signal);
    if (!this.storage?.open || !this.locks?.request) return fallback;
    const controller = new AbortController(), abort = () => controller.abort(signal.reason);
    signal?.addEventListener('abort', abort, { once: true });
    const timeout = setTimeout(() => controller.abort(new DOMException('Cache timed out', 'TimeoutError')), this.timeoutMs);
    try {
      // Keep the lock until non-cancellable storage writes settle, even when
      // the viewer stops waiting. A timed-out writer cannot race a new iframe.
      const work = this.locks.request(SEGMENT_CACHE_NAME, { mode: 'exclusive', signal: controller.signal }, async () => {
        aborted(controller.signal);
        const cache = await this.storage.open(SEGMENT_CACHE_NAME);
        aborted(controller.signal);
        return action(cache, controller.signal);
      });
      return await waitFor(work, controller.signal);
    } catch (error) {
      aborted(signal);
      return fallback;
    } finally {
      clearTimeout(timeout); signal?.removeEventListener('abort', abort);
    }
  }
  metadata(response) {
    if (!response || response.status !== 200 || !response.body) return null;
    const value = { url: response.headers.get('X-Rangle-Source'),
      sha256: response.headers.get('X-Rangle-SHA256'), size_bytes: Number(response.headers.get('Content-Length')),
      expires: Number(response.headers.get('X-Rangle-Expires')), stored: Number(response.headers.get('X-Rangle-Stored')) };
    if (!keyFor(value) || !Number.isSafeInteger(value.expires) || !Number.isSafeInteger(value.stored) ||
        value.expires <= this.now() || value.stored > this.now() || value.expires <= value.stored ||
        value.expires > value.stored + MAX_AGE_SECONDS * 1000) return null;
    return value;
  }
  get(asset, signal) {
    const key = keyFor(asset);
    if (!key) return Promise.resolve(null);
    return this.use(signal, null, async (cache, active) => {
      for (const slot of this.slots) {
        const response = await cache.match(slot);
        if (!response) continue;
        let reader;
        try {
          aborted(active);
          const metadata = this.metadata(response);
          if (!metadata) { await cache.delete(slot); continue; }
          if (metadata.url !== key) continue;
          if (metadata.sha256 !== asset.sha256 || metadata.size_bytes !== asset.size_bytes) { await cache.delete(slot); continue; }
          const bytes = new Uint8Array(asset.size_bytes); let offset = 0;
          reader = response.body.getReader();
          while (true) {
            const part = await waitFor(reader.read(), active);
            aborted(active);
            if (part.done) break;
            if (part.value.byteLength > bytes.length - offset) throw Error('Cached segment exceeds bounds');
            bytes.set(part.value, offset); offset += part.value.byteLength;
          }
          if (offset !== bytes.length) throw Error('Truncated cached segment');
          return bytes;
        } catch (error) {
          if (!active.aborted) await cache.delete(slot);
          throw error;
        } finally {
          if (reader) { await reader.cancel().catch(() => {}); reader.releaseLock(); }
          else await response.body?.cancel().catch(() => {});
        }
      }
      return null;
    });
  }
  put(asset, bytes, signal, expiresAt = this.now() + MAX_AGE_SECONDS * 1000) {
    const key = keyFor(asset);
    if (!key || !(bytes instanceof Uint8Array) || bytes.byteLength !== asset.size_bytes ||
        !Number.isSafeInteger(expiresAt) || expiresAt <= this.now() || expiresAt > this.now() + MAX_AGE_SECONDS * 1000) return Promise.resolve();
    return this.use(signal, undefined, async (cache, active) => {
      const entries = [];
      for (const slot of this.slots) {
        const response = await cache.match(slot);
        aborted(active);
        entries.push({ slot, metadata: this.metadata(response) });
        await response?.body?.cancel().catch(() => {});
      }
      const target = entries.find(entry => entry.metadata?.url === key) ?? entries.find(entry => !entry.metadata) ??
        entries.reduce((oldest, entry) => entry.metadata.stored < oldest.metadata.stored ? entry : oldest);
      aborted(active);
      if (expiresAt <= this.now()) return;
      await cache.put(target.slot, new Response(bytes, { headers: {
        'Content-Type': 'application/octet-stream', 'Content-Length': String(bytes.byteLength),
        'X-Rangle-Source': key, 'X-Rangle-SHA256': asset.sha256,
        'X-Rangle-Stored': String(this.now()), 'X-Rangle-Expires': String(expiresAt),
      } }));
      if (active.aborted || expiresAt <= this.now()) { await cache.delete(target.slot); aborted(active); }
    });
  }
  remove(asset, signal) {
    const key = keyFor(asset);
    if (!key) return Promise.resolve();
    return this.use(signal, undefined, async (cache, active) => {
      for (const slot of this.slots) {
        const response = await cache.match(slot); aborted(active);
        const source = response?.headers.get('X-Rangle-Source');
        await response?.body?.cancel().catch(() => {});
        if (source === key) await cache.delete(slot);
      }
    });
  }
}

export const publicSegmentCache = new SegmentCache();
