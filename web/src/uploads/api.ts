import { api, unwrap } from '../api/client';
import type { UploadAPI } from './coordinator';

export const uploadAPI: UploadAPI = {
  async list() { return unwrap(await api.GET('/api/v1/upload-batches')); },
  async get(id) { return unwrap(await api.GET('/api/v1/upload-sessions/{id}', { params: { path: { id } } })); },
  async create(body, operationId) { return unwrap(await api.POST('/api/v1/upload-sessions', { params: { header: { 'Idempotency-Key': operationId } }, body })); },
  async complete(id, body, operationId) { return unwrap(await api.POST('/api/v1/upload-sessions/{id}/complete', { params: { path: { id }, header: { 'Idempotency-Key': operationId } }, body })); },
  async cancel(id) { return unwrap(await api.DELETE('/api/v1/upload-sessions/{id}', { params: { path: { id } } })); },
  async retry(id, operationId) { return unwrap(await api.POST('/api/v1/jobs/{id}/retry', { params: { path: { id }, header: { 'Idempotency-Key': operationId } } })); },
  async operation(id) { return unwrap(await api.GET('/api/v1/operations/{id}', { params: { path: { id } } })); },
};
