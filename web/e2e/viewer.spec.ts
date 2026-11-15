import {test,expect} from '@playwright/test';

test('one viewer keeps context, transforms, three-second views and Back focus',async({page,request},testInfo)=>{
 await page.goto('/library?sort=UploadedAsc&page=2');
 const first=page.locator('.media-card a').first();await expect(first).toBeVisible();
 const link=await first.getAttribute('href');const id=Number(link!.match(/content\/(\d+)/)![1]);
 const before=await (await request.get(`/api/v1/items/${id}`)).json();let viewRequests=0;
 page.on('request',r=>{if(r.method()==='POST'&&r.url().endsWith(`/items/${id}/views`))viewRequests++;});
 await first.click();await expect(page).toHaveURL(new RegExp(`/content/${id}\\?context=`));
 await expect(page.getByTestId('viewer-image')).toBeVisible();
 const height=await page.getByTestId('viewer-stage').evaluate(e=>e.getBoundingClientRect().height);
 expect(Math.abs(height-0.8*await page.evaluate(()=>window.innerHeight))).toBeLessThan(2);
 await expect(page.getByTestId('image-tools-panel')).not.toBeVisible();
 await page.getByTestId('image-tools-toggle').click();
 await page.getByRole('button',{name:'Zoom in',exact:true}).click();
 await page.getByRole('button',{name:'Rotate 90°',exact:true}).click();
 await expect(page.getByTestId('viewer-image')).toHaveAttribute('style',/scale\(1.25\) rotate\(90deg\)/);
 await page.keyboard.press('Escape');await expect(page.getByTestId('image-tools-panel')).not.toBeVisible();
 await expect(page.getByTestId('image-tools-toggle')).toBeFocused();
 await page.getByRole('button',{name:'Edit tags and collection',exact:true}).click();
 await expect(page.getByRole('dialog')).toBeVisible();await page.getByRole('button',{name:'Cancel',exact:true}).click();
 await expect(page.getByRole('button',{name:'Edit tags and collection',exact:true})).toBeFocused();
 await expect.poll(async()=>(await (await request.get(`/api/v1/items/${id}`)).json()).viewCount,{timeout:5000}).toBe(before.viewCount+1);
 expect(viewRequests).toBe(1);
 await page.screenshot({path:testInfo.outputPath('viewer.png')});
 await page.getByTestId('viewer-stage').focus();await page.keyboard.press('ArrowRight');
 await expect(page).toHaveURL(new RegExp(`/content/${id+1}\\?context=`));
 await page.getByRole('link',{name:'Back to results',exact:true}).click();
 await expect(page).toHaveURL(/\/library\?sort=UploadedAsc&page=2$/);
 await expect(page.locator(`a[data-item-id="${id+1}"]`)).toBeFocused();
});

test('list Edit returns focus and Clear removes collection/type context while keeping sort',async({page})=>{
 await page.goto('/library?collectionId=1&photos=1&sort=UploadedAsc');
 await page.getByRole('button',{name:'List view'}).click();
 const edit=page.getByRole('button',{name:'Edit media',exact:true}).first();
 await edit.focus();await edit.click();await page.getByRole('button',{name:'Cancel',exact:true}).click();
 await expect(edit).toBeFocused();
 await page.getByRole('button',{name:'Clear search',exact:true}).click();
 await expect(page).toHaveURL(/\/library\?sort=UploadedAsc$/);
 await expect(page.getByRole('button',{name:'Photos',exact:true})).toHaveAttribute('aria-pressed','false');
 await expect(page.getByRole('button',{name:'Videos',exact:true})).toHaveAttribute('aria-pressed','false');
 await page.getByRole('button',{name:'Grid view'}).click();
 for(let n=0;n<3;n++)await page.getByRole('button',{name:'Smaller thumbnails'}).click();
 await expect(page.getByRole('button',{name:'Smaller thumbnails'})).toBeDisabled();
 for(let n=0;n<6;n++)await page.getByRole('button',{name:'Larger thumbnails'}).click();
 await expect(page.getByRole('button',{name:'Larger thumbnails'})).toBeDisabled();
});
