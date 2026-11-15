import type { QueryClient } from '@tanstack/react-query';
import type { Schema } from '../api/client';
/** A delayed view receipt must never replace newer favorite or metadata state. */
export function acceptItem(client: QueryClient, incoming: Schema<'Item'>): void {
  client.setQueryData<Schema<'Item'>>(['item', incoming.id], (current) => current && current.version > incoming.version ? current : incoming);
}
