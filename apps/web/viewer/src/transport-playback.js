import { TemporalSession } from './temporal-session.js';
import { TransportABR } from './transport-abr.js';
import { PlaybackClock } from './playback-clock.js';
import { strictJson } from './strict-json.js';
import { handoffTimeline } from './playback-handoff.js';
import { publicSegmentCache, segmentCacheAge } from './segment-cache.js';

function abortable(promise, signal) {
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, { once: true });
    Promise.resolve(promise).then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
  });
}

/** One current mesh and one next encoded segment; no history-sized media cache. */
export class TransportPlayback {
  constructor({ createViewer, notify, fetchAsset = (...args) => fetch(...args), parseScene,
    createTemporal = options => new TemporalSession(options), requestFrame = cb => requestAnimationFrame(cb),
    cancelFrame = id => cancelAnimationFrame(id), abrConfig = {}, transferTimeoutMs = 60000, segmentCache = publicSegmentCache }) {
    Object.assign(this, { createViewer, notify, fetchAsset, parseScene, createTemporal, requestFrame, cancelFrame, abrConfig, transferTimeoutMs, segmentCache });
    this.paused = false; this.disposed = false; this.epoch = 0; this.inits = new Map(); this.index = -1;
    this.position = 0; this.currentId = ''; this.abort = new AbortController();
  }
  async read(url, size, signal, onResponse) {
    if (!Number.isSafeInteger(size) || size <= 0 || size > 32 * 1024 * 1024) throw Error('Transport asset exceeds bounds');
    signal.throwIfAborted();
    const controller = new AbortController(), abort = () => controller.abort(signal.reason);
    if (signal.aborted) abort(); else signal.addEventListener('abort', abort, { once: true });
    const timeout = setTimeout(() => controller.abort(new DOMException('Transport transfer timed out', 'TimeoutError')), this.transferTimeoutMs);
    let reader;
    try {
      // Cache Storage hits bypass this path. Misses are real transfers for ABR.
      const pending = this.fetchAsset(url, { signal: controller.signal, redirect: 'error', credentials: 'omit', cache: 'no-store' });
      const response = await abortable(Promise.resolve(pending).then(response => {
        if (controller.signal.aborted) { void response.body?.cancel().catch(() => {}); throw controller.signal.reason; }
        return response;
      }), controller.signal);
      reader = response.body?.getReader();
      if (response.status !== 200 || !reader) throw Error('Transport asset request failed');
      onResponse?.(response);
      // The manifest fixes the bounded length. Fill one destination without
      // retaining every received chunk alongside a later concatenation buffer.
      const result = new Uint8Array(size); let bytes = 0;
      while (true) {
        const part = await abortable(reader.read(), controller.signal);
        if (part.done) break;
        if (part.value.length > size - bytes) throw Error('Transport asset size mismatch');
        result.set(part.value, bytes);
        bytes += part.value.length;
      }
      if (bytes !== size || controller.signal.aborted) throw Error('Transport asset size mismatch');
      return result;
    } finally {
      clearTimeout(timeout); signal.removeEventListener('abort', abort);
      if (reader) { await reader.cancel().catch(() => {}); reader.releaseLock(); }
    }
  }
  async init(r, signal) {
    if (this.inits.has(r.id)) return this.inits.get(r.id);
    if (!r.init_url || r.init_size_bytes > 65536) throw Error('Transport needs bounded scene initialization');
    const bytes = await this.read(r.init_url, r.init_size_bytes, signal);
    const scene = this.parseScene(strictJson(new TextDecoder('utf-8', { fatal: true }).decode(bytes)), r.init_url);
    if (!scene.temporal || scene.temporal.url !== r.segments[0].url) throw Error('Transport initial model mismatch');
    const legacy = this.manifest.codec === 'rangle-temporal-rgs1';
    if (legacy && r.segments.length !== 1 || !legacy && r.segments.length < 2) throw Error('Invalid profile segment count');
    const coverage = (scene.frameCount - (legacy ? 1 : 0)) / scene.frameRate;
    if (r.segments.some((s, i) => Math.abs(s.duration_seconds - coverage) > 1e-6 || Math.abs(s.start_seconds - i * coverage) > 1e-6)) throw Error('Unsupported transport timeline');
    if (this.scene && (scene.frameCount !== this.scene.frameCount || scene.frameRate !== this.scene.frameRate ||
        JSON.stringify(scene.camera) !== JSON.stringify(this.scene.camera) || JSON.stringify(scene.attribution) !== JSON.stringify(this.scene.attribution))) throw Error('Transport scene changed across quality');
    this.inits.set(r.id, scene); return scene;
  }
  async acquire(index, bufferSeconds, signal) {
    const decision = this.abr.select({ segmentIndex: index, currentId: this.currentId, bufferSeconds, initCached: new Set(this.inits.keys()) });
    const r = decision.representation, scene = await this.init(r, signal), segment = r.segments[index];
    const verified = async bytes => {
      if (!(bytes instanceof Uint8Array) || bytes.byteLength !== segment.size_bytes) return false;
      const digest = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(x => x.toString(16).padStart(2, '0')).join('');
      signal.throwIfAborted(); return digest === segment.sha256;
    };
    let bytes = await this.segmentCache?.get(segment, signal);
    if (bytes && !await verified(bytes)) { await this.segmentCache.remove(segment, signal); bytes = null; }
    const cacheHit = !!bytes;
    if (!cacheHit) {
      let expiresAt = 0;
      const started = performance.now();
      bytes = await this.read(segment.url, segment.size_bytes, signal, response => {
        const receivedAt = Date.now(), delay = (performance.now() - started) / 1000;
        expiresAt = receivedAt + Math.floor(segmentCacheAge(response, delay, receivedAt) * 1000);
      });
      const seconds = (performance.now() - started) / 1000;
      if (!await verified(bytes)) throw Error('Transport segment hash mismatch');
      await this.segmentCache?.put(segment, bytes, signal, expiresAt);
      signal.throwIfAborted();
      if (Number.isFinite(seconds) && seconds > 0) this.abr.observe({ bytes: bytes.length, seconds });
    }
    signal.throwIfAborted();
    return { decision, r, scene: { ...scene, temporal: { url: segment.url } }, segment, bytes: bytes.buffer };
  }
  async load(manifest, options = {}) {
    this.manifest = manifest; this.options = options; this.abr = new TransportABR(manifest, this.abrConfig);
    try {
      const first = await this.acquire(0, 0, this.abort.signal);
      if (this.disposed) return;
      this.scene = first.scene; this.sceneSource = first.r.init_url;
      this.frameRate = this.scene.frameRate; this.frameCount = this.scene.frameCount * first.r.segments.length;
      if (!Number.isSafeInteger(this.frameCount)) throw Error('Invalid transport frame count');
      this.clock = new PlaybackClock(this.frameCount, this.frameRate); this.clock.suspend(this.paused);
      this.temporal = this.createTemporal({ createViewer: this.createViewer, notify: message => this.localMessage(message) });
      this.temporal.togglePlayback = () => this.togglePlayback();
      this.temporal.pause(this.paused);
      this.initial = true;
      await this.temporal.load(first.scene, { strict: true, sha256: first.segment.sha256, bytes: first.bytes });
      if (this.disposed) return;
      this.index = 0; this.currentId = first.r.id; this.initial = false;
      if (options.deferFirstFrame) this.notify({ type: 'prepared', ...handoffTimeline({ ...this.scene, frameCount: this.frameCount }, { ...options, source: this.sceneSource }) });
      else this.ready(0);
      this.schedule();
    } catch (error) { if (!this.disposed) { this.notify({ type: 'error' }); await this.dispose(); } }
  }
  localMessage(message) {
    if (message.type === 'error' && !this.disposed) { this.notify(message); void this.dispose(); }
  }
  ready(frame, presentationId) {
    this.notify({ type: 'ready', frame, frameRate: this.frameRate, frameCount: this.frameCount,
      attribution: this.scene.attribution, canPlay: true, playing: this.clock.playing,
      ...(presentationId === undefined ? {} : { presentationId }) });
  }
  prefetch() {
    const next = this.index + 1;
    if (this.paused || this.disposed || this.pending || next >= this.manifest.representations[0].segments.length) return;
    const controller = new AbortController();
    this.pending = { index: next, controller, promise: this.acquire(next,
      Math.max(0, (next * this.scene.frameCount / this.frameRate) - this.clock.value()), controller.signal) };
    // Keep rejection handled while this one bounded candidate waits for use.
    this.pending.promise.catch(() => {});
  }
  async show(frame, playing, presentationId, automatic = false) {
    if (!this.temporal || this.disposed || !Number.isInteger(frame)) return;
    frame = Math.max(0, Math.min(this.frameCount - 1, Math.floor(frame)));
    const epoch = ++this.epoch, index = Math.floor(frame / this.scene.frameCount), local = frame % this.scene.frameCount;
    const changing = index !== this.index, holdClock = changing || !automatic;
    this.temporal.cancelReplacement(); this.switchAbort?.abort();
    const controller = this.switchAbort = new AbortController();
    if (holdClock) { this.clock.seek(frame / this.frameRate); this.clock.suspend(true); }
    this.switching = true;
    if (holdClock) this.notify({ type: 'loading', frame });
    try {
      if (index !== this.index) {
        let asset;
        if (this.pending?.index === index) {
          const pending = this.pending; asset = await pending.promise;
          if (this.pending === pending) this.pending = null;
        }
        else { this.pending?.controller.abort(); this.pending = null; asset = await this.acquire(index, 0, controller.signal); }
        if (this.disposed || epoch !== this.epoch || controller.signal.aborted || this.paused) return;
        this.temporal.userPaused = true;
        if (!await this.temporal.replace(asset.scene, { sha256: asset.segment.sha256, bytes: asset.bytes })) throw Error('Transport model replacement failed');
        if (this.disposed || epoch !== this.epoch || controller.signal.aborted || this.paused) return;
        this.index = index; this.currentId = asset.r.id;
      }
      await this.temporal.seek(local);
      if (this.disposed || epoch !== this.epoch || controller.signal.aborted || this.paused) return;
      if (holdClock) this.clock.seek(frame / this.frameRate);
      if (!automatic) { this.temporal.viewer?.updateSplatMesh(); this.temporal.viewer?.render(); }
      if (typeof playing === 'boolean') this.clock.playing = playing;
      this.position = frame / this.frameRate; this.lastFrame = frame;
      if (!automatic || changing) {
        this.notifyPlayback();
        this.ready(frame, presentationId);
      }
    } catch (error) {
      if (!this.disposed && epoch === this.epoch && !this.paused && !controller.signal.aborted) { this.notify({ type: 'error' }); await this.dispose(); }
    } finally {
      if (epoch === this.epoch) { this.switching = false; if (holdClock) this.clock.suspend(this.paused); this.prefetch(); }
    }
  }
  notifyPlayback() {
    const positionSeconds = this.position, playing = this.clock.playing, playIntent = this.clock.playing;
    const previous = this.lastPlayback;
    if (previous && previous.positionSeconds === positionSeconds &&
        previous.playing === playing && previous.playIntent === playIntent) return;
    // Exact bridge-state equality only: do not round positions, throttle time,
    // or suppress loading/ready acknowledgements for explicit presentations.
    this.lastPlayback = { positionSeconds, playing, playIntent };
    this.notify({ type: 'playback', ...this.lastPlayback });
  }
  schedule() {
    if (this.disposed) return;
    this.raf = this.requestFrame(() => {
      if (!this.paused && !this.switching) {
        const position = this.clock.value(), frame = Math.floor(position * this.frameRate + 1e-7);
        if (frame !== this.lastFrame) void this.show(frame, undefined, undefined, true);
        this.notifyPlayback();
        if (this.clock.playing) this.prefetch();
      }
      this.schedule();
    });
  }
  seek(frame) { return this.show(frame); }
  present(frame, playing, presentationId) { return this.show(frame, playing, presentationId); }
  togglePlayback() { if (this.clock) { this.clock.toggle(); if (this.clock.playing) this.prefetch(); } }
  pause(paused) { this.paused = paused; this.clock?.suspend(paused); this.temporal?.pause(paused);
    if (paused) { this.pending?.controller.abort(); this.pending = null; this.switchAbort?.abort(); this.temporal?.cancelReplacement(); } }
  cancelReplacement() {
    this.epoch++; this.switchAbort?.abort(); this.pending?.controller.abort(); this.pending = null;
    this.temporal?.cancelReplacement();
    if (this.switching) { this.clock?.seek(this.position); this.clock?.suspend(this.paused); }
    this.switching = false;
  }
  dispose() { if (this.release) return this.release; this.disposed = true; this.epoch++; this.abort.abort();
    this.switchAbort?.abort(); this.pending?.controller.abort(); this.pending = null; this.cancelFrame(this.raf);
    return this.release = Promise.resolve(this.temporal?.dispose()); }
}
