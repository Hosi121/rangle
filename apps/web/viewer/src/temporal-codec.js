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
  if (!count || count > 500000 || header.getUint32(12, true) !== STRIDE || header.getUint32(28, true) !== 0 ||
      rawSize > MAX_TEMPORAL_BYTES || (!compressed && data.byteLength !== rawSize) ||
      (compressed && data.byteLength <= HEADER_BYTES) || frameCount < 2 || frameCount > 15000 ||
      !Number.isFinite(frameRate) || frameRate <= 0 || frameRate > 240 ||
      !Number.isFinite(duration) || duration <= 0 || Math.abs(duration - (frameCount - 1) / frameRate) > 1e-5) {
    throw new Error('Invalid temporal model header');
  }
  return { count, frameCount, frameRate, duration, compressed, rawSize };
}

/** Bind the float32 wire clock to the scene before installing any GPU resource. */
export function checkTemporalTimeline(model, manifest) {
  if (model.frameCount !== manifest.frameCount || model.frameRate !== Math.fround(manifest.frameRate)) {
    throw new Error('Temporal model timeline mismatch');
  }
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

/** Every native pass has the same output bound and cancellation/lock cleanup. */
async function inflateBounded(blob, format, expected, consume, signal) {
  signal?.throwIfAborted();
  const reader = blob.stream().pipeThrough(new DecompressionStream(format)).getReader();
  const abort = () => { void reader.cancel(signal.reason).catch(() => {}); };
  signal?.addEventListener('abort', abort, { once: true });
  let written = 0;
  try {
    while (true) {
      signal?.throwIfAborted();
      const { value, done } = await reader.read();
      signal?.throwIfAborted();
      if (done) break;
      if (written + value.byteLength > expected) throw new Error('Decompressed temporal model exceeds declared size');
      consume(value, written);
      written += value.byteLength;
    }
    if (written !== expected) throw new Error('Truncated temporal model');
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally {
    signal?.removeEventListener('abort', abort);
    reader.releaseLock();
  }
}

/** Locate only the gzip envelope; DEFLATE itself stays in native decoders. */
function gzipBody(bytes, expected) {
  const end = bytes.length - 8;
  if (end < 10 || bytes[0] !== 31 || bytes[1] !== 139 || bytes[2] !== 8 || (bytes[3] & 224)) {
    throw new Error('Invalid temporal gzip header');
  }
  // A single member's ISIZE is exact below 32 MiB. Reject appended empty
  // member chains before native gzip spends time walking their headers.
  const size = new DataView(bytes.buffer, bytes.byteOffset + end + 4, 4).getUint32(0, true);
  if (size > expected) throw new Error('Decompressed temporal model exceeds declared size');
  if (size < expected) throw new Error('Truncated temporal model');
  const flags = bytes[3];
  let start = 10;
  if (flags & 4) {
    if (start + 2 > end) throw new Error('Truncated temporal gzip extra field');
    start += 2 + bytes[start] + 256 * bytes[start + 1];
  }
  for (const flag of [8, 16]) {
    if (flags & flag) {
      while (start < end && bytes[start] !== 0) start++;
      start++;
    }
  }
  if (flags & 2) start += 2;
  if (start >= end) throw new Error('Truncated temporal gzip envelope');
  return { start, end };
}

/** Decode into AoS once; a second bounded native pass enforces one gzip member. */
export async function decodeTemporal(data, { signal } = {}) {
  signal?.throwIfAborted();
  const { count, compressed, rawSize } = temporalHeader(data);
  if (!compressed) return data;
  const encoded = new Uint8Array(data, HEADER_BYTES), body = gzipBody(encoded, rawSize - HEADER_BYTES);
  const blob = new Blob([encoded]);
  const output = new Uint8Array(rawSize), adler = { a: 1, b: 0 };
  output.set(new Uint8Array(data, 0, HEADER_BYTES));
  output.set(new TextEncoder().encode(RAW_MAGIC));
  await inflateBounded(blob, 'gzip', rawSize - HEADER_BYTES, (value, written) => {
    let offset = 0, a = adler.a, b = adler.b;
    while (offset < value.length) {
      const column = Math.floor(written / count), row = written % count;
      // RFC 1950: reduce at most every 5552 bytes to keep sums exact.
      // Compute in the same traversal as AoS restoration, using local sums.
      const length = Math.min(value.length - offset, count - row, 5552);
      for (let i = 0; i < length; i++) {
        const byte = value[offset + i];
        output[HEADER_BYTES + (row + i) * STRIDE + column] = byte;
        a += byte; b += a;
      }
      a %= 65521; b %= 65521;
      offset += length; written += length;
    }
    adler.a = a; adler.b = b;
  }, signal);
  // Native gzip verifies CRC/ISIZE/FHCRC but some runtimes accept extra members.
  // Rewrap the presumed single member's raw body in zlib: the native 'deflate'
  // decoder must verify Adler-32 AND reject any input after its first stream.
  // 'deflate-raw' cannot substitute: some runtimes silently ignore its tail.
  const checksum = new Uint8Array(4);
  new DataView(checksum.buffer).setUint32(0, ((adler.b << 16) | adler.a) >>> 0);
  // Blob slices share immutable compressed storage; do not copy the full body
  // into a second Uint8Array/Blob or retain a second decoded model.
  const zlib = new Blob([new Uint8Array([0x78, 0x01]), blob.slice(body.start, body.end), checksum]);
  await inflateBounded(zlib, 'deflate', rawSize - HEADER_BYTES, () => {}, signal);
  return output.buffer;
}
