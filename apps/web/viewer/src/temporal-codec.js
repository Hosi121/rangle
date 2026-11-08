export const MAX_TEMPORAL_BYTES = 32 * 1024 * 1024;
const RAW_MAGIC = 'RANGL4D1', PACKED_MAGIC = 'RANGL4Z1';
const HEADER_BYTES = 32, STRIDE = 56;

export function temporalHeader(data) {
  if (!(data instanceof ArrayBuffer) || data.byteLength < HEADER_BYTES || data.byteLength > MAX_TEMPORAL_BYTES) {
    throw new Error('Invalid temporal model size');
  }
  const header = new DataView(data);
  const magic = new TextDecoder().decode(new Uint8Array(data, 0, 8));
  if (magic !== RAW_MAGIC && magic !== PACKED_MAGIC) throw new Error('Invalid temporal model magic');
  const compressed = magic === PACKED_MAGIC;
  const count = header.getUint32(8, true), frameCount = header.getUint32(16, true);
  const frameRate = header.getFloat32(20, true), duration = header.getFloat32(24, true);
  const rawSize = HEADER_BYTES + count * STRIDE;
  if (!count || header.getUint32(12, true) !== STRIDE || header.getUint32(28, true) !== 0 ||
      rawSize > MAX_TEMPORAL_BYTES || (!compressed && data.byteLength !== rawSize) ||
      (compressed && data.byteLength <= HEADER_BYTES) || frameCount < 2 || frameCount > 15000 ||
      !Number.isFinite(frameRate) || frameRate <= 0 || frameRate > 240 ||
      !Number.isFinite(duration) || duration <= 0 || Math.abs(duration - (frameCount - 1) / frameRate) > 1e-5) {
    throw new Error('Invalid temporal model header');
  }
  return { count, frameCount, frameRate, duration, compressed, rawSize };
}

/** Rearrange bytes, never values or canonical primitive IDs. gzip is supplied by the offline packer. */
export function packTemporal(data, gzip) {
  const { count, compressed } = temporalHeader(data);
  if (compressed) throw new Error('Temporal model is already compressed');
  const source = new Uint8Array(data, HEADER_BYTES), columns = new Uint8Array(source.length);
  for (let column = 0; column < STRIDE; column++) {
    for (let row = 0; row < count; row++) columns[column * count + row] = source[row * STRIDE + column];
  }
  const payload = gzip(columns);
  if (!(payload instanceof Uint8Array) || HEADER_BYTES + payload.length > MAX_TEMPORAL_BYTES) {
    throw new Error('Compressed temporal model exceeds limit');
  }
  const output = new Uint8Array(HEADER_BYTES + payload.length);
  output.set(new Uint8Array(data, 0, HEADER_BYTES));
  output.set(new TextEncoder().encode(PACKED_MAGIC));
  output.set(payload, HEADER_BYTES);
  return output.buffer;
}

/** Stream gzip output straight into the original AoS layout, bounded by the validated header. */
export async function decodeTemporal(data) {
  const { count, compressed, rawSize } = temporalHeader(data);
  if (!compressed) return data;
  const output = new Uint8Array(rawSize);
  output.set(new Uint8Array(data, 0, HEADER_BYTES));
  output.set(new TextEncoder().encode(RAW_MAGIC));
  const reader = new Blob([new Uint8Array(data, HEADER_BYTES)]).stream()
    .pipeThrough(new DecompressionStream('gzip')).getReader();
  let written = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      if (written + value.byteLength > rawSize - HEADER_BYTES) throw new Error('Decompressed temporal model exceeds declared size');
      let offset = 0;
      while (offset < value.length) {
        const column = Math.floor(written / count), row = written % count;
        const length = Math.min(value.length - offset, count - row);
        for (let i = 0; i < length; i++) output[HEADER_BYTES + (row + i) * STRIDE + column] = value[offset + i];
        offset += length;
        written += length;
      }
    }
    if (written !== rawSize - HEADER_BYTES) throw new Error('Truncated temporal model');
    return output.buffer;
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally { reader.releaseLock(); }
}
