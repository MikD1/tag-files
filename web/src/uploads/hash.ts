import { sha256 } from '@noble/hashes/sha2.js';

/** Memory bound applies to the entire fingerprint as well as individual parts. */
export const HASH_SLICE_BYTES = 1024 * 1024;
export interface HashDigest { hex: string; base64: string }
export interface HashOptions {
  signal?: AbortSignal | undefined;
  progress?: ((bytes: number) => void) | undefined;
  sliceBytes?: number;
}

export async function hashBlob(blob: Pick<Blob, 'size' | 'slice'>, options: HashOptions = {}): Promise<HashDigest> {
  const sliceBytes = options.sliceBytes ?? HASH_SLICE_BYTES;
  if (!Number.isSafeInteger(sliceBytes) || sliceBytes < 1 || sliceBytes > HASH_SLICE_BYTES) throw new Error('Invalid hash slice size');
  if (!Number.isSafeInteger(blob.size) || blob.size < 0) throw new Error('Invalid source size');
  const hash = sha256.create();
  try {
    for (let offset = 0; offset < blob.size; offset += sliceBytes) {
      options.signal?.throwIfAborted();
      const end = Math.min(offset + sliceBytes, blob.size);
      const bytes = await blob.slice(offset, end).arrayBuffer();
      options.signal?.throwIfAborted();
      if (bytes.byteLength !== end - offset) throw new Error('Source changed while reading');
      hash.update(new Uint8Array(bytes));
      options.progress?.(end);
    }
    options.signal?.throwIfAborted();
    const bytes = hash.digest();
    const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
    const base64 = btoa(String.fromCharCode(...bytes));
    return { hex, base64 };
  } finally {
    hash.destroy();
  }
}
