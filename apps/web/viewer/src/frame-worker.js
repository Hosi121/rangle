import { MAX_FRAME_BYTES, prepareFrame } from './frame-format.js';

// This worker owns the download and conversion of one requested frame at a time.
// Terminating it on supersession also discards in-flight conversion and buffers.
self.addEventListener('message', async ({ data }) => {
  const { id, url } = data;
  try {
    const response = await fetch(url);
    if (!response.ok || Number(response.headers.get('Content-Length')) > MAX_FRAME_BYTES) {
      throw new Error('Frame request failed');
    }
    const reader = response.body.getReader();
    const chunks = [];
    let length = 0;
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        length += value.byteLength;
        if (length > MAX_FRAME_BYTES) throw new Error('Frame exceeds size limit');
        chunks.push(value);
      }
    } catch (error) {
      await reader.cancel().catch(() => {});
      throw error;
    } finally {
      reader.releaseLock();
    }
    const raw = new Uint8Array(length);
    let offset = 0;
    for (const chunk of chunks) {
      raw.set(chunk, offset);
      offset += chunk.byteLength;
    }
    chunks.length = 0;
    const buffer = prepareFrame(raw.buffer);
    self.postMessage({ id, buffer }, [buffer]);
  } catch {
    self.postMessage({ id, error: true });
  }
});
