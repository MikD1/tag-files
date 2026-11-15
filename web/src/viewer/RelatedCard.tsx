import { useRef, useState, type MouseEvent } from 'react';
import { Link, useNavigate } from 'react-router';
import { Tooltip } from '@mantine/core';
import { api, errorMessage, unwrap, type Schema } from '../api/client';
import { viewerURL } from '../catalog/url';
import { Icon } from '../components/Icon';
import { mediaName } from './labels';
export function RelatedCard({ item, source }: { item: Schema<'Item'>; source: Schema<'NewNavigation'> }) {
  const navigate = useNavigate();
  const pending = useRef(false);
  const request = useRef<{ operationId: string; body: Schema<'NewNavigation'> } | null>(null);
  const [error, setError] = useState('');
  const [posterFailed, setPosterFailed] = useState(false);
  async function open(event: MouseEvent<HTMLAnchorElement>) {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    event.preventDefault(); if (pending.current) return;
    pending.current = true; setError('');
    const command = request.current ??= { operationId: crypto.randomUUID(), body: structuredClone(source) };
    try {
      const context = unwrap(await api.POST('/api/v1/navigation-contexts', { params: { header: { 'Idempotency-Key': command.operationId } }, body: command.body }));
      await navigate(viewerURL(item.id, context.contextId, command.body.origin));
    } catch (error) { setError(errorMessage(error)); }
    finally { pending.current = false; }
  }
  const poster = item.posterUrl ?? (item.mediaKind === 'Image' ? item.contentUrl : undefined);
  return <article className="media-card"><Tooltip label={item.tags.map((tag) => tag.name).join(' · ')} disabled={!item.tags.length} interactive multiline maw={480} openDelay={250} closeDelay={160} position="bottom-start" events={{ hover: true, focus: true, touch: false }}><Link className="card-media" to={`/content/${item.id}?${new URLSearchParams({ origin: source.origin })}`} aria-label={mediaName(item)} onClick={(event) => { void open(event); }}>
    {poster && !posterFailed ? <img src={poster} alt="" loading="lazy" onError={() => setPosterFailed(true)}/> : <span className="media-placeholder"><Icon name={item.mediaKind === 'Video' ? 'video' : 'image'}/></span>}
    {item.mediaKind === 'Video' && <span className="video-badge"><Icon name="play"/>{item.durationSeconds !== null && `${Math.floor(item.durationSeconds / 60)}:${String(Math.floor(item.durationSeconds % 60)).padStart(2, '0')}`}</span>}
    {item.isFavorite && <span className="favorite-marker"><Icon name="star"/></span>}
  </Link></Tooltip>{error && <p className="form-error" role="alert">{error}</p>}</article>;
}
