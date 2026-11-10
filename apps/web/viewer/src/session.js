import { TransportPlayback } from './transport-playback.js';
import { parseTransport } from './transport-manifest.js';
import { validIntrinsics } from './camera-intrinsics.js';
import { TemporalSession } from './temporal-session.js';
import { liveSceneUrl } from './live-model.js';
import { handoffTimeline } from './playback-handoff.js';
import { strictJson, exactFields } from './strict-json.js';

export const CHANNEL = 'rangle-scene-v1';

function httpUrl(value, base) {
  const url = new URL(value, base);
  if (!['http:', 'https:'].includes(url.protocol)) throw new Error('Invalid URL');
  return url;
}

export function parseManifest(input, source) {
  const vector = (value) => Array.isArray(value) && value.length === 3 && value.every(Number.isFinite);
  const camera = input?.camera;
  const temporal = input?.temporal;
  const temporalValid = exactFields(temporal, ['url']) && temporal.url === 'model.rgs' &&
    Number.isInteger(input.frameCount) && input.frameCount >= 2 && input.frameCount <= 15000 &&
    input.frameRate <= 240;
  const shape = exactFields(input, ['version', 'frameRate', 'camera', 'attribution', ...(temporal ? ['temporal', 'frameCount'] : ['frames'])], ['timeline']) &&
    exactFields(camera, ['position', 'lookAt', 'up', 'fovYDegrees'], ['referenceImageHeight', 'intrinsics']) &&
    exactFields(input.attribution, ['label', 'source', 'license']) &&
    (camera.intrinsics === undefined || exactFields(camera.intrinsics, ['width', 'height', 'fx', 'fy', 'cx', 'cy'])) &&
    (input.timeline === undefined || (exactFields(input.timeline, ['previewUrl', 'previewStartSeconds', 'sceneStartSeconds']) &&
      typeof input.timeline.previewUrl === 'string' && input.timeline.previewUrl.length > 0 &&
      [input.timeline.previewStartSeconds, input.timeline.sceneStartSeconds].every(value => Number.isFinite(value) && value >= 0)));
  if (!shape || input?.version !== 1 || !Number.isFinite(input.frameRate) || input.frameRate <= 0 ||
      (temporal ? !temporalValid : !Array.isArray(input.frames) || input.frames.length === 0) ||
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
  if (temporal) {
    const url = httpUrl(temporal.url, source);
    if (!url.pathname.endsWith('.rgs')) throw new Error('Invalid temporal format');
    return { ...input, temporal: { url: url.href } };
  }
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

  load(source, options = {}) {
    this.liveOptions = options;
    this.source = source;
    this.sourceSha256 = options.sha256;
    return this.initialLoad = this.loadInitial(source);
  }

  async manifestFrom(source, signal) {
    const requested = httpUrl(source);
    const transportSource = this.liveOptions.transportIdentity !== undefined ||
      /^\/manifests\/[A-Za-z0-9_-]{1,64}\/[A-Za-z0-9_-]{1,64}\.json$/.test(requested.pathname);
    let options = transportSource ? { signal, redirect: 'error', credentials: 'omit' } : { signal };
    if (this.liveOptions.live) {
      const checked = liveSceneUrl(source, this.liveOptions.origins, this.liveSession);
      this.liveSession = checked.session;
      options = { ...options, redirect: 'error', credentials: 'omit' };
    }
    const response = await this.fetchManifest(httpUrl(source).href, options);
    if (!response.ok) throw new Error('Manifest request failed');
    if (transportSource && response.url && response.url !== requested.href) throw Error('Transport manifest redirected');
    let json;
    if (this.liveOptions.live || transportSource) {
      if (Number(response.headers.get('Content-Length')) > 1048576) {
        await response.body?.cancel().catch(() => {}); throw new Error('Manifest too large');
      }
      const reader = response.body.getReader(), chunks = [];
      let size = 0, rejectAbort;
      const aborted = new Promise((_, reject) => { rejectAbort = reject; });
      const cancel = () => {
        rejectAbort(signal.reason ?? new DOMException('Aborted', 'AbortError'));
        void reader.cancel().catch(() => {});
      };
      signal.addEventListener('abort', cancel, { once: true });
      if (signal.aborted) cancel();
      try {
        while (true) {
          const { value, done } = await Promise.race([reader.read(), aborted]);
          if (done) break;
          size += value.byteLength;
          if (size > 1048576) throw new Error('Manifest too large');
          chunks.push(value);
        }
      } finally { signal.removeEventListener('abort', cancel); await reader.cancel().catch(() => {}); reader.releaseLock(); }
      const bytes = new Uint8Array(size);
      let offset = 0;
      for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
      json = strictJson(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    } else json = strictJson(await response.text());
    const manifest = transportSource || ['rangle-temporal-rgs1-segments-v1', 'rangle-temporal-rgs1'].includes(json?.codec) ?
      parseTransport(json, requested.href, this.liveOptions.transportIdentity ?? {}) : parseManifest(json, response.url || source);
    if (this.liveOptions.live) {
      const model = new URL(manifest.temporal?.url ?? '');
      const expected = new URL('model.rgs', source);
      if (model.href !== expected.href) throw new Error('Untrusted live model');
    }
    return manifest;
  }

  async loadInitial(source) {
    const timeout = setTimeout(() => this.abort.abort(), 15000);
    try {
      this.manifest = await this.manifestFrom(source, this.abort.signal);
      clearTimeout(timeout);
      if (this.disposed) return;
      if (this.manifest.representations) {
        if (this.liveOptions.live) throw Error('LIVE cannot use VOD transport');
        this.temporal = new TransportPlayback({ createViewer: this.createViewer, notify: this.notify,
          fetchAsset: this.fetchManifest, parseScene: parseManifest });
        this.temporal.pause(this.paused);
        await this.temporal.load(this.manifest, { ...this.liveOptions, source });
        return;
      }
      const continuity = this.liveOptions.deferFirstFrame ?
        handoffTimeline(this.manifest, { ...this.liveOptions, source }) : null;
      if (this.manifest.temporal) {
        this.temporal = new TemporalSession({ createViewer: this.createViewer, notify: this.notify });
        this.temporal.pause(this.paused);
        await this.temporal.load(this.manifest, { autoplay: this.liveOptions.live,
          strict: this.liveOptions.live, sha256: this.liveOptions.sha256,
          continuity });
        return;
      }
      this.viewer = this.createViewer(this.manifest.camera);
      if (continuity) this.notify({ type: 'prepared', ...continuity });
      else await this.seek(0);
    } catch (error) {
      if (!this.disposed) {
        this.notify({ type: 'error' });
        await this.dispose();
      }
    } finally {
      clearTimeout(timeout);
    }
  }

  replace(source, sha256) {
    if (!this.liveOptions?.live || this.disposed || this.paused) return Promise.resolve(false);
    for (const request of [this.activeReplacement, this.pendingReplacement]) {
      if (request?.source === source && request.sha256 === sha256) return request.promise;
    }
    if (!this.activeReplacement && this.temporal?.initialized &&
        source === this.source && sha256 === this.sourceSha256) return Promise.resolve(true);
    let resolve;
    const request = { source, sha256, promise: new Promise(done => { resolve = done; }), resolve: value => resolve(value) };
    // New revisions replace waiting metadata, never the download/prepare in flight.
    this.pendingReplacement?.resolve(false);
    this.pendingReplacement = request;
    this.drainReplacement();
    return request.promise;
  }

  drainReplacement() {
    if (this.activeReplacement || !this.pendingReplacement || this.disposed || this.paused) return;
    const request = this.activeReplacement = this.pendingReplacement;
    this.pendingReplacement = null;
    void this.replaceActive(request).then(ready => {
      request.resolve(ready);
      if (this.activeReplacement !== request) return;
      this.activeReplacement = null;
      this.drainReplacement();
    });
  }

  async replaceActive({ source, sha256 }) {
    const abort = this.replacementAbort = new AbortController();
    const timeout = setTimeout(() => abort.abort(), 15000);
    abort.signal.addEventListener('abort', () => {
      if (this.replacementAbort === abort) this.temporal?.cancelReplacement();
    });
    try {
      await this.initialLoad;
      if (abort.signal.aborted || this.disposed || !this.temporal) return false;
      const manifest = await this.manifestFrom(source, abort.signal);
      if (abort.signal.aborted || this.disposed) return false;
      clearTimeout(timeout);
      const ready = await this.temporal.replace(manifest, { sha256 });
      if (!ready || abort.signal.aborted || this.disposed) return false;
      this.source = source;
      this.sourceSha256 = sha256;
      this.manifest = manifest;
      return true;
    } catch { return false; }
    finally { clearTimeout(timeout); }
  }

  cancelReplacement() {
    const active = this.activeReplacement;
    this.activeReplacement = null;
    this.pendingReplacement?.resolve(false);
    this.pendingReplacement = null;
    this.replacementAbort?.abort();
    this.temporal?.cancelReplacement();
    active?.resolve(false);
  }

  readyMessage() {
    return { frame: 0, frameCount: this.manifest.frameCount, frameRate: this.manifest.frameRate,
      canPlay: true, playing: this.temporal.clock.playing && !this.temporal.clock.ended && !this.paused };
  }

  present(frame, playing, presentationId) {
    if (this.temporal) return this.temporal.present(frame, playing, presentationId);
    return this.seek(frame, { presentationId });
  }

  seek(frame, presentation = null) {
    if (this.temporal) return this.temporal.seek(frame);
    if (this.disposed || !this.viewer || !Number.isInteger(frame)) return Promise.resolve();
    const target = Math.max(0, Math.min(this.manifest.frames.length - 1, frame));
    this.presentation = presentation;
    if (!this.processing && this.loadedFrame === target) {
      if (presentation) this.drawReady(target);
      return Promise.resolve();
    }
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
        this.drawReady(frame);
      }
    } catch (error) {
      if (!this.disposed) {
        this.notify({ type: 'error' });
        await this.dispose();
      }
    }
  }

  drawReady(frame) {
    // A hidden handoff must acknowledge the actual target draw, not a stale canvas.
    this.viewer.updateSplatMesh?.();
    this.viewer.render?.();
    this.notify({ type: 'ready', frame, frameCount: this.manifest.frames.length,
      frameRate: this.manifest.frameRate, attribution: this.manifest.attribution,
      canPlay: false, playing: false, ...this.presentation });
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
    if (paused && this.liveOptions?.live) this.cancelReplacement();
    if (this.temporal) return this.temporal.pause(paused);
    if (!this.viewer) return;
    if (paused) this.viewer.stop();
    else if (this.hasScene && !this.processing) this.viewer.start();
  }

  dispose() {
    if (this.release) return this.release;
    this.disposed = true;
    this.pending = null;
    this.abort.abort();
    this.cancelReplacement();
    this.frameLoader.dispose();
    this.viewer?.stop();
    this.release = Promise.resolve().then(() => this.temporal ? this.temporal.dispose() : this.viewer?.dispose()).catch(() => {});
    return this.release;
  }

  togglePlayback() { this.temporal?.togglePlayback(); }

  capturePlayback() {
    return { positionSeconds: this.temporal ? this.temporal.position : (this.loadedFrame ?? 0) / this.manifest.frameRate,
      playing: this.temporal ? this.temporal.clock.playing : false };
  }
}
