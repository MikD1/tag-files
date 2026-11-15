import { describe, expect, it } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import { acceptItem } from './cache';
import type { Schema } from '../api/client';
const item: Schema<'Item'> = { id: 1, mediaKind: 'Image', description: null, uploadedAt: '2026-01-01T00:00:00Z', tags: [], collectionId: null, isFavorite: true, viewCount: 1, lastViewAt: null, durationSeconds: null, processingState: 'ready', version: 7 };
describe('ordered item receipts', () => {
  it('keeps newer favorite/metadata when an earlier view response arrives late', () => {
    const client = new QueryClient();
    client.setQueryData(['item', 1], item);
    acceptItem(client, { ...item, isFavorite: false, version: 6 });
    expect(client.getQueryData(['item', 1])).toMatchObject({ isFavorite: true, version: 7 });
    acceptItem(client, { ...item, tags: [{ id: 2, name: 'later', version: 1 }], version: 8 });
    expect(client.getQueryData(['item', 1])).toMatchObject({ isFavorite: true, tags: [{ id: 2 }], version: 8 });
  });
});
