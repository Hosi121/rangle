import { SplatParser, SplatBufferGenerator } from '@mkkellogg/gaussian-splats-3d';

export const MAX_FRAME_BYTES = 32 * 1024 * 1024;

/** Match the viewer's alpha=1, float32, optimized .splat conversion. */
export function prepareFrame(data) {
  if (!(data instanceof ArrayBuffer) || data.byteLength === 0 ||
      data.byteLength > MAX_FRAME_BYTES || data.byteLength % 32 !== 0) {
    throw new Error('Invalid or oversized splat frame');
  }
  const splats = SplatParser.parseStandardSplatToUncompressedSplatArray(data);
  return SplatBufferGenerator.getStandardGenerator(1, 0)
    .generateFromUncompressedSplatArray(splats).bufferData;
}
