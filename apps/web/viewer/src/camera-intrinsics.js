export function validIntrinsics(value) {
  if (!value || !['width', 'height', 'fx', 'fy', 'cx', 'cy'].every((key) => Number.isFinite(value[key]))) return false;
  const fov = 2 * Math.atan(value.height / (2 * value.fy));
  const scales = [value.fx / value.fy, 2 * value.fx / value.height, 2 * value.fy / value.height];
  const stable = scales.every((scale) => Math.fround(scale) > 0 &&
    Number.isFinite(Math.fround(scale)) && Number.isFinite(Math.fround(1 / scale)));
  return fov > 0 && fov < Math.PI && stable &&
    Number.isInteger(value.width) && Number.isInteger(value.height) &&
    value.width >= 1 && value.height >= 1 && value.width <= 16384 && value.height <= 16384 &&
    value.fx > 0 && value.fy > 0 && value.cx >= 0 && value.cx <= value.width &&
    value.cy >= 0 && value.cy <= value.height;
}

/** Preserve calibrated pixel geometry while cropping to the viewport aspect. */
export function attachCameraIntrinsics(camera, intrinsics) {
  if (intrinsics === undefined) return () => {};
  const { width, height, fx, fy, cx, cy } = intrinsics;
  const update = camera.updateProjectionMatrix;
  camera.fov = 2 * Math.atan(height / (2 * fy)) * 180 / Math.PI;
  camera.updateProjectionMatrix = function () {
    update.call(this);
    const projection = this.projectionMatrix;
    projection.elements[0] *= fx / fy;
    projection.elements[8] = -2 * (cx - width / 2) / (height * this.aspect);
    projection.elements[9] = 2 * (cy - height / 2) / height;
    this.projectionMatrixInverse.copy(projection).invert();
  };
  camera.updateProjectionMatrix();
  return () => { camera.updateProjectionMatrix = update; };
}
