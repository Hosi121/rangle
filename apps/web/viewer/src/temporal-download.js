import { MAX_TEMPORAL_BYTES } from './temporal-codec.js';

/** Content-Length is an allocation hint: fetch can decode HTTP content coding. */
export async function readTemporalBody(response) {
  const declared = Number(response.headers.get('Content-Length'));
  if (!response.ok || declared > MAX_TEMPORAL_BYTES || !response.body) {
    await response.body?.cancel().catch(() => {});
    throw new Error('Model request failed');
  }
  let buffer = Number.isSafeInteger(declared) && declared > 0 ? new Uint8Array(declared) : null;
  const chunks = [], reader = response.body.getReader();
  let size = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      if (!value.byteLength) continue;
      if (size + value.byteLength > MAX_TEMPORAL_BYTES) throw new Error('Model exceeds limit');
      if (buffer && size + value.byteLength <= buffer.length) {
        buffer.set(value, size);
      } else {
        // An encoded HTTP body can expand beyond its Content-Length. Preserve
        // bytes already received and retain the same bounded fallback as before.
        if (buffer) {
          if (size) chunks.push(buffer.subarray(0, size));
          buffer = null;
        }
        chunks.push(value);
      }
      size += value.byteLength;
    }
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally { reader.releaseLock(); }
  if (buffer) return size === buffer.length ? buffer : buffer.slice(0, size);
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  return bytes;
}
