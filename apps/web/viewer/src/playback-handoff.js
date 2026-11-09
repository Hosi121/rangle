/** Preview and scene seconds share a clock only when the publisher binds them. */
export function handoffTimeline(manifest, options) {
  const timeline = manifest.timeline;
  let mapping = options.mapping ?? null;
  if (timeline !== undefined) {
    if (!timeline || typeof timeline.previewUrl !== 'string' ||
        ![timeline.previewStartSeconds, timeline.sceneStartSeconds].every(value =>
          Number.isFinite(value) && value >= 0)) throw new Error('Invalid scene timeline');
    const preview = new URL(timeline.previewUrl, options.source);
    if (!['http:', 'https:'].includes(preview.protocol)) throw new Error('Invalid preview timeline');
    mapping = preview.href === options.previewUrl ? timeline : null;
  }
  if (mapping && ![mapping.previewStartSeconds, mapping.sceneStartSeconds].every(value =>
    Number.isFinite(value) && value >= 0)) throw new Error('Invalid preview mapping');
  return { frameRate: manifest.frameRate,
    frameCount: manifest.temporal ? manifest.frameCount : manifest.frames.length,
    mapping: mapping ? { previewStartSeconds: mapping.previewStartSeconds,
      sceneStartSeconds: mapping.sceneStartSeconds } : null };
}
