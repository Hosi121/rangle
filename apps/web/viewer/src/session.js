import { validIntrinsics } from './camera-intrinsics.js';

export const CHANNEL = 'rangle-scene-v1';

function httpUrl(value, base) {
  const url = new URL(value, base);
  if (!['http:', 'https:'].includes(url.protocol)) throw new Error('Invalid URL');
  return url;
}

export function parseManifest(input, source) {
  const vector = (value) => Array.isArray(value) && value.length === 3 && value.every(Number.isFinite);
  const camera = input?.camera;
  if (input?.version !== 1 || !Number.isFinite(input.frameRate) || input.frameRate <= 0 ||
      !Array.isArray(input.frames) || input.frames.length === 0 ||
      !vector(camera?.position) || !vector(camera?.lookAt) || !vector(camera?.up) ||
      !camera.up.some((value) => value !== 0) ||
      camera.position.every((value, index) => value === camera.lookAt[index]) ||
      !Number.isFinite(camera.fovYDegrees) || camera.fovYDegrees <= 0 || camera.fovYDegrees >= 180 ||
      (camera.referenceImageHeight !== undefined && (!Number.isInteger(camera.referenceImageHeight) ||
        camera.referenceImageHeight < 64 || camera.referenceImageHeight > 2160)) ||
      (camera.intrinsics !== undefined && !validIntrinsics(camera.intrinsics)) ||
      !['label', 'source', 'license'].every((key) => typeof input.attribution?.[key] === 'string' && input.attribution[key].trim())) {
    throw new Error('Invalid scene manifest');
  }
  httpUrl(input.attribution.source, source);
  const frames = input.frames.map((value) => {
    if (typeof value !== 'string' || !value) throw new Error('Invalid frame');
    const url = httpUrl(value, source);
    if (!url.pathname.endsWith('.splat')) throw new Error('Invalid frame format');
    return url.href;
  });
  return { ...input, frames };
}

/** One viewer and one serialized scene mutation; pending seeks retain only the latest index. */
export class SceneSession {
  constructor({ createViewer, frameLoader, fetchManifest = (...args) => globalThis.fetch(...args), notify }) {
    this.createViewer = createViewer;
    this.frameLoader = frameLoader;
    this.fetchManifest = fetchManifest;
    this.notify = notify;
    this.abort = new AbortController();
    this.disposed = false;
    this.paused = false;
    this.hasScene = false;
    this.pending = null;
    this.processing = null;
    this.release = null;
  }

  async load(source) {
    const timeout = setTimeout(() => this.abort.abort(), 15000);
    try {
      const response = await this.fetchManifest(httpUrl(source).href, { signal: this.abort.signal });
      if (!response.ok) throw new Error('Manifest request failed');
      this.manifest = parseManifest(await response.json(), response.url || source);
      if (this.disposed) return;
      this.viewer = this.createViewer(this.manifest.camera);
      await this.seek(0);
    } catch (error) {
      if (!this.disposed) {
        this.notify({ type: 'error' });
        await this.dispose();
      }
    } finally {
      clearTimeout(timeout);
    }
  }

  seek(frame) {
    if (this.disposed || !this.viewer || !Number.isInteger(frame)) return Promise.resolve();
    const target = Math.max(0, Math.min(this.manifest.frames.length - 1, frame));
    if (!this.processing && this.loadedFrame === target) return Promise.resolve();
    if (this.processing && this.loadingFrame === target && this.pending === null) return this.processing;
    this.pending = target;
    if (this.processing) {
      this.frameLoader.cancel();
      return this.processing;
    }
    this.processing = this.drain().finally(() => { this.processing = null; });
    return this.processing;
  }

  async drain() {
    try {
      while (this.pending !== null && !this.disposed) {
        const frame = this.pending;
        this.pending = null;
        this.loadingFrame = frame;
        this.notify({ type: 'loading', frame });
        let buffer;
        try {
          buffer = await this.frameLoader.load(this.manifest.frames[frame]);
        } catch (error) {
          if (!this.disposed && this.pending !== null && error.name === 'AbortError') continue;
          throw error;
        }
        if (this.disposed) return;
        if (this.pending !== null) continue;
        // Keep the previous scene interactive while the worker prepares data.
        // Capture the most recent camera only when replacing GPU resources.
        const camera = this.captureCamera();
        this.viewer.stop();
        if (this.hasScene) await this.viewer.removeSplatScene(0, false);
        if (this.disposed) return;
        this.hasScene = false;
        // Use the public remove/add lifecycle. The pinned SDK's replace path
        // retains old scene children and races its asynchronous tree build.
        await this.viewer.addSplatBuffers([buffer], [{ splatAlphaRemovalThreshold: 1 }],
          true, false, false);
        if (this.disposed) return;
        this.hasScene = true;
        this.loadedFrame = frame;
        this.restoreCamera(camera);
        if (this.pending !== null) continue;
        if (!this.paused) this.viewer.start();
        this.notify({ type: 'ready', frame, frameCount: this.manifest.frames.length,
          frameRate: this.manifest.frameRate, attribution: this.manifest.attribution });
      }
    } catch (error) {
      if (!this.disposed) {
        this.notify({ type: 'error' });
        await this.dispose();
      }
    }
  }

  captureCamera() {
    const { camera, controls } = this.viewer;
    return camera ? { position: camera.position.clone(), quaternion: camera.quaternion.clone(),
      up: camera.up.clone(), target: controls?.target.clone(), fov: camera.fov } : null;
  }

  restoreCamera(saved) {
    if (!saved || !this.viewer.camera) return;
    const { camera, controls } = this.viewer;
    camera.position.copy(saved.position);
    camera.quaternion.copy(saved.quaternion);
    camera.up.copy(saved.up);
    camera.fov = saved.fov;
    camera.updateProjectionMatrix();
    if (saved.target && controls) controls.target.copy(saved.target);
    controls?.update();
  }

  pause(paused) {
    if (this.disposed || this.paused === paused) return;
    this.paused = paused;
    if (!this.viewer) return;
    if (paused) this.viewer.stop();
    else if (this.hasScene && !this.processing) this.viewer.start();
  }

  dispose() {
    if (this.release) return this.release;
    this.disposed = true;
    this.pending = null;
    this.abort.abort();
    this.frameLoader.dispose();
    this.viewer?.stop();
    this.release = Promise.resolve().then(() => this.viewer?.dispose()).catch(() => {});
    return this.release;
  }
}
