import { ApiError } from '../api/client';
import type { PartBinding } from './coordinator';
import type { UploadPart } from './types';

export function sendPartXHR(binding: PartBinding, blob: Blob, signal: AbortSignal, progress: (bytes: number) => void, createXHR: () => XMLHttpRequest = () => new XMLHttpRequest()): Promise<UploadPart> {
  signal.throwIfAborted();
  if (blob.size !== binding.size) return Promise.reject(new Error('Part size does not match its immutable binding'));
  return new Promise((resolve, reject) => {
    const xhr = createXHR();
    const cleanup = () => {
      signal.removeEventListener('abort', abort);
      xhr.removeEventListener('load', load);
      xhr.removeEventListener('error', network);
      xhr.removeEventListener('timeout', timeout);
      xhr.removeEventListener('abort', aborted);
      xhr.upload.removeEventListener('progress', uploadProgress);
    };
    const abort = () => xhr.abort();
    const aborted = () => { cleanup(); reject(new DOMException('Upload cancelled', 'AbortError')); };
    const network = () => { cleanup(); reject(new TypeError('Connection lost; part outcome is uncertain')); };
    const timeout = () => { cleanup(); reject(new TypeError('Part request timed out; outcome is uncertain')); };
    const uploadProgress = (event: ProgressEvent) => { if (event.lengthComputable) progress(Math.min(event.loaded, blob.size)); };
    const load = () => {
      cleanup();
      if (xhr.status >= 200 && xhr.status < 300) {
        if (xhr.response && typeof xhr.response === 'object') resolve(xhr.response as UploadPart);
        else reject(new TypeError('Part response is missing; outcome is uncertain'));
      } else if (xhr.status === 0) reject(new TypeError('Connection lost; part outcome is uncertain'));
      else reject(new ApiError(new Response(null, { status: xhr.status }), xhr.response));
    };
    xhr.open('PUT', `/api/v1/upload-sessions/${encodeURIComponent(binding.sessionId)}/parts/${binding.partNumber}`);
    xhr.responseType = 'json';
    xhr.timeout = 10 * 60 * 1000;
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.setRequestHeader('X-Upload-Generation', String(binding.generation));
    xhr.setRequestHeader('X-Part-SHA256', binding.sha256);
    // Blob has a known length. Content-Length is a forbidden script header:
    // the browser supplies it for the exact Blob sent, including in Safari.
    signal.addEventListener('abort', abort, { once: true });
    xhr.addEventListener('load', load);
    xhr.addEventListener('error', network);
    xhr.addEventListener('timeout', timeout);
    xhr.addEventListener('abort', aborted);
    xhr.upload.addEventListener('progress', uploadProgress);
    try { xhr.send(blob); } catch (error) { cleanup(); reject(error); }
  });
}
