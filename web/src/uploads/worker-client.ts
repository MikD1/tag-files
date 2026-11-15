import type { HashDigest } from './hash';
import type { HashRequest, HashResponse } from './hash.worker';
export type FileHasher = (blob: Blob, signal: AbortSignal, progress?: (bytes: number) => void) => Promise<HashDigest>;

/** A single worker queue bounds hashing buffers even when transfers run concurrently. */
class HashWorkerQueue {
  private worker: Worker | undefined;
  private tail: Promise<unknown> = Promise.resolve();
  private sequence = 0;
  constructor(private readonly createWorker: () => Worker) {}
  hash: FileHasher = (blob, signal, progress) => {
    if (signal.aborted) return Promise.reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
    const result = this.tail.then(() => this.execute(blob, signal, progress));
    this.tail = result.catch(() => undefined);
    // A queued hash must settle immediately on cancel even if an unrelated,
    // much larger fingerprint currently owns the worker.
    return new Promise((resolve, reject) => {
      const abort = () => reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
      signal.addEventListener('abort', abort, { once: true });
      void result.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
    });
  };
  private execute(blob: Blob, signal: AbortSignal, progress?: (bytes: number) => void): Promise<HashDigest> {
    signal.throwIfAborted();
    const worker = this.worker ??= this.createWorker();
    const id = ++this.sequence;
    return new Promise((resolve, reject) => {
      const cleanup = () => {
        signal.removeEventListener('abort', abort);
        worker.removeEventListener('message', message);
        worker.removeEventListener('error', failure);
      };
      const abort = () => {
        cleanup();
        worker.terminate();
        this.worker = undefined;
        reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
      };
      const failure = (event: ErrorEvent) => {
        cleanup(); worker.terminate(); this.worker = undefined;
        reject(new Error(event.message || 'File hashing worker failed'));
      };
      const message = (event: MessageEvent<HashResponse>) => {
        if (event.data.id !== id) return;
        if ('progress' in event.data) { progress?.(event.data.progress); return; }
        cleanup();
        if ('error' in event.data) reject(new Error(event.data.error));
        else resolve(event.data.digest);
      };
      signal.addEventListener('abort', abort, { once: true });
      worker.addEventListener('message', message);
      worker.addEventListener('error', failure);
      const request: HashRequest = { id, blob };
      try { worker.postMessage(request); } catch (error) { cleanup(); worker.terminate(); this.worker = undefined; reject(error); }
    });
  }
}
export function createWorkerHasher(createWorker: () => Worker = () => new Worker(new URL('./hash.worker.ts', import.meta.url), { type: 'module' })): FileHasher {
  return new HashWorkerQueue(createWorker).hash;
}
export const hashInWorker: FileHasher = createWorkerHasher();
