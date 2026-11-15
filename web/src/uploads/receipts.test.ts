// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { ReceiptStore, RECEIPT_KEY } from './receipts';

describe('durable upload receipts', () => {
  it('refuses a future format without overwriting its bytes', () => {
    const original = JSON.stringify({ version: 2, commands: ['future-input'] });
    let value = original;
    const storage = { getItem: () => value, setItem: (_key: string, next: string) => { value = next; } };
    expect(() => new ReceiptStore(storage).load()).toThrow(/version|format/i);
    expect(value).toBe(original);
  });
  it('preserves an immutable operation body through reopen', () => {
    const memory = new Map<string, string>();
    const storage = { getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, value: string) => { memory.set(key, value); } };
    const store = new ReceiptStore(storage);
    const receipts = store.load();
    receipts.commands.push({ kind: 'complete', operationId: '00000000-0000-4000-8000-000000000001', createdAt: 12345, uploadId: '00000000-0000-4000-8000-000000000002', body: { generation: 3, parts: [{ partNumber: 1, etag: 'opaque-value' }] } });
    store.save(receipts);
    expect(memory.has(RECEIPT_KEY)).toBe(true);
    expect(new ReceiptStore(storage).load().commands).toEqual(receipts.commands);
  });
  it('refuses corrupt bindings and does not clear them', () => {
    const bytes = JSON.stringify({ version: 1, commands: [], identities: [{ id: 'wrong', clientFileId: 'wrong', size: 100, fingerprint: 'name-only' }], maxParallelTransfers: 2 });
    const storage = { getItem: () => bytes, setItem: () => { throw new Error('must not write'); } };
    expect(() => new ReceiptStore(storage).load()).toThrow(/receipt|invalid/i);
  });
});
