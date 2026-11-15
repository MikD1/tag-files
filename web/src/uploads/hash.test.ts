// @vitest-environment node
import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import { hashBlob, HASH_SLICE_BYTES } from './hash';

describe('bounded incremental file identity', () => {
  it.each([0, 1, 55, 56, 63, 64, 65, 127, 128, 129, 1048575, 1048576, 1048577])('matches independent SHA-256 across %i bytes and SHA final blocks', async (size) => {
    const bytes = Uint8Array.from({ length: size }, (_, i) => (i * 131 + 17) % 256);
    const expected = createHash('sha256').update(bytes).digest();
    const result = await hashBlob(new Blob([bytes]), { sliceBytes: 57 });
    expect(result.hex).toBe(expected.toString('hex'));
    expect(result.base64).toBe(expected.toString('base64'));
  });

  it('only reads bounded slices, never the complete source', async () => {
    const bytes = new Uint8Array(HASH_SLICE_BYTES * 2 + 13).fill(93);
    const source = new Blob([bytes]);
    const observed: number[] = [];
    const guarded = {
      size: source.size,
      arrayBuffer: () => { throw new Error('whole source was read'); },
      slice: (start = 0, end = source.size) => {
        observed.push(end - start);
        return source.slice(start, end);
      },
    };
    const progress: number[] = [];
    const result = await hashBlob(guarded, { progress: (value) => progress.push(value) });
    expect(result.hex).toBe(createHash('sha256').update(bytes).digest('hex'));
    expect(Math.max(...observed)).toBeLessThanOrEqual(HASH_SLICE_BYTES);
    expect(observed).toHaveLength(3);
    expect(progress.at(-1)).toBe(bytes.length);
  });

  it('cancellation stops before reading another slice', async () => {
    const abort = new AbortController();
    let slices = 0;
    const source = new Blob([new Uint8Array(4096)]);
    const guarded = { size: source.size, slice: (start = 0, end = source.size) => {
      slices++;
      return source.slice(start, end);
    } };
    await expect(hashBlob(guarded, { sliceBytes: 64, signal: abort.signal, progress: () => abort.abort() })).rejects.toMatchObject({ name: 'AbortError' });
    expect(slices).toBe(1);
  });
});
