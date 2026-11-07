// GaussianSplats3D 0.4.7 reserves at least 4M texels per texture even for small
// frames. Keep its width and power-of-two growth, starting at one row instead.
// Fail closed on dependency changes so this workaround must be reviewed again.
export function patchTextureSizing(source, version) {
  if (version !== '0.4.7') throw new Error('Review texture sizing for the new GaussianSplats3D version');
  const before = 'const texSize = new THREE.Vector2(4096, 1024);';
  if (source.split(before).length !== 2) throw new Error('GaussianSplats3D texture sizing source changed');
  return source.replace(before, 'const texSize = new THREE.Vector2(4096, 1);');
}
