import {chromium, webkit, expect} from '@playwright/test';
import {mkdir, writeFile} from 'node:fs/promises';
import {join} from 'node:path';

const origin = process.env.TAG_FILES_RELEASE_ORIGIN;
const output = process.env.TAG_FILES_OFFLINE_OUTPUT;
if (!origin || !output) throw new Error('Run scripts/check-offline-images');
await mkdir(output, {recursive: true});
const reports = [];
for (const [name, engine] of Object.entries({chromium, webkit})) {
 const browser = await engine.launch();
 try {
  const page = await browser.newPage();
  const errors = [], external = [];
  page.on('pageerror', error => errors.push(error.message));
  page.context().on('request', request => { if (new URL(request.url()).origin !== origin) external.push(request.url()); });
  page.on('response', response => { if (response.status() >= 400) errors.push(`HTTP ${response.status()} ${response.url()}`); });
  const items = [];
  for (const id of [1, 2]) {
   const response = await page.request.get(`${origin}/api/v1/items/${id}`);
   expect(response.ok()).toBe(true);
   const item = await response.json();
   expect(item.id).toBe(id);
   await page.goto(`${origin}/content/${id}`);
   const image = page.getByTestId('viewer-image');
   await expect(image).toBeVisible();
   await expect(image).toHaveAttribute('src', item.contentUrl);
   const decoded = await image.evaluate(async img => { await img.decode(); return {width: img.naturalWidth, height: img.naturalHeight, source: img.currentSrc}; });
   expect(decoded.width).toBeGreaterThan(0);
   expect(decoded.height).toBeGreaterThan(0);
   await page.screenshot({path: join(output, `${name}-restored-viewer-${id}.png`)});
   items.push({id, contentUrl: item.contentUrl, decoded});
  }
  expect(errors).toEqual([]);
  expect(external).toEqual([]);
  reports.push({browser: name, version: browser.version(), items, errors, external});
 } finally { await browser.close(); }
}
await writeFile(join(output, 'viewer.json'), JSON.stringify(reports, null, 2) + '\n');
console.log('Both browsers decoded both retained canonical viewer deep links after restart');
