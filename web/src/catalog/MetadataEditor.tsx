import {useRef,useState} from 'react';
import {Combobox,Modal,useCombobox} from '@mantine/core';
import {useMutation,useQuery} from '@tanstack/react-query';
import {api,errorMessage,unwrap,type Schema} from '../api/client';
import {ImmutableCommand} from '../api/command';
import {collectionsQuery,tagsQuery,invalidateLibrary} from '../api/queries';
import {Button} from '../components/Button';

function SelectField({label,options,value,onSelect,clearable=false}:{label:string;options:{id:number;name:string}[];value:number|null;onSelect:(id:number|null)=>void;clearable?:boolean}){
 const selected=options.find(o=>o.id===value)?.name??'';
 const [draft,setDraft]=useState<string|null>(null);
 const store=useCombobox({onDropdownClose:()=>{setDraft(null);store.resetSelectedOption();}});
 const matches=options.filter(o=>o.name.toLocaleLowerCase().includes((draft??'').toLocaleLowerCase()));
 return <Combobox store={store} onOptionSubmit={id=>{onSelect(Number(id));setDraft(null);store.closeDropdown();}} withinPortal position="bottom-start" zIndex={250}>
  <div className="editor-single-select"><Combobox.Target withExpandedAttribute><input aria-label={label} value={draft??selected} onFocus={()=>{setDraft('');store.openDropdown();}} onClick={()=>store.openDropdown()} onChange={e=>{setDraft(e.target.value);store.openDropdown();store.updateSelectedOptionIndex();}} onBlur={()=>store.closeDropdown()} placeholder="No collection"/></Combobox.Target>{clearable&&value!==null&&<Button label="Clear collection" icon="close" className="ghost icon-button" onMouseDown={e=>e.preventDefault()} onClick={()=>{onSelect(null);setDraft(null);store.closeDropdown();}}/>}</div>
  <Combobox.Dropdown className="production-options"><Combobox.Options mah={220} style={{overflowY:'auto',overscrollBehavior:'contain'}}>{matches.length?matches.map(o=><Combobox.Option key={o.id} value={String(o.id)}>{o.name}</Combobox.Option>):<Combobox.Empty>No matches</Combobox.Empty>}</Combobox.Options></Combobox.Dropdown>
 </Combobox>;
}
function TagField({tags,ids,onChange}:{tags:Schema<'Tag'>[];ids:number[];onChange:(ids:number[])=>void}){
 const [search,setSearch]=useState('');const input=useRef<HTMLInputElement>(null);
 const store=useCombobox();
 const options=tags.filter(t=>!ids.includes(t.id)&&t.name.toLocaleLowerCase().includes(search.toLocaleLowerCase()));
 return <><div className="editor-selected-tags">{ids.map(id=><span key={id} className="selected-tag"><span>{tags.find(t=>t.id===id)?.name??'Unavailable tag'}</span><Button label={`Remove ${tags.find(t=>t.id===id)?.name??'tag'}`} icon="close" className="ghost icon-button" onClick={()=>onChange(ids.filter(v=>v!==id))}/></span>)}</div>
 <Combobox store={store} onOptionSubmit={id=>{onChange([...ids,Number(id)]);setSearch('');store.resetSelectedOption();input.current?.focus();}} withinPortal zIndex={250}>
  <Combobox.Target withExpandedAttribute><input ref={input} data-autofocus aria-label="Add tags" placeholder="Search tags" value={search} onChange={e=>{setSearch(e.target.value);store.openDropdown();store.updateSelectedOptionIndex();}} onFocus={()=>store.openDropdown()} onBlur={()=>store.closeDropdown()}/></Combobox.Target>
  <Combobox.Dropdown className="production-options"><Combobox.Options mah={220} style={{overflowY:'auto',overscrollBehavior:'contain'}}>{options.length?options.map(tag=><Combobox.Option key={tag.id} value={String(tag.id)}>{tag.name}</Combobox.Option>):<Combobox.Empty>No matching tags</Combobox.Empty>}</Combobox.Options></Combobox.Dropdown>
 </Combobox></>;
}
export function MetadataEditor({items,onClose}:{items:Schema<'Item'>[];onClose:()=>void}){
 const opener=useRef(document.activeElement instanceof HTMLElement?document.activeElement:null);
 const close=()=>{onClose();requestAnimationFrame(()=>{if(opener.current?.isConnected)opener.current.focus({preventScroll:true});});};
 const first=items[0]!;const bulk=items.length>1;
 const [tagIds,setTagIds]=useState(bulk?[]:first.tags.map(t=>t.id));
 const [collectionId,setCollectionId]=useState<number|null>(bulk?null:first.collectionId);
 const operation=useRef(new ImmutableCommand<Schema<'BulkMetadata'>,Schema<'BulkCommand'>>());const tags=useQuery(tagsQuery),collections=useQuery(collectionsQuery);
 const save=useMutation({mutationFn:async()=>{
  if(bulk)return operation.current.submit({items:items.map(({id,version})=>({id,version})),tagIds,collectionId},async(body,key)=>unwrap(await api.POST('/api/v1/items/bulk-metadata',{params:{header:{'Idempotency-Key':key}},body})));
  return unwrap(await api.PATCH('/api/v1/items/{id}',{params:{path:{id:first.id},header:{'If-Match':`"${first.version}"`}},body:{tagIds,collectionId}}));
 },onSuccess:async()=>{await invalidateLibrary();close();}});
 return <Modal opened onClose={()=>{if(!save.isPending)close();}} withCloseButton={false} size={590} centered classNames={{content:'production-editor modal editor-modal',body:'production-editor-body'}} closeOnClickOutside={!save.isPending} closeOnEscape={!save.isPending} aria-label={bulk?'Edit selected media':'Edit tags and collection'}>
  <form onSubmit={e=>{e.preventDefault();save.mutate();}}>
   <header className="modal-header"><h2>{bulk?`Tags · ${items.length}`:'Tags'}</h2><Button label="Close editor" className="ghost icon-button" icon="close" disabled={save.isPending} onClick={close}/></header>
   <div className="modal-body">
    {bulk&&<p className="bulk-warning">Replace tags and collection for {items.length} selected items.</p>}
    <fieldset className="field" disabled={save.isPending||!tags.data||!collections.data}><legend className="sr-only">Tags</legend><TagField tags={tags.data??[]} ids={tagIds} onChange={setTagIds}/></fieldset>
    <div className="field"><label className="field-label">Collection</label><SelectField label="Collection" options={collections.data??[]} value={collectionId} onSelect={setCollectionId} clearable/></div>
    {(save.error||tags.error||collections.error)&&<p role="alert" className="form-error">{errorMessage(save.error??tags.error??collections.error)}</p>}
   </div>
   <footer className="modal-footer"><Button className="secondary" disabled={save.isPending} onClick={close}>Cancel</Button><Button type="submit" className="primary" disabled={save.isPending||!tags.data||!collections.data}>{save.isPending?'Saving…':'Save'}</Button></footer>
  </form>
 </Modal>;
}
