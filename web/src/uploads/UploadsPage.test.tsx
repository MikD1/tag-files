import { describe, expect, it } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { UploadsPage } from './UploadsPage';
import { UploadCoordinator, type UploadAPI } from './coordinator';

function renderPage() {
  const requests: unknown[] = [];
  const api: UploadAPI = {
    list: async () => [],
    create: async (body, operationId) => {
      requests.push(body);
      return { operationId, batchId: '00000000-0000-4000-8000-000000000002', maxParallelTransfers: 1, maxFileSize: 21474836480, sessions: body.files.map((file) => ({ id: '00000000-0000-4000-8000-000000000001', batchId: '00000000-0000-4000-8000-000000000002', clientFileId: file.clientFileId, filename: file.filename, size: file.size, fingerprint: file.fingerprint!, generation: 1, state: 'queued', partSize: 8388608, partCount: 1, parts: [] })) };
    },
    get: async () => { throw new Error('not expected'); },
    complete: async () => { throw new Error('not expected'); },
    cancel: async () => undefined,
    retry: async () => { throw new Error('not expected'); },
    operation: async () => { throw new Error('not expected'); },
  };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(['collections'], [{ id: 7, name: 'Trips', version: 1, itemCount: 0 }]);
  const coordinator = new UploadCoordinator({ api, storage: { getItem: () => null, setItem: () => undefined }, hash: async () => ({ hex: 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad', base64: 'ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=' }), sendPart: async () => { throw new Error('not expected'); }, pollInterval: 60000 });
  render(<MantineProvider env="test" theme={{ components: { Popover: { defaultProps: { hideDetached: false } } } }}><QueryClientProvider client={client}><UploadsPage coordinator={coordinator}/></QueryClientProvider></MantineProvider>);
  return { requests, coordinator };
}

describe('Uploads page', () => {
  it('shows only the current queue and an accessible Add media control', async () => {
    renderPage();
    expect(screen.getByRole('heading', { name: 'Uploads' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Add media' })).toBeVisible();
    expect(await screen.findByText('Queue is empty')).toBeVisible();
    expect(screen.queryByText(/history|demo/i)).not.toBeInTheDocument();
  });

  it('allows collection assignment before creating sessions; cancelling the chooser creates nothing', async () => {
    const { requests, coordinator } = renderPage();
    const user = userEvent.setup();
    const input = screen.getByLabelText('Choose media files');
    await user.upload(input, new File(['abc'], 'photo.jpg', { type: 'image/jpeg' }));
    expect(requests).toHaveLength(0);
    await user.click(await screen.findByRole('button', { name: 'Cancel' }));
    expect(requests).toHaveLength(0);
    await user.upload(input, new File(['abc'], 'photo.jpg', { type: 'image/jpeg' }));
    const collection = await screen.findByRole('combobox', { name: 'Collection' });
    await user.click(collection);
    await user.click(await screen.findByRole('option', { name: 'Trips' }));
    await user.click(screen.getByRole('button', { name: 'Upload' }));
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0]).toMatchObject({ collectionId: 7, files: [{ filename: 'photo.jpg' }] });
    await coordinator.cancel('00000000-0000-4000-8000-000000000001');
    await waitFor(() => expect(screen.getByText('Queue is empty')).toBeVisible());
  });
});
