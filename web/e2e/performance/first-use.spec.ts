import {test, expect, type Page} from '@playwright/test';
import {readFileSync, writeFileSync, mkdirSync} from 'node:fs';
import {join} from 'node:path';

interface Scenario {name:string;query:string;expectedIDs:number[];total:number}
interface Sample {
 phase:'cold-context'|'warm-navigation'|'warm-search';scenario:string;index:number;
 startedAt:string;endedAt:string;timeOrigin:number;startMs:number;cardsMs:number;
 decodedMs:number;usefulMs:number;elapsedMs:number;firstDecodedID:number;imageURL:string;
 expectedIDs:number[];actualIDs:number[];
}

// Runs in the browser once per animation frame. It stops when all expected
// first-page cards are present and ONE intersecting thumbnail has decoded and
// reached a subsequent rendering opportunity; it does not wait for all media.
async function useful(page:Page, scenario:Scenario, startMs:number) {
 return page.evaluate(async ({expectedIDs,startMs})=>{
 let cardsMs:number|undefined;
 async function observe(){
  const cards=Array.from(document.querySelectorAll<HTMLAnchorElement>('a.card-media[data-item-id]'));
  const actualIDs=cards.map(card=>Number(card.dataset.itemId));
  if(actualIDs.length!==expectedIDs.length||actualIDs.some((id,index)=>id!==expectedIDs[index]))return false;
  cardsMs??=performance.now();
  for(const card of cards){
   const img=card.querySelector('img');if(!img||!img.complete||img.naturalWidth===0)continue;
   const rect=img.getBoundingClientRect();
   if(rect.width<=0||rect.height<=0||rect.bottom<=0||rect.right<=0||rect.top>=innerHeight||rect.left>=innerWidth)continue;
   const style=getComputedStyle(img);if(style.visibility==='hidden'||style.display==='none')continue;
   await img.decode();const decodedMs=performance.now();
   await new Promise<void>(resolve=>requestAnimationFrame(()=>resolve()));
   // A delayed image decode must still belong to the matching visible result.
   if(!img.isConnected) return false;
   const usefulMs=performance.now();
   return {timeOrigin:performance.timeOrigin,startMs,cardsMs,decodedMs,usefulMs,elapsedMs:usefulMs-startMs,firstDecodedID:Number(card.dataset.itemId),imageURL:img.currentSrc,actualIDs};
  }
  return false;
 }
 const deadline=performance.now()+10_000;
 while(performance.now()<deadline){
  const result=await observe();if(result)return result;
  await new Promise<void>(resolve=>requestAnimationFrame(()=>resolve()));
 }
 throw new Error('No matching useful cards and decoded visible thumbnail within10s');
 },{expectedIDs:scenario.expectedIDs,startMs});
}

function distribution(samples:Sample[]){
 const values=samples.map(s=>s.elapsedMs).sort((a,b)=>a-b);
 return {n:values.length,p50Ms:values[Math.ceil(values.length*0.50)-1],p95Ms:values[Math.ceil(values.length*0.95)-1],maxMs:values.at(-1)};
}

test('growth catalog first useful rendering through the actual app and private S3',async({page,request},info)=>{
 const root=process.env.TAG_FILES_BROWSER_PERFORMANCE_WORK!;
 const fixture=JSON.parse(readFileSync(join(root,'fixture.json'),'utf8')) as {scenarios:Scenario[]};
 const output=process.env.TAG_FILES_BROWSER_PERFORMANCE_OUTPUT!;
 const samples:Sample[]=[],errors:string[]=[];
 const n=Number(process.env.TAG_FILES_BROWSER_PERFORMANCE_SAMPLES??20);
 expect(Number.isInteger(n)&&n>=5&&n<=100).toBe(true);
 expect(new Set(fixture.scenarios.map(scenario=>JSON.stringify(scenario.expectedIDs))).size).toBe(fixture.scenarios.length);
 page.on('pageerror',error=>errors.push(error.message));
 page.on('response',response=>{if(response.status()>=400)errors.push(`HTTP${response.status()} ${response.url()}`);});
 page.on('requestfailed',request=>{if(request.failure()?.errorText!=='net::ERR_ABORTED'&&request.failure()?.errorText!=='cancelled')errors.push(`${request.failure()?.errorText} ${request.url()}`);});
 // Check responses against independently generated fixture membership and order.
 for(const scenario of fixture.scenarios){
  const response=await request.get('/api/v1/items',{params:{q:scenario.query,sort:'UploadedDesc',page:1,pageSize:24}});
  expect(response.ok()).toBe(true);
  const body=await response.json();expect(body.items.map((item:{id:number})=>item.id)).toEqual(scenario.expectedIDs);expect(body.totalItems).toBe(scenario.total);
 }
 async function navigation(scenario:Scenario,phase:Sample['phase'],index:number){
  const startedAt=new Date().toISOString();
  await page.goto(`/library${scenario.query?'?'+new URLSearchParams({q:scenario.query}):''}`,{waitUntil:'commit'});
  const result=await useful(page,scenario,0);
  samples.push({...result,phase,scenario:scenario.name,index,startedAt,endedAt:new Date().toISOString(),expectedIDs:scenario.expectedIDs});
 }
 function save(){
  mkdirSync(output,{recursive:true});
  const summaries=fixture.scenarios.flatMap(scenario=>['warm-navigation','warm-search'].map(phase=>({scenario:scenario.name,phase,...distribution(samples.filter(s=>s.phase===phase&&s.scenario===scenario.name))}))).filter(row=>row.n>0);
  const report={browser:info.project.name,browserVersion:page.context().browser()?.version(),viewport:page.viewportSize(),nPerScenario:n,fixture,images:JSON.parse(readFileSync(join(root,'images.json'),'utf8')),method:'navigation timeOrigin or browser search-action performance.now through exact first-page card IDs and one decoded visible thumbnail plus next requestAnimationFrame; no all-images/networkidle wait',samples,summaries,errors};
  writeFileSync(join(output,`${info.project.name}-samples.json`),JSON.stringify(report,null,2)+'\n');
  return summaries;
 }
 try {
  await navigation(fixture.scenarios[0]!,'cold-context',0);
  // Warm the actual SPA, API, browser cache and each first-page image set.
  for(const scenario of fixture.scenarios)await navigation(scenario,'warm-navigation',-1);
  samples.splice(1); // Retain the separate cold record, omit warmup observations.
  for(let index=0;index<n;index++)for(const scenario of fixture.scenarios)await navigation(scenario,'warm-navigation',index);
  // Exercise actual search submission in the same warmed SPA session, including
  // its normal Query cache. Filling the field is preparation; Enter starts timing.
  for(let index=0;index<n;index++)for(const scenario of fixture.scenarios){
   expect(await page.locator('a.card-media[data-item-id]').evaluateAll(cards=>cards.map(card=>Number((card as HTMLElement).dataset.itemId)))).not.toEqual(scenario.expectedIDs);
   await page.getByRole('textbox',{name:'Search by tags'}).fill(scenario.query);
   const startedAt=new Date().toISOString();
   const startMs=await page.evaluate(()=>performance.now());
   await page.getByRole('textbox',{name:'Search by tags'}).press('Enter');
   await page.waitForURL(url=>(url.searchParams.get('q')??'')===scenario.query);
   const result=await useful(page,scenario,startMs);
   samples.push({...result,phase:'warm-search',scenario:scenario.name,index,startedAt,endedAt:new Date().toISOString(),expectedIDs:scenario.expectedIDs});
  }
  await page.screenshot({path:join(output,`${info.project.name}-first-use.png`)});
  const summaries=save();console.log(JSON.stringify({browser:info.project.name,cold:samples[0],summaries,errors}));
  expect(errors).toEqual([]);
  for(const sample of samples.filter(sample=>sample.phase!=='cold-context'))expect(sample.elapsedMs,`${sample.phase}/${sample.scenario}/${sample.index}`).toBeLessThanOrEqual(2000);
 } finally { save(); }
});
