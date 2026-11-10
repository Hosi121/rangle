import { prepareTemporal } from './temporal-format.js';
import { decodeTemporal } from './temporal-codec.js';
import { readTemporalBody } from './temporal-download.js';
import { TemporalSorter } from './temporal-sort.js';

let sorter, pendingLoad, loading;
let hidden = false;

async function loadPending() {
  if (!pendingLoad || loading || hidden) return;
  const data = pendingLoad, abort = loading = new AbortController();
  try {
    let bytes;
    if (data.bytes instanceof ArrayBuffer) bytes = new Uint8Array(data.bytes);
    else {
      const response = await fetch(data.url, { signal: abort.signal,
        redirect: data.strict ? 'error' : 'follow', credentials: data.strict ? 'omit' : 'same-origin' });
      bytes = await readTemporalBody(response);
    }
    if (abort.signal.aborted || hidden) return;
    if (data.sha256 !== undefined) {
      if (!/^[a-f0-9]{64}$/.test(data.sha256)) throw new Error('Invalid model digest');
      const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes.buffer)),
        byte => byte.toString(16).padStart(2, '0')).join('');
      if (digest !== data.sha256) throw new Error('Model digest mismatch');
    }
    if (abort.signal.aborted || hidden) return;
    const decoded = await decodeTemporal(bytes.buffer, { signal: abort.signal });
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
