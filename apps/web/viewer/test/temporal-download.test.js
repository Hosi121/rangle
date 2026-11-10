import test from 'node:test';
import assert from 'node:assert/strict';
import { readTemporalBody } from '../src/temporal-download.js';
import { MAX_TEMPORAL_BYTES } from '../src/temporal-codec.js';

function response(chunks, length, { status = 200, cancel = () => {} } = {}) {
  const headers = new Headers();
  if (length !== undefined) headers.set('Content-Length', String(length));
  return new Response(new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(Uint8Array.from(chunk));
      controller.close();
    }, cancel,
  }), { headers, status });
}

test('known length writes successive chunks into one exact response buffer', async () => {
  const result = await readTemporalBody(response([[1, 2], [], [3], [4, 5]], 5));
  assert.deepEqual(result, new Uint8Array([1, 2, 3, 4, 5]));
  assert.equal(result.buffer.byteLength, 5);
});

test('missing or unusable length retains byte-exact bounded stream behavior', async () => {
  for (const length of [undefined, '', 'invalid', '-1', '2.5', '0']) {
    const result = await readTemporalBody(response([[1], [2, 3]], length));
    assert.deepEqual(result, new Uint8Array([1, 2, 3]), String(length));
    assert.equal(result.buffer.byteLength, 3);
  }
});

test('HTTP decoding can expand beyond the declared wire length without losing the prefix', async () => {
  for (const chunks of [[[1, 2], [3], [4, 5]], [[1, 2, 3, 4, 5]]]) {
    const result = await readTemporalBody(response(chunks, 2));
    assert.deepEqual(result, new Uint8Array([1, 2, 3, 4, 5]));
    assert.equal(result.buffer.byteLength, 5);
  }
});

test('short bodies return only received bytes, with no zero-filled suffix for hashing', async () => {
  const result = await readTemporalBody(response([[1, 2], [3]], 10));
  assert.deepEqual(result, new Uint8Array([1, 2, 3]));
  assert.equal(result.buffer.byteLength, 3);
});

test('the byte limit applies to actual streamed bytes with and without a length hint', async () => {
  for (const declared of [undefined, 1]) {
    let canceled = false;
    const stream = new ReadableStream({
      pull(controller) {
        controller.enqueue(new Uint8Array(MAX_TEMPORAL_BYTES));
        controller.enqueue(new Uint8Array([1]));
      },
      cancel() { canceled = true; },
    });
    const headers = new Headers();
    if (declared !== undefined) headers.set('Content-Length', String(declared));
    const value = new Response(stream, { headers });
    await assert.rejects(readTemporalBody(value), /exceeds limit/);
    assert.equal(canceled, true);
    assert.equal(stream.locked, false);
  }
});

test('rejected status or oversized declared body is canceled without reading', async () => {
  for (const headers of [new Headers({ 'Content-Length': String(MAX_TEMPORAL_BYTES + 1) }), new Headers()]) {
    let canceled = false;
    const stream = new ReadableStream({ cancel() { canceled = true; } });
    const value = new Response(stream, { headers, status: headers.has('Content-Length') ? 200 : 503 });
    await assert.rejects(readTemporalBody(value), /request failed/);
    assert.equal(canceled, true);
    assert.equal(stream.locked, false);
  }
});

test('a body read error releases the reader and preserves the original error', async () => {
  const expected = new DOMException('Stopped', 'AbortError');
  const stream = new ReadableStream({ start(controller) { controller.error(expected); } });
  await assert.rejects(readTemporalBody(new Response(stream)), error => error === expected);
  assert.equal(stream.locked, false);
});
