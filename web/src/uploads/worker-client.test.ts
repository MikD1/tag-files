import { describe, expect, it } from 'vitest';
import { createWorkerHasher } from './worker-client';
import type { HashRequest } from './hash.worker';
class FakeWorker extends EventTarget {
  messages: HashRequest[] = [];
  terminated = false;
  postMessage(message: HashRequest) { this.messages.push(message); }
  terminate() { this.terminated = true; }
  complete(id: number) { this.dispatchEvent(new MessageEvent('message', { data: { id, digest: { hex: 'computed-hex', base64: 'computed-base64' } } })); }
}
describe('hash worker lifetime', () => {
  it('serializes worker reads, immediately cancels queued work and ignores its eventual slot', async () => {
    const worker = new FakeWorker();
    const hash = createWorkerHasher(() => worker as unknown as Worker);
    const first = hash(new Blob(['first']), new AbortController().signal);
    const abort = new AbortController();
    const second = hash(new Blob(['second']), abort.signal);
    await Promise.resolve();
    expect(worker.messages).toHaveLength(1);
    abort.abort();
    await expect(second).rejects.toMatchObject({ name: 'AbortError' });
    expect(worker.terminated).toBe(false);
    worker.complete(worker.messages[0]!.id);
    expect(await first).toEqual({ hex: 'computed-hex', base64: 'computed-base64' });
    const third = hash(new Blob(['third']), new AbortController().signal);
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(worker.messages).toHaveLength(2);
    worker.complete(worker.messages[1]!.id);
    await third;
  });
  it('terminates a cancelled active worker and creates a fresh worker for later input', async () => {
    const workers: FakeWorker[] = [];
    const hash = createWorkerHasher(() => { const worker = new FakeWorker(); workers.push(worker); return worker as unknown as Worker; });
    const abort = new AbortController();
    const first = hash(new Blob(['first']), abort.signal);
    await Promise.resolve();
    abort.abort();
    await expect(first).rejects.toMatchObject({ name: 'AbortError' });
    expect(workers[0]!.terminated).toBe(true);
    const next = hash(new Blob(['next']), new AbortController().signal);
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(workers).toHaveLength(2);
    workers[1]!.complete(workers[1]!.messages[0]!.id);
    await expect(next).resolves.toEqual({ hex: 'computed-hex', base64: 'computed-base64' });
  });
});
