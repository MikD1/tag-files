import type {Schema} from '../api/client';
export interface LibraryState {q:string;photos:boolean;videos:boolean;sort:Schema<'Sort'>;page:number;seed:string;collectionId?:number;savedSearchId?:number}
const sorts:Schema<'Sort'>[]=['UploadedDesc','UploadedAsc','VideoDurationDesc','VideoDurationAsc','ViewCountDesc','ViewCountAsc','Random'];
export function positiveID(raw:string|null|undefined):number|undefined {
 if(!raw || !/^[1-9][0-9]*$/.test(raw))return undefined;
 const value=Number(raw);return Number.isSafeInteger(value)?value:undefined;
}
export function readLibrary(url:URL):LibraryState {
 const p=url.searchParams;
 const state:LibraryState={q:p.get('q')??'',photos:p.get('photos')==='1',videos:p.get('videos')==='1',sort:sorts.find(s=>s===p.get('sort'))??'UploadedDesc',page:positiveID(p.get('page'))??1,seed:p.get('seed')??''};
 const collection=positiveID(p.get('collectionId')),saved=positiveID(p.get('savedSearchId'));
 if(collection!==undefined)state.collectionId=collection;if(saved!==undefined)state.savedSearchId=saved;
 return state;
}
export function apiFilter(s:LibraryState){
 const filter:{q?:string;kind?:Schema<'MediaKind'>;sort:Schema<'Sort'>;page:number;pageSize:number;seed?:string;collectionId?:number;savedSearchId?:number}={sort:s.sort,page:s.page,pageSize:24};
 if(s.savedSearchId!==undefined)filter.savedSearchId=s.savedSearchId;
 else {if(s.q)filter.q=s.q;if(s.photos!==s.videos)filter.kind=s.photos?'Image':'Video';}
 if(s.collectionId!==undefined)filter.collectionId=s.collectionId;
 if(s.sort==='Random' && s.seed)filter.seed=s.seed;
 return filter;
}
export function libraryURL(s:LibraryState):string {
 const p=new URLSearchParams();
 if(s.q)p.set('q',s.q);if(s.photos)p.set('photos','1');if(s.videos)p.set('videos','1');
 if(s.sort!=='UploadedDesc')p.set('sort',s.sort);if(s.page>1)p.set('page',String(s.page));
 if(s.seed)p.set('seed',s.seed);if(s.collectionId!==undefined)p.set('collectionId',String(s.collectionId));if(s.savedSearchId!==undefined)p.set('savedSearchId',String(s.savedSearchId));
 return '/library'+(p.size?'?'+p.toString():'');
}
export function safeOrigin(raw:string|null|undefined):string {
 if(!raw || raw.includes('\\') || !/^\/(library|collections|tags|categories)(\?|\/|$)/.test(raw))return '/library';
 return raw;
}
export function viewerURL(id:number,context:string,origin:string):string {
 return `/content/${id}?`+new URLSearchParams({context,origin:safeOrigin(origin)}).toString();
}
