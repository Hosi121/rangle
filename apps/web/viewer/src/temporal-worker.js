import { MAX_TEMPORAL_BYTES, prepareTemporal } from './temporal-format.js';
import { TemporalSorter } from './temporal-sort.js';

let sorter;
self.addEventListener('message', async ({ data }) => {
  try {
    if (data.type === 'load') {
      const response = await fetch(data.url);
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
      const bytes = new Uint8Array(size);
      let offset = 0;
      for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
      chunks.length = 0;
      const { motion, ...model } = prepareTemporal(bytes.buffer);
      sorter = new TemporalSorter(motion);
      self.postMessage({ type: 'model', ...model }, [model.buffer, model.texture.buffer]);
    } else if (data.type === 'sort' && sorter) {
      const started = performance.now();
      const recycled = data.recycle ? new Uint32Array(data.recycle) : undefined;
      const result = sorter.sort(data.time, data.view, recycled);
      self.postMessage({ type: 'sorted', id: data.id, time: data.time,
        elapsedMs: performance.now() - started, ...result }, [result.indexes.buffer]);
    }
  } catch {
    self.postMessage({ type: 'error' });
  }
});
