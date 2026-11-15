import {useEffect,useRef,useState,type CSSProperties,type MouseEvent} from 'react';
import {Link,useLocation,useNavigate} from 'react-router';
import {Menu,Tooltip} from '@mantine/core';
import {useQuery} from '@tanstack/react-query';
import {api,unwrap,errorMessage,type Schema} from '../api/client';
import {itemsQuery,queryClient,collectionsQuery,categoriesQuery} from '../api/queries';
import {readLibrary,libraryURL,apiFilter,viewerURL,type LibraryState} from './url';
import {MetadataEditor} from './MetadataEditor';
import {Button} from '../components/Button';
import {Icon} from '../components/Icon';
import type {Preferences} from '../preferences';

const sorts:[Schema<'Sort'>,string,string][]=[['UploadedDesc','Uploaded ↓','history'],['UploadedAsc','Uploaded ↑','history'],['VideoDurationDesc','Duration ↓','clock'],['VideoDurationAsc','Duration ↑','clock'],['ViewCountDesc','Views ↓','eye'],['ViewCountAsc','Views ↑','eye'],['Random','Random','shuffle']];
const defaultState=()=>readLibrary(new URL('/library',location.origin));
export function mediaLabel(item:Schema<'Item'>){return `${item.mediaKind==='Video'?'Video':item.mediaKind==='Image'?'Photo':'Media'}${item.tags.length?': '+item.tags.map(t=>t.name).join(', '):' without tags'}`;}
export function MediaLink({item,origin,state,list=false}:{item:Schema<'Item'>;origin:string;state:LibraryState;list?:boolean}){
 const navigate=useNavigate();const pending=useRef(false);const [error,setError]=useState('');
 const [failedSources,setFailedSources]=useState<Set<string>>(()=>new Set());
 const source=[item.posterUrl,item.mediaKind==='Image'?item.contentUrl:undefined].find(src=>src&&!failedSources.has(src));
 async function open(e:MouseEvent<HTMLAnchorElement>){
  if(e.button!==0||e.metaKey||e.ctrlKey||e.shiftKey||e.altKey)return;
  e.preventDefault();if(pending.current)return;pending.current=true;
  try{
   const {page:_page,pageSize:_pageSize,...filter}=apiFilter(state);void _page;void _pageSize;
   const context=unwrap(await api.POST('/api/v1/navigation-contexts',{params:{header:{'Idempotency-Key':crypto.randomUUID()}},body:{origin,...filter}}));
   await navigate(viewerURL(item.id,context.contextId,origin));
  }catch(err){setError(errorMessage(err));}finally{pending.current=false;}
 }
 return <><Tooltip label={item.tags.map(t=>t.name).join(' · ')} disabled={list||!item.tags.length} interactive multiline maw={480} openDelay={250} closeDelay={160} position="bottom-start" events={{hover:true,focus:true,touch:false}}>
 <Link className={list?'list-thumbnail card-media':'card-media'} to={`/content/${item.id}?origin=${encodeURIComponent(origin)}`} onClick={e=>{void open(e);}} aria-label={mediaLabel(item)} data-item-id={item.id}>
  {source?<img src={source} alt="" loading="lazy" onError={()=>setFailedSources(previous=>new Set([...previous,source]))}/>:<span className="media-placeholder"><Icon name={item.mediaKind==='Video'?'video':'image'}/></span>}
  {item.mediaKind==='Video'&&<span className="video-badge"><Icon name="play"/>{item.durationSeconds!==null&&`${Math.floor(item.durationSeconds/60)}:${String(Math.floor(item.durationSeconds%60)).padStart(2,'0')}`}</span>}
  {item.isFavorite&&<span className="favorite-marker"><Icon name="star"/></span>}
 </Link></Tooltip>{error&&<p role="alert" className="form-error">{error}</p>}</>;
}
export function LibraryPage({preferences,onPreferences}:{preferences:Preferences;onPreferences:(p:Preferences)=>void}){
 const loc=useLocation();const state=readLibrary(new URL(loc.pathname+loc.search,location.origin));
 return <Library key={loc.key} restoreFocus={(loc.state as {restoreItemFocus?:number}|null)?.restoreItemFocus} state={state} preferences={preferences} onPreferences={onPreferences}/>;
}
function Library({state,preferences,onPreferences,restoreFocus}:{restoreFocus?:number|undefined;state:LibraryState;preferences:Preferences;onPreferences:(p:Preferences)=>void}){
 const result=useQuery(itemsQuery(state));const collections=useQuery(collectionsQuery);const categories=useQuery({...categoriesQuery,enabled:state.savedSearchId!==undefined});const navigate=useNavigate();
 const saved=categories.data?.find(category=>category.id===state.savedSearchId);
 const [draft,setDraft]=useState<string|null>(null),[error,setError]=useState(''),[pending,setPending]=useState(false),[selected,setSelected]=useState<number[]>([]),[editing,setEditing]=useState<Schema<'Item'>[]|null>(null);
 const queryDraft=draft??saved?.queryText??state.q;
 const focusRestored=useRef(false);const data=result.data;
 useEffect(()=>{if(data&&restoreFocus!==undefined&&!focusRestored.current){focusRestored.current=true;(document.querySelector<HTMLAnchorElement>(`a[data-item-id="${restoreFocus}"]`)??document.getElementById('workspace'))?.focus({preventScroll:true});}},[restoreFocus,data]);
 const effective={...state,photos:saved?saved.mediaKind==='Image':state.photos,videos:saved?saved.mediaKind==='Video':state.videos,seed:data?.seed??state.seed};const origin=libraryURL(effective);
 const chosen=sorts.find(s=>s[0]===state.sort)!;
 async function search(patch:Partial<LibraryState>={},clearFilters=false){
  const next={...effective,...patch,page:patch.page??1};
  if(patch.q!==undefined&&(patch.q!==saved?.queryText||patch.photos!==undefined||patch.videos!==undefined))delete next.savedSearchId;
  if(clearFilters){delete next.collectionId;delete next.savedSearchId;next.photos=false;next.videos=false;}
  if(next.sort==='Random'&&patch.page===undefined)next.seed=crypto.randomUUID();
  setPending(true);setError('');
  try{await queryClient.fetchQuery(itemsQuery(next));await navigate(libraryURL(next));}catch(err){setError(errorMessage(err));}finally{setPending(false);}
 }
 const pageItems=data?.items??[];
 return <>
  <h1 className={state.collectionId!==undefined||state.savedSearchId!==undefined?'context-title':'sr-only'}>{state.collectionId!==undefined?collections.data?.find(c=>c.id===state.collectionId)?.name??'Collection':state.savedSearchId!==undefined?saved?.name??'Category':'Library'}</h1>
  <section className="search-toolbar" aria-label="Search and filters">
   <form className="search-box" role="search" onSubmit={e=>{e.preventDefault();void search({q:queryDraft});}}><Button type="submit" icon="search" label="Search" disabled={pending||state.savedSearchId!==undefined&&!saved}/><input type="text" aria-label="Search by tags" placeholder="Search by tags" autoComplete="off" spellCheck={false} value={queryDraft} onChange={e=>setDraft(e.target.value)} aria-invalid={!!error} aria-describedby={error?'query-error':undefined}/><div className="search-actions"><div role="group" aria-label="Media types" className="filters"><Button label="Photos" icon="image" aria-pressed={effective.photos} disabled={state.savedSearchId!==undefined&&!saved} onClick={()=>{void search({q:queryDraft,photos:!effective.photos});}}/><Button label="Videos" icon="video" aria-pressed={effective.videos} disabled={state.savedSearchId!==undefined&&!saved} onClick={()=>{void search({q:queryDraft,videos:!effective.videos});}}/></div>
   <Menu width={190} position="bottom-end"><Menu.Target><button type="button" className="button ghost icon-button" aria-label={`Sort: ${chosen[1]}`} title={`Sort: ${chosen[1]}`}><Icon name={chosen[2]}/></button></Menu.Target><Menu.Dropdown className="production-options">{sorts.map(([sort,label,icon])=><Menu.Item key={sort} role="menuitemradio" aria-checked={sort===state.sort} leftSection={<Icon name={icon}/>} onClick={()=>{void search({sort});}}>{label}</Menu.Item>)}</Menu.Dropdown></Menu>
   <Button label="Clear search" icon="close" onClick={()=>{setDraft('');void search({q:''},true);}}/></div></form>
   <div className="library-view-controls">{preferences.view==='grid'&&<div className="density-control" role="group" aria-label="Thumbnail size"><Button icon="plus" label="Larger thumbnails" className="ghost icon-button" disabled={preferences.density<=1} onClick={()=>onPreferences({...preferences,density:preferences.density-1})}/><Button icon="minus" label="Smaller thumbnails" className="ghost icon-button" disabled={preferences.density>=7} onClick={()=>onPreferences({...preferences,density:preferences.density+1})}/></div>}<div className="view-switch" role="group" aria-label="Library view">{(['grid','list'] as const).map(view=><Button key={view} icon={view} label={view==='grid'?'Grid view':'List view'} className="ghost icon-button" aria-pressed={preferences.view===view} onClick={()=>{setSelected([]);onPreferences({...preferences,view});}}/>)}</div></div>
  </section>
  {(error||result.error)&&<p className="form-error" id="query-error" role="alert">{error||errorMessage(result.error)}</p>}
  {preferences.view==='list'&&selected.length>0&&<div className="selection-toolbar"><span>{selected.length} selected</span><Button icon="edit" className="small secondary" onClick={()=>setEditing(pageItems.filter(i=>selected.includes(i.id)))}>Edit selected</Button><Button className="small ghost" onClick={()=>setSelected([])}>Clear selection</Button></div>}
  {result.isPending?<p role="status">Loading…</p>:pageItems.length===0?<section className="empty-state"><Icon name="search"/><h2>{state.q?'No results':'No media yet'}</h2>{state.q?<Button onClick={()=>{setDraft('');void search({q:''},true);}}>Clear search</Button>:<Link to="/uploads" className="button primary">Upload media</Link>}</section>:preferences.view==='grid'?<div className="media-grid" style={{'--card-min':`${[0,360,310,270,220,185,150,120][preferences.density]}px`} as CSSProperties}>{pageItems.map(item=><article key={item.id} className="media-card"><MediaLink item={item} state={effective} origin={origin}/></article>)}</div>:<div className="table-scroll"><table className="data-table media-list"><thead className="sr-only"><tr><th>Media</th><th>Tags</th><th>Collection</th><th>Actions</th></tr></thead><tbody>{pageItems.map(item=><tr key={item.id}><td><MediaLink item={item} state={effective} origin={origin} list/></td><td><div className="tag-list">{item.tags.map(tag=><Link key={tag.id} className="tag" to={libraryURL({...defaultState(),q:tag.name})}>{tag.name}</Link>)}</div></td><td>{item.collectionId!==null&&<Link className="list-collection" to={libraryURL({...defaultState(),collectionId:item.collectionId})}>{collections.data?.find(c=>c.id===item.collectionId)?.name}</Link>}</td><td><div className="list-row-actions"><Button label="Edit media" icon="edit" className="ghost icon-button list-edit" onClick={()=>setEditing([item])}/><label className="list-select-control"><input className="list-select" type="checkbox" aria-label={`Select ${mediaLabel(item)}`} checked={selected.includes(item.id)} onChange={()=>setSelected(ids=>ids.includes(item.id)?ids.filter(id=>id!==item.id):[...ids,item.id])}/></label></div></td></tr>)}</tbody></table></div>}
  {data&&data.totalItems>0&&<footer className="library-footer"><strong>{data.totalItems.toLocaleString('en-US')} items</strong><nav className="pagination" aria-label="Result pages"><span>Page {data.page} of {data.totalPages}</span><Button className="ghost small" disabled={data.page<=1} onClick={()=>{void search({page:data.page-1});}}>Previous</Button>{[...new Set([1,...Array.from({length:5},(_,i)=>data.page+i-2),data.totalPages])].filter(page=>page>=1&&page<=data.totalPages).sort((a,b)=>a-b).map(page=><Button key={page} className={page===data.page?'small primary':'small ghost'} aria-current={page===data.page?'page':undefined} onClick={()=>{void search({page});}}>{page}</Button>)}<Button className="ghost small" disabled={data.page>=data.totalPages} onClick={()=>{void search({page:data.page+1});}}>Next</Button></nav></footer>}
  {editing&&<MetadataEditor items={editing} onClose={()=>setEditing(null)}/>}
 </>;
}
