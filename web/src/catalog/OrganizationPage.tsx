import {useRef,useState} from 'react';
import {Link,useSearchParams} from 'react-router';
import {Modal} from '@mantine/core';
import {useMutation,useQuery} from '@tanstack/react-query';
import {api,unwrap,errorMessage,type Schema} from '../api/client';
import {ImmutableCommand} from '../api/command';
import {tagsQuery,collectionsQuery,categoriesQuery,invalidateLibrary} from '../api/queries';
import {libraryURL,readLibrary} from './url';
import {Button} from '../components/Button';
import {Icon} from '../components/Icon';
import {TagStatisticsTable} from './TagStatisticsTable';

type Kind='tags'|'collections'|'categories';
type Entity=Schema<'Tag'>|Schema<'Collection'>|Schema<'SavedSearch'>;
const base=()=>readLibrary(new URL('/library',location.origin));
function EntityEditor({kind,entity,onClose}:{kind:Kind;entity:Entity|null;onClose:()=>void}){
 const [name,setName]=useState(entity?.name??'');const old=entity&&'queryText'in entity?entity:null;
 const [queryText,setQueryText]=useState(old?.queryText??'');const [mediaKind,setMediaKind]=useState<string>(old?.mediaKind??'');const [confirmDelete,setConfirmDelete]=useState(false);
 const command=useRef(new ImmutableCommand<Schema<'NameInput'>|Schema<'NewSavedSearch'>,unknown>());
 const mutation=useMutation({mutationFn:async(remove:boolean)=>{
  if(kind==='tags'){
   if(entity)return unwrap(await api.PATCH('/api/v1/tags/{id}',{params:{path:{id:entity.id},header:{'If-Match':`"${entity.version}"`}},body:{name}}));
   return command.current.submit({name},async(body,key)=>unwrap(await api.POST('/api/v1/tags',{params:{header:{'Idempotency-Key':key}},body:{name:body.name}})));
  }
  if(kind==='collections'){
   if(entity){const params={path:{id:entity.id},header:{'If-Match':`"${entity.version}"`}};return remove?unwrap(await api.DELETE('/api/v1/collections/{id}',{params})):unwrap(await api.PATCH('/api/v1/collections/{id}',{params,body:{name}}));}
   return command.current.submit({name},async(body,key)=>unwrap(await api.POST('/api/v1/collections',{params:{header:{'Idempotency-Key':key}},body:{name:body.name}})));
  }
  const filter=mediaKind?mediaKind as Schema<'MediaKind'>:null;
  if(entity){
   const params={path:{id:entity.id},header:{'If-Match':`"${entity.version}"`}};
   if(remove)return unwrap(await api.DELETE('/api/v1/saved-searches/{id}',{params}));
   const body:Schema<'SavedSearchPatch'>={name};
   if(queryText!==old?.queryText)body.queryText=queryText;
   if(filter!==old?.mediaKind)body.mediaKind=filter;
   return unwrap(await api.PATCH('/api/v1/saved-searches/{id}',{params,body}));
  }
  return command.current.submit({name,queryText,mediaKind:filter},async(body,key)=>{if(!('queryText'in body))throw new Error('Invalid category command');return unwrap(await api.POST('/api/v1/saved-searches',{params:{header:{'Idempotency-Key':key}},body}));});
 },onSuccess:async()=>{await invalidateLibrary();onClose();}});
 const title=`${entity?'Edit':'New'} ${kind==='categories'?'category':kind==='collections'?'collection':'tag'}`;
 return <Modal opened centered title={title} onClose={()=>{if(!mutation.isPending)onClose();}} styles={{content:{background:'#1b2325'}}}><form onSubmit={e=>{e.preventDefault();mutation.mutate(false);}}><div className="field"><label className="field-label" htmlFor="entity-name">Name</label><input id="entity-name" data-autofocus value={name} required maxLength={kind==='tags'?50:200} disabled={mutation.isPending} onChange={e=>setName(e.target.value)}/></div>{kind==='categories'&&<><div className="field"><label className="field-label" htmlFor="category-query">Query</label><input id="category-query" value={queryText} maxLength={4096} disabled={mutation.isPending} onChange={e=>setQueryText(e.target.value)}/></div><div className="field"><label className="field-label" htmlFor="category-kind">Items type</label><select id="category-kind" value={mediaKind} onChange={e=>setMediaKind(e.target.value)}><option value="">All types</option>{(['Image','Video','Unknown','Text','Audio'] as const).map(kind=><option key={kind}>{kind}</option>)}</select></div></>}{mutation.error&&<p className="form-error" role="alert">{errorMessage(mutation.error)}</p>}{confirmDelete&&<p role="alert">Delete this {kind==='categories'?'category':'collection'}? Media will remain in the library.</p>}<div className="entity-footer">{entity&&kind!=='tags'&&<Button className="danger" disabled={mutation.isPending} onClick={()=>{if(confirmDelete)mutation.mutate(true);else setConfirmDelete(true);}}>{confirmDelete?'Confirm delete':'Delete'}</Button>}<Button disabled={mutation.isPending} onClick={onClose}>Cancel</Button><Button type="submit" className="primary" disabled={mutation.isPending}>{mutation.isPending?'Saving…':'Save'}</Button></div></form></Modal>;
}
export function OrganizationPage({kind}:{kind:Kind}){
 const tags=useQuery({...tagsQuery,enabled:kind==='tags'}),collections=useQuery({...collectionsQuery,enabled:kind==='collections'}),categories=useQuery({...categoriesQuery,enabled:kind==='categories'});
 const stats=useQuery({queryKey:['tag-statistics'],enabled:kind==='tags',queryFn:async({signal})=>unwrap(await api.GET('/api/v1/tag-statistics',{signal}))});
 const [params,setParams]=useSearchParams();const search=params.get('search')??'';
 const [editing,setEditing]=useState<{entity:Entity|null}|null>(null);
 const result=kind==='tags'?tags:kind==='collections'?collections:categories;
 const title=kind==='categories'?'Categories':kind==='collections'?'Collections':'Tags';
 const rows:Entity[]=result.data??[];
 return <><header className="page-header"><h1 className="page-title">{title}</h1><Button label={`New ${kind==='categories'?'category':kind==='collections'?'collection':'tag'}`} className="ghost icon-button" icon="plus" onClick={()=>setEditing({entity:null})}/></header>
 {result.error&&<p role="alert" className="form-error">{errorMessage(result.error)}</p>}
 {kind==='collections'&&<div className="collections-search"><input type="search" aria-label="Search collections" placeholder="Search collections" value={search} onChange={e=>setParams(e.target.value?{search:e.target.value}:{},{replace:true})}/><Button label="Clear collection search" icon="close" className="ghost icon-button" disabled={!search} onClick={()=>setParams({},{replace:true})}/></div>}
 {result.isPending?<p role="status">Loading…</p>:kind==='collections'?<div className="collections-grid">{(collections.data??[]).filter(c=>c.name.toLocaleLowerCase().includes(search.toLocaleLowerCase())).map(c=><article key={c.id} className="organization-card"><Link className="collection-card" to={libraryURL({...base(),collectionId:c.id})}>{c.coverUrl?<img className="collection-cover" src={c.coverUrl} alt="" loading="lazy"/>:<div className="collection-cover collection-cover-empty"><Icon name="folder"/></div>}<h2 className="collection-title">{c.name}</h2></Link><Button label={`Edit ${c.name}`} icon="edit" className="ghost icon-button organization-edit" onClick={()=>setEditing({entity:c})}/></article>)}</div>:kind==='tags'?<>{stats.error&&<p className="form-error" role="alert">{errorMessage(stats.error)}</p>}<TagStatisticsTable statistics={stats.data??[]} tags={tags.data??[]} onEdit={tag=>setEditing({entity:tag})}/></>:<table className="support-table categories-table"><thead><tr><th>Name</th><th>Query</th><th>Items type</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{(categories.data??[]).map(row=><tr key={row.id}><td><Link className="tag support-link" to={libraryURL({...base(),savedSearchId:row.id})}>{row.name}</Link></td><td><code>{row.queryText}</code>{row.validationState!=='valid'&&<span className="muted"> · {row.validationState==='needs_review'?'Needs review':'Unresolved tags'}</span>}</td><td>{row.mediaKind==='Image'?'Photo':row.mediaKind??'All types'}</td><td><Button icon="edit" label={`Edit ${row.name}`} className="ghost icon-button" onClick={()=>setEditing({entity:row})}/></td></tr>)}</tbody></table>}
 {!result.isPending&&<p className="support-total">Total: {rows.length.toLocaleString('en-US')}</p>}
 {editing&&<EntityEditor kind={kind} entity={editing.entity} onClose={()=>setEditing(null)}/>}
 </>;
}
