import { Matrix4 } from 'three';
import { SplatBuffer } from '@mkkellogg/gaussian-splats-3d';
import { attachTemporalMaterial } from './temporal-material.js';
import { PlaybackClock } from './playback-clock.js';
import { attachPlaybackGestures } from './playback-gestures.js';
import { prepareLiveMesh } from './live-model.js';

const noSort = Promise.resolve(false);

/** One resident mesh. Sort results commit their time AND camera together. */
export class TemporalSession {
  constructor({ createViewer, notify, prepareReplacement = prepareLiveMesh, createWorker = () =>
    new Worker(new URL('./temporal-worker.js', import.meta.url), { type: 'module' }) }) {
    this.createViewer = createViewer;
    this.notify = notify;
    this.createWorker = createWorker;
    this.prepareReplacement = prepareReplacement;
    this.view = new Matrix4();
    this.epoch = 0;
    this.sequence = 0;
    this.lastProgress = -Infinity;
    this.position = 0;
    this.paused = false;
    this.disposed = false;
  }

  load(manifest, { autoplay = false, strict = false, sha256 } = {}) {
    this.live = strict;
    this.userPaused = false;
    this.manifest = manifest;
    this.clock = new PlaybackClock(manifest.frameCount, manifest.frameRate, undefined, { loop: !this.live });
    this.clock.suspend(this.paused);
    if (autoplay) this.clock.toggle();
    this.notify({ type: 'loading', frame: 0 });
    this.worker = this.createWorker();
    this.worker.addEventListener('message', ({ data }) => {
      if (this.disposed) return;
      if (data.type === 'model') {
        if (this.live && this.paused) this.deferredModel = data;
        else void this.install(data).catch(() => this.fail());
      }
      else if (data.type === 'sorted') this.commit(data);
      else if (data.type === 'error') this.fail();
    });
    this.worker.addEventListener('error', () => this.fail());
    return new Promise((resolve, reject) => {
      this.resolveLoad = resolve;
      this.rejectLoad = reject;
      if (!this.live || !this.paused) this.timeout = setTimeout(() => this.fail(), 30000);
      this.worker.postMessage({ type: 'load', url: manifest.temporal.url, strict, sha256, hidden: this.paused });
    });
  }

  async install(model) {
    if (model.frameCount !== this.manifest.frameCount || model.frameRate !== Math.fround(this.manifest.frameRate)) {
      throw new Error('Temporal model timeline mismatch');
    }
    this.viewer = this.createViewer(this.manifest.camera);
    await this.viewer.addSplatBuffers([new SplatBuffer(model.buffer)],
      [{ splatAlphaRemovalThreshold: 1 }], false, false, false);
    while (this.live && this.paused && !this.disposed) {
      await new Promise(resolve => { this.resumeInstall = resolve; });
    }
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
        canPlay: true, playing: this.clock.playing && !this.clock.ended && !this.paused });
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
      playing: this.clock.playing && !this.clock.ended && !this.paused });
  }

  cancelReplacement() { this.replacement?.cancel(); }

  /** At most one candidate worker/mesh, alongside the current interactive model. */
  replace(manifest, { sha256 } = {}) {
    this.cancelReplacement();
    if (this.disposed || !this.initialized || this.paused) return Promise.resolve(false);
    const worker = this.createWorker();
    let candidate, camera, sortId, finished = false;
    return new Promise(resolve => {
      const finish = (ok) => {
        if (finished) return;
        finished = true;
        clearTimeout(timeout);
        if (!ok) { worker.terminate(); candidate?.dispose(); }
        if (this.replacement?.worker === worker) this.replacement = null;
        resolve(ok);
      };
      const timeout = setTimeout(() => finish(false), 30000);
      this.replacement = { worker, cancel: () => finish(false) };
      worker.addEventListener('error', () => finish(false));
      worker.addEventListener('message', ({ data }) => {
        if (finished) {
          if (this.worker === worker && !this.disposed) {
            if (data.type === 'sorted') this.commit(data);
            else if (data.type === 'error') this.fail();
          }
          return;
        }
        try {
          if (data.type === 'error') return finish(false);
          if (data.type === 'model') {
            if (data.frameCount !== manifest.frameCount || data.frameRate !== Math.fround(manifest.frameRate)) {
              return finish(false);
            }
            candidate = this.prepareReplacement(this.viewer, data);
            camera = this.viewer.camera.clone();
            camera.updateMatrixWorld();
            candidate.mesh.updateMatrixWorld();
            const view = new Matrix4().copy(camera.matrixWorldInverse).multiply(candidate.mesh.matrixWorld);
            sortId = ++this.sequence;
            worker.postMessage({ type: 'sort', id: sortId, time: 0, view: view.toArray() });
          } else if (data.type === 'sorted' && data.id === sortId && candidate) {
            candidate.mesh.devicePixelRatio = this.viewer.devicePixelRatio;
            candidate.mesh.updateRenderIndexes(data.indexes, data.active);
            candidate.material.setTime(0);
            const oldMesh = this.viewer.splatMesh, oldCamera = this.displayCamera;
            this.viewer.splatMesh = candidate.mesh;
            this.displayCamera = camera;
            try {
              // Commit only after one real draw succeeds. The same canvas,
              // desired camera and OrbitControls remain owned by this viewer.
              this.viewer.updateSplatMesh();
              this.viewer.render();
            } catch (error) {
              this.viewer.splatMesh = oldMesh;
              this.displayCamera = oldCamera;
              this.viewer.forceRenderNextFrame();
              throw error;
            }
            const wasPlaying = this.live ? !this.userPaused : this.clock.playing;
            this.worker.terminate();
            this.material.dispose();
            oldMesh.dispose();
            this.worker = worker;
            this.material = candidate.material;
            this.manifest = manifest;
            this.clock = new PlaybackClock(manifest.frameCount, manifest.frameRate, undefined, { loop: !this.live });
            this.clock.suspend(this.paused);
            if (wasPlaying) this.clock.toggle();
            this.epoch++;
            this.pendingSeek?.resolve?.();
            this.job = this.pendingSeek = this.lastRequest = null;
            this.recycle = data.indexes.buffer;
            this.position = 0;
            this.progress(true);
            finish(true);
          }
        } catch { finish(false); }
      });
      worker.postMessage({ type: 'load', url: manifest.temporal.url, strict: true, sha256 });
    });
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
    if (this.live && this.clock.ended) {
      this.clock.seek(0);
      if (!this.clock.playing) this.clock.toggle();
      this.userPaused = false;
    } else {
      this.clock.toggle();
      this.userPaused = !this.clock.playing;
    }
    this.progress(true);
    this.queue();
  }

  pause(value) {
    if (this.disposed || value === this.paused) return;
    this.paused = value;
    this.clock?.suspend(value);
    if (!value) { this.resumeInstall?.(); this.resumeInstall = null; }
    if (this.live && !this.initialized && this.worker) {
      clearTimeout(this.timeout);
      this.worker.postMessage({ type: 'visibility', hidden: value });
      if (!value) {
        this.timeout = setTimeout(() => this.fail(), 30000);
        if (this.deferredModel) {
          const model = this.deferredModel;
          this.deferredModel = null;
          void this.install(model).catch(() => this.fail());
        }
      }
    }
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
    this.cancelReplacement();
    this.deferredModel = null;
    this.resumeInstall?.();
    this.resumeInstall = null;
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
