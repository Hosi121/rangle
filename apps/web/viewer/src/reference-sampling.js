/** Keep the model's projected pixel filter at its training raster scale.
 * GaussianSplats3D 0.4.7 uses separate renderer and splat-mesh pixel ratios.
 * CSS size, vertical field of view and orbit controls remain viewport based.
 */
export function attachReferenceSampling(viewer, referenceImageHeight) {
  if (referenceImageHeight === undefined) return () => {};
  const resize = () => {
    const width = viewer.rootElement.offsetWidth;
    const height = viewer.rootElement.offsetHeight;
    if (width <= 0 || height <= 0) return;
    // Avoid floating-point rounding producing a drawing buffer one row short.
    const ratio = (referenceImageHeight + 1e-6) / height;
    viewer.devicePixelRatio = ratio;
    viewer.splatMesh.devicePixelRatio = ratio;
    viewer.renderer.setPixelRatio(ratio);
    viewer.renderer.setSize(width, height);
    viewer.forceRenderNextFrame();
  };
  const observer = new ResizeObserver(resize);
  observer.observe(viewer.rootElement);
  resize();
  return () => observer.disconnect();
}
