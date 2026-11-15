import { hashBlob, type HashDigest } from './hash';
export type HashRequest = { id: number; blob: Blob };
export type HashResponse = { id: number } & ({ progress: number } | { digest: HashDigest } | { error: string });
const scope = self as unknown as { onmessage: ((event: MessageEvent<HashRequest>) => void) | null; postMessage: (message: HashResponse) => void };
scope.onmessage = (event) => {
  const { id, blob } = event.data;
  let lastProgress = 0;
  void hashBlob(blob, { progress: (bytes) => {
    const now = performance.now();
    if (bytes === blob.size || now - lastProgress >= 50) {
      lastProgress = now;
      scope.postMessage({ id, progress: bytes });
    }
  } }).then((digest) => scope.postMessage({ id, digest }), (error: unknown) => scope.postMessage({ id, error: error instanceof Error ? error.message : 'Could not read file' }));
};
