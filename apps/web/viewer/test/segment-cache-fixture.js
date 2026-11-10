/** Storage returns independent bodies, like Cache API, rather than tee clones. */
export class MemoryStorage {
  constructor() { this.entries = new Map(); this.opens = 0; this.failOpen = false; this.failPut = false; }
  async open() {
    this.opens++;
    if (this.failOpen) throw new DOMException('Storage denied', 'SecurityError');
    return this;
  }
  key(request) { return typeof request === 'string' ? request : request.url; }
  async keys() { return [...this.entries.keys()].map(url => new Request(url)); }
  async match(request) {
    const stored = this.entries.get(this.key(request));
    return stored ? new Response(stored.bytes, { headers: stored.headers, status: stored.status ?? 200 }) : undefined;
  }
  async delete(request) { return this.entries.delete(this.key(request)); }
  async put(request, response) {
    if (this.failPut) throw new DOMException('Full', 'QuotaExceededError');
    this.entries.set(this.key(request), { bytes: new Uint8Array(await response.arrayBuffer()), headers: new Headers(response.headers) });
  }
}
export class MemoryLocks {
  constructor() { this.tail = Promise.resolve(); }
  request(name, options, action) {
    const work = this.tail.catch(() => {}).then(() => { options.signal?.throwIfAborted(); return action(); });
    this.tail = work.catch(() => {}); return work;
  }
}
