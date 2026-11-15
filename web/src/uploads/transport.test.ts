import { describe, expect, it } from 'vitest';
import { sendPartXHR } from './transport';
import type { PartBinding } from './coordinator';
class TestXHR extends EventTarget {
  upload = new EventTarget();
  headers = new Map<string, string>();
  method = ''; url = ''; body: unknown; status = 0; response: unknown; responseType = ''; timeout = 0;
  open(method: string, url: string) { this.method = method; this.url = url; }
  setRequestHeader(key: string, value: string) { this.headers.set(key, value); }
  send(body: unknown) { this.body = body; }
  abort() { this.dispatchEvent(new Event('abort')); }
  answer(status: number, response: unknown) { this.status = status; this.response = response; this.dispatchEvent(new Event('load')); }
}
const binding: PartBinding = { sessionId: '00000000-0000-4000-8000-000000000001', generation: 7, partNumber: 2, size: 4, sha256: 'base64-sha256' };
describe('same-origin binary part transport', () => {
  it('sends the immutable Blob directly and allows the browser to calculate its Content-Length', async () => {
    const xhr = new TestXHR();
    const blob = new Blob(['data']);
    const seen: number[] = [];
    const pending = sendPartXHR(binding, blob, new AbortController().signal, (value) => seen.push(value), () => xhr as unknown as XMLHttpRequest);
    expect(xhr.method).toBe('PUT');
    expect(xhr.url).toBe('/api/v1/upload-sessions/00000000-0000-4000-8000-000000000001/parts/2');
    expect(xhr.body).toBe(blob);
    expect(xhr.headers.get('X-Upload-Generation')).toBe('7');
    expect(xhr.headers.get('X-Part-SHA256')).toBe('base64-sha256');
    expect(xhr.headers.get('Content-Type')).toBe('application/octet-stream');
    expect(xhr.headers.has('Content-Length')).toBe(false);
    xhr.upload.dispatchEvent(new ProgressEvent('progress', { lengthComputable: true, loaded: 3, total: 4 }));
    xhr.answer(200, { partNumber: 2, size: 4, sha256: 'base64-sha256', state: 'confirmed', etag: 'opaque' });
    expect(await pending).toMatchObject({ etag: 'opaque' });
    expect(seen).toEqual([3]);
  });
  it('aborts an outstanding XHR when cancellation fences the session', async () => {
    const xhr = new TestXHR();
    const abort = new AbortController();
    const pending = sendPartXHR(binding, new Blob(['data']), abort.signal, () => undefined, () => xhr as unknown as XMLHttpRequest);
    abort.abort();
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
  });
  it('preserves Problem Details for a rejected binding and treats timeout as an uncertain outcome', async () => {
    const xhr = new TestXHR();
    const pending = sendPartXHR(binding, new Blob(['data']), new AbortController().signal, () => undefined, () => xhr as unknown as XMLHttpRequest);
    xhr.answer(409, { title: 'Part conflict', status: 409, code: 'part_binding_conflict', detail: 'This part belongs to other bytes' });
    await expect(pending).rejects.toMatchObject({ status: 409, problem: { code: 'part_binding_conflict' } });
    const slow = new TestXHR();
    const timeout = sendPartXHR(binding, new Blob(['data']), new AbortController().signal, () => undefined, () => slow as unknown as XMLHttpRequest);
    slow.dispatchEvent(new Event('timeout'));
    await expect(timeout).rejects.toThrow(/uncertain|timed out/i);
  });
});
