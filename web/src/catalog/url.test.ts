import {describe,it,expect} from 'vitest';
import {readLibrary,libraryURL,apiFilter,viewerURL,safeOrigin} from './url';
describe('public library URLs',()=>{
 it('round trips Unicode expression, filters, page, flags and random seed',()=>{
  const state=readLibrary(new URL('http://local/library?q=%D0%9A%D0%BE%D1%82%20%26%26%20!dog&photos=0&videos=0&sort=Random&page=3&seed=stable&collectionId=7'));
  expect(state.q).toBe('Кот && !dog'); expect(state.photos).toBe(false);expect(state.videos).toBe(false);
  expect(readLibrary(new URL(libraryURL(state),'http://local'))).toEqual(state);
  expect(apiFilter(state)).toMatchObject({q:'Кот && !dog',collectionId:7,page:3,seed:'stable'});
  expect(apiFilter(state)).not.toHaveProperty('kind');
 });
 it('does not silently round unsafe IDs/pages or mix saved expressions with query/kind',()=>{
  const state=readLibrary(new URL('http://local/library?page=9007199254740992&collectionId=1e3&savedSearchId=5&q=cat&photos=1&videos=0'));
  expect(state.page).toBe(1);expect(state.collectionId).toBeUndefined();expect(apiFilter(state)).toMatchObject({savedSearchId:5});
  expect(apiFilter(state)).not.toHaveProperty('q');expect(apiFilter(state)).not.toHaveProperty('kind');
 });
 it('restricts Back to local app paths and builds one canonical viewer route',()=>{
  for(const origin of ['https://evil.test','//evil.test','/\\evil.test','javascript:alert(1)','/api/v1/items','/content/7'])expect(safeOrigin(origin)).toBe('/library');
  expect(safeOrigin('/library?q=cat&page=2')).toBe('/library?q=cat&page=2');
  expect(viewerURL(7,'ctx','/library?page=2')).toBe('/content/7?context=ctx&origin=%2Flibrary%3Fpage%3D2');
 });
});
