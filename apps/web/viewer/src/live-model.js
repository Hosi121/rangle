import { SplatBuffer } from '@mkkellogg/gaussian-splats-3d';
import { attachTemporalMaterial } from './temporal-material.js';

/** Pinned 0.4.7 seam: build a separate mesh without modifying the displayed one.
 * createSplatMesh applies the viewer's renderer settings. Its synchronous field
 * assignment is restored before control returns; no replacement scene tree or
 * second canvas/controls is created. A failed candidate owns only its resources.
 */
export function prepareLiveMesh(viewer, model) {
  const displayed = viewer.splatMesh;
  let mesh, material;
  try {
    viewer.createSplatMesh();
    mesh = viewer.splatMesh;
  } finally { viewer.splatMesh = displayed; }
  try {
    mesh.setRenderer(viewer.renderer);
    mesh.build([new SplatBuffer(model.buffer)], [{ splatAlphaRemovalThreshold: 1 }], false, false);
    material = attachTemporalMaterial(mesh.material, model);
    return { mesh, material, dispose() { material.dispose(); mesh.dispose(); } };
  } catch (error) {
    material?.dispose(); mesh?.dispose();
    throw error;
  }
}

export function liveSceneUrl(value, origins, session) {
  const url = new URL(value);
  const match = url.pathname.match(/^\/(?:live-assets|live)\/([A-Za-z0-9_-]{1,64})\/\d{6,}-[a-f0-9]{16}\/scene\.json$/);
  if (!['http:', 'https:'].includes(url.protocol) || !origins.includes(url.origin) ||
      url.username || url.password || url.search || url.hash || !match || (session && match[1] !== session)) {
    throw new Error('Untrusted live scene');
  }
  return { url, session: match[1] };
}
