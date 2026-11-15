import {QueryClient,queryOptions} from '@tanstack/react-query';
import {api,unwrap} from './client';
import {apiFilter,type LibraryState} from '../catalog/url';
export const queryClient=new QueryClient({defaultOptions:{queries:{staleTime:15_000,retry:1,refetchOnWindowFocus:false},mutations:{retry:false}}});
export const itemsQuery=(state:LibraryState)=>queryOptions({queryKey:['items',apiFilter(state)],queryFn:async({signal})=>unwrap(await api.GET('/api/v1/items',{params:{query:apiFilter(state)},signal})),retry:false});
export const itemQuery=(id:number)=>queryOptions({queryKey:['item',id],queryFn:async({signal})=>unwrap(await api.GET('/api/v1/items/{id}',{params:{path:{id}},signal}))});
export const tagsQuery=queryOptions({queryKey:['tags'],queryFn:async({signal})=>unwrap(await api.GET('/api/v1/tags',{signal}))});
export const collectionsQuery=queryOptions({queryKey:['collections'],queryFn:async({signal})=>unwrap(await api.GET('/api/v1/collections',{signal}))});
export const categoriesQuery=queryOptions({queryKey:['categories'],queryFn:async({signal})=>unwrap(await api.GET('/api/v1/saved-searches',{signal}))});
export async function invalidateLibrary(){await Promise.all(['items','item','collections','tags','categories','tag-statistics'].map(key=>queryClient.invalidateQueries({queryKey:[key]})));}
