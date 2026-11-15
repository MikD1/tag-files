import type { components } from '../api/schema';
type Schema<K extends keyof components['schemas']> = components['schemas'][K];
export type UploadSession = Schema<'UploadSession'>;
export type UploadOutcome = Schema<'UploadOutcome'>;
export type UploadsCreated = Schema<'UploadsCreated'>;
export type UploadPart = Schema<'UploadPart'>;
export type CompleteUpload = Schema<'CompleteUpload'>;
export type UploadBatch = Schema<'UploadBatch'>;
export type NewUploads = Schema<'NewUploads'>;
export type JobReceipt = Schema<'JobReceipt'>;
export type Operation = Schema<'Operation'>;
export type UploadCommand =
  | { kind: 'create'; operationId: string; createdAt: number; body: NewUploads }
  | { kind: 'complete'; operationId: string; createdAt: number; uploadId: string; body: CompleteUpload }
  | { kind: 'retry'; operationId: string; createdAt: number; uploadId: string; jobId: string }
  | { kind: 'cancel'; operationId: string; createdAt: number; uploadId: string };
export interface UploadIdentity { id: string; clientFileId: string; size: number; fingerprint: string }
export interface UploadReceipts { version: 1; commands: UploadCommand[]; identities: UploadIdentity[]; maxParallelTransfers: number }
