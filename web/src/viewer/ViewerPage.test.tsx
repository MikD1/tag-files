import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { ViewerPage } from './ViewerPage';
import type { Schema } from '../api/client';
const context = '00000000-0000-4000-8000-000000000001';
const base: Schema<'Item'> = { id: 1, mediaKind: 'Image', description: 'private metadata', uploadedAt: '2026-01-01T00:00:00Z', tags: [{ id: 2, name: 'Cat', version: 1 }, { id: 1, name: 'cat', version: 1 }], collectionId: 7, isFavorite: false, viewCount: 0, lastViewAt: null, durationSeconds: null, processingState: 'ready', version: 5, contentUrl: '/media/primary' };
function page(items: Schema<'Item'>[] = []) { return { items, totalItems: items.length, page: 1, pageSize: 4, totalPages: items.length ? 1 : 0 }; }
function setup(options: { related?: boolean; expired?: boolean } = {}) {
  const calls: { method: string; path: string; body: unknown; headers: Headers }[] = [];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  client.setQueryData(['item', 1], base);
  client.setQueryData(['collections'], [{ id: 7, name: 'Trips', version: 1, itemCount: 1 }]);
  client.setQueryData(['tags'], base.tags);
  vi.stubGlobal('fetch', async (request: Request) => {
    const url = new URL(request.url);
    const body: unknown = request.method === 'GET' || request.method === 'DELETE' ? null : await request.json();
    calls.push({ method: request.method, path: url.pathname, body, headers: request.headers });
    let result: unknown;
    if (url.pathname.endsWith('/neighbors')) result = { expired: options.expired ?? false, origin: { route: '/library?page=3&sort=UploadedAsc', sort: 'UploadedAsc' }, total: 72, position: 25, previousId: 9, nextId: 11 };
    else if (url.pathname.endsWith('/similar')) result = page(options.related ? [{ ...base, id: 4, collectionId: null, contentUrl: '/media/related' }] : []);
    else if (url.pathname.endsWith('/favorite')) result = { ...base, isFavorite: (body as { isFavorite: boolean }).isFavorite, version: 6 };
    else if (request.method === 'DELETE') return new Response(null, { status: 204 });
    else if (url.pathname === '/api/v1/items') result = page();
    else if (url.pathname.startsWith('/api/v1/items/')) result = { ...base, id: Number(url.pathname.split('/').at(-1)) };
    else throw new Error(`Unexpected API route: ${request.method} ${url.pathname}`);
    return new Response(JSON.stringify(result), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  render(<MantineProvider env="test" theme={{ components: { Popover: { defaultProps: { hideDetached: false } } } }}><QueryClientProvider client={client}><MemoryRouter initialEntries={[`/content/1?context=${context}&origin=%2Flibrary%3Fpage%3D2`]}><Routes><Route path="/content/:id" element={<ViewerPage/>}/><Route path="/library" element={<h1>Results</h1>}/></Routes></MemoryRouter></QueryClientProvider></MantineProvider>);
  return { calls, client };
}
afterEach(() => vi.unstubAllGlobals());
describe('canonical inline viewer', () => {
  it('keeps zoom through rotation, resets both together and preserves browser zoom keys', async () => {
    setup(); const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Image tools' }));
    await user.click(screen.getByRole('button', { name: 'Zoom in' }));
    await user.click(screen.getByRole('button', { name: 'Rotate 90°' }));
    expect(screen.getByTestId('viewer-image')).toHaveStyle({ transform: 'scale(1.25) rotate(90deg)' });
    const zoom = new KeyboardEvent('keydown', { key: '=', metaKey: true, bubbles: true, cancelable: true });
    document.dispatchEvent(zoom); expect(zoom.defaultPrevented).toBe(false);
    await user.click(screen.getByRole('button', { name: 'Reset zoom and rotation' }));
    expect(screen.getByTestId('viewer-image')).toHaveStyle({ transform: 'scale(1) rotate(0deg)' });
    fireEvent.keyDown(screen.getByRole('button', { name: 'Zoom in' }), { key: 'Escape' });
    expect(screen.getByLabelText('Image tools', { selector: 'summary' })).toHaveFocus();
    expect(screen.getByLabelText('Image tools', { selector: 'summary' })).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('private metadata')).not.toBeInTheDocument();
  });
  it('uses authoritative context neighbors and Back URL, without rebuilding a new order', async () => {
    const { calls } = setup();
    await waitFor(() => expect(screen.getByRole('link', { name: 'Back to results' })).toHaveAttribute('href', '/library?page=3&sort=UploadedAsc'));
    expect(screen.getByRole('link', { name: 'Next item' }).getAttribute('href')).toContain(`context=${context}`);
    expect(screen.getByRole('link', { name: 'Previous item' }).getAttribute('href')).toContain('/content/9?');
    expect(screen.getByText('25 / 72')).toBeVisible();
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);
  });
  it('keeps a collection heading without siblings and links Same tags to an ordinary unrestricted AND gallery', async () => {
    setup({ related: true });
    expect(await screen.findByRole('link', { name: 'Trips' })).toHaveAttribute('href', '/library?collectionId=7');
    const heading = await screen.findByRole('link', { name: 'Same tags' });
    const url = new URL(heading.getAttribute('href')!, 'http://localhost');
    expect(url.searchParams.get('q')).toBe('Cat && cat');
    expect(url.searchParams.has('collectionId')).toBe(false); expect(url.searchParams.has('kind')).toBe(false);
    expect(screen.getByRole('link', { name: 'Cat' })).toHaveAttribute('href', '/library?q=Cat');
    expect(screen.getByRole('link', { name: 'cat' })).toHaveAttribute('href', '/library?q=cat');
  });
  it('exposes expired navigation and disables neighbors without rebuilding its order', async () => {
    const { calls } = setup({ expired: true });
    expect(await screen.findByText(/Navigation has expired/)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Next item' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Previous item' })).toBeDisabled();
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);
  });
  it('does not navigate from editor input keys and returns focus to Edit after Cancel', async () => {
    const { calls } = setup(); const user = userEvent.setup();
    await waitFor(() => expect(screen.getByRole('link', { name: 'Next item' })).toBeVisible());
    const edit = screen.getByRole('button', { name: 'Edit tags and collection' });
    await user.click(edit);
    const field = await screen.findByRole('combobox', { name: 'Add tags' });
    field.focus(); fireEvent.keyDown(field, { key: 'ArrowRight' });
    expect(screen.getByRole('dialog')).toBeVisible();
    expect(calls.filter((call) => call.path === '/api/v1/items/11')).toHaveLength(0);
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(edit).toHaveFocus());
  });
  it('sets an explicit favorite and requires confirmation before permanent deletion', async () => {
    const { calls } = setup(); const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Add to favorites' }));
    expect(await screen.findByRole('button', { name: 'Remove from favorites' })).toHaveAttribute('aria-pressed', 'true');
    expect(calls.find((call) => call.path.endsWith('/favorite'))!.body).toEqual({ isFavorite: true });
    await user.click(screen.getByRole('button', { name: 'More media actions' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Delete media' }));
    expect(calls.filter((call) => call.method === 'DELETE')).toHaveLength(0);
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(calls.filter((call) => call.method === 'DELETE')).toHaveLength(0);
    await user.click(screen.getByRole('button', { name: 'More media actions' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Delete media' }));
    await user.click(screen.getByRole('button', { name: 'Delete permanently' }));
    expect(await screen.findByRole('heading', { name: 'Results' })).toBeVisible();
    const deleted = calls.find((call) => call.method === 'DELETE')!;
    expect(deleted.path).toBe('/api/v1/items/1'); expect(deleted.headers.get('If-Match')).toBe('"6"');
  });
});
