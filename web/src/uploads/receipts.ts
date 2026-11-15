import type { UploadReceipts } from './types';
export const RECEIPT_KEY = 'tag-files.upload-receipts.v1';
export interface ReceiptStorage { getItem(key: string): string | null; setItem(key: string, value: string): void }
const uuid = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
const positive = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) > 0;
const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const digest = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{64}$/.test(value);
function parse(value: unknown): UploadReceipts {
  if (!object(value) || value.version !== 1) throw new Error('Upload receipt format version is unsupported. Existing receipts were preserved.');
  const invalid = () => { throw new Error('Invalid upload receipt. Existing receipts were preserved.'); };
  if (!positive(value.maxParallelTransfers) || !Array.isArray(value.identities) || !Array.isArray(value.commands)) return invalid();
  for (const identity of value.identities) {
    if (!object(identity) || !uuid(identity.id) || !uuid(identity.clientFileId) || !positive(identity.size) || !digest(identity.fingerprint)) return invalid();
  }
  const ids = new Set<string>();
  for (const command of value.commands) {
    if (!object(command) || !uuid(command.operationId) || !Number.isFinite(command.createdAt) || ids.has(command.operationId)) return invalid();
    ids.add(command.operationId);
    if (command.kind === 'create') {
      if (!object(command.body) || !Array.isArray(command.body.files) || command.body.files.length < 1 || command.body.files.length > 500) return invalid();
      if (command.body.collectionId !== undefined && command.body.collectionId !== null && !positive(command.body.collectionId)) return invalid();
      for (const file of command.body.files) {
        if (!object(file) || !uuid(file.clientFileId) || typeof file.filename !== 'string' || !file.filename.length || !positive(file.size) || !digest(file.fingerprint)) return invalid();
      }
    } else if (command.kind === 'complete') {
      if (!uuid(command.uploadId) || !object(command.body) || !positive(command.body.generation) || !Array.isArray(command.body.parts) || !command.body.parts.length || command.body.parts.length > 10000) return invalid();
      for (const [index, part] of command.body.parts.entries()) if (!object(part) || part.partNumber !== index + 1 || typeof part.etag !== 'string' || !part.etag.length) return invalid();
    } else if (command.kind === 'retry') {
      if (!uuid(command.uploadId) || !uuid(command.jobId)) return invalid();
    } else if (command.kind === 'cancel') {
      if (!uuid(command.uploadId)) return invalid();
    } else return invalid();
  }
  return value as unknown as UploadReceipts;
}

export class ReceiptStore {
  constructor(private readonly storage: ReceiptStorage) {}
  load(): UploadReceipts {
    const stored = this.storage.getItem(RECEIPT_KEY);
    if (stored === null) return { version: 1, commands: [], identities: [], maxParallelTransfers: 1 };
    let value: unknown;
    try { value = JSON.parse(stored); } catch { throw new Error('Invalid upload receipts. Existing bytes were preserved.'); }
    return parse(value);
  }
  save(value: UploadReceipts): void {
    parse(value);
    // Receipts hold JSON command/identity metadata only. File/Blob handles stay in memory.
    this.storage.setItem(RECEIPT_KEY, JSON.stringify(value));
  }
}
