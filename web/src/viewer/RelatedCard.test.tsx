import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router';
import { RelatedCard } from './RelatedCard';
import type { Schema } from '../api/client';
const item: Schema<'Item'> = { id: 2, mediaKind: 'Image', description: null, uploadedAt: '2026-01-01T00:00:00Z', tags: [{ id: 1, name: 'Cat', version: 1 }, { id: 2, name: 'cat', version: 1 }, { id: 3, name: 'nature', version: 1 }], collectionId: null, isFavorite: false, viewCount: 0, lastViewAt: null, durationSeconds: null, processingState: 'ready', version: 1, contentUrl: '/media/related' };
describe('related grid tag popup', () => {
  it('exposes every tag on hover and focus, remains reachable across the gap and closes with Escape without moving focus', async () => {
    render(<MantineProvider env="test"><MemoryRouter><RelatedCard item={item} source={{ origin: '/library?q=Cat', orderedIds: [2] }}/></MemoryRouter></MantineProvider>);
    const user = userEvent.setup();
    const link = screen.getByRole('link', { name: 'Photo: Cat, cat, nature' });
    await user.hover(link);
    const tooltip = await screen.findByRole('tooltip');
    expect(tooltip).toHaveTextContent('Cat · cat · nature');
    await user.hover(tooltip);
    expect(tooltip).toBeVisible();
    await user.unhover(tooltip); link.focus();
    await waitFor(() => expect(link).toHaveAttribute('aria-describedby'));
    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument());
    expect(link).toHaveFocus();
  });
});
