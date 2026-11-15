import type { Schema } from '../api/client';
export function mediaName(item: Schema<'Item'>): string { return `${item.mediaKind === 'Video' ? 'Video' : item.mediaKind === 'Image' ? 'Photo' : 'Media'}${item.tags.length ? ': ' + item.tags.map((tag) => tag.name).join(', ') : ' without tags'}`; }
