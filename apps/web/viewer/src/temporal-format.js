import { SplatBuffer, SplatParser } from '@mkkellogg/gaussian-splats-3d';
import { temporalHeader } from './temporal-codec.js';

export { MAX_TEMPORAL_BYTES } from './temporal-codec.js';
export const MOTION_TEXTURE_WIDTH = 2048;

/** RGS1 retains canonical IDs: the SDK's spatial partitioner must not reorder them. */
export function prepareTemporal(data) {
  const { count, frameCount, frameRate, duration, compressed } = temporalHeader(data);
  if (compressed) throw new Error('Temporal model must be decompressed before preparation');
  const raw = new ArrayBuffer(count * 32);
  const rawBytes = new Uint8Array(raw);
  const words = new Uint32Array(data), staticWords = new Uint32Array(raw);
  const floats = new Float32Array(data);
  const floatFields = [0, 1, 2, 3, 4, 5, 8, 9, 10, 11, 12, 13];
  const motion = new Float32Array(count * 9);
  const textureHeight = Math.ceil(count * 2 / MOTION_TEXTURE_WIDTH);
  const texture = new Float32Array(MOTION_TEXTURE_WIDTH * textureHeight * 4);
  for (let i = 0; i < count; i++) {
    const source = 8 + i * 14, target = i * 8, model = i * 9;
    for (const k of floatFields) {
      if (!Number.isFinite(floats[source + k])) throw new Error('Non-finite temporal primitive');
    }
    if ([3, 4, 5, 12].some(k => floats[source + k] <= 0) ||
        !Number.isFinite(Math.fround(1 / floats[source + 12])) ||
        floats[source + 13] < 0 || floats[source + 13] > 1) throw new Error('Invalid temporal primitive');
    for (let k = 0; k < 8; k++) staticWords[target + k] = words[source + k];
    // Opacity is evaluated from the float32 peak, rather than culled at t=0.
    rawBytes[i * 32 + 27] = 255;
    for (let k = 0; k < 3; k++) {
      motion[model + k] = floats[source + k];
      motion[model + 3 + k] = floats[source + 8 + k];
      texture[target + k] = floats[source + 8 + k];
    }
    motion[model + 6] = texture[target + 3] = floats[source + 11];
    motion[model + 7] = texture[target + 4] = floats[source + 12];
    motion[model + 8] = texture[target + 5] = floats[source + 13];
  }
  const { splatBuffer, splatBufferDataOffsetBytes } = SplatBuffer.preallocateUncompressed(count, 0);
  SplatParser.parseToUncompressedSplatBufferSection(0, count - 1, raw, 0,
    splatBuffer.bufferData, splatBufferDataOffsetBytes);
  return { count, frameCount, frameRate, duration, textureHeight, motion, texture, buffer: splatBuffer.bufferData };
}
