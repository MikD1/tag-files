import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { OrganizationPage } from './OrganizationPage';
import type { Schema } from '../api/client';
function setup(lost = false) {
  const writes: { key: string; body: { name: string } }[] = [];
  const operations = new Map<string, Schema<'Operation'>>();
  let lookupOffline = lost;
  vi.stubGlobal('fetch', async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === 'POST' && path === '/api/v1/tags') {
      const key = request.headers.get('Idempotency-Key')!;
      const body = await request.json() as { name: string };
      const prior = writes.find((write) => write.key === key);
      writes.push({ key, body });
      if (prior && prior.body.name !== body.name) return Response.json({ title: 'Key conflict', status: 409 }, { status: 409 });
      const status = body.name.includes('_') ? 422 : 201;
      const result = status === 422 ? { title: 'Invalid tag name', status: 422 } : { operationId: key, resource: { id: 7, name: body.name, version: 1 } };
      operations.set(key, { operationId: key, state: status === 422 ? 'failed' : 'completed', httpStatus: status, result, createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 604800000).toISOString() });
      if (lost) throw new TypeError('response lost');
      return Response.json(result, { status });
    }
    if (path.startsWith('/api/v1/operations/')) {
      if (lookupOffline) throw new TypeError('operation lookup offline');
      const receipt = operations.get(path.split('/').at(-1)!);
      return receipt ? Response.json(receipt) : Response.json({ title: 'Not found', status: 404 }, { status: 404 });
    }
    return Response.json([]);
  });
  const cache = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  cache.setQueryData(['tags'], []); cache.setQueryData(['tag-statistics'], []);
  render(<MantineProvider env="test"><QueryClientProvider client={cache}><MemoryRouter><OrganizationPage kind="tags"/></MemoryRouter></QueryClientProvider></MantineProvider>);
  return { writes, enableLookup: () => { lookupOffline = false; } };
}
afterEach(() => vi.unstubAllGlobals());
describe('organization create receipts', () => {
  it('saves a corrected tag after422 with a new body-bound operation key', async () => {
    const { writes } = setup(); const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'New tag' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'a_b');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid tag name');
    await user.clear(screen.getByRole('textbox', { name: 'Name' })); await user.type(screen.getByRole('textbox', { name: 'Name' }), 'a-b');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(writes.map((write) => write.body)).toEqual([{ name: 'a_b' }, { name: 'a-b' }]);
    expect(writes[1]!.key).not.toBe(writes[0]!.key);
  });
  it('resolves a lost original success before accepting edited input and never creates a second entity', async () => {
    const { writes, enableLookup } = setup(true); const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'New tag' }));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'original');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('alert')).toBeVisible();
    await user.clear(screen.getByRole('textbox', { name: 'Name' })); await user.type(screen.getByRole('textbox', { name: 'Name' }), 'edited');
    enableLookup(); await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(writes).toHaveLength(1); expect(writes[0]!.body).toEqual({ name: 'original' });
  });
});
