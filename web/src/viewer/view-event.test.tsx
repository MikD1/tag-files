import { StrictMode, type ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useViewOpening } from './view-event';
const item = { id: 1, mediaKind: 'Image', description: null, uploadedAt: '2026-01-01T00:00:00Z', tags: [], collectionId: null, isFavorite: false, viewCount: 1, lastViewAt: null, durationSeconds: null, processingState: 'ready', version: 1 };
function setup() {
  const calls: { id: number; body: { eventId: string } }[] = [];
  const client = new QueryClient();
  const fetcher = vi.fn(async (request: Request) => { calls.push({ id: Number(request.url.split('/').at(-2)), body: await request.json() as { eventId: string } }); return new Response(JSON.stringify(item), { status: 200, headers: { 'Content-Type': 'application/json' } }); });
  vi.stubGlobal('fetch', fetcher);
  const wrapper = ({ children }: { children: ReactNode }) => <StrictMode><QueryClientProvider client={client}>{children}</QueryClientProvider></StrictMode>;
  return { calls, fetcher, wrapper };
}
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });
describe('one three-second view event per opening', () => {
  it('does not count before three seconds and StrictMode/effect rerenders retain one event UUID', async () => {
    vi.useFakeTimers(); const { calls, wrapper } = setup();
    const { rerender } = renderHook(() => useViewOpening(1, true), { wrapper });
    await act(() => vi.advanceTimersByTimeAsync(2999)); expect(calls).toHaveLength(0);
    rerender(); await act(() => vi.advanceTimersByTimeAsync(1));
    expect(calls).toHaveLength(1); expect(calls[0]!.body.eventId).toMatch(/^[0-9a-f-]{36}$/);
    rerender(); await act(() => vi.advanceTimersByTimeAsync(10000)); expect(calls).toHaveLength(1);
  });
  it('cancels an unopened view and does not add visibility or playback conditions', async () => {
    vi.useFakeTimers(); const { calls, wrapper } = setup();
    const first = renderHook(() => useViewOpening(1, true), { wrapper });
    await act(() => vi.advanceTimersByTimeAsync(2999)); first.unmount();
    await act(() => vi.advanceTimersByTimeAsync(100)); expect(calls).toHaveLength(0);
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    const next = renderHook(() => useViewOpening(1, true), { wrapper });
    await act(() => vi.advanceTimersByTimeAsync(3000)); expect(calls).toHaveLength(1);
    next.unmount(); Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  });
  it('retries an ambiguous failure with the exact same event UUID', async () => {
    vi.useFakeTimers(); const { calls, fetcher, wrapper } = setup();
    fetcher.mockImplementationOnce(async (request) => { calls.push({ id: 1, body: await request.json() as { eventId: string } }); throw new TypeError('lost response'); });
    renderHook(() => useViewOpening(1, true), { wrapper });
    await act(() => vi.advanceTimersByTimeAsync(4000));
    expect(calls).toHaveLength(2); expect(calls[1]).toEqual(calls[0]);
  });
});
