// @vitest-environment node
import { createHash } from 'node:crypto';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { UploadCoordinator, type UploadAPI, type PartSender } from './coordinator';
import { hashBlob } from './hash';
import { RECEIPT_KEY } from './receipts';
import type { UploadSession, UploadOutcome, UploadsCreated, UploadPart, CompleteUpload, UploadBatch, Operation } from './types';

const uploadId = '00000000-0000-4000-8000-000000000001';
const batchId = '00000000-0000-4000-8000-000000000002';
const clientId = '00000000-0000-4000-8000-000000000003';
const text = 'abcdefghijkl';
const fingerprint = createHash('sha256').update(text).digest('hex');
const file = () => new File([text], 'same.jpg', { type: 'image/jpeg' });
const partDigest = (data: string) => createHash('sha256').update(data).digest('base64');
const session = (overrides: Partial<UploadSession> = {}): UploadSession => ({ id: uploadId, batchId, clientFileId: clientId, filename: 'same.jpg', size: 12, generation: 1, partSize: 4, partCount: 3, fingerprint, state: 'receiving', parts: [], ...overrides });
const outcome = (value: UploadSession): UploadOutcome => ({ uploadId: value.id, state: value.state === 'failed' ? 'failed' : 'pending', session: value });
class MemoryStorage {
  data = new Map<string, string>();
  getItem(key: string) { return this.data.get(key) ?? null; }
  setItem(key: string, value: string) { this.data.set(key, value); }
}
class Server implements UploadAPI {
  sessions: UploadSession[] = [];
  creates: { key: string; body: unknown }[] = [];
  completions: { key: string; body: CompleteUpload }[] = [];
  receipts = new Map<string, Operation>();
  maxParallelTransfers = 2;
  ready = false;
  async list(): Promise<UploadBatch[]> { return this.sessions.length ? [{ id: batchId, sessions: structuredClone(this.sessions) }] : []; }
  async get(id: string): Promise<UploadOutcome> {
    if (this.ready) return { uploadId: id, state: 'ready', itemId: 42 };
    const found = this.sessions.find((value) => value.id === id);
    if (!found) throw Object.assign(new Error('missing'), { status: 404 });
    return outcome(structuredClone(found));
  }
  async create(body: Parameters<UploadAPI['create']>[0], key: string): Promise<UploadsCreated> {
    this.creates.push({ key, body });
    const result = { operationId: key, batchId, maxParallelTransfers: this.maxParallelTransfers, maxFileSize: 21474836480, sessions: body.files.map((value, index) => session({ id: index ? `00000000-0000-4000-8000-${String(index + 5).padStart(12, '0')}` : uploadId, clientFileId: value.clientFileId, filename: value.filename, size: value.size, ...(value.fingerprint === undefined ? {} : { fingerprint: value.fingerprint }), ...(body.collectionId === undefined ? {} : { collectionId: body.collectionId }) })) };
    if (!this.receipts.has(key)) this.sessions = structuredClone(result.sessions);
    this.remember(key, result, 201);
    return result;
  }
  async complete(id: string, body: CompleteUpload, key: string): Promise<UploadOutcome> {
    this.completions.push({ key, body: structuredClone(body) });
    const found = this.sessions.find((value) => value.id === id)!;
    found.state = 'queued';
    const result = outcome(structuredClone(found));
    this.remember(key, result, 202);
    return result;
  }
  async cancel(id: string): Promise<void> { this.sessions = this.sessions.filter((value) => value.id !== id); }
  async retry() { return { jobId: '00000000-0000-4000-8000-000000000009' }; }
  async operation(id: string): Promise<Operation> {
    const result = this.receipts.get(id);
    if (!result) throw Object.assign(new Error('missing'), { status: 404 });
    return structuredClone(result);
  }
  remember(key: string, result: object, httpStatus: number) {
    this.receipts.set(key, { operationId: key, state: 'completed', result: result as Record<string, unknown>, httpStatus, createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 7 * 86400000).toISOString() });
  }
  put: PartSender = async (binding, blob, signal, progress) => {
    signal.throwIfAborted();
    const bytes = Buffer.from(await blob.arrayBuffer());
    expect(createHash('sha256').update(bytes).digest('base64')).toBe(binding.sha256);
    const found = this.sessions.find((value) => value.id === binding.sessionId)!;
    expect(found.generation).toBe(binding.generation);
    const part: UploadPart = { partNumber: binding.partNumber, size: blob.size, sha256: binding.sha256, state: 'confirmed', etag: `opaque-${binding.partNumber}` };
    found.parts = found.parts.filter((value) => value.partNumber !== part.partNumber).concat(part);
    progress(blob.size);
    return structuredClone(part);
  };
}
function setup(server = new Server(), storage = new MemoryStorage(), sender = server.put) {
  let id = 100;
  const coordinator = new UploadCoordinator({ api: server, storage, sendPart: sender, hash: (blob, signal, progress) => hashBlob(blob, { signal, progress }), uuid: () => `00000000-0000-4000-8000-${String(id++).padStart(12, '0')}`, pollInterval: 1000 });
  return { server, storage, coordinator };
}
async function settle(predicate: () => boolean) {
  for (let i = 0; i < 100 && !predicate(); i++) await new Promise((resolve) => setTimeout(resolve, 2));
  expect(predicate()).toBe(true);
}
afterEach(() => vi.useRealTimers());

describe('browser upload coordinator', () => {
  it('does not let an older empty listing erase a concurrently accepted upload or abort its bytes', async () => {
    const server = new Server();
    let releaseList: (value: UploadBatch[]) => void = () => { throw new Error('listing did not start'); };
    let aborted = false;
    const sender: PartSender = (_binding, _blob, signal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => { aborted = true; reject(new DOMException('Aborted', 'AbortError')); });
    });
    const { coordinator } = setup(server, new MemoryStorage(), sender);
    await coordinator.start();
    const original = server.list.bind(server);
    server.list = () => new Promise((resolve) => { releaseList = resolve; });
    const oldList = coordinator.refresh();
    await coordinator.add([file()], null);
    await settle(() => coordinator.getSnapshot().rows[0]?.status === 'uploading');
    releaseList([]);
    await oldList;
    expect(aborted).toBe(false);
    expect(coordinator.getSnapshot().rows).toHaveLength(1);
    expect(coordinator.getSnapshot().rows[0]!.status).toBe('uploading');
    server.list = original;
    await coordinator.refresh();
    expect(coordinator.getSnapshot().rows[0]!.status).toBe('uploading');
    await coordinator.cancel(uploadId);
  });

  it('does not downgrade an accepted Complete when an older receiving listing arrives', async () => {
    const server = new Server();
    server.sessions = [session()];
    const { coordinator } = setup(server);
    await coordinator.start();
    const oldListing = await server.list();
    const original = server.list.bind(server);
    let releaseList: (value: UploadBatch[]) => void = () => { throw new Error('listing did not start'); };
    server.list = () => new Promise((resolve) => { releaseList = resolve; });
    const refreshing = coordinator.refresh();
    await coordinator.reselect(uploadId, file());
    await settle(() => coordinator.getSnapshot().rows[0]?.session.state === 'queued');
    releaseList(oldListing);
    await refreshing;
    expect(coordinator.getSnapshot().rows[0]!.session.state).toBe('queued');
    expect(server.completions).toHaveLength(1);
    server.list = original;
    await coordinator.cancel(uploadId);
  });

  it('cancels fingerprint preparation before creating any remote session', async () => {
    const server = new Server();
    const coordinator = new UploadCoordinator({ api: server, storage: new MemoryStorage(), sendPart: server.put, hash: (_blob, signal) => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))) });
    await coordinator.start();
    const pending = coordinator.add([file()], null);
    const preparing = coordinator.getSnapshot().preparing[0]!;
    coordinator.cancelPreparation(preparing.id);
    await expect(pending).resolves.toBeUndefined();
    expect(server.creates).toHaveLength(0);
    expect(coordinator.getSnapshot().preparing).toHaveLength(0);
  });

  it('resolves a saved Complete after reopening without a File handle or another Complete', async () => {
    const server = new Server();
    const storage = new MemoryStorage();
    const operationId = '00000000-0000-4000-8000-000000000099';
    storage.setItem(RECEIPT_KEY, JSON.stringify({ version: 1, maxParallelTransfers: 2, identities: [{ id: uploadId, clientFileId: clientId, size: 12, fingerprint }], commands: [{ kind: 'complete', operationId, createdAt: Date.now(), uploadId, body: { generation: 1, parts: [{ partNumber: 1, etag: 'old-opaque-etag' }] } }] }));
    server.remember(operationId, { uploadId, state: 'ready', itemId: 42 }, 200);
    const { coordinator } = setup(server, storage);
    await coordinator.start();
    expect(coordinator.getSnapshot().rows).toHaveLength(0);
    expect(coordinator.getSnapshot().uncertainOperations).toBe(0);
    expect(server.completions).toHaveLength(0);
    expect(JSON.parse(storage.getItem(RECEIPT_KEY)!).identities).toHaveLength(0);
  });

  it('retains an expired unknown Create receipt without silently creating another import', async () => {
    const server = new Server();
    const storage = new MemoryStorage();
    storage.setItem(RECEIPT_KEY, JSON.stringify({ version: 1, maxParallelTransfers: 1, identities: [], commands: [{ kind: 'create', operationId: '00000000-0000-4000-8000-000000000099', createdAt: Date.now() - 8 * 86400000, body: { collectionId: null, files: [{ clientFileId: clientId, filename: 'same.jpg', size: 12, fingerprint }] } }] }));
    const { coordinator } = setup(server, storage);
    await coordinator.start();
    expect(coordinator.getSnapshot().error).toMatch(/retention|review/i);
    expect(coordinator.getSnapshot().uncertainOperations).toBe(1);
    expect(server.creates).toHaveLength(0);
  });

  it('refuses any network write when a recovery receipt cannot be stored', async () => {
    const server = new Server();
    const coordinator = new UploadCoordinator({ api: server, storage: { getItem: () => null, setItem: () => { throw new Error('storage full'); } }, sendPart: server.put, hash: (blob, signal) => hashBlob(blob, { signal }) });
    await coordinator.start();
    await expect(coordinator.add([file()], null)).rejects.toThrow(/save|storage/i);
    expect(server.creates).toHaveLength(0);
  });

  it('replays a request lost before commit with the exact same UUID and body', async () => {
    const server = new Server();
    const original = server.create.bind(server);
    const attempts: { key: string; body: unknown }[] = [];
    server.create = async (body, key) => {
      attempts.push({ key, body: structuredClone(body) });
      if (attempts.length === 1) throw new TypeError('lost before commit');
      return original(body, key);
    };
    const { coordinator } = setup(server);
    await coordinator.start(); await coordinator.add([file()], 12);
    await settle(() => server.completions.length === 1);
    expect(attempts).toHaveLength(2);
    expect(attempts[1]).toEqual(attempts[0]);
    await coordinator.cancel(uploadId);
  });

  it('fingerprints before creation, assigns collection, sends bound parts and freezes an ordered Complete manifest', async () => {
    const { coordinator, server, storage } = setup();
    await coordinator.start();
    await coordinator.add([file()], 71);
    await settle(() => server.completions.length === 1);
    expect(server.creates[0]!.body).toMatchObject({ collectionId: 71, files: [{ fingerprint, filename: 'same.jpg', size: 12 }] });
    expect(server.completions[0]!.body).toEqual({ generation: 1, parts: [{ partNumber: 1, etag: 'opaque-1' }, { partNumber: 2, etag: 'opaque-2' }, { partNumber: 3, etag: 'opaque-3' }] });
    const persisted = JSON.stringify([...storage.data.values()]);
    expect(persisted).not.toContain('lastModified');
    expect(persisted).not.toContain('abcdefghijkl');
    await coordinator.cancel(uploadId);
  });

  it('does not reuse parts when reselected bytes differ despite identical name and size', async () => {
    const { coordinator, server } = setup();
    server.sessions = [session({ parts: [{ partNumber: 1, size: 4, sha256: partDigest('abcd'), state: 'confirmed', etag: 'original' }] })];
    await coordinator.start();
    await expect(coordinator.reselect(uploadId, new File(['XXXXXXXXXXXX'], 'same.jpg'))).rejects.toThrow(/match|different/i);
    expect(server.sessions[0]!.parts[0]!.etag).toBe('original');
    expect(server.completions).toHaveLength(0);
    expect(coordinator.getSnapshot().rows[0]!.status).toBe('error');
  });

  it('requires a proven original fingerprint even if an old session has matching filename and size', async () => {
    const { coordinator, server } = setup();
    const missingFingerprint = session();
    delete missingFingerprint.fingerprint;
    server.sessions = [missingFingerprint];
    await coordinator.start();
    await expect(coordinator.reselect(uploadId, file())).rejects.toThrow(/fingerprint|identity/i);
    expect(server.completions).toHaveLength(0);
  });

  it('resumes matching full content and skips only confirmed parts with the same digest and size', async () => {
    const { coordinator, server } = setup();
    server.sessions = [session({ parts: [{ partNumber: 1, size: 4, sha256: partDigest('abcd'), state: 'confirmed', etag: 'previous-etag' }] })];
    await coordinator.start();
    await coordinator.reselect(uploadId, file());
    await settle(() => server.completions.length === 1);
    expect(server.completions[0]!.body.parts[0]).toEqual({ partNumber: 1, etag: 'previous-etag' });
    await coordinator.cancel(uploadId);
  });

  it('resolves a lost Create response by its durable operation UUID without creating a second import', async () => {
    const server = new Server();
    const original = server.create.bind(server);
    server.create = async (body, key) => { await original(body, key); throw new TypeError('connection lost'); };
    const { coordinator } = setup(server);
    await coordinator.start();
    await coordinator.add([file()], null);
    await settle(() => server.completions.length === 1);
    expect(server.creates).toHaveLength(1);
    expect(coordinator.getSnapshot().rows).toHaveLength(1);
    await coordinator.cancel(uploadId);
  });

  it('resolves lost Complete response with the original UUID and immutable manifest', async () => {
    const server = new Server();
    const original = server.complete.bind(server);
    server.complete = async (id, body, key) => { await original(id, body, key); throw new TypeError('connection lost'); };
    const { coordinator } = setup(server);
    await coordinator.start();
    await coordinator.add([file()], null);
    await settle(() => coordinator.getSnapshot().rows[0]?.session.state === 'queued');
    expect(server.completions).toHaveLength(1);
    await coordinator.cancel(uploadId);
  });

  it('reconciles lost part acknowledgement before any resend', async () => {
    const server = new Server();
    let lost = false;
    const sends: number[] = [];
    const sender: PartSender = async (...args) => {
      sends.push(args[0].partNumber);
      const result = await server.put(...args);
      if (!lost) { lost = true; throw new TypeError('connection lost'); }
      return result;
    };
    const { coordinator } = setup(server, new MemoryStorage(), sender);
    await coordinator.start();
    await coordinator.add([file()], null);
    await settle(() => server.completions.length === 1);
    expect(sends).toEqual([1, 2, 3]);
    await coordinator.cancel(uploadId);
  });

  it('never sends different bytes over a server part binding', async () => {
    const { coordinator, server } = setup();
    server.sessions = [session({ parts: [{ partNumber: 1, size: 4, sha256: partDigest('WRNG'), state: 'bound' }] })];
    await coordinator.start();
    await coordinator.reselect(uploadId, file());
    await settle(() => coordinator.getSnapshot().rows[0]?.status === 'error');
    expect(server.sessions[0]!.parts).toHaveLength(1);
    expect(server.completions).toHaveLength(0);
  });

  it('cancels in-flight bytes, removes the row and never sends Complete after a late response', async () => {
    let aborted = false;
    const sender: PartSender = (_binding, _blob, signal) => new Promise((_resolve, reject) => signal.addEventListener('abort', () => { aborted = true; reject(new DOMException('Aborted', 'AbortError')); }));
    const { coordinator, server } = setup(new Server(), new MemoryStorage(), sender);
    await coordinator.start();
    await coordinator.add([file()], null);
    await settle(() => coordinator.getSnapshot().rows[0]?.status === 'uploading');
    await coordinator.cancel(uploadId);
    expect(aborted).toBe(true);
    expect(coordinator.getSnapshot().rows).toHaveLength(0);
    expect(server.completions).toHaveLength(0);
  });

  it('uses the server concurrency limit across files', async () => {
    const server = new Server();
    server.maxParallelTransfers = 1;
    let active = 0;
    let peak = 0;
    const sender: PartSender = async (...args) => {
      active++; peak = Math.max(peak, active);
      await new Promise((resolve) => setTimeout(resolve, 4));
      try { return await server.put(...args); } finally { active--; }
    };
    const { coordinator } = setup(server, new MemoryStorage(), sender);
    await coordinator.start();
    await coordinator.add([file(), file(), file()], null);
    await settle(() => server.completions.length === 3);
    expect(peak).toBe(1);
    for (const value of [...server.sessions]) await coordinator.cancel(value.id);
  });

  it('keeps polling unrelated processing work after a rejected new batch', async () => {
    vi.useFakeTimers();
    const { coordinator, server } = setup();
    server.sessions = [session({ state: 'processing' })];
    let reads = 0;
    const original = server.list.bind(server);
    server.list = async () => { reads++; return original(); };
    server.create = async () => { throw Object.assign(new Error('unsupported input'), { status: 422 }); };
    await coordinator.start();
    await expect(coordinator.add([file()], null)).rejects.toThrow('unsupported input');
    await vi.advanceTimersByTimeAsync(1000);
    expect(reads).toBe(2);
  });

  it('invalidates the library when publication removes a session from the current queue', async () => {
    const server = new Server();
    server.sessions = [session({ state: 'processing' })];
    let changes = 0;
    const coordinator = new UploadCoordinator({ api: server, storage: new MemoryStorage(), hash: (blob, signal) => hashBlob(blob, { signal }), sendPart: server.put, onLibraryChange: () => { changes++; } });
    await coordinator.start();
    server.sessions = [];
    await coordinator.refresh();
    expect(changes).toBe(1);
    expect(coordinator.getSnapshot().rows).toHaveLength(0);
  });

  it('stops polling for idle receiving and failed-only queues, restarts for processing, and drops ready rows', async () => {
    vi.useFakeTimers();
    const { coordinator, server } = setup();
    server.sessions = [session(), session({ id: '00000000-0000-4000-8000-000000000004', state: 'failed' })];
    let reads = 0;
    const original = server.list.bind(server);
    server.list = async () => { reads++; return original(); };
    await coordinator.start();
    await vi.advanceTimersByTimeAsync(10000);
    expect(reads).toBe(1);
    server.sessions[0]!.state = 'processing';
    await coordinator.refresh();
    await vi.advanceTimersByTimeAsync(1000);
    expect(reads).toBe(3);
    server.sessions = [];
    await vi.advanceTimersByTimeAsync(1000);
    expect(coordinator.getSnapshot().rows).toHaveLength(0);
    const finished = reads;
    await vi.advanceTimersByTimeAsync(10000);
    expect(reads).toBe(finished);
  });
});
