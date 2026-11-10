import test from 'node:test';
import assert from 'node:assert/strict';

test('hidden live workers defer downloads, abort in-flight I/O, and resume one request', async () => {
  const previousSelf = globalThis.self, previousFetch = globalThis.fetch;
  const worker = new EventTarget(), errors = [];
  worker.postMessage = message => errors.push(message);
  globalThis.self = worker;
  let requests = 0, aborted = 0;
  globalThis.fetch = (_url, { signal }) => new Promise((_resolve, reject) => {
    requests++;
    signal.addEventListener('abort', () => { aborted++; reject(new DOMException('Hidden', 'AbortError')); });
  });
  const emit = data => worker.dispatchEvent(Object.assign(new Event('message'), { data }));
  const flush = () => new Promise(resolve => setImmediate(resolve));
  try {
    await import('../src/temporal-worker.js?live-visibility-test');
    emit({ type: 'load', strict: true, hidden: true, url: 'https://example.test/model.rgs' });
    assert.equal(requests, 0);
    emit({ type: 'visibility', hidden: false });
    assert.equal(requests, 1);
    emit({ type: 'visibility', hidden: true });
    await flush();
    assert.equal(aborted, 1);
    assert.deepEqual(errors, []);
    emit({ type: 'visibility', hidden: false });
    assert.equal(requests, 2);
    emit({ type: 'visibility', hidden: true });
    await flush();
    assert.equal(aborted, 2);
    assert.deepEqual(errors, []);
  } finally { globalThis.self = previousSelf; globalThis.fetch = previousFetch; }
});

test('hiding after a response starts releases its reader and retries only after resume', async () => {
  const originalSelf = globalThis.self, originalFetch = globalThis.fetch;
  const worker = new EventTarget(), messages = [], bodies = [];
  worker.postMessage = message => messages.push(message);
  globalThis.self = worker;
  let requests = 0;
  globalThis.fetch = async (_url, { signal }) => {
    requests++;
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new Uint8Array(64));
        signal.addEventListener('abort', () => controller.error(new DOMException('Hidden', 'AbortError')), { once: true });
      },
    });
    bodies.push(stream);
    return new Response(stream, { headers: { 'Content-Length': '128' } });
  };
  const emit = data => worker.dispatchEvent(Object.assign(new Event('message'), { data }));
  const flush = () => new Promise(resolve => setImmediate(resolve));
  try {
    await import('../src/temporal-worker.js?partial-response-visibility');
    emit({ type: 'load', strict: true, url: 'https://example.test/model.rgs' });
    await flush();
    assert.equal(bodies[0].locked, true);
    emit({ type: 'visibility', hidden: true });
    await flush();
    assert.equal(bodies[0].locked, false);
    assert.deepEqual(messages, []);
    assert.equal(requests, 1);
    emit({ type: 'visibility', hidden: false });
    await flush();
    assert.equal(requests, 2);
    emit({ type: 'visibility', hidden: true });
    await flush();
    assert.equal(bodies[1].locked, false);
    assert.deepEqual(messages, []);
  } finally { globalThis.self = originalSelf; globalThis.fetch = originalFetch; }
});
