import {test,expect} from '@playwright/test';

test('category exposes current expression and kind without rebinding an unchanged search',async({page,request})=>{
 const name=`BrowserCategory-${crypto.randomUUID()}`;
 const created=await request.post('/api/v1/saved-searches',{headers:{'Idempotency-Key':crypto.randomUUID()},data:{name,queryText:'cat',mediaKind:'Image'}});
 expect(created.status()).toBe(201);const category=(await created.json()).resource;
 await page.goto(`/library?savedSearchId=${category.id}`);
 await expect(page.getByRole('textbox',{name:'Search by tags'})).toHaveValue('cat');
 await expect(page.getByRole('button',{name:'Photos',exact:true})).toHaveAttribute('aria-pressed','true');
 await expect(page.getByRole('button',{name:'Videos',exact:true})).toHaveAttribute('aria-pressed','false');
 await page.getByRole('button',{name:'Search',exact:true}).click();
 await expect(page).toHaveURL(new RegExp(`savedSearchId=${category.id}(?:$|&)`));
 await page.getByRole('textbox',{name:'Search by tags'}).fill('nature');
 await page.getByRole('button',{name:'Search',exact:true}).click();
 await expect(page).toHaveURL(/q=nature/);expect(page.url()).not.toContain('savedSearchId');
 await expect(page.locator('.media-card')).toHaveCount(10);
});

test('global tag statistics aggregate duplicate names and keep server usage order',async({page,request})=>{
 const statistics=await (await request.get('/api/v1/tag-statistics')).json() as {name:string;usageCount:number}[];
 await page.goto('/tags');await expect(page.locator('tbody tr')).toHaveCount(statistics.length);
 const names=await page.locator('tbody tr td:first-child').allTextContents();expect(names).toEqual(statistics.map(s=>s.name));
 await expect(page.getByRole('link',{name:'cat',exact:true})).toHaveCount(1);
});
