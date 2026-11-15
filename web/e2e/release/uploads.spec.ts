import {test,expect} from '@playwright/test';
import {resolve} from 'node:path';
import type {Schema} from '../../src/api/client';

test('built app uploads through real worker/XHR and S3 while navigating away',async({page,request,browserName})=>{
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
});
