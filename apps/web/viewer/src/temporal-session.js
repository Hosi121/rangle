import { Matrix4 } from 'three';
import { SplatBuffer } from '@mkkellogg/gaussian-splats-3d';
import { attachTemporalMaterial } from './temporal-material.js';
import { PlaybackClock } from './playback-clock.js';
import { attachPlaybackGestures } from './playback-gestures.js';

const noSort = Promise.resolve(false);

/** One resident mesh. Sort results commit their time AND camera together. */
export class TemporalSession {
  constructor({ createViewer, notify, createWorker = () =>
    new Worker(new URL('./temporal-worker.js', import.meta.url), { type: 'module' }) }) {
    this.createViewer = createViewer;
    this.notify = notify;
    this.createWorker = createWorker;
    this.view = new Matrix4();
    this.epoch = 0;
    this.sequence = 0;
    this.lastProgress = -Infinity;
    this.position = 0;
    this.paused = false;
    this.disposed = false;
  }

  load(manifest) {
    this.manifest = manifest;
    this.clock = new PlaybackClock(manifest.frameCount, manifest.frameRate);
    this.clock.suspend(this.paused);
    this.notify({ type: 'loading', frame: 0 });
    this.worker = this.createWorker();
    this.worker.addEventListener('message', ({ data }) => {
      if (this.disposed) return;
      if (data.type === 'model') void this.install(data).catch(() => this.fail());
      else if (data.type === 'sorted') this.commit(data);
      else if (data.type === 'error') this.fail();
    });
    this.worker.addEventListener('error', () => this.fail());
    return new Promise((resolve, reject) => {
      this.resolveLoad = resolve;
      this.rejectLoad = reject;
      this.timeout = setTimeout(() => this.fail(), 30000);
      this.worker.postMessage({ type: 'load', url: manifest.temporal.url });
    });
  }

  async install(model) {
    if (model.frameCount !== this.manifest.frameCount || model.frameRate !== this.manifest.frameRate) {
      throw new Error('Temporal model timeline mismatch');
    }
    this.viewer = this.createViewer(this.manifest.camera);
    await this.viewer.addSplatBuffers([new SplatBuffer(model.buffer)],
      [{ splatAlphaRemovalThreshold: 1 }], false, false, false);
    if (this.disposed) return;
    this.viewer.disposeSortWorker();
    this.material = attachTemporalMaterial(this.viewer.splatMesh.material, model);
    this.displayCamera = this.viewer.camera.clone();
    this.viewer.checkForFocalPointChange = () => {};
    // Controls keep a desired camera. Rendering uses the camera whose depth order is ready.
    const withDisplayCamera = action => {
      const desired = this.viewer.camera;
      this.viewer.camera = this.displayCamera;
      try { return action(); } finally { this.viewer.camera = desired; }
    };
    const render = this.viewer.render.bind(this.viewer);
    const updateMesh = this.viewer.updateSplatMesh.bind(this.viewer);
    this.viewer.render = () => withDisplayCamera(render);
    this.viewer.updateSplatMesh = () => withDisplayCamera(updateMesh);
    this.viewer.runSplatSort = () => { this.queue(); return noSort; };
    this.releaseGestures = attachPlaybackGestures(this.viewer.renderer.domElement, () => this.togglePlayback());
    this.viewer.updateForRendererSizeChanges();
    this.pendingSeek = { frame: 0, position: 0 };
    this.queue();
  }

  queue() {
    if (this.disposed || !this.material || this.job || (this.paused && this.initialized)) return;
    const desired = this.viewer.camera;
    desired.updateMatrixWorld();
    this.viewer.splatMesh.updateMatrixWorld();
    const position = this.pendingSeek?.position ?? this.clock.value();
    const normalized = Math.fround(position / this.clock.duration);
    if (!this.pendingSeek && this.lastRequest?.time === normalized &&
        this.lastRequest.camera.matrixWorld.equals(desired.matrixWorld) &&
        this.lastRequest.camera.projectionMatrix.equals(desired.projectionMatrix)) return;
    this.view.copy(desired.matrixWorldInverse).multiply(this.viewer.splatMesh.matrixWorld);
    const camera = desired.clone();
    this.job = { id: ++this.sequence, time: normalized, position, camera,
      epoch: this.epoch, seek: this.pendingSeek };
    this.lastRequest = { time: normalized, camera };
    const message = { type: 'sort', id: this.job.id, time: normalized,
      view: this.view.toArray(), recycle: this.recycle };
    this.recycle = null;
    this.worker.postMessage(message, message.recycle ? [message.recycle] : []);
  }

  commit(data) {
    const job = this.job;
    if (this.disposed || !job || data.id !== job.id) return;
    this.job = null;
    this.recycle = data.indexes.buffer;
    if (job.epoch !== this.epoch) { this.queue(); return; }
    this.viewer.splatMesh.updateRenderIndexes(data.indexes, data.active);
    this.material.setTime(job.time);
    this.displayCamera.copy(job.camera);
    this.position = job.position;
    this.viewer.forceRenderNextFrame();
    const firstFrame = !this.initialized;
    if (firstFrame) {
      // A hidden preloader still prepares one real GPU frame before ready.
      this.viewer.updateSplatMesh();
      this.viewer.render();
      this.initialized = true;
      clearTimeout(this.timeout);
      if (!this.paused) this.viewer.start();
    }
    if (job.seek && this.pendingSeek === job.seek) {
      this.pendingSeek = null;
      this.clock.seek(job.position);
      this.progress(true);
      this.notify({ type: 'ready', frame: job.seek.frame, frameCount: this.manifest.frameCount,
        frameRate: this.manifest.frameRate, attribution: this.manifest.attribution,
        canPlay: true, playing: this.clock.playing && !this.paused });
      job.seek.resolve?.();
    } else this.progress();
    if (firstFrame) this.resolveLoad();
    // A user seek supersedes the old request immediately. Automatic playback
    // waits for the viewer's next RAF, rather than spinning the worker freely.
    if (this.pendingSeek) this.queue();
  }

  progress(force = false) {
    const now = performance.now();
    if (!force && now - this.lastProgress < 100) return;
    this.lastProgress = now;
    this.notify({ type: 'playback', positionSeconds: this.position,
      playing: this.clock.playing && !this.paused });
  }

  seek(frame) {
    if (this.disposed || !this.material || !Number.isInteger(frame)) return Promise.resolve();
    frame = Math.max(0, Math.min(frame, this.manifest.frameCount - 1));
    this.pendingSeek?.resolve?.();
    this.epoch++;
    this.clock.seek(frame / this.manifest.frameRate);
    return new Promise(resolve => {
      this.pendingSeek = { frame, position: frame / this.manifest.frameRate, resolve };
      this.queue();
    });
  }

  togglePlayback() {
    if (!this.initialized || this.disposed) return;
    this.clock.toggle();
    this.progress(true);
    this.queue();
  }

  pause(value) {
    if (this.disposed || value === this.paused) return;
    this.paused = value;
    this.clock?.suspend(value);
    if (this.viewer && this.initialized) {
      if (value) this.viewer.stop();
      else this.viewer.start();
      this.progress(true);
    }
  }

  fail() {
    if (this.disposed) return;
    if (this.initialized) this.notify({ type: 'error' });
    else this.rejectLoad?.(new Error('Temporal scene failed'));
    void this.dispose();
  }

  dispose() {
    if (this.release) return this.release;
    this.disposed = true;
    this.epoch++;
    clearTimeout(this.timeout);
    this.viewer?.stop();
    this.worker?.terminate();
    this.releaseGestures?.();
    this.material?.dispose();
    this.pendingSeek?.resolve?.();
    this.rejectLoad?.(new DOMException('Scene disposed', 'AbortError'));
    this.release = Promise.resolve().then(() => this.viewer?.dispose()).catch(() => {});
    return this.release;
  }
}
