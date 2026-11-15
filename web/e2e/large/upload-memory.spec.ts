import {test, expect, type Page} from '@playwright/test';
import {readFileSync, readdirSync, statSync, writeFileSync} from 'node:fs';
import type {Schema} from '../../src/api/client';

interface Manifest {video:string;unsupported:string;maximum:number;companion:number;usefulVideoBytes:number;videoSHA256:string;unsupportedSHA256:string}
interface LoopSample {at:number;delay:number;phase:string}
interface Probe {ticks:number;maxDelay:number;phase:string;timeOrigin:number;samples:LoopSample[];phases:Record<string,{ticks:number;maxDelay:number}>}
declare global {interface Window {__largeMemoryProbe:Probe}}
interface ProcessInfo {parent:number;rss:number;name:string}
function processTable() {
 const table=new Map<number,ProcessInfo>();
 for(const entry of readdirSync('/proc')) {
  if(!/^\d+$/.test(entry))continue;
  try {const text=readFileSync(`/proc/${entry}/status`,'utf8');table.set(Number(entry),{parent:Number(/^PPid:\s+(\d+)/m.exec(text)?.[1]),rss:Number(/^VmRSS:\s+(\d+)/m.exec(text)?.[1]??0)*1024,name:/^Name:\s+(.+)$/m.exec(text)?.[1]??''});}catch{/* Process exited between directory/status reads. */}
 }
 return table;
}
function descendants(table:Map<number,ProcessInfo>,parent:number) {
 const result=new Set<number>();let added=true;
 while(added){added=false;for(const [pid,info] of table)if(!result.has(pid)&&(info.parent===parent||result.has(info.parent))){result.add(pid);added=true;}}
 return [...result];
}
function loopSummary(probe:Probe) {
 const summarize=(samples:LoopSample[])=>{const sorted=samples.map(sample=>sample.delay).sort((a,b)=>a-b);return {count:sorted.length,p95:sorted[Math.max(0,Math.ceil(sorted.length*.95)-1)]??0,p99:sorted[Math.max(0,Math.ceil(sorted.length*.99)-1)]??0,max:sorted.at(-1)??0};};
 const phases=Object.fromEntries(Object.keys(probe.phases).map(phase=>[phase,summarize(probe.samples.filter(sample=>sample.phase===phase))]));
 return {ticks:probe.ticks,timeOrigin:probe.timeOrigin,...summarize(probe.samples),phases,worst:probe.samples.toSorted((a,b)=>b.delay-a.delay).slice(0,10)};
}
async function controls(page:Page) {
 const began=performance.now();const toggle=page.locator('button[aria-controls="sidebar"]');
 const expanded=await toggle.getAttribute('aria-expanded');await toggle.click();
 await expect(toggle).toHaveAttribute('aria-expanded',expanded==='true'?'false':'true');
 await page.getByRole('link',{name:'Library',exact:true}).click();
 const search=page.getByRole('textbox',{name:'Search by tags'});await search.fill('responsiveness-probe');
 await expect(search).toBeFocused();await expect(search).toHaveValue('responsiveness-probe');
 await search.press('Tab');await expect(page.getByRole('button',{name:'Photos',exact:true})).toBeFocused();
 await page.getByRole('button',{name:'Clear search',exact:true}).click();
 await expect(search).toHaveValue('');await page.getByRole('link',{name:'Uploads',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Uploads',exact:true})).toBeVisible();
 return performance.now()-began;
}

test('actual OS File maximum upload stays bounded and responsive',async({page,request,browserName,browser},testInfo)=>{
 test.skip(!process.env.TAG_FILES_BROWSER_LARGE_MANIFEST,'opt-in scripts/check-browser-large only');
 const manifest=JSON.parse(readFileSync(process.env.TAG_FILES_BROWSER_LARGE_MANIFEST!,'utf8')) as Manifest;
 expect(statSync(manifest.video).size).toBe(manifest.maximum);expect(statSync(manifest.unsupported).size).toBe(manifest.companion);
 const appPID=Number(process.env.TAG_FILES_BROWSER_APP_PID);const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 let workers=0;page.on('worker',()=>workers++);
 let created:Schema<'UploadsCreated'>|undefined;let creationError:unknown;
 page.on('response',response=>{if(response.request().method()==='POST'&&new URL(response.url()).pathname==='/api/v1/upload-sessions')void response.json().then(value=>{if(response.ok())created=value as Schema<'UploadsCreated'>;else creationError=new Error(`Create HTTP${response.status()}: ${JSON.stringify(value)}`);}).catch(error=>{creationError=error;});});
 await page.addInitScript(()=>{
  const probe:Probe={ticks:0,maxDelay:0,phase:'startup',timeOrigin:performance.timeOrigin,samples:[],phases:{}};window.__largeMemoryProbe=probe;let last=performance.now();
  setInterval(()=>{const now=performance.now(),delay=Math.max(0,now-last-50);last=now;probe.ticks++;probe.maxDelay=Math.max(probe.maxDelay,delay);const phase=probe.phases[probe.phase]??={ticks:0,maxDelay:0};phase.ticks++;phase.maxDelay=Math.max(phase.maxDelay,delay);if(probe.samples.length<36_000)probe.samples.push({at:now,delay,phase:probe.phase});},50);
 });
 await page.goto('/uploads');await expect(page.getByRole('heading',{name:'Uploads',exact:true})).toBeVisible();
 await controls(page);await page.waitForTimeout(500);await page.evaluate(()=>{window.__largeMemoryProbe.phase='idle';});
 const idleTable=processTable(),initialChildren=descendants(idleTable,process.pid);
 expect(initialChildren.length,'browser must have an observable process tree beneath the Playwright worker').toBeGreaterThan(0);
 const idleRSS=initialChildren.reduce((sum,pid)=>sum+(idleTable.get(pid)?.rss??0),0);
 let peakRSS=idleRSS,peakAppRSS=0,peakTestRSS=process.memoryUsage().rss,samples=0;
 const idleTestRSS=peakTestRSS;const names=new Set(initialChildren.map(pid=>idleTable.get(pid)?.name));
 const monitor=setInterval(()=>{const table=processTable(),children=descendants(table,process.pid);peakRSS=Math.max(peakRSS,children.reduce((sum,pid)=>sum+(table.get(pid)?.rss??0),0));peakAppRSS=Math.max(peakAppRSS,table.get(appPID)?.rss??0);peakTestRSS=Math.max(peakTestRSS,process.memoryUsage().rss);for(const pid of children)names.add(table.get(pid)?.name);samples++;},200);
 const controlTimes:{phase:string;milliseconds:number}[]=[];const progressValues=new Set<number>();const began=performance.now();
 let lastLog=-30_000;
 const progressLog=async(phase:string,states:unknown=null)=>{if(performance.now()-lastLog<30_000)return;lastLog=performance.now();const rows=await page.evaluate(()=>Array.from(document.querySelectorAll('[role="progressbar"]')).map(node=>({name:node.getAttribute('aria-label'),percent:node.getAttribute('aria-valuenow'),state:node.closest('tr')?.getAttribute('data-status')})));console.log('Large browser progress',JSON.stringify({browserName,phase,elapsedSeconds:(performance.now()-began)/1000,rows,states,peakBrowserRSS:peakRSS,peakAppRSS,samples}));};

 try {
  await page.getByTestId('upload-input').setInputFiles([manifest.video,manifest.unsupported]);
  await expect(page.getByRole('dialog',{name:'Upload media'})).toBeVisible();
  await page.evaluate(()=>{window.__largeMemoryProbe.phase='hash';});
  await page.getByRole('button',{name:'Upload',exact:true}).click();
  while(!created){await progressLog('whole-file fingerprint');controlTimes.push({phase:'hash',milliseconds:await controls(page)});if(creationError)throw creationError;expect(performance.now()-began).toBeLessThan(15*60_000);await page.waitForTimeout(1000);}
  console.log('Whole fingerprints accepted',JSON.stringify({browserName,elapsedSeconds:(performance.now()-began)/1000,sessions:created.sessions.map(session=>({id:session.id,size:session.size,partCount:session.partCount}))}));
  expect(created.maxParallelTransfers).toBe(2);expect(created.maxFileSize).toBe(20*1024**3);expect(created.sessions).toHaveLength(2);
  const video=created.sessions.find(row=>row.filename==='padded-video.mp4')!,unsupported=created.sessions.find(row=>row.filename==='unsupported.mp4')!;
  expect(video.size).toBe(manifest.maximum);expect(video.fingerprint).toBe(manifest.videoSHA256);expect(unsupported.fingerprint).toBe(manifest.unsupportedSHA256);
  for(const session of created.sessions){expect(session.partSize).toBe(32*1024**2);expect(session.partCount).toBe(Math.ceil(session.size/session.partSize));}
  await page.evaluate(()=>{window.__largeMemoryProbe.phase='upload';});
  let outcomes:Schema<'UploadOutcome'>[]=[];
  for(;;){
   outcomes=await Promise.all(created.sessions.map(async session=>{const response=await request.get(`/api/v1/upload-sessions/${session.id}`);expect(response.ok()).toBe(true);try{return await response.json() as Schema<'UploadOutcome'>;}finally{await response.dispose();}}));
   if(outcomes.every(outcome=>outcome.state==='ready'||outcome.state==='failed'))break;
   await progressLog('upload-or-verification',outcomes.map(outcome=>({id:outcome.uploadId,state:outcome.state,sessionState:outcome.session?.state,confirmedParts:outcome.session?.parts.length})));
   const progress=page.getByRole('progressbar',{name:'Progress for padded-video.mp4',exact:true});
   if(await progress.count())progressValues.add(Number(await progress.getAttribute('aria-valuenow')));
   controlTimes.push({phase:'upload-or-processing',milliseconds:await controls(page)});
   expect(performance.now()-began).toBeLessThan(25*60_000);await page.waitForTimeout(1000);
  }
  const ready=outcomes.find(outcome=>outcome.uploadId===video.id)!,failed=outcomes.find(outcome=>outcome.uploadId===unsupported.id)!;
  expect(ready.state).toBe('ready');expect(failed.state).toBe('failed');expect(failed.session?.size).toBe(manifest.companion);
  expect(failed.session?.parts).toHaveLength(unsupported.partCount);expect(failed.session?.errorCode).toBeTruthy();
  const item=await (await request.get(`/api/v1/items/${ready.itemId}`)).json() as Schema<'Item'>;
  expect(item.mediaKind).toBe('Video');expect(item.contentUrl).toBeTruthy();
  const head=await request.head(item.contentUrl!);expect(head.status()).toBe(200);expect(Number(head.headers()['content-length'])).toBeGreaterThan(0);
  await expect(page.getByTestId('upload-total')).toHaveText('1');await expect(page.getByTestId('upload-errors')).toHaveText('1');
  const metrics=await (await request.get('/__browser-large/metrics')).json() as {maximum:number;parts:{path:string;length:number;bytes:number;status:number;generation:string;digest:string;start:number;end:number;paced:boolean;error?:string}[]};
  const ownParts=metrics.parts.filter(part=>created!.sessions.some(session=>part.path.includes(session.id)));
  expect(ownParts).toHaveLength(video.partCount+unsupported.partCount);
  for(const part of ownParts){expect(part.status).toBe(200);expect(part.error).toBeUndefined();expect(part.bytes).toBe(part.length);expect(part.length).toBe(32*1024**2);expect(part.generation).toBe('1');expect(part.digest).toMatch(/^[A-Za-z0-9+/]{43}=$/);}
  expect(ownParts.reduce((total,part)=>total+part.bytes,0)).toBe(manifest.maximum+manifest.companion);
  expect(ownParts.some((part,index)=>ownParts.some((other,otherIndex)=>otherIndex!==index&&part.start<other.end&&other.start<part.end)),'two real successfully acknowledged PUT intervals must overlap').toBe(true);
  expect(metrics.maximum).toBe(2);expect([...progressValues].some(value=>value>0&&value<100),'native XHR progress must become visible before completion').toBe(true);
  await page.getByRole('button',{name:'Cancel upload',exact:true}).click();await expect(page.getByTestId('upload-total')).toHaveText('0');
  await expect.poll(async()=>await (await request.get('/api/v1/upload-batches')).json()).toEqual([]);
  const probe=await page.evaluate(()=>window.__largeMemoryProbe);
  expect(workers).toBeGreaterThan(0);expect(errors).toEqual([]);expect(samples).toBeGreaterThan(5);
  expect(peakAppRSS,'app RSS must be observable separately').toBeGreaterThan(0);
  expect(peakRSS-idleRSS,'browser process-tree RSS growth ceiling512MiB').toBeLessThanOrEqual(512*1024**2);
  expect(peakTestRSS-idleTestRSS,'Playwright must not buffer source payloads').toBeLessThanOrEqual(256*1024**2);
  for(const control of controlTimes){expect(control.milliseconds).toBeGreaterThanOrEqual(0);expect(control.milliseconds,'sidebar/native-input/focus/navigation roundtrip ceiling2s').toBeLessThanOrEqual(2000);}
  expect(controlTimes.some(control=>control.phase==='hash')).toBe(true);expect(controlTimes.some(control=>control.phase==='upload-or-processing')).toBe(true);
  const evidence={browserName,browserVersion:browser.version(),appImage:process.env.TAG_FILES_BROWSER_APP_ID,storageImage:process.env.TAG_FILES_BROWSER_STORAGE_ID,
   maximumBytes:manifest.maximum,companionBytes:manifest.companion,usefulVideoBytes:manifest.usefulVideoBytes,sourceSHA256:manifest.videoSHA256,
   idleBrowserTreeRSS:idleRSS,peakBrowserTreeRSS:peakRSS,browserGrowth:peakRSS-idleRSS,peakAppRSS,idleTestRSS,peakTestRSS,samples,
   browserProcesses:[...names],eventLoop:loopSummary(probe),controlTimes,progressValues:[...progressValues],parts:ownParts.length,transferred:manifest.maximum+manifest.companion,maxConcurrentSuccessfulParts:metrics.maximum,elapsedMilliseconds:performance.now()-began,
   finalVideo:ready,retainedUnsupportedBeforeCancel:failed.session?.state,cancelledQueueEmpty:true};
  writeFileSync(testInfo.outputPath('memory-evidence.json'),JSON.stringify(evidence,null,2));
  console.log(JSON.stringify(evidence));
 }finally{
  clearInterval(monitor);
  const eventLoop=await page.evaluate(()=>window.__largeMemoryProbe).catch(()=>null);
  const diagnostic={browserName,browserVersion:browser.version(),idleBrowserTreeRSS:idleRSS,peakBrowserTreeRSS:peakRSS,browserGrowth:peakRSS-idleRSS,peakAppRSS,idleTestRSS,peakTestRSS,samples,browserProcesses:[...names],eventLoop:eventLoop?loopSummary(eventLoop):null,controlTimes,progressValues:[...progressValues],elapsedMilliseconds:performance.now()-began,errors};
  writeFileSync(testInfo.outputPath('process-evidence.json'),JSON.stringify(diagnostic,null,2));
  if(eventLoop)writeFileSync(testInfo.outputPath('event-loop-samples.json'),JSON.stringify(eventLoop));
  console.log('Browser process summary',JSON.stringify({...diagnostic,controlTimes:controlTimes.length}));
 }
});
