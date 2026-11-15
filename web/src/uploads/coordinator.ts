import { ReceiptStore, type ReceiptStorage } from './receipts';
import type { FileHasher } from './worker-client';
import type { CompleteUpload, JobReceipt, NewUploads, Operation, UploadBatch, UploadCommand, UploadOutcome, UploadPart, UploadReceipts, UploadSession, UploadsCreated } from './types';

export interface UploadAPI {
  list(): Promise<UploadBatch[]>;
  create(body: NewUploads, operationId: string): Promise<UploadsCreated>;
  get(id: string): Promise<UploadOutcome>;
  complete(id: string, body: CompleteUpload, operationId: string): Promise<UploadOutcome>;
  cancel(id: string): Promise<void | UploadOutcome | JobReceipt>;
  retry(jobId: string, operationId: string): Promise<JobReceipt>;
  operation(id: string): Promise<Operation>;
}
export interface PartBinding { readonly sessionId: string; readonly generation: number; readonly partNumber: number; readonly size: number; readonly sha256: string }
export type PartSender = (binding: PartBinding, blob: Blob, signal: AbortSignal, progress: (bytes: number) => void) => Promise<UploadPart>;
export type UploadStatus = UploadSession['state'] | 'hashing' | 'uploading' | 'needs-file' | 'error' | 'cancelling';
export interface UploadRow { id: string; session: UploadSession; status: UploadStatus; progress: number; error?: string; canRetry: boolean; needsFile: boolean }
export interface PreparingUpload { id: string; filename: string; progress: number; cancellable: boolean }
export interface UploadSnapshot { rows: UploadRow[]; preparing: PreparingUpload[]; loading: boolean; error: string | null; uncertainOperations: number }
interface CoordinatorOptions {
  api: UploadAPI;
  storage: ReceiptStorage;
  sendPart: PartSender;
  hash: FileHasher;
  uuid?: () => string;
  pollInterval?: number;
  onLibraryChange?: () => void;
}
const message = (error: unknown): string => error instanceof Error ? error.message : 'Upload request failed';
const status = (error: unknown): number | undefined => typeof error === 'object' && error !== null && 'status' in error && typeof error.status === 'number' ? error.status : undefined;
const fingerprintPattern = /^[0-9a-f]{64}$/;
const activeOnServer = (session: UploadSession) => ['completing', 'queued', 'processing'].includes(session.state);
const abortError = () => new DOMException('Aborted', 'AbortError');

export class UploadCoordinator {
  private readonly store: ReceiptStore;
  private receipts: UploadReceipts = { version: 1, commands: [], identities: [], maxParallelTransfers: 1 };
  private storageError: string | undefined;
  private readonly api: UploadAPI;
  private readonly uuid: () => string;
  private sessions = new Map<string, UploadSession>();
  private sessionRevisions = new Map<string, number>();
  private nextSessionRevision = 0;
  private files = new Map<string, File>();
  private stagedFiles = new Map<string, File>();
  private preparing = new Map<string, Omit<PreparingUpload, 'cancellable'>>();
  private preparations = new Map<string, AbortController>();
  private active = new Map<string, { abort: AbortController; phase: 'hashing' | 'uploading'; bytes: number }>();
  private errors = new Map<string, string>();
  private cancelling = new Set<string>();
  private retired = new Set<string>();
  private commandRuns = new Map<string, Promise<void>>();
  private listeners = new Set<() => void>();
  private snapshot: UploadSnapshot = { rows: [], preparing: [], loading: false, error: null, uncertainOperations: 0 };
  private started: Promise<void> | undefined;
  private refreshing: Promise<void> | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private error: string | null = null;
  private pollBlocked = false;
  private blockedCommands = new Set<string>();
  private loading = false;

  constructor(private readonly options: CoordinatorOptions) {
    this.api = options.api;
    this.uuid = options.uuid ?? (() => crypto.randomUUID());
    this.store = new ReceiptStore(options.storage);
    try { this.receipts = this.store.load(); } catch (error) { this.storageError = message(error); }
    this.publish();
  }
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getSnapshot = (): UploadSnapshot => this.snapshot;
  start(): Promise<void> {
    return this.started ??= (async () => {
      await this.refresh();
      if (!this.storageError) await this.recoverCommands();
      this.schedule();
    })();
  }
  async refresh(): Promise<void> {
    if (this.refreshing) return this.refreshing;
    this.refreshing = (async () => {
      this.loading = true; this.publish();
      const startingRevisions = new Map(this.sessionRevisions);
      try {
        const batches = await this.api.list();
        const next = new Map<string, UploadSession>();
        for (const batch of batches) for (const session of batch.sessions) if (!this.retired.has(session.id)) next.set(session.id, session);
        // A listing is a snapshot taken before its response arrives. Preserve
        // each session accepted or advanced locally after that read began.
        for (const [id, session] of this.sessions) {
          if (this.sessionRevisions.get(id) !== startingRevisions.get(id)) next.set(id, session);
        }
        const changed = [...this.sessions.keys()].some((id) => !next.has(id));
        for (const id of this.sessions.keys()) if (!next.has(id)) this.removeLocal(id);
        this.sessions = next;
        if (changed) this.options.onLibraryChange?.();
        this.pollBlocked = false;
        if (!this.blockedCommands.size) this.error = null;
        if (!this.storageError) this.updateReceipts((value) => {
          value.identities = value.identities.filter((identity) => next.has(identity.id));
          for (const session of next.values()) this.rememberIdentity(value, session);
        });
      } catch (error) { this.error = message(error); this.pollBlocked = true; }
      finally { this.loading = false; this.refreshing = undefined; this.publish(); this.pump(); this.schedule(); }
    })();
    return this.refreshing;
  }
  async add(files: readonly File[], collectionId: number | null): Promise<void> {
    this.assertWritable();
    if (!files.length || files.length > 500) throw new Error('Choose between 1 and 500 files');
    if (collectionId !== null && (!Number.isSafeInteger(collectionId) || collectionId < 1)) throw new Error('Invalid collection');
    for (const file of files) if (!Number.isSafeInteger(file.size) || file.size < 1 || !file.name.length || file.name.length > 1024) throw new Error('Choose nonempty files with valid names');
    const entries = files.map((file) => ({ file, id: this.uuid() }));
    const request: NewUploads = { collectionId, files: [] };
    for (const { id, file } of entries) { this.preparing.set(id, { id, filename: file.name, progress: 0 }); this.stagedFiles.set(id, file); this.preparations.set(id, new AbortController()); }
    this.publish();
    try {
      for (const { id, file } of entries) {
        const abort = this.preparations.get(id)!;
        if (abort.signal.aborted) continue;
        try {
          const digest = await this.options.hash(file, abort.signal, (bytes) => {
            if (!abort.signal.aborted) this.preparing.set(id, { id, filename: file.name, progress: bytes / file.size }); this.publish();
          });
          if (!abort.signal.aborted) request.files.push({ clientFileId: id, filename: file.name, size: file.size, fingerprint: digest.hex });
        } catch (error) { if (!abort.signal.aborted) throw error; }
      }
      for (let index = request.files.length - 1; index >= 0; index--) {
        if (this.preparations.get(request.files[index]!.clientFileId)?.signal.aborted) request.files.splice(index, 1);
      }
      if (!request.files.length) return;
      const command: UploadCommand = { kind: 'create', operationId: this.uuid(), createdAt: Date.now(), body: request };
      this.updateReceipts((value) => { value.commands.push(command); });
      this.publish();
      await this.runCommand(command);
    } catch (error) { this.error = message(error); throw error; }
    finally {
      for (const entry of entries) {
        this.preparing.delete(entry.id); this.preparations.delete(entry.id);
        if (!this.receipts.commands.some((command) => command.kind === 'create' && command.body.files.some((file) => file.clientFileId === entry.id))) this.stagedFiles.delete(entry.id);
      }
      this.publish(); this.pump(); this.schedule();
    }
  }
  cancelPreparation(id: string): void { if (this.receipts.commands.some((command) => command.kind === 'create' && command.body.files.some((file) => file.clientFileId === id))) return; this.preparations.get(id)?.abort(abortError()); this.preparing.delete(id); this.stagedFiles.delete(id); this.publish(); }
  async reselect(id: string, file: File): Promise<void> {
    this.assertWritable();
    const session = this.sessions.get(id);
    if (!session) throw new Error('Upload session is no longer available');
    if (this.active.has(id) || this.cancelling.has(id)) throw new Error('Upload is already running');
    const identity = this.receipts.identities.find((value) => value.id === id);
    const fingerprint = session.fingerprint ?? identity?.fingerprint;
    if (!fingerprint || !fingerprintPattern.test(fingerprint)) throw new Error('The original content fingerprint is unavailable. Cancel this upload and add the file as a new upload.');
    if (identity && session.fingerprint && identity.fingerprint !== session.fingerprint) throw new Error('The saved content identity conflicts with this session');
    const abort = new AbortController();
    this.active.set(id, { abort, phase: 'hashing', bytes: 0 }); this.errors.delete(id); this.publish();
    try {
      if (file.size !== session.size) throw new Error('The selected file does not match the original upload');
      const digest = await this.options.hash(file, abort.signal, (bytes) => {
        const running = this.active.get(id); if (running) running.bytes = bytes;
        this.publish();
      });
      abort.signal.throwIfAborted();
      if (digest.hex !== fingerprint) throw new Error('The selected file has different content. Add it as a new upload.');
      if (this.sessions.get(id)?.generation !== session.generation) throw new Error('Upload generation changed. Refresh and choose the file again.');
      this.files.set(id, file);
    } catch (error) {
      if (!abort.signal.aborted) this.errors.set(id, message(error));
      throw error;
    } finally { this.active.delete(id); this.publish(); this.pump(); }
  }
  async retry(id: string): Promise<void> {
    this.assertWritable();
    this.errors.delete(id); this.error = null;
    const outstanding = this.receipts.commands.filter((command) => command.kind !== 'create' && command.uploadId === id);
    if (outstanding.length) {
      for (const command of outstanding) { this.blockedCommands.delete(command.operationId); await this.runCommand(command, true); }
      this.publish(); this.pump(); this.schedule(); return;
    }
    const session = this.sessions.get(id);
    if (!session) return;
    if (session.state === 'failed') {
      if (!session.jobId) throw new Error('This upload cannot be retried without selecting the original file');
      const command: UploadCommand = { kind: 'retry', operationId: this.uuid(), createdAt: Date.now(), uploadId: id, jobId: session.jobId };
      this.updateReceipts((value) => { value.commands.push(command); });
      await this.runCommand(command);
      await this.refresh();
    } else if (!this.files.has(id)) throw new Error('Choose the original file to continue this upload');
    this.publish(); this.pump(); this.schedule();
  }
  async recoverRequests(): Promise<void> { this.assertWritable(); this.error = null; this.pollBlocked = false; this.blockedCommands.clear(); await this.recoverCommands(); await this.refresh(); }
  async cancel(id: string): Promise<void> {
    this.assertWritable();
    if (this.cancelling.has(id)) return;
    this.cancelling.add(id); this.active.get(id)?.abort.abort(abortError()); this.publish();
    try {
      let command = this.receipts.commands.find((value) => value.kind === 'cancel' && value.uploadId === id);
      if (!command) {
        command = { kind: 'cancel', operationId: this.uuid(), createdAt: Date.now(), uploadId: id };
        this.updateReceipts((value) => { value.commands.push(command!); });
      }
      await this.runCommand(command);
    } catch (error) { this.errors.set(id, message(error)); throw error; }
    finally { this.cancelling.delete(id); this.publish(); this.pump(); this.schedule(); }
  }
  private async recoverCommands(): Promise<void> {
    for (const command of [...this.receipts.commands].filter((command) => !this.blockedCommands.has(command.operationId))) {
      try { await this.runCommand(command, true); } catch (error) {
        if (command.kind !== 'create') this.errors.set(command.uploadId, message(error));
        this.error = message(error);
      }
    }
    this.publish(); this.pump(); this.schedule();
  }
  private runCommand(command: UploadCommand, resolveFirst = false): Promise<void> {
    const existing = this.commandRuns.get(command.operationId);
    if (existing) return existing;
    const running = this.executeCommand(command, resolveFirst).catch((error: unknown) => { if (this.receipts.commands.some((value) => value.operationId === command.operationId)) this.blockedCommands.add(command.operationId); throw error; }).finally(() => { this.commandRuns.delete(command.operationId); });
    this.commandRuns.set(command.operationId, running);
    return running;
  }
  private async executeCommand(command: UploadCommand, resolveFirst: boolean): Promise<void> {
    if (resolveFirst && await this.resolveCommand(command)) return;
    try { this.acceptCommand(command, await this.sendCommand(command)); }
    catch (error) {
      const code = status(error);
      if (code !== undefined && code >= 400 && code < 500 && code !== 408) {
        this.dropCommand(command.operationId); throw error;
      }
      // A timeout or 5xx can follow a committed write: resolve first, then repeat
      // only this exact durable request, never generate a replacement UUID/body.
      if (await this.resolveCommand(command)) return;
      try { this.acceptCommand(command, await this.sendCommand(command)); }
      catch (retryError) {
        if (await this.resolveCommand(command)) return;
        throw new Error(`The request outcome is uncertain. Retry resolves the saved request. ${message(retryError)}`, { cause: retryError });
      }
    }
  }
  private sendCommand(command: UploadCommand): Promise<unknown> {
    switch (command.kind) {
      case 'create': return this.api.create(command.body, command.operationId);
      case 'complete': return this.api.complete(command.uploadId, command.body, command.operationId);
      case 'retry': return this.api.retry(command.jobId, command.operationId);
      case 'cancel': return this.api.cancel(command.uploadId);
    }
  }
  private async resolveCommand(command: UploadCommand): Promise<boolean> {
    if (command.kind === 'cancel') {
      try {
        const result = await this.api.get(command.uploadId);
        if (result.state === 'ready') { this.acceptCommand(command, result); return true; }
        return false;
      } catch (error) {
        if (status(error) === 404) { this.acceptCommand(command, undefined); return true; }
        throw error;
      }
    }
    try {
      const operation = await this.api.operation(command.operationId);
      if (operation.state === 'failed' || operation.httpStatus >= 400) {
        this.dropCommand(command.operationId);
        const result = operation.result;
        throw new Error(typeof result?.detail === 'string' ? result.detail : typeof result?.title === 'string' ? result.title : 'The saved upload request failed');
      }
      if (operation.result !== null && (operation.state === 'completed' || command.kind === 'complete')) {
        this.acceptCommand(command, operation.result); return true;
      }
      // Its durable intent is known. Do not resend a pending operation.
      return true;
    } catch (error) {
      if (status(error) !== 404) throw error;
      if (command.kind === 'complete') {
        const result = await this.api.get(command.uploadId);
        if (result.state === 'ready' || (result.session && activeOnServer(result.session))) { this.acceptCommand(command, result); return true; }
      }
      if (command.kind === 'create') {
        const sessions = [...this.sessions.values()].filter((session) => command.body.files.some((file) => file.clientFileId === session.clientFileId));
        if (sessions.length === command.body.files.length) {
          this.acceptCommand(command, { operationId: command.operationId, batchId: sessions[0]!.batchId, sessions, maxParallelTransfers: this.receipts.maxParallelTransfers, maxFileSize: Number.MAX_SAFE_INTEGER } satisfies UploadsCreated);
          return true;
        }
      }
      // Receipt retention is seven days. An unknown older key cannot prove a
      // prior Create did not already publish; never silently create it again.
      if (Date.now() - command.createdAt > 6 * 86400000) throw new Error('The saved request is older than operation retention. Its outcome needs review; it was not sent again.', { cause: error });
      return false;
    }
  }
  private acceptCommand(command: UploadCommand, response: unknown): void {
    if (command.kind === 'create') {
      const result = response as UploadsCreated;
      if (!result || !Array.isArray(result.sessions) || result.sessions.length !== command.body.files.length || !Number.isSafeInteger(result.maxParallelTransfers) || result.maxParallelTransfers < 1) throw new Error('Invalid upload creation result');
      const seen = new Set<string>();
      for (const session of result.sessions) {
        const input = command.body.files.find((file) => file.clientFileId === session.clientFileId);
        if (!input || seen.has(session.clientFileId) || session.size !== input.size || (session.fingerprint !== undefined && session.fingerprint !== input.fingerprint)) throw new Error('Upload creation content identity mismatch');
        seen.add(session.clientFileId);
      }
      this.updateReceipts((value) => {
        value.maxParallelTransfers = result.maxParallelTransfers;
        value.commands = value.commands.filter((entry) => entry.operationId !== command.operationId);
        for (const session of result.sessions) {
          const input = command.body.files.find((file) => file.clientFileId === session.clientFileId)!;
          value.identities = value.identities.filter((identity) => identity.id !== session.id);
          value.identities.push({ id: session.id, clientFileId: session.clientFileId, size: session.size, fingerprint: input.fingerprint! });
        }
      });
      for (const session of result.sessions) {
        if (this.retired.has(session.id)) continue;
        this.putSession(session);
        const file = this.stagedFiles.get(session.clientFileId);
        if (file) { this.files.set(session.id, file); this.stagedFiles.delete(session.clientFileId); }
      }
    } else if (command.kind === 'cancel') {
      this.updateReceipts((value) => {
        value.commands = value.commands.filter((entry) => entry.kind === 'create' || entry.uploadId !== command.uploadId);
        value.identities = value.identities.filter((identity) => identity.id !== command.uploadId);
      });
      this.retire(command.uploadId);
    } else if (command.kind === 'complete') {
      const result = response as UploadOutcome;
      if (!result || result.uploadId !== command.uploadId || !['pending', 'failed', 'ready'].includes(result.state)) throw new Error('Invalid upload completion result');
      this.dropCommand(command.operationId);
      if (result.state === 'ready') this.retire(command.uploadId);
      else if (result.session && !this.retired.has(command.uploadId)) this.putSession(result.session);
      else if (result.state === 'pending') {
        const current = this.sessions.get(command.uploadId);
        if (current) this.putSession({ ...current, state: 'completing' });
      }
    } else {
      this.dropCommand(command.operationId);
      const current = this.sessions.get(command.uploadId);
      if (current) this.putSession({ ...current, state: 'queued' });
    }
    this.publish(); this.pump(); this.schedule();
  }
  private pump(): void {
    if (this.storageError) return;
    const limit = Math.min(2, this.receipts.maxParallelTransfers);
    for (const session of this.sessions.values()) {
      if (this.active.size >= limit) break;
      if (!['creating', 'receiving'].includes(session.state) || !this.files.has(session.id) || this.active.has(session.id) || this.errors.has(session.id) || this.cancelling.has(session.id) || this.receipts.commands.some((command) => command.kind !== 'create' && command.uploadId === session.id)) continue;
      const abort = new AbortController();
      this.active.set(session.id, { abort, phase: 'hashing', bytes: 0 });
      void this.transfer(session, this.files.get(session.id)!, abort.signal).catch((error: unknown) => {
        if (!abort.signal.aborted && !this.retired.has(session.id)) this.errors.set(session.id, message(error));
      }).finally(() => { this.active.delete(session.id); this.publish(); this.pump(); this.schedule(); });
    }
    this.publish();
  }
  private async transfer(session: UploadSession, file: File, signal: AbortSignal): Promise<void> {
    if (!Number.isSafeInteger(session.partSize) || session.partSize < 1 || session.partCount !== Math.ceil(session.size / session.partSize) || session.partCount < 1 || session.partCount > 10000 || !Number.isSafeInteger(session.generation) || session.generation < 1 || file.size !== session.size) throw new Error('Invalid server upload part layout');
    const manifest: CompleteUpload['parts'] = [];
    let sourceParts = session.parts;
    for (let number = 1; number <= session.partCount; number++) {
      signal.throwIfAborted();
      const offset = (number - 1) * session.partSize;
      const blob = file.slice(offset, Math.min(offset + session.partSize, file.size));
      const running = this.active.get(session.id);
      if (running) { running.phase = 'hashing'; running.bytes = offset; }
      this.publish();
      const digest = await this.options.hash(blob, signal);
      const binding = Object.freeze({ sessionId: session.id, generation: session.generation, partNumber: number, size: blob.size, sha256: digest.base64 });
      const previous = sourceParts.find((part) => part.partNumber === number);
      this.assertPart(previous, binding);
      let part = previous?.state === 'confirmed' && previous.etag ? previous : undefined;
      if (!part) {
        if (running) running.phase = 'uploading'; this.publish();
        for (let attempt = 0; !part && attempt < 2; attempt++) {
          signal.throwIfAborted();
          try {
            part = await this.options.sendPart(binding, blob, signal, (bytes) => {
              if (running) running.bytes = offset + Math.max(0, Math.min(bytes, blob.size)); this.publish();
            });
          } catch (error) {
            signal.throwIfAborted();
            const resolved = await this.api.get(session.id);
            if (resolved.state === 'ready') { this.retire(session.id); return; }
            const current = resolved.session;
            if (!current || current.generation !== binding.generation || !['creating', 'receiving'].includes(current.state)) throw new Error('Upload session changed while resolving a part', { cause: error });
            sourceParts = current.parts;
            const confirmed = current.parts.find((value) => value.partNumber === number);
            this.assertPart(confirmed, binding);
            if (confirmed?.state === 'confirmed' && confirmed.etag) part = confirmed;
            else if (attempt === 1 || (status(error) !== undefined && status(error)! < 500 && status(error) !== 408)) throw error;
          }
        }
      }
      signal.throwIfAborted();
      this.assertPart(part, binding);
      if (!part || part.state !== 'confirmed' || !part.etag) throw new Error('Part acknowledgement is not confirmed');
      manifest.push({ partNumber: number, etag: part.etag });
      const current = this.sessions.get(session.id);
      if (!current || current.generation !== session.generation || this.cancelling.has(session.id)) throw abortError();
      this.putSession({ ...current, parts: current.parts.filter((value) => value.partNumber !== number).concat(part) });
      if (running) running.bytes = offset + blob.size;
      this.publish();
    }
    signal.throwIfAborted();
    const command: UploadCommand = { kind: 'complete', operationId: this.uuid(), createdAt: Date.now(), uploadId: session.id, body: { generation: session.generation, parts: manifest } };
    this.updateReceipts((value) => { value.commands.push(command); });
    await this.runCommand(command);
  }
  private assertPart(part: UploadPart | undefined, binding: PartBinding): void {
    if (part && (part.partNumber !== binding.partNumber || part.size !== binding.size || part.sha256 !== binding.sha256)) throw new Error('The server part binding belongs to different bytes. Add the file as a new upload.');
  }
  private rememberIdentity(value: UploadReceipts, session: UploadSession): void {
    if (!session.fingerprint || !fingerprintPattern.test(session.fingerprint) || value.identities.some((identity) => identity.id === session.id)) return;
    value.identities.push({ id: session.id, clientFileId: session.clientFileId, size: session.size, fingerprint: session.fingerprint });
  }
  private updateReceipts(change: (value: UploadReceipts) => void): void {
    this.assertWritable();
    const next = structuredClone(this.receipts);
    change(next);
    if (JSON.stringify(next) === JSON.stringify(this.receipts)) return;
    try { this.store.save(next); } catch (error) { this.error = `Could not save upload recovery receipts. ${message(error)}`; throw new Error(this.error, { cause: error }); }
    this.receipts = next;
  }
  private assertWritable(): void { if (this.storageError) throw new Error(this.storageError); }
  private dropCommand(operationId: string): void { this.updateReceipts((value) => { value.commands = value.commands.filter((command) => command.operationId !== operationId); }); }
  private putSession(session: UploadSession): void {
    this.sessions.set(session.id, session);
    this.sessionRevisions.set(session.id, ++this.nextSessionRevision);
  }
  private removeLocal(id: string): void {
    this.active.get(id)?.abort.abort(abortError()); this.files.delete(id); this.errors.delete(id); this.sessions.delete(id); this.sessionRevisions.delete(id);
  }
  private retire(id: string): void {
    this.retired.add(id); this.removeLocal(id);
    this.updateReceipts((value) => { value.identities = value.identities.filter((identity) => identity.id !== id); });
    this.options.onLibraryChange?.(); this.publish();
  }
  private schedule(): void {
    if (this.timer !== undefined) { clearTimeout(this.timer); this.timer = undefined; }
    if (this.pollBlocked || this.storageError) return;
    const work = this.receipts.commands.some((command) => !this.blockedCommands.has(command.operationId)) || [...this.sessions.values()].some(activeOnServer);
    if (!work) return;
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void (async () => { await this.refresh(); if (!this.pollBlocked) await this.recoverCommands(); })();
    }, this.options.pollInterval ?? 1500);
  }
  private publish(): void {
    const rows: UploadRow[] = [...this.sessions.values()].map((session) => {
      const running = this.active.get(session.id);
      const error = this.errors.get(session.id);
      let state: UploadStatus = session.state;
      if (this.cancelling.has(session.id)) state = 'cancelling';
      else if (error) state = 'error';
      else if (running) state = running.phase;
      else if (['creating', 'receiving'].includes(session.state) && !this.files.has(session.id)) state = 'needs-file';
      const confirmedBytes = session.parts.reduce((sum, part) => sum + (part.state === 'confirmed' ? part.size : 0), 0);
      const progress = activeOnServer(session) || session.state === 'failed' ? session.progress ?? 1 : Math.min(1, (running?.bytes ?? confirmedBytes) / session.size);
      const needsFile = !this.files.has(session.id) && ['creating', 'receiving'].includes(session.state) && !running;
      const row: UploadRow = { id: session.id, session, status: state, progress, needsFile, canRetry: !running && !needsFile && (error !== undefined || session.state === 'failed') };
      if (error || session.errorCode) row.error = error ?? session.errorCode!;
      return row;
    });
    this.snapshot = { rows, preparing: [...this.preparing.values()].map((entry) => ({ ...entry, cancellable: !this.receipts.commands.some((command) => command.kind === 'create' && command.body.files.some((file) => file.clientFileId === entry.id)) })), error: this.storageError ?? this.error, loading: this.loading, uncertainOperations: this.receipts.commands.length };
    for (const listener of this.listeners) listener();
  }
}
