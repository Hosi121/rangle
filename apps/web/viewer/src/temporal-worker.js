import { MAX_TEMPORAL_BYTES, prepareTemporal } from './temporal-format.js';
import { decodeTemporal } from './temporal-codec.js';
import { TemporalSorter } from './temporal-sort.js';

let sorter, pendingLoad, loading;
let hidden = false;

async function loadPending() {
  if (!pendingLoad || loading || hidden) return;
  const data = pendingLoad, abort = loading = new AbortController();
  try {
    const response = await fetch(data.url, { signal: abort.signal,
      redirect: data.strict ? 'error' : 'follow', credentials: data.strict ? 'omit' : 'same-origin' });
    if (!response.ok || Number(response.headers.get('Content-Length')) > MAX_TEMPORAL_BYTES) throw new Error('Model request failed');
    const reader = response.body.getReader(), chunks = [];
    let size = 0;
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        size += value.byteLength;
        if (size > MAX_TEMPORAL_BYTES) throw new Error('Model exceeds limit');
        chunks.push(value);
      }
    } catch (error) {
      await reader.cancel().catch(() => {});
      throw error;
    } finally { reader.releaseLock(); }
    if (abort.signal.aborted || hidden) return;
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    chunks.length = 0;
    if (data.sha256 !== undefined) {
      if (!/^[a-f0-9]{64}$/.test(data.sha256)) throw new Error('Invalid model digest');
      const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes.buffer)),
        byte => byte.toString(16).padStart(2, '0')).join('');
      if (digest !== data.sha256) throw new Error('Model digest mismatch');
    }
    if (abort.signal.aborted || hidden) return;
    const decoded = await decodeTemporal(bytes.buffer);
    if (abort.signal.aborted || hidden) return;
    const { motion, ...model } = prepareTemporal(decoded);
    sorter = new TemporalSorter(motion);
    pendingLoad = null;
    self.postMessage({ type: 'model', ...model }, [model.buffer, model.texture.buffer]);
  } catch {
    if (!abort.signal.aborted) {
      pendingLoad = null;
      self.postMessage({ type: 'error' });
    }
  } finally {
    if (loading === abort) loading = null;
    if (!hidden && pendingLoad) void loadPending();
  }
}

self.addEventListener('message', ({ data }) => {
  try {
    if (data.type === 'load') {
      pendingLoad = data;
      hidden = data.strict && data.hidden === true;
      void loadPending();
    } else if (data.type === 'visibility') {
      hidden = data.hidden === true;
      if (hidden) loading?.abort();
      else void loadPending();
    } else if (data.type === 'sort' && sorter) {
      const started = performance.now();
      const recycled = data.recycle ? new Uint32Array(data.recycle) : undefined;
      const result = sorter.sort(data.time, data.view, recycled);
      self.postMessage({ type: 'sorted', id: data.id, time: data.time,
        elapsedMs: performance.now() - started, ...result }, [result.indexes.buffer]);
    }
  } catch { self.postMessage({ type: 'error' }); }
});
