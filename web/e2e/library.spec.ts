import {test,expect} from '@playwright/test';

test('library pagination, invalid query, row selection and Cancel preserve real metadata',async({page,request},testInfo)=>{
 await page.goto('/library');
 await expect(page.locator('.media-card')).toHaveCount(24);
 await expect(page.getByText('72 items',{exact:true})).toBeVisible();
 await page.screenshot({path:testInfo.outputPath('library-grid.png')});
 await expect(page.getByText('Legacy text stays private')).toHaveCount(0);
 await page.getByRole('button',{name:'Next',exact:true}).click();
 await expect(page).toHaveURL(/page=2/);
 const firstCard=page.locator('.media-card a').first();
 const before='/content/48?origin=%2Flibrary%3Fpage%3D2';
 await expect(firstCard).toHaveAttribute('href',before);
 await page.getByRole('textbox',{name:'Search by tags'}).fill('cat AND (');
 await page.getByRole('button',{name:'Search',exact:true}).click();
 await expect(page.getByRole('alert')).toBeVisible();
 await expect(page).toHaveURL(/page=2/);
 await expect(firstCard).toHaveAttribute('href',before);
 await page.getByRole('button',{name:'List view'}).click();
 await expect(page.getByRole('checkbox',{name:'Select current page'})).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Edit selected'})).toHaveCount(0);
 const rows=page.locator('tbody tr');
 await rows.nth(0).getByRole('checkbox').check();
 await rows.nth(1).getByRole('checkbox').check();
 await expect(page.getByText('2 selected',{exact:true})).toBeVisible();
 const original=await request.get('/api/v1/items/48');const metadata=await original.json();
 await page.getByRole('button',{name:'Edit selected'}).click();
 await expect(page.getByRole('dialog')).toBeVisible();
 await page.screenshot({path:testInfo.outputPath('bulk-editor.png')});
 await page.getByRole('combobox',{name:'Add tags'}).fill('nature');
 await page.getByRole('option',{name:'nature',exact:true}).click();
 await page.getByRole('button',{name:'Cancel',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(await (await request.get('/api/v1/items/48')).json()).toEqual(metadata);
 await page.getByRole('button',{name:'Clear selection'}).click();
 await expect(page.getByRole('button',{name:'Edit selected'})).toHaveCount(0);
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

test('editor search Enter never saves and an actual pending PATCH freezes every field',async({page,request})=>{
 const original=await (await request.get('/api/v1/items/72')).json();
 await page.goto('/library');await page.getByRole('button',{name:'List view'}).click();
 await page.locator('tbody tr').first().getByRole('button',{name:'Edit media'}).click();
 const writes:string[]=[];let release!:()=>void;
 const pending=new Promise<void>(resolve=>{release=resolve;});
 await page.route('**/api/v1/items/72',async route=>{
  if(route.request().method()!=='PATCH')return route.continue();
  writes.push(route.request().postData()!);
  const response=await route.fetch();await pending;await route.fulfill({response});
 });
 try{
  for(const name of ['Add tags','Collection']){
   const search=page.getByRole('combobox',{name});
   await search.fill('no-match');await search.press('Enter');
   await expect(search).toBeFocused();await expect(page.getByRole('dialog')).toBeVisible();
   expect(writes).toHaveLength(0);
  }
  const tags=page.getByRole('combobox',{name:'Add tags'});
  await tags.fill('nature');await tags.press('Enter');
  await expect(page.getByRole('button',{name:'Remove nature'})).toBeVisible();
  await tags.fill('travel');await tags.press(',');
  await expect(page.getByRole('button',{name:'Remove travel'})).toBeVisible();
  const collection=page.getByRole('combobox',{name:'Collection'});
  await collection.fill('Collection 350');await collection.press('ArrowDown');await collection.press('Enter');
  await expect(collection).toHaveValue('Collection 350');expect(writes).toHaveLength(0);
  await page.getByRole('button',{name:'Save',exact:true}).click();
  await expect(page.getByRole('button',{name:'Saving…'})).toBeDisabled();
  await expect(tags).toBeDisabled();await expect(collection).toBeDisabled();
  for(const name of ['Clear collection','Remove nature','Remove travel','Cancel','Close editor'])await expect(page.getByRole('button',{name,exact:true})).toBeDisabled();
  expect(writes).toHaveLength(1);
  expect(JSON.parse(writes[0]!)).toEqual({tagIds:[...original.tags.map((tag:{id:number})=>tag.id),6,7],collectionId:350});
 }finally{release();}
 await expect(page.getByRole('dialog')).toHaveCount(0);
 await page.unroute('**/api/v1/items/72');
 const updated=await (await request.get('/api/v1/items/72')).json();
 expect(updated.collectionId).toBe(350);
 const restore=await request.patch('/api/v1/items/72',{headers:{'If-Match':`"${updated.version}"`},data:{tagIds:original.tags.map((tag:{id:number})=>tag.id),collectionId:original.collectionId}});
 expect(restore.ok()).toBe(true);
});
