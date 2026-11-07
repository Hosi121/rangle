import { SplatBuffer } from '@mkkellogg/gaussian-splats-3d';

/** One reusable worker, one request, and no retained frame cache. */
export class FrameLoader {
  constructor(createWorker = () => new Worker(new URL('./frame-worker.js', import.meta.url), { type: 'module' })) {
    this.createWorker = createWorker;
    this.worker = null;
    this.pending = null;
    this.sequence = 0;
    this.disposed = false;
  }

  load(url) {
    if (this.disposed) return Promise.reject(new Error('Frame loader disposed'));
    this.cancel();
    if (!this.worker) {
      const worker = this.createWorker();
      this.worker = worker;
      worker.addEventListener('message', ({ data }) => {
        const job = this.pending;
        if (this.worker !== worker || !job || data.id !== job.id) return;
        this.pending = null;
        try {
          if (data.error || !(data.buffer instanceof ArrayBuffer) ||
              data.buffer.byteLength === 0 || data.buffer.byteLength > 64 * 1024 * 1024) {
            throw new Error('Invalid prepared frame');
          }
          job.resolve(new SplatBuffer(data.buffer));
        } catch (error) {
          job.reject(error);
        }
      });
      worker.addEventListener('error', () => {
        if (this.worker !== worker) return;
        const job = this.pending;
        this.pending = null;
        this.worker?.terminate();
        this.worker = null;
        job?.reject(new Error('Frame worker failed'));
      });
    }
    return new Promise((resolve, reject) => {
      const id = ++this.sequence;
      this.pending = { id, resolve, reject };
      this.worker.postMessage({ id, url });
    });
  }

  cancel() {
    const job = this.pending;
    if (!job) return;
    this.pending = null;
    this.worker?.terminate();
    this.worker = null;
    job.reject(new DOMException('Frame superseded', 'AbortError'));
  }

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.cancel();
    this.worker?.terminate();
    this.worker = null;
  }
}
