export interface Preferences {view:'grid'|'list';density:number;sidebarOpen:boolean}
export const defaultPreferences:Preferences={view:'grid',density:4,sidebarOpen:true};
type StorageLike=Pick<Storage,'getItem'|'setItem'>;
const key='tag-files.preferences';
export function readPreferences(storage:StorageLike=localStorage):Preferences {
 try{
  const p:unknown=JSON.parse(storage.getItem(key)??'null');
  if(!p || typeof p!=='object' || !('version' in p) || p.version!==1)return {...defaultPreferences};
  const value=p as Partial<Preferences>;
  return {view:value.view==='list'?'list':'grid',density:Number.isInteger(value.density)&&value.density!>=1&&value.density!<=7?value.density!:4,sidebarOpen:typeof value.sidebarOpen==='boolean'?value.sidebarOpen:true};
 }catch{return {...defaultPreferences};}
}
export function savePreferences(value:Preferences,storage:StorageLike=localStorage):void {try{storage.setItem(key,JSON.stringify({version:1,...value}));}catch{/* Preferences never prevent API workflows. */}}
