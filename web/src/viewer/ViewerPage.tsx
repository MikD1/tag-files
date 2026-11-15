import { useCallback, useEffect, useRef, useState } from 'react';
import { Menu, Modal } from '@mantine/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useLocation, useNavigate, useParams } from 'react-router';
import { api, ApiError, errorMessage, unwrap, type Schema } from '../api/client';
import { collectionsQuery, invalidateLibrary, itemQuery } from '../api/queries';
import { positiveID, safeOrigin, viewerURL } from '../catalog/url';
import { MetadataEditor } from '../catalog/MetadataEditor';
import { Button } from '../components/Button';
import { Icon } from '../components/Icon';
import { acceptItem } from './cache';
import { ImageTools } from './ImageTools';
import { MediaStage } from './MediaStage';
import { RelatedCard } from './RelatedCard';
import { useViewOpening } from './view-event';
import './viewer.css';
const validContext = (value: string | null): value is string => value !== null && /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
function gallery(parameters: Record<string, string>): string { const query = new URLSearchParams(parameters); return '/library' + (query.size ? '?' + query.toString() : ''); }
export function ViewerPage() {
  const { id: rawID } = useParams();
  const location = useLocation();
  const id = positiveID(rawID);
  if (id === undefined) return <section className="empty-state"><h1>Media not found</h1><Link to="/library" className="button secondary">Back to library</Link></section>;
  return <ViewerSession key={`${id}:${location.key}`} id={id} search={location.search}/>;
}
function ViewerSession({ id, search }: { id: number; search: string }) {
  const params = new URLSearchParams(search);
  const context = params.get('context');
  const navigate = useNavigate();
  const client = useQueryClient();
  const heading = useRef<HTMLHeadingElement>(null);
  const actions = useRef<HTMLDivElement>(null);
  const wasDeleting = useRef(false);
  const item = useQuery(itemQuery(id));
  const collections = useQuery(collectionsQuery);
  const neighbors = useQuery({ queryKey: ['neighbors', context, id], enabled: validContext(context), retry: false, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/navigation-contexts/{id}/neighbors', { params: { path: { id: context! }, query: { itemId: id } }, signal })) });
  const collectionId = item.data?.collectionId;
  const tags = item.data?.tags ?? [];
  const sameTags = useQuery({ queryKey: ['similar', id, tags.map((tag) => tag.id)], enabled: tags.length > 0, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/items/{id}/similar', { params: { path: { id }, query: { page: 1, pageSize: 4 } }, signal })) });
  const members = useQuery({ queryKey: ['items', { viewerCollection: collectionId }], enabled: collectionId !== undefined && collectionId !== null, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/items', { params: { query: { collectionId: collectionId!, sort: 'UploadedDesc', page: 1, pageSize: 5 } }, signal })) });
  const view = useViewOpening(id, item.data !== undefined);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [zoom, setZoom] = useState(1);
  const [rotation, setRotation] = useState(0);
  useEffect(() => {
    if (wasDeleting.current && !deleting) actions.current?.querySelector<HTMLButtonElement>('button[aria-label="More media actions"]')?.focus({ preventScroll: true });
    wasDeleting.current = deleting;
  }, [deleting]);
  const origin = safeOrigin(neighbors.data?.origin.route ?? params.get('origin'));
  const previous = neighbors.data?.expired ? null : neighbors.data?.previousId;
  const next = neighbors.data?.expired ? null : neighbors.data?.nextId;
  const go = useCallback((target: number) => { if (validContext(context)) void navigate(viewerURL(target, context, origin)); }, [context, navigate, origin]);
  useEffect(() => { if (item.data?.id === id) heading.current?.focus({ preventScroll: true }); }, [id, item.data?.id]);
  useEffect(() => {
    if (item.data?.mediaKind !== 'Image' || editing || deleting) return;
    const key = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
      const active = document.activeElement;
      if (active instanceof HTMLElement && (active.isContentEditable || active.closest('input, textarea, select, button, a, summary, video, audio, [role="dialog"]'))) return;
      const target = event.key === 'ArrowLeft' ? previous : event.key === 'ArrowRight' ? next : null;
      if (target !== undefined && target !== null) { event.preventDefault(); go(target); }
    };
    document.addEventListener('keydown', key);
    return () => document.removeEventListener('keydown', key);
  }, [item.data?.mediaKind, editing, deleting, previous, next, go]);
  const favorite = useMutation({ mutationFn: async (input: { isFavorite: boolean }) => unwrap(await api.PUT('/api/v1/items/{id}/favorite', { params: { path: { id } }, body: input })), onSuccess: (result) => { acceptItem(client, result); void client.invalidateQueries({ queryKey: ['items'] }); } });
  const remove = useMutation({ mutationFn: async (input: { id: number; version: number }) => unwrap(await api.DELETE('/api/v1/items/{id}', { params: { path: { id: input.id }, header: { 'If-Match': `"${input.version}"` } } })),
    retry: (count, error) => count < 1 && (!(error instanceof ApiError) || error.status >= 500 || error.status === 408),
    onSuccess: async () => { void invalidateLibrary(); await navigate(origin, { replace: true, state: { restoreItemFocus: id } }); client.removeQueries({ queryKey: ['item', id], exact: true }); void client.invalidateQueries({ queryKey: ['neighbors'] }); },
    onError: () => { void client.invalidateQueries({ queryKey: ['item', id], exact: true }); },
  });
  if (!item.data) return <section className="empty-state"><h1>{item.isPending ? 'Loading…' : item.error instanceof ApiError && item.error.status === 404 ? 'Media not found' : 'Unable to open media'}</h1>{item.error && <p role="alert" className="form-error">{errorMessage(item.error)}</p>}<Link to={origin} className="button secondary">Back to results</Link></section>;
  const media = item.data;
  const collection = collectionId !== null && collectionId !== undefined ? collections.data?.find((entry) => entry.id === collectionId) : undefined;
  const collectionURL = collectionId !== null && collectionId !== undefined ? gallery({ collectionId: String(collectionId) }) : '/library';
  const sameTagsURL = gallery({ q: tags.map((tag) => tag.name).join(' && ') });
  const related = sameTags.data?.items ?? [];
  const siblings = (members.data?.items ?? []).filter((entry) => entry.id !== id).slice(0, 4);
  const relatedSource: Schema<'NewNavigation'> = { origin: sameTagsURL, orderedIds: related.map((entry) => entry.id), sort: 'UploadedDesc' };
  return <div className="production-viewer"><h1 className="sr-only" tabIndex={-1} ref={heading}>Media viewer</h1>
    <MediaStage key={id} item={media} zoom={zoom} rotation={rotation}/>
    <section className="viewer-info" data-testid="viewer-secondary" aria-label="Media controls, tags and collection"><div className="viewer-navigation" data-testid="viewer-navigation">
      <div ref={actions} className="viewer-primary-actions"><Link to={origin} state={{ restoreItemFocus: id }} className="button ghost icon-button" aria-label="Back to results" title="Back to results" data-testid="back-to-library"><Icon name="back"/></Link><div className="viewer-info-actions" role="group" aria-label="Organize media">
        <Button icon="edit" label="Edit tags and collection" className="ghost icon-button" onClick={() => setEditing(true)}/>
        <Button icon="star" label={media.isFavorite ? 'Remove from favorites' : 'Add to favorites'} className="ghost icon-button favorite-button" aria-pressed={media.isFavorite} disabled={favorite.isPending} onClick={() => favorite.mutate({ isFavorite: !media.isFavorite })}/>
        <Menu position="bottom-start"><Menu.Target><button type="button" className="button ghost icon-button" aria-label="More media actions" title="More media actions"><Icon name="more"/></button></Menu.Target><Menu.Dropdown className="production-options"><Menu.Item color="red" onClick={() => { remove.reset(); setDeleting(true); }}>Delete media</Menu.Item></Menu.Dropdown></Menu>
      </div></div>
      <nav className="viewer-nav" aria-label="Viewer navigation">{previous !== undefined && previous !== null && validContext(context) ? <Link className="button ghost icon-button" to={viewerURL(previous, context, origin)} aria-label="Previous item" title="Previous item" data-testid="viewer-previous"><Icon name="previous"/></Link> : <Button icon="previous" label="Previous item" className="ghost icon-button" disabled/>}<span className="viewer-position">{neighbors.data && !neighbors.data.expired && neighbors.data.position !== null ? `${neighbors.data.position} / ${neighbors.data.total}` : ''}</span>{next !== undefined && next !== null && validContext(context) ? <Link className="button ghost icon-button" to={viewerURL(next, context, origin)} aria-label="Next item" title="Next item" data-testid="viewer-next"><Icon name="chevron"/></Link> : <Button icon="chevron" label="Next item" className="ghost icon-button" disabled/>}</nav>
      {media.mediaKind === 'Image' && <ImageTools zoom={zoom} onZoom={setZoom} onRotate={() => setRotation((value) => (value + 90) % 360)} onReset={() => { setZoom(1); setRotation(0); }}/>}
    </div><div className="viewer-organizing"><div className="tag-list">{tags.map((tag) => <Link key={tag.id} className="tag" to={gallery({ q: tag.name })}>{tag.name}</Link>)}</div></div></section>
    {favorite.error && <p role="alert" className="form-error">{errorMessage(favorite.error)}</p>}
    {view.error && <p role="alert" className="form-error">{view.error} <Button className="ghost small" onClick={view.retry}>Retry</Button></p>}
    {neighbors.data?.expired && <p role="status" className="viewer-context-message">Navigation has expired. Return to results to start again.</p>}
    {neighbors.error && <p role="alert" className="form-error">{errorMessage(neighbors.error)} <Button className="ghost small" onClick={() => { void neighbors.refetch(); }}>Retry</Button></p>}
    {collectionId !== null && collectionId !== undefined && <section className="related-section" data-testid="collection-section"><div className="section-heading"><h2><Link className="collection-heading" to={collectionURL}>{collection?.name ?? 'Collection'}</Link></h2></div>{siblings.length > 0 && <div className="media-grid related-grid">{siblings.map((entry) => <RelatedCard key={entry.id} item={entry} source={{ origin: collectionURL, collectionId, sort: 'UploadedDesc' }}/>)}</div>}</section>}
    {related.length > 0 && <section className="related-section" data-testid="same-tags-section"><div className="section-heading"><h2><Link className="related-heading" to={sameTagsURL}>Same tags</Link></h2></div><div className="media-grid related-grid">{related.map((entry) => <RelatedCard key={entry.id} item={entry} source={relatedSource}/>)}</div></section>}
    {(sameTags.error || members.error) && <p role="alert" className="form-error">{errorMessage(sameTags.error ?? members.error)}</p>}
    {editing && <MetadataEditor items={[media]} onClose={() => setEditing(false)}/>}
    <Modal opened={deleting} onClose={() => { if (!remove.isPending) setDeleting(false); }} title="Delete media?" closeButtonProps={{ 'aria-label': 'Close delete dialog' }} centered closeOnEscape={!remove.isPending} closeOnClickOutside={!remove.isPending} classNames={{ content: 'production-dialog', header: 'production-dialog-header' }}>
      <p>This permanently deletes this media. This cannot be undone.</p>{remove.error && <p className="form-error" role="alert">{errorMessage(remove.error)}</p>}<div className="dialog-actions"><Button className="secondary" disabled={remove.isPending} onClick={() => setDeleting(false)}>Cancel</Button><Button className="danger" disabled={remove.isPending || (remove.error instanceof ApiError && remove.error.status === 412)} onClick={() => remove.mutate({ id, version: media.version })}>{remove.isPending ? 'Deleting…' : 'Delete permanently'}</Button></div>
    </Modal>
  </div>;
}
