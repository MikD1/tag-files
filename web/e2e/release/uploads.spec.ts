import {test,expect} from '@playwright/test';
import {resolve} from 'node:path';
import type {Schema} from '../../src/api/client';

test('built app uploads through worker/XHR/S3 and freezes metadata while saving',async({page,request,browserName})=>{
 const before=await (await request.get('/api/v1/items')).json() as Schema<'ItemPage'>;
 const consoleErrors:string[]=[];page.on('pageerror',e=>consoleErrors.push(e.message));
 let workers=0,parts=0;page.on('worker',()=>workers++);page.on('request',r=>{if(r.method()==='PUT'&&r.url().includes('/parts/'))parts++;});
 let lostReceipt=false;
 // WebKit's intercepted Blob request is not exposed as postData by Playwright;
 // route.fetch then sends an empty body. Its native XHR path is tested directly.
 if(browserName==='chromium')await page.route('**/api/v1/upload-sessions/*/parts/*',async route=>{
  if(!lostReceipt){lostReceipt=true;const accepted=await route.fetch();expect(accepted.ok()).toBe(true);await route.abort('connectionfailed');}
  else await route.continue();
 });
 await page.goto('/uploads');
 await page.getByTestId('upload-input').setInputFiles(resolve('../app/internal/media/testdata/quadrants.png'));
 await expect(page.getByRole('dialog',{name:'Upload media'})).toBeVisible();
 await page.getByRole('button',{name:'Upload',exact:true}).click();
 await page.getByRole('link',{name:'Library',exact:true}).click();
 await expect.poll(async()=>((await (await request.get('/api/v1/items')).json()) as Schema<'ItemPage'>).totalItems,{timeout:60_000}).toBe(before.totalItems+1);
 const items=await (await request.get('/api/v1/items')).json() as Schema<'ItemPage'>;
 const item=items.items.find(row=>!before.items.some(old=>old.id===row.id))!;
 expect(item.contentUrl).toBeTruthy();expect(item.description).toBeNull();
 expect(workers).toBeGreaterThan(0);expect(parts).toBe(1);expect(lostReceipt).toBe(browserName==='chromium');expect(consoleErrors).toEqual([]);
 await expect(page.locator('.media-card')).toHaveCount(items.totalItems);
 const image=page.locator('.media-card img').first();await expect(image).toBeVisible();
 await expect.poll(()=>image.evaluate(img=>(img as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
 const head=await request.head(item.contentUrl!);expect(head.status()).toBe(200);expect(Number(head.headers()['content-length'])).toBeGreaterThan(32);
 const partial=await request.get(item.contentUrl!,{headers:{Range:'bytes=0-31'}});expect(partial.status()).toBe(206);expect((await partial.body()).length).toBe(32);
 await page.getByRole('link',{name:'Uploads',exact:true}).click();
 await expect(page.getByTestId('upload-total')).toHaveText('0');await expect(page.getByTestId('upload-empty')).toHaveText('Queue is empty');
 expect(await (await request.get('/api/v1/upload-batches')).json()).toEqual([]);

 const tagName=`release-${crypto.randomUUID()}`;
 const createdTag=await request.post('/api/v1/tags',{headers:{'Idempotency-Key':crypto.randomUUID()},data:{name:tagName}});
 expect(createdTag.status()).toBe(201);const {resource:tag}=await createdTag.json() as Schema<'TagCommand'>;
 const createdCollection=await request.post('/api/v1/collections',{headers:{'Idempotency-Key':crypto.randomUUID()},data:{name:tagName}});
 expect(createdCollection.status()).toBe(201);const {resource:collection}=await createdCollection.json() as Schema<'CollectionCommand'>;
 // External fixture writes precede a fresh SPA cache, as normal UI writes would invalidate it.
 await page.goto('/library');
 await page.getByRole('button',{name:'List view'}).click();
 const row=page.locator('tbody tr').first();await row.hover();
 await row.getByRole('button',{name:'Edit media'}).click();
 let writes=0;let release!:()=>void;const pending=new Promise<void>(resolve=>{release=resolve;});
 await page.route(`**/api/v1/items/${item.id}`,async route=>{
  if(route.request().method()!=='PATCH')return route.continue();
  writes++;const response=await route.fetch();await pending;await route.fulfill({response});
 });
 try{
  const tags=page.getByRole('combobox',{name:'Add tags'});
  await tags.fill('unknown-name');await tags.press('Enter');await expect(tags).toBeFocused();expect(writes).toBe(0);
  await tags.fill(tagName);await tags.press('Enter');await expect(page.getByRole('button',{name:`Remove ${tagName}`})).toBeVisible();
  const select=page.getByRole('combobox',{name:'Collection'});
  await select.fill('unknown-name');await select.press('Enter');await expect(select).toBeFocused();expect(writes).toBe(0);
  await select.fill(tagName);await expect(page.getByRole('option',{name:tagName,exact:true})).toBeVisible();
  await select.press('ArrowDown');await select.press('Enter');await expect(select).toHaveValue(tagName);
  await expect(page.getByRole('button',{name:'Clear collection',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Save',exact:true}).click();
  await expect(page.getByRole('button',{name:'Saving…'})).toBeDisabled();
  await expect(tags).toBeDisabled();await expect(select).toBeDisabled();
  for(const name of ['Clear collection',`Remove ${tagName}`,'Cancel','Close editor'])await expect(page.getByRole('button',{name,exact:true})).toBeDisabled();
  expect(writes).toBe(1);
 }finally{release();}
 await expect(page.getByRole('dialog')).toHaveCount(0);
 const updated=await (await request.get(`/api/v1/items/${item.id}`)).json() as Schema<'Item'>;
 expect(updated.tags.map(t=>t.id)).toEqual([tag.id]);expect(updated.collectionId).toBe(collection.id);
 expect(consoleErrors).toEqual([]);
});
