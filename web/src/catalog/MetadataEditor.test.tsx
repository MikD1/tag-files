import {it,expect,vi,afterEach} from 'vitest';
import {render,screen,waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {MantineProvider} from '@mantine/core';
import {QueryClientProvider,QueryClient} from '@tanstack/react-query';
import {MetadataEditor} from './MetadataEditor';
import type {Schema} from '../api/client';

const item:Schema<'Item'>={id:1,mediaKind:'Image',description:null,uploadedAt:'2026-01-01T00:00:00Z',tags:[{id:1,name:'cat',version:1}],collectionId:1,isFavorite:false,viewCount:0,lastViewAt:null,durationSeconds:null,processingState:'ready',version:7};
afterEach(()=>vi.unstubAllGlobals());
function setup(){
 const writes:Request[]=[];
 vi.stubGlobal('fetch',vi.fn(async(request:Request)=>{
  const path=new URL(request.url,'http://local').pathname;
  if(request.method==='PATCH' || request.method==='POST'){writes.push(request);return Response.json({type:'about:blank',title:'Conflict',status:412,code:'stale_version',traceId:crypto.randomUUID(),detail:'Reload before saving'},{status:412});}
  return Response.json(path.endsWith('/tags')?[{id:1,name:'cat',version:1},{id:2,name:'cat',version:1},{id:3,name:'犬',version:1}]:[{id:1,name:'First',version:1,itemCount:1},{id:2,name:'Second',version:1,itemCount:0}]);
 }));
 const close=vi.fn();const cache=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<MantineProvider env="test" forceColorScheme="dark"><QueryClientProvider client={cache}><MetadataEditor items={[item]} onClose={close}/></QueryClientProvider></MantineProvider>);
 return {writes,close};
}
it('keeps ID chips stable with duplicate labels and Cancel performs no write',async()=>{
 const {writes,close}=setup();const user=userEvent.setup();
 const search=await screen.findByRole('combobox',{name:'Add tags'});
 await user.click(search);await user.type(search,'cat');
 await user.click(await screen.findByRole('option',{name:'cat'}));
 expect(screen.getAllByRole('button',{name:'Remove cat'})).toHaveLength(2);
 expect(search).toHaveFocus();
 await user.click(screen.getByRole('button',{name:'Cancel'}));expect(close).toHaveBeenCalledOnce();expect(writes).toHaveLength(0);
});
it('sends tags and one collection atomically with If-Match and preserves draft on failure',async()=>{
 const {writes,close}=setup();const user=userEvent.setup();
 const search=await screen.findByRole('combobox',{name:'Collection'});
 await user.click(search);await user.clear(search);await user.type(search,'Second');await user.click(await screen.findByRole('option',{name:'Second'}));
 await user.click(screen.getByRole('button',{name:'Save'}));
 await waitFor(()=>expect(writes).toHaveLength(1));expect(writes[0]!.headers.get('If-Match')).toBe('"7"');
 expect(await writes[0]!.clone().json()).toEqual({tagIds:[1],collectionId:2});
 expect(await screen.findByRole('alert')).toHaveTextContent('Reload before saving');expect(screen.getByRole('combobox',{name:'Collection'})).toHaveValue('Second');expect(close).not.toHaveBeenCalled();
});

it('gives corrected bulk metadata a new key after a definitive validation rejection',async()=>{
 const writes:{key:string;body:Schema<'BulkMetadata'>}[]=[];
 vi.stubGlobal('fetch',async(request:Request)=>{
  if(request.method==='POST'){
   const key=request.headers.get('Idempotency-Key')!;const body=await request.json() as Schema<'BulkMetadata'>;
   const prior=writes.find(w=>w.key===key);writes.push({key,body});
   if(prior&&JSON.stringify(prior.body)!==JSON.stringify(body))return Response.json({title:'Key conflict',status:409},{status:409});
   if(body.tagIds.length)return Response.json({title:'Tag was removed',status:422},{status:422});
   return Response.json({operationId:key,itemIds:[1,2]});
  }
  return Response.json([]);
 });
 const cache=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity},mutations:{retry:false}}});
 cache.setQueryData(['tags'],[{id:2,name:'removed',version:1}]);cache.setQueryData(['collections'],[]);
 const close=vi.fn();
 render(<MantineProvider env="test" theme={{components:{Popover:{defaultProps:{hideDetached:false}}}}}><QueryClientProvider client={cache}><MetadataEditor items={[item,{...item,id:2}]} onClose={close}/></QueryClientProvider></MantineProvider>);
 const user=userEvent.setup();
 await user.click(screen.getByRole('combobox',{name:'Add tags'}));await user.click(await screen.findByRole('option',{name:'removed'}));
 await user.click(screen.getByRole('button',{name:'Save'}));expect(await screen.findByRole('alert')).toHaveTextContent('Tag was removed');
 await user.click(screen.getByRole('button',{name:'Remove removed'}));await user.click(screen.getByRole('button',{name:'Save'}));
 await waitFor(()=>expect(close).toHaveBeenCalledOnce());
 expect(writes).toHaveLength(2);expect(writes[0]!.body.tagIds).toEqual([2]);expect(writes[1]!.body.tagIds).toEqual([]);expect(writes[1]!.key).not.toBe(writes[0]!.key);
});

it('resolves the original bulk save after a lost response before sending any edited replacement',async()=>{
 const writes:{key:string;body:Schema<'BulkMetadata'>}[]=[];
 let lookupOffline=true;
 vi.stubGlobal('fetch',async(request:Request)=>{
  const path=new URL(request.url).pathname;
  if(request.method==='POST'){
   writes.push({key:request.headers.get('Idempotency-Key')!,body:await request.json() as Schema<'BulkMetadata'>});
   throw new TypeError('successful reply lost');
  }
  if(path.startsWith('/api/v1/operations/')){
   if(lookupOffline)throw new TypeError('lookup unavailable');
   return Response.json({operationId:writes[0]!.key,state:'completed',httpStatus:200,result:{operationId:writes[0]!.key,itemIds:[1,2]},createdAt:new Date().toISOString(),expiresAt:new Date(Date.now()+604800000).toISOString()});
  }
  return Response.json([]);
 });
 const cache=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity},mutations:{retry:false}}});
 cache.setQueryData(['tags'],[{id:2,name:'original',version:1}]);cache.setQueryData(['collections'],[]);
 const close=vi.fn();
 render(<MantineProvider env="test" theme={{components:{Popover:{defaultProps:{hideDetached:false}}}}}><QueryClientProvider client={cache}><MetadataEditor items={[item,{...item,id:2}]} onClose={close}/></QueryClientProvider></MantineProvider>);
 const user=userEvent.setup();
 await user.click(screen.getByRole('combobox',{name:'Add tags'}));await user.click(await screen.findByRole('option',{name:'original'}));
 await user.click(screen.getByRole('button',{name:'Save'}));expect(await screen.findByRole('alert')).toHaveTextContent(/uncertain|resolve/i);
 await user.click(screen.getByRole('button',{name:'Remove original'}));lookupOffline=false;await user.click(screen.getByRole('button',{name:'Save'}));
 await waitFor(()=>expect(close).toHaveBeenCalledOnce());
 expect(writes).toHaveLength(1);expect(writes[0]!.body.tagIds).toEqual([2]);
});
