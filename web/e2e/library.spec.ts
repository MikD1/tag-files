import {test,expect} from '@playwright/test';

test('library pagination, invalid query, page selection and Cancel preserve real metadata',async({page,request},testInfo)=>{
 await page.goto('/library');
 await expect(page.locator('.media-card')).toHaveCount(24);
 await expect(page.getByText('72 items',{exact:true})).toBeVisible();
 await page.screenshot({path:testInfo.outputPath('library-grid.png')});
 await expect(page.getByText('Legacy text stays private')).toHaveCount(0);
 await page.getByRole('button',{name:'Next',exact:true}).click();
 await expect(page).toHaveURL(/page=2/);
 const before=await page.locator('.media-card a').first().getAttribute('href');
 await page.getByRole('textbox',{name:'Search by tags'}).fill('cat AND (');
 await page.getByRole('button',{name:'Search',exact:true}).click();
 await expect(page.getByRole('alert')).toBeVisible();
 await expect(page).toHaveURL(/page=2/);
 expect(await page.locator('.media-card a').first().getAttribute('href')).toBe(before);
 await page.getByRole('button',{name:'List view'}).click();
 await page.getByRole('checkbox',{name:'Select current page'}).check();
 await expect(page.getByText('24 selected',{exact:true})).toBeVisible();
 const original=await request.get('/api/v1/items/48');const metadata=await original.json();
 await page.getByRole('button',{name:'Edit selected'}).click();
 await expect(page.getByRole('dialog')).toBeVisible();
 await page.screenshot({path:testInfo.outputPath('bulk-editor.png')});
 await page.getByRole('combobox',{name:'Add tags'}).fill('nature');
 await page.getByRole('option',{name:'nature',exact:true}).click();
 await page.getByRole('button',{name:'Cancel',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(await (await request.get('/api/v1/items/48')).json()).toEqual(metadata);
 await page.getByRole('button',{name:'Collapse sidebar'}).click();
 await expect(page.locator('.sidebar')).toHaveCSS('width','72px');
 await page.reload();
 await expect(page.getByRole('button',{name:'List view'})).toHaveAttribute('aria-pressed','true');
 await expect(page.getByRole('button',{name:'Expand sidebar'})).toBeVisible();
 await expect(page.locator('tbody tr')).toHaveCount(24);
 await page.screenshot({path:testInfo.outputPath('library-list-rail.png')});
});

test('collection search reaches all 350 records and organization CRUD uses real HTTP',async({page})=>{
 await page.goto('/collections');
 await expect(page.locator('.organization-card')).toHaveCount(350);
 await page.getByRole('searchbox',{name:'Search collections'}).fill('350');
 await expect(page.locator('.organization-card')).toHaveCount(1);
 await page.getByRole('link',{name:'Collection 350',exact:true}).click();
 await expect(page).toHaveURL(/collectionId=350/);
 await expect(page.getByRole('heading',{name:'Collection 350'})).toBeVisible();
 await page.getByRole('link',{name:'Tags',exact:true}).click();
 await page.getByRole('button',{name:'New tag',exact:true}).click();
 const name=`Browser-${crypto.randomUUID().slice(0,16)}`;
 await page.getByRole('textbox',{name:'Name',exact:true}).fill(name);
 await page.getByRole('button',{name:'Save',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 await expect(page.getByRole('link',{name,exact:true})).toBeVisible();
 await page.getByRole('button',{name:`Edit ${name}`,exact:true}).click();
 await page.getByRole('textbox',{name:'Name',exact:true}).fill(name+'-renamed');
 await page.getByRole('button',{name:'Save',exact:true}).click();
 await expect(page.getByRole('link',{name:name+'-renamed',exact:true})).toBeVisible();
});

test('bulk Save replaces both metadata fields on precisely the selected rows',async({page,request})=>{
 const originals=await Promise.all([24,23].map(async id=>(await request.get(`/api/v1/items/${id}`)).json()));
 await page.goto('/library?page=3');
 await page.getByRole('button',{name:'List view'}).click();
 const rows=page.locator('tbody tr');await expect(rows).toHaveCount(24);
 await rows.nth(0).getByRole('checkbox').check();await rows.nth(1).getByRole('checkbox').check();
 await page.getByRole('button',{name:'Edit selected'}).click();
 await page.getByRole('combobox',{name:'Add tags'}).fill('nature');
 await page.getByRole('option',{name:'nature',exact:true}).click();
 await page.getByRole('combobox',{name:'Collection',exact:true}).fill('350');
 await page.getByRole('option',{name:'Collection 350',exact:true}).click();
 await page.getByRole('button',{name:'Save',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 for(const original of originals){
  const updated=await (await request.get(`/api/v1/items/${original.id}`)).json();
  expect(updated.tags.map((t:{id:number})=>t.id)).toEqual([6]);expect(updated.collectionId).toBe(350);
  expect(updated.version).toBe(original.version+1);
  const restore=await request.patch(`/api/v1/items/${original.id}`,{headers:{'If-Match':`"${updated.version}"`},data:{tagIds:original.tags.map((t:{id:number})=>t.id),collectionId:original.collectionId}});
  expect(restore.ok()).toBe(true);
 }
});
