/** Browser acceptance checks for the synthetic UX prototype, never the backend.
 * Run: LD_LIBRARY_PATH=... FONTCONFIG_FILE=... node docs/rewrite/prototype/qa/check.mjs
 * PLAYWRIGHT_MODULE may override the existing global Playwright installation.
 * TF_QA_ONLY=12 runs a selected case while diagnosing a failure; omitted = all cases.
 */
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || '/usr/local/share/mise/installs/npm-playwright/1.65.0/node_modules/playwright');
const directory = path.dirname(fileURLToPath(import.meta.url));
const prototype = path.dirname(directory);
const target = pathToFileURL(path.join(prototype, 'index.html')).href;
const artifactBytes = await readFile(path.join(prototype, 'index.html'));
const only = new Set((process.env.TF_QA_ONLY || '').split(',').filter(Boolean).map(Number));
const report = {
  target, startedAt: new Date().toISOString(), browser: null,
  fixture: '426 synthetic records; public stock photographs and silent H.264 test clip',
  artifact: { bytes: artifactBytes.length, sha256: createHash('sha256').update(artifactBytes).digest('hex') },
  selection: { mode: only.size ? 'selected' : 'all', requestedCases: [...only].sort((a, b) => a - b) },
  viewports: only.size && !only.has(15) ? [1440] : [1440, 1024, 640], externalRequests: [], pageErrors: [], tests: [], screenshots: [],
};
await mkdir(directory, { recursive: true });
let browser;
let page;
const cleanError = (value) => String(value).replace(/data:[^\s"']+/g, '[data URI omitted]').slice(0, 4500);
const wait = (ms) => page.waitForTimeout(ms);
const state = () => page.evaluate(() => window.TFDemo.getState());
const item = async (id = 1) => (await state()).items.find((entry) => entry.id === id);
const metadata = (entry) => ({ tagIds: entry.tagIds, collectionId: entry.collectionId });
const uploadStorage = () => page.evaluate(() => JSON.parse(localStorage.getItem(Object.keys(localStorage).find((key) => key.startsWith('tagfiles-ui-demo-')))));
const cards = () => page.getByTestId('media-card');
const cardIds = () => cards().evaluateAll((elements) => elements.map((element) => Number(element.dataset.id)));
const visibleTotal = async () => Number((await page.getByTestId('results-total').innerText()).replace(/\D/g, ''));
async function waitRoute(type, id) {
  await page.waitForFunction(({ type, id }) => {
    const route = window.TFDemo?.getState().route;
    return route?.type === type && (id === undefined || route.id === id);
  }, { type, id });
}
async function reset() {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.evaluate(() => window.TFDemo.reset());
  await waitRoute('library');
  await page.getByTestId('tag-query').waitFor();
  await page.evaluate(() => window.scrollTo(0, 0));
}
async function search(query) {
  await page.getByTestId('tag-query').fill(query);
  await page.getByTestId('apply-search').click();
  await page.waitForFunction((query) => window.TFDemo.getState().route.context?.q === query, query);
}
async function openItem(id = 1) {
  await page.locator(`[data-testid="media-card"][data-id="${id}"] [data-content-link]`).first().click();
  await waitRoute('content', id);
}
async function editorTag(id, selected = true) {
  const remove = page.getByTestId('editor-tag-remove-' + id);
  if (!selected) { await remove.click(); return; }
  if (await remove.count()) return;
  const name = await page.evaluate((id) => window.TFDemoFixtures.tags.find((tag) => tag.id === id).name, id);
  await page.getByTestId('editor-tag-search').fill(name);
  await page.getByTestId('editor-tag-option-' + id).click();
}
async function editorCollection(id) {
  if (id === null) { await page.getByTestId('editor-collection-clear').click(); return; }
  const name = await page.evaluate((id) => window.TFDemoFixtures.collections.find((collection) => collection.id === id).name, id);
  await page.getByTestId('editor-collection-search').fill(name);
  await page.getByTestId('editor-collection-option-' + id).click();
}
const editorRects = () => page.getByRole('dialog').evaluate((dialog) => [dialog, dialog.querySelector('.modal-header'), dialog.querySelector('.modal-footer')].map((element) => { const rect = element.getBoundingClientRect(); return [rect.x, rect.y, rect.width, rect.height]; }));
async function editorPopupFit(kind, label) {
  const bounds = await page.evaluate((kind) => {
    const input = document.querySelector('[data-testid="editor-' + kind + '-search"]').getBoundingClientRect();
    const list = document.querySelector('[data-testid="editor-' + kind + '-options"]');
    const popup = list.getBoundingClientRect();
    return { position: getComputedStyle(list).position, input: { left: input.left, right: input.right, top: input.top, bottom: input.bottom }, popup: { left: popup.left, right: popup.right, top: popup.top, bottom: popup.bottom, width: popup.width, height: popup.height }, viewport: { width: innerWidth, height: innerHeight } };
  }, kind);
  assert.equal(bounds.position, 'fixed', label + ': popup must stay outside the editor flow');
  assert.ok(bounds.popup.width > 0 && bounds.popup.height > 0 && bounds.popup.left >= -1 && bounds.popup.right <= bounds.viewport.width + 1 && bounds.popup.top >= -1 && bounds.popup.bottom <= bounds.viewport.height + 1, label + ': popup is clipped');
  assert.ok(Math.abs(bounds.popup.left - bounds.input.left) <= 1 && Math.abs(bounds.popup.right - bounds.input.right) <= 1, label + ': popup must align with the field');
  assert.ok(bounds.popup.top >= bounds.input.bottom - 1 || bounds.popup.bottom <= bounds.input.top + 1, label + ': popup must be below or above its field');
}
async function imagesLoaded() {
  await page.waitForFunction(() => [...document.images].filter((image) => {
    const rect = image.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0 && rect.bottom > 0 && rect.top < innerHeight;
  }).every((image) => image.complete && image.naturalWidth > 0));
}
async function screenshot(name, fullPage = false) {
  await imagesLoaded();
  // Capture the resting surface after transient reset feedback has disappeared.
  await page.locator('#toast-root .toast').waitFor({ state: 'detached' });
  await page.screenshot({ path: path.join(directory, name), fullPage, animations: 'disabled' });
  if (!report.screenshots.includes(name)) report.screenshots.push(name);
}
async function noHorizontalOverflow(label) {
  const dimensions = await page.evaluate(() => ({
    viewport: innerWidth, document: document.documentElement.scrollWidth, body: document.body.scrollWidth,
  }));
  assert.ok(dimensions.document <= dimensions.viewport + 1 && dimensions.body <= dimensions.viewport + 1,
    `${label}: horizontal document overflow ${JSON.stringify(dimensions)}`);
}
async function noUploadHistory() {
  assert.equal(await page.locator('[data-status="ready"], .summary-tile.ready, [data-testid="open-ready"]').count(), 0);
  assert.doesNotMatch(await page.locator('#workspace').innerText(), /\bReady\b|Open added media|Upload history/);
}
async function searchAndResultsLayout(label, width = 1440) {
  await page.mouse.move(210, 10);
  const geometry = await page.evaluate(() => {
    const form = document.querySelector('#search-form');
    const rectangle = (element) => { const bounds = element.getBoundingClientRect(); return { left: bounds.left, right: bounds.right, top: bounds.top, bottom: bounds.bottom, width: bounds.width, height: bounds.height }; };
    const controls = ['kind-image', 'kind-video', 'sort-filter', 'clear-filters'].map((id) => {
      const element = document.querySelector('[data-testid="' + id + '"]');
      const style = getComputedStyle(element);
      return { id, inside: form.contains(element), ...rectangle(element), borderRadius: style.borderRadius, borderWidth: style.borderWidth, background: style.backgroundColor };
    });
    const outside = [...document.querySelectorAll('[data-action="view-grid"], [data-action="view-list"], [data-testid="thumbnail-larger"], [data-testid="thumbnail-smaller"]')]
      .map((element) => ({ inside: form.contains(element), ...rectangle(element) }));
    const content = document.querySelector('.media-grid, .media-list, .empty-state');
    const total = document.querySelector('[data-testid="results-total"]');
    return { form: rectangle(form), controls, outside, content: rectangle(content), total: rectangle(total), headerCount: Boolean(total.closest('.search-toolbar, #search-form')) };
  });
  for (const control of geometry.controls) {
    assert.equal(control.inside, true, label + ': ' + control.id + ' must be inside the common search frame');
    assert.equal(control.width, 32);
    assert.equal(control.height, 32);
  }
  const base = geometry.controls[0];
  for (const control of geometry.controls.slice(1)) assert.deepEqual([control.borderRadius, control.borderWidth, control.background], [base.borderRadius, base.borderWidth, base.background], label + ': search controls must share the same idle style');
  for (const control of geometry.outside) {
    assert.equal(control.inside, false, label + ': view and sizing controls must remain outside the form');
    if (width >= 1024) assert.ok(control.left >= geometry.form.right - 1, label + ': outside controls must be to the right of the search frame');
  }
  assert.equal(geometry.headerCount, false);
  assert.ok(geometry.total.top >= geometry.content.bottom - 1, label + ': matching count must follow the result content');
  return { common32pxSearchControls: true, outsideViewControls: true, matchingCountBelowContent: true };
}
async function contentFirstLayout(label) {
  const geometry = await page.evaluate(() => {
    const rectangle = (selector) => {
      const element = document.querySelector(selector);
      if (!element) return null;
      const rect = element.getBoundingClientRect();
      const computed = getComputedStyle(element);
      if (!rect.width || !rect.height || computed.visibility === 'hidden' || computed.display === 'none') return null;
      return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right, width: rect.width };
    };
    const workspace = document.querySelector('#workspace');
    const style = getComputedStyle(workspace);
    return {
      stage: rectangle('[data-testid="viewer-stage"]'),
      secondary: rectangle('[data-testid="viewer-secondary"]'),
      controls: rectangle('.viewer-controls'),
      navigation: rectangle('[data-testid="viewer-navigation"]'),
      neighbors: rectangle('[data-testid="viewer-navigation"] .viewer-nav'),
      back: rectangle('[data-testid="back-to-library"]'),
      tools: rectangle('[data-testid="image-tools-toggle"]'),
      tags: rectangle('[data-testid="viewer-secondary"] .tag-list'),
      collection: rectangle('[data-testid="collection-heading"]'),
      sameTags: rectangle('[data-testid="same-tags-section"]'),
      edit: rectangle('[data-testid="edit-item"]'),
      favorite: rectangle('[data-testid="favorite-toggle"]'),
      visiblePermanentControls: [...document.querySelectorAll('[data-testid="viewer-secondary"] a.button, [data-testid="viewer-secondary"] button, [data-testid="viewer-secondary"] summary')]
        .filter((element) => element.getBoundingClientRect().width > 0 && element.getBoundingClientRect().height > 0).length,
      organizingActionsShareNavigation: Boolean(document.querySelector('[data-testid="viewer-navigation"] [data-testid="edit-item"]') && document.querySelector('[data-testid="viewer-navigation"] [data-testid="favorite-toggle"]')),
      collectionDuplicatedUnderTags: Boolean(document.querySelector('[data-testid="viewer-secondary"] .collection-link, [data-testid="viewer-secondary"] [data-testid="collection-heading"]')),
      availableWidth: workspace.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight),
    };
  });
  assert.ok(geometry.stage?.width > 0, label + ': canonical media stage is missing');
  assert.ok(Math.abs(geometry.stage.width - geometry.availableWidth) <= 4, label + ': media does not occupy the available workspace width');
  for (const part of ['secondary', 'navigation', 'neighbors', 'back', 'tools', 'tags', 'collection', 'edit', 'favorite']) {
    assert.ok(geometry[part], label + ': missing ' + part);
    assert.ok(geometry[part].top >= geometry.stage.bottom - 1, label + ': ' + part + ' is beside or over the media');
  }
  assert.equal(geometry.controls, null, label + ': image tools must stay collapsed by default');
  assert.ok(geometry.visiblePermanentControls <= 6, label + ': more than six permanent viewer controls');
  assert.ok(Math.abs((geometry.neighbors.left + geometry.neighbors.right) / 2 - (geometry.navigation.left + geometry.navigation.right) / 2) <= 4, label + ': previous/counter/next group is not centered');
  assert.ok(geometry.favorite.right <= geometry.neighbors.left + 1 && geometry.tools.left >= geometry.neighbors.right - 1, label + ': navigation controls are not grouped left/center/right');
  assert.equal(geometry.organizingActionsShareNavigation, true, label + ': edit/favorite must be grouped in the navigation row');
  assert.ok(geometry.edit.left >= geometry.back.right - 1 && geometry.edit.left - geometry.back.right <= 18, label + ': edit is separated from Back');
  assert.ok(geometry.favorite.left >= geometry.edit.right - 1 && geometry.favorite.left - geometry.edit.right <= 18, label + ': favorite is separated from Edit');
  for (const part of ['edit', 'favorite']) assert.ok(Math.abs((geometry.back.top + geometry.back.bottom) / 2 - (geometry[part].top + geometry[part].bottom) / 2) <= 4, label + ': Back/Edit/Favorite are not on one line');
  assert.ok(geometry.tags.top >= geometry.navigation.bottom - 1, label + ': tags must be separate below navigation');
  assert.ok(geometry.collection.top >= geometry.tags.bottom - 1, label + ': collection must follow tags');
  assert.equal(geometry.collectionDuplicatedUnderTags, false, label + ': collection link must not be duplicated under tags');
  assert.ok(geometry.sameTags && geometry.collection.top < geometry.sameTags.top, label + ': collection section must precede Same tags');
  return { mediaWidth: Math.round(geometry.stage.width), availableWidth: Math.round(geometry.availableWidth), secondaryAndActionsBelowMedia: true, visiblePermanentControls: geometry.visiblePermanentControls, toolsCollapsedByDefault: true, centeredNavigation: true, backEditFavoriteTogether: true, tagsBelowNavigation: true, collectionHeadingBeforeSameTags: true, duplicateCollectionUnderTags: false };
}
async function imageToolsFit(label) {
  const bounds = await page.evaluate(() => {
    const panel = document.querySelector('[data-testid="image-tools-panel"]').getBoundingClientRect();
    const media = document.querySelector('[data-testid="viewer-stage"]').getBoundingClientRect();
    return { left: panel.left, right: panel.right, top: panel.top, width: panel.width, mediaBottom: media.bottom, viewport: innerWidth };
  });
  assert.ok(bounds.width > 0 && bounds.left >= -1 && bounds.right <= bounds.viewport + 1, label + ': image-tools panel is clipped horizontally');
  assert.ok(bounds.top >= bounds.mediaBottom - 1, label + ': image-tools panel overlays the media');
  return { panelFits: true, panelBelowMedia: true };
}
async function test(number, name, run) {
  if (only.size && !only.has(number)) return;
  const started = Date.now();
  try {
    await reset();
    const evidence = await run();
    report.tests.push({ number, name, status: 'PASS', durationMs: Date.now() - started, evidence });
    console.log(`PASS ${String(number).padStart(2, '0')} ${name}`);
  } catch (error) {
    const result = { number, name, status: 'FAIL', durationMs: Date.now() - started, error: cleanError(error.stack || error) };
    report.tests.push(result);
    console.error(`FAIL ${String(number).padStart(2, '0')} ${name}: ${cleanError(error.message || error)}`);
    try { await screenshot(`failure-${String(number).padStart(2, '0')}.png`); } catch { /* Preserve original failure. */ }
  }
}

try {
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage', '--allow-file-access-from-files'] });
  report.browser = browser.version();
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'en-US', reducedMotion: 'reduce', offline: true });
  await context.route('**/*', async (route) => {
    const url = route.request().url();
    if (/^https?:/i.test(url)) {
      const parsed = new URL(url);
      report.externalRequests.push(parsed.origin + parsed.pathname); // Never persist URL query credentials.
      await route.abort();
    }
    else await route.continue();
  });
  page = await context.newPage();
  page.setDefaultTimeout(6500);
  page.on('pageerror', (error) => report.pageErrors.push(cleanError(error.message)));
  await page.goto(target, { waitUntil: 'load' });
  await page.waitForFunction(() => Boolean(window.TFDemo?.getState));

  await test(1, 'Library total, bounded page and local loaded photographs', async () => {
    assert.equal((await state()).items.length, 426);
    assert.equal(await visibleTotal(), 426);
    assert.equal(await cards().count(), 24);
    assert.deepEqual((await cardIds()).slice(0, 4), [1, 2, 3, 4]);
    await imagesLoaded();
    assert.equal(await page.locator('video').count(), 0, 'Library must not preload every video');
    assert.equal(await page.getByTestId('open-uploads').count(), 0, 'Gallery must not include an Upload/Add shortcut');
    assert.equal(await page.locator('#workspace .search-help, #workspace input[type="range"]').count(), 0);
    assert.equal(await page.locator('#sidebar a[href="#/uploads"]').isVisible(), true);
    const layout = await searchAndResultsLayout('grid');
    const embeddedBytes = await page.evaluate(() => Object.values(window.TFDemoFixtures.assets).reduce((sum, uri) => sum + atob(uri.split(',')[1]).length, 0) + atob(window.TFDemoFixtures.videoSrc.split(',')[1]).length);
    assert.ok(embeddedBytes < 2_000_000, `Unexpected large demo media: ${embeddedBytes}`);
    await screenshot('library.png');
    const toggle = page.getByTestId('sidebar-toggle');
    assert.deepEqual(await page.locator('#sidebar a.nav-link').evaluateAll((links) => links.map((link) => [link.getAttribute('href'), link.textContent.replace(/\d+/g, '').trim()])), [['#/library', 'Library'], ['#/collections', 'Collections'], ['#/tags', 'Tags'], ['#/categories', 'Categories'], ['#/uploads', 'Uploads']]);
    assert.equal((await state()).prefs.sidebarOpen, true);
    assert.equal(await toggle.getAttribute('aria-controls'), 'sidebar');
    assert.equal(await toggle.getAttribute('aria-expanded'), 'true');
    assert.equal(await toggle.getAttribute('aria-label'), 'Collapse sidebar');
    assert.equal(await toggle.evaluate((button) => Boolean(button.closest('#sidebar'))), false);
    assert.equal(await page.locator('#demo-reset').count(), 0);
    const uploads = page.locator('#sidebar a[href="#/uploads"]');
    const pinned = await uploads.boundingBox();
    assert.ok(pinned.y > 900 && pinned.y + pinned.height <= 1000, 'Uploads must be pinned to the viewport bottom');
    await page.evaluate(() => scrollTo(0, 400));
    assert.equal(Math.round((await uploads.boundingBox()).y), Math.round(pinned.y));
    await page.evaluate(() => scrollTo(0, 0));
    await screenshot('sidebar-open.png');
    const inputNode = await page.getByTestId('tag-query').elementHandle();
    const cardNode = await cards().first().elementHandle();
    const openWidth = (await page.locator('#workspace').boundingBox()).width;
    await page.getByTestId('tag-query').fill('unsubmitted query');
    await toggle.focus(); await page.keyboard.press('Enter');
    assert.equal(await toggle.getAttribute('aria-expanded'), 'false');
    assert.equal(await toggle.getAttribute('aria-label'), 'Expand sidebar');
    assert.equal(await page.locator('#sidebar').isVisible(), true);
    assert.equal((await page.locator('#sidebar').boundingBox()).width, 72);
    assert.ok((await page.locator('#workspace').boundingBox()).width > openWidth + 120, 'Collapsing the sidebar must release its label width');
    assert.equal(await page.locator('#sidebar .nav-brand').isVisible(), false);
    assert.equal(await page.locator('#sidebar a.nav-link > span:not(.nav-count)').evaluateAll((labels) => labels.every((label) => !label.getClientRects().length)), true, 'Rail labels must be hidden');
    await toggle.focus();
    for (const name of ['Library', 'Collections', 'Tags', 'Categories', 'Uploads']) {
      await page.keyboard.press('Tab');
      assert.equal(await page.locator('#sidebar a.nav-link[aria-label="' + name + '"]').evaluate((link) => link === document.activeElement), true, name + ' must remain in native tab order');
    }
    const railPinned = await uploads.boundingBox();
    assert.ok(railPinned.y > 900 && railPinned.y + railPinned.height <= 1000);
    await page.evaluate(() => scrollTo(0, 400));
    assert.equal(Math.round((await uploads.boundingBox()).y), Math.round(railPinned.y));
    await page.evaluate(() => scrollTo(0, 0));
    assert.equal(await inputNode.evaluate((node) => node.isConnected && node.value === 'unsubmitted query'), true);
    assert.equal(await cardNode.evaluate((node) => node.isConnected), true);
    assert.equal((await state()).route.context.q, '');
    await screenshot('sidebar-closed.png');
    await page.reload({ waitUntil: 'load' }); await page.getByTestId('tag-query').waitFor();
    assert.equal((await state()).prefs.sidebarOpen, false);
    assert.equal((await page.locator('#sidebar').boundingBox()).width, 72);
    for (const [href, route, name] of [['library', 'library', 'Library'], ['collections', 'collections', 'Collections'], ['tags', 'tags', 'Tags'], ['categories', 'saved-searches', 'Categories'], ['uploads', 'uploads', 'Uploads']]) {
      const link = page.locator('#sidebar a.nav-link[href="#/' + href + '"]');
      assert.equal(await link.getAttribute('title'), name);
      assert.equal(await link.getAttribute('aria-label'), name);
      await link.click(); await waitRoute(route);
      assert.equal(await link.getAttribute('aria-current'), 'page');
      assert.equal((await state()).prefs.sidebarOpen, false);
      assert.equal((await page.locator('#sidebar').boundingBox()).width, 72);
    }
    await toggle.focus(); await page.keyboard.press('Space');
    assert.equal((await state()).prefs.sidebarOpen, true);
    await page.locator('#sidebar a.nav-link[href="#/library"]').click(); await waitRoute('library');
    await page.locator('[data-action="view-list"]').click(); await page.locator('.media-list').waitFor();
    await cards().first().hover(); await cards().first().locator('input[data-select]').check();
    const checkedNode = await cards().first().locator('input[data-select]').elementHandle();
    await page.getByTestId('tag-query').fill('another draft');
    await toggle.click(); await toggle.click();
    assert.equal(await checkedNode.evaluate((node) => node.isConnected && node.checked), true);
    assert.equal(await page.getByTestId('tag-query').inputValue(), 'another draft');
    await page.reload({ waitUntil: 'load' }); await page.locator('.media-list').waitFor();
    assert.equal((await state()).prefs.sidebarOpen, true);
    return { total: 426, rendered: 24, embeddedMediaBytes: embeddedBytes, layout, sidebar: { defaultOpen: true, manual72pxRail: true, allFiveTabsClickableAndFocusable: true, keyboardToggle: true, pinnedUploads: true, draftAndSelectionPreserved: true, preferenceSurvivesReloadAndRoutes: true } };
  });

  await test(2, 'Pagination exposes results after 300 and returns to page one', async () => {
    await page.getByTestId('next-page').click();
    await page.waitForFunction(() => window.TFDemo.getState().route.context.page === 2);
    assert.deepEqual((await cardIds()).slice(0, 3), [25, 26, 27]);
    await page.getByTestId('page-18').click();
    await page.waitForFunction(() => window.TFDemo.getState().route.context.page === 18);
    assert.equal(await cards().count(), 18);
    assert.deepEqual((await cardIds()).slice(0, 3), [409, 410, 411]);
    assert.equal((await cardIds()).at(-1), 426);
    assert.equal(await page.getByTestId('next-page').isDisabled(), true);
    await page.getByTestId('page-1').click();
    await page.waitForFunction(() => window.TFDemo.getState().route.context.page === 1);
    assert.equal((await cardIds())[0], 1);
    return { lastPage: 18, lastId: 426 };
  });

  await test(3, 'Exact tag AND/NOT search returns the correct subset and accepts Unicode input', async () => {
    await search('mountains && !city');
    assert.equal(await visibleTotal(), 151);
    assert.deepEqual((await cardIds()).slice(0, 6), [1, 2, 8, 9, 11, 13]);
    assert.equal((await state()).route.context.page, 1);
    assert.match(decodeURIComponent(new URL(page.url()).hash), /mountains/);
    await search('Пример');
    assert.equal((await state()).route.context.q, 'Пример');
    assert.notEqual(await page.getByTestId('tag-query').getAttribute('aria-invalid'), 'true');
    assert.equal(await visibleTotal(), 0);
    return { query: 'mountains && !city', matching: 151, unrestrictedUnicodeInput: 'Пример' };
  });

  await test(4, 'Invalid query preserves results; empty result and clear are distinct', async () => {
    await search('mountains && !city');
    const ids = await cardIds();
    const url = page.url();
    await page.getByTestId('tag-query').fill('mountains &&');
    await page.getByTestId('apply-search').click();
    assert.equal(await page.getByTestId('tag-query').getAttribute('aria-invalid'), 'true');
    assert.ok((await page.locator('#query-error').innerText()).trim().length > 0, 'Invalid query needs a concise actionable error');
    assert.equal(page.url(), url);
    assert.deepEqual(await cardIds(), ids);
    await search('missing-tag');
    assert.equal(await visibleTotal(), 0);
    assert.equal(await cards().count(), 0);
    assert.equal(await page.locator('.empty-state h2').isVisible(), true);
    await searchAndResultsLayout('empty results');
    await page.locator('[data-action="clear-search"]').first().click();
    await page.waitForFunction(() => window.TFDemo.getState().route.context.q === '');
    assert.equal(await visibleTotal(), 426);
    for (const expected of [{ click: 'kind-image', kind: 'image', total: 355 }, { click: 'kind-video', kind: '', total: 426 }, { click: 'kind-image', kind: 'video', total: 71 }]) {
      await page.getByTestId(expected.click).click();
      await page.waitForFunction((expected) => window.TFDemo.getState().route.context.kind === expected.kind, expected);
      assert.equal(await visibleTotal(), expected.total);
    }
    await page.getByTestId('clear-filters').click();
    await page.waitForFunction(() => !window.TFDemo.getState().route.context.imageSelected && !window.TFDemo.getState().route.context.videoSelected);
    assert.equal(await visibleTotal(), 426);
    return { invalidPreservesPrevious: true, empty: 0, cleared: 426 };
  });

  await test(5, 'Seven sort-menu choices work; list/grid, density and random order survive reload', async () => {
    const sortValues = ['uploadedDesc', 'uploadedAsc', 'durationDesc', 'durationAsc', 'viewsDesc', 'viewsAsc', 'random'];
    const trigger = page.getByTestId('sort-filter');
    const sortLabelText = await trigger.evaluate((element) => {
      const copy = element.cloneNode(true);
      copy.querySelectorAll('svg, [aria-hidden="true"]').forEach((node) => node.remove());
      return copy.textContent.trim();
    });
    assert.equal(sortLabelText, '', 'Sort trigger must use icons without a visible text label');
    assert.equal(await trigger.getAttribute('aria-haspopup'), 'menu');
    for (const value of sortValues) {
      await trigger.click();
      await page.getByRole('menu').waitFor();
      assert.deepEqual(await page.getByRole('menuitemradio').evaluateAll((nodes) => nodes.map((node) => node.dataset.sort)), sortValues);
      await page.locator('[role="menuitemradio"][data-sort="' + value + '"]').click();
      await page.waitForFunction((value) => window.TFDemo.getState().route.context.sort === value, value);
      assert.equal(await page.getByRole('menu').isVisible(), false);
      assert.match(await trigger.getAttribute('aria-label'), /^Sort:/);
      if (value === 'uploadedDesc') assert.equal((await cardIds())[0], 1);
      if (value === 'uploadedAsc') assert.equal((await cardIds())[0], 426);
      await trigger.click();
      assert.deepEqual(await page.getByRole('menuitemradio').evaluateAll((nodes) => nodes.filter((node) => node.getAttribute('aria-checked') === 'true').map((node) => node.dataset.sort)), [value]);
      await page.keyboard.press('Escape');
      assert.equal(await page.getByRole('menu').isVisible(), false);
      await page.waitForFunction(() => document.activeElement === document.querySelector('[data-testid="sort-filter"]'));
    }
    const randomIds = await cardIds();
    assert.notDeepEqual(randomIds, Array.from({ length: 24 }, (_, i) => i + 1));
    await page.reload({ waitUntil: 'load' });
    await page.getByTestId('sort-filter').waitFor();
    assert.equal((await state()).route.context.sort, 'random');
    assert.deepEqual(await cardIds(), randomIds);
    const larger = page.getByTestId('thumbnail-larger');
    const smaller = page.getByTestId('thumbnail-smaller');
    const initialWidth = (await cards().first().boundingBox()).width;
    await larger.click();
    assert.equal((await state()).prefs.density, 3);
    assert.ok((await cards().first().boundingBox()).width > initialWidth, 'Plus must enlarge thumbnails');
    assert.equal(await larger.evaluate((element) => document.activeElement === element), true);
    while ((await state()).prefs.density > 1) await larger.click();
    assert.equal(await larger.isDisabled(), true);
    await larger.evaluate((element) => element.click());
    assert.equal((await state()).prefs.density, 1);
    const largestWidth = (await cards().first().boundingBox()).width;
    const levels = [1];
    for (let level = 2; level <= 7; level++) {
      await smaller.click();
      assert.equal((await state()).prefs.density, level);
      if (level < 7) assert.equal(await smaller.evaluate((element) => document.activeElement === element), true);
      levels.push(level);
    }
    assert.equal(await smaller.isDisabled(), true);
    await smaller.evaluate((element) => element.click());
    assert.equal((await state()).prefs.density, 7);
    assert.ok((await cards().first().boundingBox()).width < largestWidth, 'Minus must reduce thumbnail size');
    await larger.click();
    assert.equal((await state()).prefs.density, 6);
    await page.locator('[data-action="view-list"]').click();
    await page.locator('.data-table tbody tr').first().waitFor();
    await searchAndResultsLayout('list');
    assert.equal(await larger.count() + await smaller.count(), 0, 'List must not expose thumbnail sizing controls');
    await page.reload({ waitUntil: 'load' });
    await page.locator('[data-action="view-list"]').waitFor();
    assert.equal((await state()).prefs.view, 'list');
    assert.equal((await state()).prefs.density, 6);
    assert.equal(await page.locator('[data-action="view-list"]').getAttribute('aria-pressed'), 'true');
    await page.locator('[data-action="view-grid"]').click();
    await page.locator('.media-grid').first().waitFor();
    assert.equal(await cards().count(), 24);
    return { sortChoices: sortValues, selectedSortExposed: true, randomOrderStableAfterReload: true, thumbnailLevels: levels, plusEnlargesMinusReduces: true, disabledBoundsClamp: true, interiorFocusPreserved: true, sizingControlsGridOnly: true, viewAfterReload: 'list', densityAfterReload: 6, switchedBack: 'grid' };
  });

  await test(6, 'Full viewer returns to query, page and scroll; reload preserves origin', async () => {
    await page.setViewportSize({ width: 1440, height: 800 }); // Compact cards need a shorter viewport to exercise real scrolling.
    await search('mountains && !city');
    await page.getByTestId('next-page').click();
    await page.waitForFunction(() => window.TFDemo.getState().route.context.page === 2);
    const chosen = cards().nth(22).locator('[data-content-link]').first();
    await chosen.scrollIntoViewIfNeeded();
    const scroll = await page.evaluate(() => scrollY);
    assert.ok(scroll > 50, `Scroll precondition must be real: ${scroll}`);
    const origin = new URL(page.url()).hash;
    await chosen.click();
    await waitRoute('content');
    await page.getByTestId('back-to-library').click();
    await waitRoute('library');
    await page.waitForFunction((expected) => Math.abs(scrollY - expected) < 8, scroll);
    assert.equal(new URL(page.url()).hash, origin);
    assert.equal((await state()).route.context.page, 2);
    await cards().nth(22).locator('[data-content-link]').first().click();
    await waitRoute('content');
    await page.reload({ waitUntil: 'load' });
    await page.getByTestId('back-to-library').click();
    await waitRoute('library');
    assert.equal((await state()).route.context.q, 'mountains && !city');
    assert.equal((await state()).route.context.page, 2);
    return { restoredScroll: scroll, query: 'mountains && !city', page: 2, originSurvivesReload: true };
  });

  await test(7, 'Grid/list do not count views; one viewer counts once after three seconds across edits', async () => {
    const before = (await item()).viewCount;
    await wait(3200);
    assert.equal((await item()).viewCount, before);
    assert.equal((await state()).route.type, 'library');
    await page.locator('[data-action="view-list"]').click();
    await page.locator('.media-list').waitFor();
    await wait(3200);
    assert.equal((await item()).viewCount, before);
    assert.equal((await state()).route.type, 'library');
    await openItem();
    await wait(900);
    assert.equal((await item()).viewCount, before);
    await page.waitForFunction((before) => window.TFDemo.getState().items.find((entry) => entry.id === 1).viewCount === before + 1, before);
    const eventId = (await state()).viewEvent.eventId;
    async function favoriteAppearance() {
      const favorite = page.getByTestId('favorite-toggle');
      await favorite.hover();
      await favorite.focus();
      await wait(200); // Compare settled CSS with identical hover/focus in both states.
      return favorite.evaluate((element) => {
        const style = getComputedStyle(element);
        const svg = element.querySelector('svg');
        const icon = getComputedStyle(svg);
        return {
          hovered: element.matches(':hover'), focused: document.activeElement === element,
          button: Object.fromEntries(['backgroundColor', 'backgroundImage', 'borderColor', 'borderWidth', 'borderStyle', 'color', 'boxShadow', 'outlineColor', 'outlineStyle', 'outlineWidth'].map((property) => [property, style[property]])),
          icon: { color: icon.color, fill: icon.fill, pathFill: getComputedStyle(svg.querySelector('path')).fill },
        };
      });
    }
    const favoriteOff = await favoriteAppearance();
    assert.equal(await page.getByTestId('favorite-toggle').getAttribute('aria-pressed'), 'false');
    await page.getByTestId('favorite-toggle').click();
    const favoriteOn = await favoriteAppearance();
    assert.equal(favoriteOn.hovered && favoriteOn.focused && favoriteOff.hovered && favoriteOff.focused, true);
    assert.equal(await page.getByTestId('favorite-toggle').getAttribute('aria-pressed'), 'true');
    assert.deepEqual(favoriteOn.button, favoriteOff.button, 'Favorite must not change the button background, border or color');
    assert.notDeepEqual(favoriteOn.icon, favoriteOff.icon, 'Favorite must change the star fill/color');
    await page.mouse.move(210, 10);
    await page.getByTestId('favorite-toggle').evaluate((element) => element.blur());
    await wait(200);
    await screenshot('favorite.png');
    await page.getByTestId('favorite-toggle').click();
    const favoriteRestored = await favoriteAppearance();
    assert.equal(await page.getByTestId('favorite-toggle').getAttribute('aria-pressed'), 'false');
    assert.deepEqual(favoriteRestored.button, favoriteOff.button);
    assert.deepEqual(favoriteRestored.icon, favoriteOff.icon);
    await page.getByTestId('edit-item').click();
    await editorTag(5);
    await page.getByTestId('editor-save').click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    await wait(3200);
    assert.equal((await item()).viewCount, before + 1);
    assert.equal((await state()).viewEvent.eventId, eventId);
    assert.equal(await page.getByTestId('view-count').count(), 0, 'Internal view accounting must not add technical viewer metadata');
    return { gridDelta: 0, listDelta: 0, beforeThreeSecondsDelta: 0, canonicalViewerDelta: 1, favoriteAndEditorRerenderDelta: 0, favoriteButtonStyleStableAcrossStates: true, favoriteStarChangesAndRestores: true, favoriteComparedWithIdenticalHoverAndFocus: true };
  });

  await test(8, 'Collapsed image tools support keyboard, real transforms, Escape and outside closing', async () => {
    await openItem();
    await imagesLoaded();
    const layout = await contentFirstLayout('default image viewer');
    const tools = page.getByTestId('image-tools-toggle');
    const panel = page.getByTestId('image-tools-panel');
    assert.equal(await tools.getAttribute('aria-expanded'), 'false');
    assert.equal(await panel.isVisible(), false);
    await screenshot('viewer.png');
    const image = page.getByTestId('viewer-image').first();
    const initial = await image.boundingBox();
    await tools.focus();
    await page.keyboard.press('Enter');
    await panel.waitFor();
    assert.equal(await tools.getAttribute('aria-expanded'), 'true');
    await page.keyboard.press('Tab');
    assert.equal(await page.locator('[data-action="zoom-out"]').evaluate((element) => document.activeElement === element), true);
    const fit = await imageToolsFit('desktop image tools');
    await page.getByTestId('zoom-in').click();
    const magnified = await image.boundingBox();
    assert.ok(magnified.width > initial.width * 1.2, 'Zoom must change the rendered image, not just its label');
    assert.match(await image.getAttribute('style'), /scale\(1\.25\)/);
    assert.equal(await panel.isVisible(), true);
    assert.equal(await page.getByTestId('zoom-in').evaluate((element) => document.activeElement === element), true);
    await page.getByTestId('rotate-image').click();
    assert.match(await image.getAttribute('style'), /rotate\(90deg\)/);
    assert.equal(await panel.isVisible(), true);
    assert.equal(await page.getByTestId('rotate-image').evaluate((element) => document.activeElement === element), true);
    await page.locator('[data-action="zoom-reset"]').click();
    assert.match(await image.getAttribute('style'), /scale\(1\) rotate\(0deg\)/);
    await screenshot('viewer-tools.png');
    await page.keyboard.press('Escape');
    assert.equal(await panel.isVisible(), false);
    assert.equal(await tools.getAttribute('aria-expanded'), 'false');
    assert.equal(await tools.evaluate((element) => document.activeElement === element), true);
    await tools.click();
    await panel.waitFor();
    await page.getByTestId('viewer-stage').click({ position: { x: 20, y: 20 } });
    assert.equal(await panel.isVisible(), false);
    assert.equal(await tools.evaluate((element) => document.activeElement === element), false, 'Outside closing must not steal focus back to the tools trigger');
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await imagesLoaded();
    await page.evaluate(() => window.scrollTo(0, 0));
    await screenshot('viewer-page.png', true);
    return { zoomRatio: Number((magnified.width / initial.width).toFixed(2)), rotation: 90, reset: true, defaultLayout: layout, toolsFit: fit, keyboardOpenAndTab: true, transformPreservesOpenAndFocus: true, escapeReturnsFocus: true, outsideClosesWithoutFocusSteal: true };
  });

  await test(9, 'Editor is a separate draft and Cancel preserves tags/collection', async () => {
    await openItem();
    const before = metadata(await item());
    const backup = await page.evaluate(() => ({ tags: window.TFDemoFixtures.tags.slice(), collections: window.TFDemoFixtures.collections.slice() }));
    try {
      await page.evaluate(() => {
        for (let id = 9; id <= 30; id++) window.TFDemoFixtures.tags.push({ id, name: 'QA tag ' + id });
        for (let id = 4; id <= 350; id++) window.TFDemoFixtures.collections.push({ id, name: 'QA collection ' + id, description: '' });
      });
      await page.getByTestId('edit-item').click();
      const tags = page.getByTestId('editor-tag-search');
      const tagNode = await tags.elementHandle();
      await tags.press('Escape');
      const baseline = await editorRects();
      async function stableEditor(label) { const actual = await editorRects(); assert.ok(actual.every((rect, index) => rect.every((value, part) => Math.abs(value - baseline[index][part]) <= 1)), label + ': dialog/header/footer geometry changed'); }
      await page.getByTestId('editor-save').focus(); await tags.focus();
      await stableEditor('tag focus'); await editorPopupFit('tag', 'tag focus');
      await tags.fill('QA tag 30');
      await tags.dispatchEvent('compositionstart');
      await tags.dispatchEvent('keydown', { key: 'Enter', code: 'Enter', isComposing: true });
      assert.equal(await page.getByTestId('editor-tag-remove-30').count(), 0);
      assert.equal(await page.getByRole('dialog').isVisible(), true);
      await tags.dispatchEvent('compositionend');
      await tags.fill('QA tag 2');
      await tags.press('ArrowDown'); await tags.press('ArrowDown'); await tags.press('ArrowUp'); await tags.press('Enter');
      assert.equal(await page.getByTestId('editor-tag-remove-20').count(), 1);
      await editorTag(20, false);
      await tags.fill('sea'); await tags.press(',');
      assert.equal(await page.getByTestId('editor-tag-remove-5').count(), 1);
      await tags.fill('travel');
      assert.equal(await page.getByTestId('editor-tag-option-1').count(), 0, 'Selected tags must be excluded from suggestions');
      await tags.fill('unknown existing tag'); await tags.press('Enter');
      await stableEditor('tag no match');
      assert.equal(await page.getByTestId('editor-selected-tags').locator('[data-tag-id]').count(), 7);
      assert.deepEqual(await page.evaluate(() => [window.TFDemoFixtures.tags.length, window.TFDemoFixtures.collections.length]), [30, 350]);
      const collection = page.getByTestId('editor-collection-search');
      const collectionNode = await collection.elementHandle();
      await collection.focus();
      assert.equal(await collection.inputValue(), 'Norway');
      assert.equal(await collection.evaluate((node) => node.selectionStart === 0 && node.selectionEnd === node.value.length), true);
      assert.equal(await page.getByTestId('editor-collection-options').getByRole('option').count(), 350);
      assert.equal(await page.getByTestId('editor-selected-collection').locator('input').count(), 1);
      assert.equal(await page.getByTestId('editor-selected-collection').locator('.selected-tag').count(), 0);
      await stableEditor('collection focus'); await editorPopupFit('collection', 'collection focus');
      await collection.fill('Lection 350');
      assert.equal(await page.getByTestId('editor-collection-options').getByRole('option').count(), 1);
      await stableEditor('one collection result'); await editorPopupFit('collection', 'one collection result');
      await screenshot('editor-collection.png');
      await collection.fill('missing collection');
      assert.equal(await page.getByTestId('editor-collection-options').getByRole('option').count(), 0);
      await stableEditor('zero collection results'); await editorPopupFit('collection', 'zero collection results');
      await collection.fill('');
      assert.equal(await page.getByTestId('editor-collection-options').getByRole('option').count(), 350);
      const bodyScroll = await page.getByRole('dialog').locator('.modal-body').evaluate((node) => node.scrollTop);
      await collection.press('ArrowUp');
      assert.ok(await page.getByTestId('editor-collection-options').evaluate((node) => node.scrollTop) > 0);
      assert.equal(await page.getByRole('dialog').locator('.modal-body').evaluate((node) => node.scrollTop), bodyScroll);
      await collection.press('Enter');
      assert.equal(await page.getByTestId('editor-selected-collection').getAttribute('data-collection-id'), '350');
      assert.equal(await collection.inputValue(), 'QA collection 350');
      await stableEditor('replace collection');
      await editorCollection(null);
      assert.equal(await page.getByTestId('editor-selected-collection').getAttribute('data-collection-id'), '');
      assert.equal(await collection.inputValue(), ''); await stableEditor('clear collection');
      await editorCollection(2);
      assert.equal(await collection.inputValue(), 'Iceland');
      await collection.fill('unknown existing collection'); await collection.press('Enter');
      assert.equal(await page.getByTestId('editor-selected-collection').getAttribute('data-collection-id'), '2');
      assert.equal(await page.evaluate(() => window.TFDemoFixtures.collections.length), 350);
      await collection.press('Escape');
      assert.equal(await collection.inputValue(), 'Iceland'); await stableEditor('collection Escape');
      await collection.fill('Nor'); await collection.press('Tab');
      assert.equal(await collection.inputValue(), 'Iceland');
      assert.equal(await page.getByTestId('editor-selected-collection').getAttribute('data-collection-id'), '2');
      assert.equal(await tagNode.evaluate((node) => node.isConnected), true);
      assert.equal(await collectionNode.evaluate((node) => node.isConnected), true);
      assert.deepEqual(metadata(await item()), before);
      await tags.fill(''); await tags.press('Escape');
      await stableEditor('closed editor');
      await screenshot('editor.png');
      await tags.fill('QA tag');
      const options = page.getByTestId('editor-tag-options');
      assert.equal(await options.getByRole('option').count(), 22);
      assert.equal(await options.evaluate((node) => node.clientHeight <= 220 && node.scrollHeight > node.clientHeight), true);
      assert.equal(await tagNode.evaluate((node) => node.isConnected && document.activeElement === node), true);
      await stableEditor('filtered tag popup'); await editorPopupFit('tag', 'filtered tag popup');
      await screenshot('editor-search.png');
      await tags.press('Escape');
      assert.equal(await options.isVisible(), false);
      assert.equal(await page.getByRole('dialog').isVisible(), true);
      await page.getByTestId('editor-cancel').click();
      assert.deepEqual(metadata(await item()), before);
      assert.equal(await page.getByTestId('edit-item').evaluate((element) => document.activeElement === element), true);
      await page.getByTestId('edit-item').click(); await page.getByTestId('editor-tag-search').fill('QA tag');
      await page.keyboard.press('Escape'); await page.keyboard.press('Escape');
      assert.equal(await page.getByRole('dialog').count(), 0);
      assert.deepEqual(metadata(await item()), before);
    } finally {
      await page.evaluate((backup) => { window.TFDemoFixtures.tags = backup.tags; window.TFDemoFixtures.collections = backup.collections; }, backup);
      await page.reload({ waitUntil: 'load' }); await waitRoute('content', 1);
    }
    return { changedDraftOnly: true, cancelPreservesMetadata: true, focusRestored: true, scaleTags: 30, scaleCollections: 350, existingOnlyKeyboardAndIme: true, scrollableSuggestions: true, inputDomPreserved: true, fixedEditorHeaderFooterGeometry: true, fixedPopupsFitAndAlign: true, singleCollectionFieldReplacesAndRestoresLabel: true, escapeClosesPopupBeforeDialog: true };
  });

  await test(10, 'Failed atomic save preserves data/draft; retry commits tags and collection together', async () => {
    await openItem();
    const before = metadata(await item());
    await page.getByTestId('edit-item').click();
    await editorTag(5);
    await editorCollection(2);
    await page.evaluate(() => window.TFDemo.debug.failNextSave());
    await page.getByTestId('editor-save').click();
    assert.equal(await page.getByTestId('editor-save').isDisabled(), true);
    assert.deepEqual(metadata(await item()), before);
    await page.locator('#save-error:visible').waitFor();
    assert.deepEqual(metadata(await item()), before);
    assert.equal(await page.getByTestId('editor-tag-remove-5').count() > 0, true);
    assert.equal(await page.getByTestId('editor-selected-collection').getAttribute('data-collection-id'), '2');
    await page.getByTestId('editor-save').click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    assert.deepEqual(metadata(await item()), { tagIds: [1, 2, 3, 4, 5, 7, 8], collectionId: 2 });
    await page.reload({ waitUntil: 'load' });
    assert.deepEqual(metadata(await item()), { tagIds: [1, 2, 3, 4, 5, 7, 8], collectionId: 2 });
    await page.getByTestId('back-to-library').click();
    await waitRoute('library');
    await search('mountains && !city');
    await openItem(2);
    const previous = await page.getByTestId('viewer-previous').getAttribute('href');
    const next = await page.getByTestId('viewer-next').getAttribute('href');
    assert.match(previous, /content\/1\?/);
    assert.match(next, /content\/8\?/);
    await page.getByTestId('edit-item').click();
    await editorTag(2, false);
    await page.getByTestId('editor-save').click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    assert.equal((await item(2)).tagIds.includes(2), false);
    assert.equal(await page.getByTestId('viewer-previous').getAttribute('href'), previous);
    assert.equal(await page.getByTestId('viewer-next').getAttribute('href'), next);
    await page.getByTestId('viewer-next').click();
    await waitRoute('content', 8);
    await page.getByTestId('viewer-previous').click();
    await waitRoute('content', 2);
    await page.getByTestId('back-to-library').click();
    await waitRoute('library');
    assert.equal(await visibleTotal(), 150);
    assert.equal((await cardIds()).includes(2), false);
    return { failedSaveChangedData: false, draftRetained: true, atomicRetryPersisted: true, neighborsAfterRemovingQueryTag: [1, 8], updatedQueryTotal: 150 };
  });

  await test(11, 'Keyboard stays in inputs/editor and navigates images only outside controls', async () => {
    const searchInput = page.getByTestId('tag-query');
    await searchInput.fill('лес');
    await searchInput.press('ArrowLeft');
    await searchInput.press('Space');
    assert.equal((await state()).route.type, 'library');
    assert.equal(await searchInput.inputValue(), 'ле с');
    await searchInput.fill('');
    await openItem();
    await page.getByTestId('edit-item').click();
    await page.getByTestId('editor-tag-search').fill('sea');
    await page.keyboard.press('ArrowRight');
    await page.keyboard.press('Space');
    assert.equal((await state()).route.id, 1);
    assert.equal(await page.getByTestId('editor-tag-search').inputValue(), 'sea ');
    assert.equal(await page.getByTestId('editor-tag-remove-5').count(), 0);
    await page.keyboard.press('Escape');
    await page.getByTestId('editor-cancel').click();
    await page.getByTestId('favorite-toggle').focus();
    await page.keyboard.press('ArrowRight');
    await wait(100); // Allow any incorrect hash navigation to settle before asserting absence.
    assert.equal((await state()).route.id, 1);
    await page.evaluate(() => document.activeElement.blur());
    await page.keyboard.press('ArrowRight');
    await waitRoute('content', 2);
    await page.evaluate(() => document.activeElement.blur());
    await page.keyboard.press('ArrowLeft');
    await waitRoute('content', 1);
    return { inputAndEditorPreservedRoute: true, bodyArrowNavigation: [1, 2, 1] };
  });

  await test(12, 'Four independent type-toggle combinations preserve context; real video controls work', async () => {
    await search(''); // Use the canonical result URL before checking exact return context.
    const combinations = [
      { image: false, video: false, kind: '', total: 426 },
      { image: true, video: false, kind: 'image', total: 355 },
      { image: true, video: true, kind: '', total: 426 },
      { image: false, video: true, kind: 'video', total: 71 },
    ];
    for (const expected of combinations) {
      for (const key of ['image', 'video']) {
        const toggle = page.getByTestId('kind-' + key);
        assert.equal((await toggle.innerText()).trim(), '', 'Type toggle must use an icon without visible text');
        if ((await toggle.getAttribute('aria-pressed') === 'true') !== expected[key]) await toggle.click();
      }
      await page.waitForFunction((expected) => {
        const context = window.TFDemo.getState().route.context;
        return context.imageSelected === expected.image && context.videoSelected === expected.video && context.kind === expected.kind;
      }, expected);
      assert.equal(await visibleTotal(), expected.total);
      const origin = new URL(page.url()).hash;
      const params = new URLSearchParams(origin.split('?')[1] || '');
      assert.equal(params.get('kind') || '', expected.kind);
      assert.equal(params.get('types'), expected.image && expected.video ? 'image,video' : null);
      const id = (await cardIds())[0];
      const favorite = (await item(id)).isFavorite;
      await openItem(id);
      await page.getByTestId('favorite-toggle').click();
      assert.equal((await item(id)).isFavorite, !favorite);
      await page.getByTestId('back-to-library').click();
      await waitRoute('library');
      assert.equal(new URL(page.url()).hash, origin, 'Viewing and favoriting must preserve the exact toggle combination');
      await page.reload({ waitUntil: 'load' });
      await page.getByTestId('kind-image').waitFor();
      assert.equal((await state()).route.context.imageSelected, expected.image);
      assert.equal((await state()).route.context.videoSelected, expected.video);
      assert.equal(await page.getByTestId('kind-image').getAttribute('aria-pressed'), String(expected.image));
      assert.equal(await page.getByTestId('kind-video').getAttribute('aria-pressed'), String(expected.video));
      assert.equal((await item(id)).isFavorite, !favorite);
      assert.equal(await visibleTotal(), expected.total);
    }
    assert.equal(await visibleTotal(), 71);
    await openItem(6);
    assert.equal(await page.getByTestId('image-tools-toggle').count(), 0, 'Video uses its native player controls');
    const videoPermanentControls = await page.locator('[data-testid="viewer-secondary"] a.button, [data-testid="viewer-secondary"] button, [data-testid="viewer-secondary"] summary')
      .evaluateAll((elements) => elements.filter((element) => element.getBoundingClientRect().width > 0 && element.getBoundingClientRect().height > 0).length);
    assert.equal(videoPermanentControls, 5);
    const video = page.getByTestId('viewer-video');
    await page.waitForFunction(() => { const video = document.querySelector('[data-testid="viewer-video"]'); return video?.readyState >= 2 && video.duration > 2; });
    assert.equal(await video.getAttribute('preload'), 'metadata');
    assert.equal(await video.evaluate((element) => element.videoWidth), 640);
    await video.focus();
    await page.keyboard.press('Space');
    await page.waitForFunction(() => document.querySelector('video').currentTime > 0.15 && !document.querySelector('video').paused);
    await page.keyboard.press('Space');
    assert.equal(await video.evaluate((element) => element.paused), true);
    await page.keyboard.press('ArrowRight');
    await page.waitForFunction(() => Math.abs(document.querySelector('video').currentTime - document.querySelector('video').duration) < 0.05);
    const duration = await video.evaluate((element) => element.duration);
    await page.keyboard.press('ArrowLeft');
    await page.waitForFunction(() => document.querySelector('video').currentTime < 0.05);
    await video.evaluate((element) => { element.volume = 0.5; });
    await page.keyboard.press('ArrowUp');
    assert.ok(Math.abs(await video.evaluate((element) => element.volume) - 0.6) < 0.001);
    await page.keyboard.press('ArrowDown');
    assert.ok(Math.abs(await video.evaluate((element) => element.volume) - 0.5) < 0.001);
    assert.equal((await state()).route.id, 6);
    return { typeCombinations: combinations, urlReloadViewerAndFavoritesPreserved: true, videoPermanentControls, actualPlayback: true, dimensions: '640×360', durationSeconds: duration, seekClamped: true, audioTrack: false };
  });

  await test(13, 'Pending uploads finish outside the queue; errors persist until retry and success leaves no history', async () => {
    await page.locator('#sidebar a[href="#/uploads"]').click();
    await waitRoute('uploads');
    const initial = await state();
    assert.deepEqual(initial.jobs.map((job) => job.status), ['processing', 'uploading', 'failed']);
    assert.equal(await page.getByTestId('retry-upload').count(), 1);
    assert.equal(await page.getByTestId('cancel-upload').count(), 3);
    assert.deepEqual(await Promise.all(['upload-total', 'upload-in-progress', 'upload-errors'].map((id) => page.getByTestId(id).innerText())), ['3', '2', '1']);
    await noUploadHistory();
    for (const testId of ['choose-files', 'add-demo-batch']) {
      assert.equal((await page.getByTestId(testId).innerText()).trim(), '', testId + ' must use an icon without visible text');
      assert.ok(await page.getByTestId(testId).getAttribute('aria-label'), testId + ' needs an accessible label');
    }
    await screenshot('uploads.png');
    await page.locator('#sidebar a.nav-link[href="#/library"]').click();
    await waitRoute('library');
    await page.waitForFunction(() => { const state = window.TFDemo.getState(); return state.jobs.length === 1 && state.jobs[0].status === 'failed'; }, undefined, { timeout: 35000 });
    assert.equal((await state()).items.length, 428);
    assert.deepEqual((await uploadStorage()).jobs.map((job) => job.id), ['demo-seed-5']);
    // Change Date without advancing intervals: errors have no age-based expiry.
    await page.clock.setFixedTime(Date.now() + 7 * 86400_000);
    await page.reload({ waitUntil: 'load' }); await waitRoute('library');
    assert.deepEqual((await state()).jobs.map((job) => [job.id, job.status]), [['demo-seed-5', 'failed']]);
    await page.clock.setSystemTime(Date.now());
    await page.locator('#sidebar a[href="#/uploads"]').click();
    await waitRoute('uploads');
    assert.equal(await page.getByTestId('upload-job').count(), 1);
    await page.getByTestId('retry-upload').focus(); await page.keyboard.press('Enter');
    assert.equal((await state()).jobs[0].id, 'demo-seed-5');
    assert.equal((await state()).jobs[0].attempt, 2);
    assert.equal((await state()).jobs.length, 1);
    assert.equal(await page.getByTestId('cancel-upload').evaluate((element) => document.activeElement === element), true);
    await page.waitForFunction(() => window.TFDemo.getState().jobs.length === 0, undefined, { timeout: 35000 });
    await page.getByTestId('upload-empty').waitFor();
    assert.equal((await state()).items.length, 429);
    assert.equal(await page.getByTestId('upload-job').count(), 0);
    assert.equal(await page.getByTestId('choose-files').evaluate((element) => document.activeElement === element), true);
    const stored = await uploadStorage();
    assert.deepEqual(stored.jobs, []);
    assert.equal(stored.extraItems.length, 3);
    assert.equal(new Set(stored.extraItems.map((entry) => entry.uploadJobId)).size, 3);
    await noUploadHistory();
    await screenshot('uploads-empty.png');
    await page.reload({ waitUntil: 'load' });
    await page.getByTestId('upload-empty').waitFor();
    assert.equal((await state()).items.length, 429);
    assert.deepEqual((await state()).jobs, []);
    return { initial: initial.jobs.map((job) => job.status), publishedOutsideQueue: 2, retriedSameJobOnce: true, successfulJobsRemovedFromStateDomAndStorage: true, sevenDayDateJumpKeptError: true, totalAfter: 429, persistedMediaWithoutHistory: true };
  });

  await test(14, 'File picker rejects text and focused upload controls keep final results fresh', async () => {
    await page.locator('#sidebar a[href="#/uploads"]').click();
    await waitRoute('uploads');
    await page.getByTestId('upload-input').setInputFiles([
      { name: 'QA — озеро.jpg', mimeType: 'image/jpeg', buffer: await readFile(path.join(prototype, 'assets/lake.jpg')) },
      { name: 'QA — движение.mp4', mimeType: 'video/mp4', buffer: await readFile(path.join(prototype, 'assets/demo-silent.mp4')) },
      { name: 'QA — заметка.txt', mimeType: 'text/plain', buffer: Buffer.from('This is deliberately not media.') },
    ]);
    const queued = await state();
    assert.equal(queued.jobs.length, 5);
    assert.equal(queued.jobs.filter((job) => job.name.startsWith('QA —')).length, 2);
    assert.match(await page.getByRole('alert').innerText(), /заметка\.txt/);
    const acceptedIds = queued.jobs.filter((job) => job.name.startsWith('QA —')).map((job) => job.id);
    await page.locator('[data-job-id="demo-seed-3"] [data-testid="cancel-upload"]').focus();
    await page.waitForFunction(() => !window.TFDemo.getState().jobs.some((job) => job.id === 'demo-seed-3'), undefined, { timeout: 20000 });
    assert.equal(await page.locator('[data-job-id="demo-seed-3"]').count(), 0);
    assert.equal(await page.getByTestId('choose-files').evaluate((element) => document.activeElement === element), true, 'Completed focused row must move focus to Add media');
    await page.waitForFunction(() => window.TFDemo.getState().jobs.length === 1 && window.TFDemo.getState().items.length === 430, undefined, { timeout: 35000 });
    assert.equal(await page.getByTestId('choose-files').evaluate((element) => document.activeElement === element), true);
    const published = await uploadStorage();
    for (const id of acceptedIds) assert.equal(published.extraItems.filter((entry) => entry.uploadJobId === id).length, 1);
    assert.deepEqual(published.jobs.map((job) => [job.id, job.status]), [['demo-seed-5', 'failed']]);
    assert.equal(await page.getByTestId('upload-job').count(), 1);
    await noUploadHistory();
    await page.getByTestId('cancel-upload').click();
    await page.getByTestId('upload-empty').waitFor();
    assert.deepEqual((await uploadStorage()).jobs, []);
    assert.equal(await page.getByTestId('choose-files').evaluate((element) => document.activeElement === element), true);
    await page.getByTestId('upload-input').setInputFiles([{ name: 'QA canceled.jpg', mimeType: 'image/jpeg', buffer: await readFile(path.join(prototype, 'assets/lake.jpg')) }]);
    const canceledId = (await state()).jobs[0].id;
    await page.getByTestId('cancel-upload').click();
    assert.deepEqual((await uploadStorage()).jobs, []);
    await page.reload({ waitUntil: 'load' }); await page.getByTestId('upload-empty').waitFor();
    await wait(3400); // Two real processing ticks must not revive a canceled job.
    assert.equal((await state()).items.length, 430);
    assert.deepEqual((await state()).jobs, []);
    assert.equal((await uploadStorage()).extraItems.some((entry) => entry.uploadJobId === canceledId), false);
    return { accepted: 2, rejected: 1, publishedIncludingSeed: 4, totalAfter: 430, successfulRowsRemovedWhileFocused: true, logicalFocusFallback: true, canceledErrorAndActivePersistentlyRemoved: true, canceledJobDidNotPublishAfterTicks: true };
  });

  await test(15, 'Desktop and narrow widths keep essentials usable without document overflow', async () => {
    const checked = [];
    const layouts = [];
    for (const width of [1440, 1024, 640]) {
      await page.setViewportSize({ width, height: 1000 });
      await noHorizontalOverflow(`library ${width}`);
      assert.ok((await page.locator('#sidebar').boundingBox()).width >= 190, 'Sidebar must not automatically become an icon rail at ' + width);
      assert.equal(await page.locator('#sidebar a[href="#/categories"]').innerText(), 'Categories');
      await searchAndResultsLayout('library ' + width, width);
      const input = await page.getByTestId('tag-query').boundingBox();
      assert.ok(input.x >= 0 && input.x + input.width <= width + 1, `Search clipped at ${width}`);
      for (const testId of ['thumbnail-larger', 'thumbnail-smaller']) {
        const bounds = await page.getByTestId(testId).boundingBox();
        assert.ok(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width + 1, testId + ' must remain visible at ' + width);
      }
      await page.getByTestId('sidebar-toggle').click();
      assert.equal((await page.locator('#sidebar').boundingBox()).width, 72);
      await noHorizontalOverflow(`library rail ${width}`);
      await searchAndResultsLayout('library rail ' + width, width);
      await page.getByTestId('sidebar-toggle').click();
      await openItem();
      const mediaNode = await page.getByTestId('viewer-image').elementHandle();
      await page.getByTestId('sidebar-toggle').click();
      await noHorizontalOverflow(`viewer rail ${width}`);
      assert.equal(await mediaNode.evaluate((node) => node.isConnected), true, 'Sidebar toggle must preserve the media element');
      await page.getByTestId('sidebar-toggle').click();
      await noHorizontalOverflow(`viewer ${width}`);
      const layout = await contentFirstLayout(`viewer ${width}`);
      await page.getByTestId('image-tools-toggle').click();
      await page.getByTestId('image-tools-panel').waitFor();
      const toolsFit = await imageToolsFit(`viewer ${width}`);
      await page.keyboard.press('Escape');
      layouts.push({ width, ...layout, ...toolsFit });
      await page.getByTestId('edit-item').click();
      const dialog = await page.getByRole('dialog').boundingBox();
      assert.ok(dialog.x >= 0 && dialog.x + dialog.width <= width + 1, `Editor clipped at ${width}`);
      await page.getByTestId('editor-tag-search').fill('sea');
      await editorPopupFit('tag', 'editor ' + width);
      if (width === 640) {
        const backup = await page.evaluate(() => window.TFDemoFixtures.collections.slice());
        try {
          await page.evaluate(() => { for (let id = 4; id <= 350; id++) window.TFDemoFixtures.collections.push({ id, name: 'QA collection ' + id, description: '' }); });
          const collection = page.getByTestId('editor-collection-search');
          await collection.fill('');
          await page.setViewportSize({ width, height: 700 });
          // The viewport protocol returns before the browser dispatches resize.
          await page.waitForFunction(() => { const input = document.querySelector('[data-testid="editor-collection-search"]').getBoundingClientRect(); const popup = document.querySelector('[data-testid="editor-collection-options"]').getBoundingClientRect(); return popup.top >= 0 && popup.bottom <= innerHeight && (popup.bottom <= input.top + 1 || popup.top >= input.bottom - 1); });
          await editorPopupFit('collection', 'short editor after resize');
          await page.evaluate(() => window.scrollTo(0, 100));
          await editorPopupFit('collection', 'short editor after page scroll');
          const bodyScroll = await page.getByRole('dialog').locator('.modal-body').evaluate((node) => node.scrollTop);
          const pageScroll = await page.evaluate(() => scrollY);
          await collection.press('ArrowUp');
          assert.ok(await page.getByTestId('editor-collection-options').evaluate((node) => node.scrollTop) > 0);
          assert.equal(await page.getByRole('dialog').locator('.modal-body').evaluate((node) => node.scrollTop), bodyScroll);
          assert.equal(await page.evaluate(() => scrollY), pageScroll);
          await editorPopupFit('collection', 'short editor after distant-option scroll');
          const save = await page.getByTestId('editor-save').boundingBox();
          assert.ok(save.y >= 0 && save.y + save.height <= 700, 'Editor footer must remain reachable on a short viewport');
          await collection.press('Escape');
        } finally {
          await page.evaluate((collections) => { window.TFDemoFixtures.collections = collections; }, backup);
          await page.setViewportSize({ width, height: 1000 });
        }
      }
      await page.getByTestId('editor-cancel').click();
      await page.getByTestId('back-to-library').click();
      await page.locator('#sidebar a[href="#/uploads"]').click();
      await waitRoute('uploads');
      await noHorizontalOverflow(`uploads ${width}`);
      assert.equal(await page.getByTestId('choose-files').isVisible(), true);
      await noUploadHistory();
      await page.locator('#sidebar a.nav-link[href="#/library"]').click();
      await waitRoute('library');
      checked.push(width);
    }
    return { widths: checked, routes: ['library', 'viewer', 'editor', 'uploads'], horizontalOverflow: false, editorPopupsFitAfterResizeAndScroll: true, shortEditorViewport: '640×700', distantArrowOptionScrollsOnlyList: true, contentFirstLayouts: layouts };
  });

  await test(16, 'Built-in UI and fixtures are English; the prototype works offline without errors', async () => {
    await page.reload({ waitUntil: 'load' });
    await page.getByTestId('tag-query').waitFor();
    await imagesLoaded();
    assert.deepEqual(report.externalRequests, []);
    assert.deepEqual(report.pageErrors, []);
    assert.equal(await visibleTotal(), 426);
    assert.equal(await page.locator('html').getAttribute('lang'), 'en');
    for (const filename of ['app.js', 'index.template.html']) {
      assert.doesNotMatch(await readFile(path.join(prototype, filename), 'utf8'), /[А-ЯЁа-яё]/u, filename + ': built-in strings must be English');
    }
    const fixtureCopy = await page.evaluate(() => JSON.stringify({ tags: window.TFDemoFixtures.tags, collections: window.TFDemoFixtures.collections, savedSearches: window.TFDemoFixtures.savedSearches }));
    assert.doesNotMatch(fixtureCopy, /[А-ЯЁа-яё]/u, 'Fixture tags, collections and saved-search names/queries must be English');
    const auditedSurfaces = [];
    async function assertEnglishSurface(label) {
      const copy = await page.evaluate(() => document.title + '\n' + document.body.innerText + '\n' + [...document.querySelectorAll('[aria-label], [aria-description], [title], [placeholder], img[alt]')]
        .map((element) => ['aria-label', 'aria-description', 'title', 'placeholder', 'alt'].map((attribute) => element.getAttribute(attribute) || '').join('\n')).join('\n'));
      assert.doesNotMatch(copy, /[А-ЯЁа-яё]/u, label + ': built-in visible/accessibility copy must be English');
      auditedSurfaces.push(label);
    }
    await assertEnglishSurface('gallery');
    await page.getByTestId('sort-filter').click();
    await assertEnglishSurface('sort menu');
    await page.keyboard.press('Escape');
    await page.locator('[data-action="view-list"]').click();
    await assertEnglishSurface('list');
    await page.locator('[data-action="view-grid"]').click();
    await openItem();
    await assertEnglishSurface('viewer');
    await page.getByTestId('image-tools-toggle').click();
    await page.getByTestId('image-tools-panel').waitFor();
    await assertEnglishSurface('image tools');
    await page.keyboard.press('Escape');
    await page.getByTestId('edit-item').click();
    await assertEnglishSurface('editor');
    await page.getByTestId('editor-cancel').click();
    await page.getByTestId('back-to-library').click();
    await waitRoute('library');
    const support = [['tags', 'tags-list', 'tags'], ['collections', 'collections-grid', 'collections'], ['categories', 'categories-list', 'saved-searches']];
    async function supportLayout(id) {
      const geometry = await page.evaluate((id) => {
        const content = document.querySelector('[data-testid="' + id + '"]');
        const total = document.querySelector('[data-testid="support-total"]');
        return { total: total.textContent, after: total.getBoundingClientRect().top >= content.getBoundingClientRect().bottom - 1 };
      }, id);
      const expected = id === 'tags-list' ? 8 : 3;
      assert.equal(geometry.total, 'Total: ' + expected);
      assert.equal(geometry.after, true, id + ': entity total must follow the content');
      await noHorizontalOverflow(id);
    }
    for (const [href, route] of [['uploads', 'uploads'], ['tags', 'tags'], ['collections', 'collections'], ['categories', 'saved-searches']]) {
      await page.locator('#sidebar a[href="#/' + href + '"]').click();
      await waitRoute(route);
      await assertEnglishSurface(href);
      if (href === 'uploads') await noUploadHistory();
      const surface = support.find((entry) => entry[0] === href);
      if (!surface) continue;
      await supportLayout(surface[1]);
      if (href === 'tags') {
        assert.equal(await page.getByTestId('tags-list').evaluate((node) => node.tagName), 'TABLE');
        assert.deepEqual(await page.getByTestId('tags-list').locator('th').allTextContents(), ['Tag', 'Usage count']);
        const expected = await page.evaluate(() => window.TFDemoFixtures.tags.map((tag) => [tag.name, window.TFDemo.getState().items.filter((item) => item.tagIds.includes(tag.id)).length]));
        assert.deepEqual(await page.getByTestId('tags-list').locator('tbody tr').evaluateAll((rows) => rows.map((row) => [row.cells[0].innerText, Number(row.cells[1].innerText)])), expected);
      } else if (href === 'categories') {
        assert.equal(await page.getByTestId('categories-list').evaluate((node) => node.tagName), 'TABLE');
        assert.deepEqual(await page.getByTestId('categories-list').locator('th').allTextContents(), ['Name', 'Query', 'Items type']);
        const expected = await page.evaluate(() => window.TFDemoFixtures.savedSearches.map((entry) => [entry.name, entry.query, 'All types']));
        assert.deepEqual(await page.getByTestId('categories-list').locator('tbody tr').evaluateAll((rows) => rows.map((row) => [...row.cells].map((cell) => cell.innerText))), expected);
      } else {
        const geometry = await page.getByTestId('collections-grid').evaluate((grid) => ({ gap: getComputedStyle(grid).gap, cards: [...grid.children].map((card) => { const cover = card.querySelector('.collection-cover').getBoundingClientRect(); const title = card.querySelector('.collection-title').getBoundingClientRect(); return { width: card.getBoundingClientRect().width, coverWidth: cover.width, coverHeight: cover.height, titleBelow: title.top >= cover.bottom - 1 }; }), text: grid.innerText }));
        assert.equal(geometry.gap, '12px');
        assert.equal(geometry.cards.length, 3);
        for (const card of geometry.cards) { assert.ok(card.width >= 159 && card.width <= 200); assert.ok(Math.abs(card.coverWidth - card.coverHeight) <= 1); assert.equal(card.titleBelow, true); }
        for (const description of await page.evaluate(() => window.TFDemoFixtures.collections.map((entry) => entry.description))) assert.equal(geometry.text.includes(description), false);
        assert.doesNotMatch(geometry.text, /Open media/);
      }
      await screenshot(href + '.png');
    }
    for (const width of [1440, 1024, 640]) {
      await page.setViewportSize({ width, height: 1000 });
      for (const open of [true, false]) {
        if ((await state()).prefs.sidebarOpen !== open) await page.getByTestId('sidebar-toggle').click();
        for (const [href, id, route] of support) { await page.locator('#sidebar a[href="#/' + href + '"]').click(); await waitRoute(route); await supportLayout(id); }
      }
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByTestId('sidebar-toggle').click();
    await page.locator('#sidebar a.nav-link[href="#/library"]').click(); await waitRoute('library');
    await page.getByTestId('kind-video').click();
    await page.locator('#sidebar a[href="#/tags"]').click(); await waitRoute('tags');
    await page.getByTestId('tags-list').locator('.support-link').first().focus(); await page.keyboard.press('Enter'); await waitRoute('library');
    assert.equal((await state()).route.context.q, 'travel');
    assert.equal((await state()).route.context.kind, '');
    assert.equal((await state()).route.context.imageSelected || (await state()).route.context.videoSelected, false);
    await page.locator('#sidebar a[href="#/collections"]').click(); await waitRoute('collections');
    await page.getByTestId('collections-grid').locator('.support-link').first().click(); await waitRoute('library');
    assert.equal((await state()).route.context.collection, 1);
    assert.equal((await state()).route.context.q, '');
    assert.equal((await state()).route.context.kind, '');
    await page.locator('#sidebar a[href="#/categories"]').click(); await waitRoute('saved-searches');
    assert.equal(await page.title(), 'Categories — TagFiles');
    assert.equal(await page.getByRole('heading', { level: 1 }).innerText(), 'Categories');
    const category = await page.evaluate(() => window.TFDemoFixtures.savedSearches[0]);
    await page.getByTestId('categories-list').locator('.support-link').first().click(); await waitRoute('library');
    assert.equal((await state()).route.context.q, category.query);
    assert.equal((await state()).route.context.kind, category.kind || '');
    assert.equal(await visibleTotal(), 151);
    await page.evaluate(() => { window.TFDemoFixtures.savedSearches[0].kind = 'video'; });
    await page.locator('#sidebar a[href="#/categories"]').click(); await waitRoute('saved-searches');
    assert.equal(await page.getByTestId('categories-list').locator('tbody tr').first().locator('td').nth(2).innerText(), 'Video');
    await page.getByTestId('categories-list').locator('.support-link').first().focus(); await page.keyboard.press('Enter'); await waitRoute('library');
    assert.equal((await state()).route.context.q, category.query);
    assert.equal((await state()).route.context.kind, 'video');
    assert.ok(await visibleTotal() > 0);
    const kinds = new Map((await state()).items.map((entry) => [entry.id, entry.kind]));
    assert.equal((await cardIds()).every((id) => kinds.get(id) === 'video'), true);
    await page.evaluate((kind) => { window.TFDemoFixtures.savedSearches[0].kind = kind; }, category.kind);
    await page.evaluate(() => { location.hash = '#/saved-searches'; }); await waitRoute('saved-searches');
    assert.equal(await page.getByRole('heading', { level: 1 }).innerText(), 'Categories');
    const collectionsBackup = await page.evaluate(() => window.TFDemoFixtures.collections.slice());
    const collectionIds = () => page.getByTestId('collections-grid').locator('.support-link').evaluateAll((links) => links.map((link) => Number(new URLSearchParams(link.getAttribute('href').split('?')[1]).get('collection'))));
    try {
      await page.evaluate(() => { for (let id = 4; id <= 350; id++) window.TFDemoFixtures.collections.push({ id, name: 'Archive collection ' + id, description: '' }); });
      await page.locator('#sidebar a[href="#/collections"]').click(); await waitRoute('collections');
      assert.equal((await collectionIds()).length, 350);
      const query = page.getByTestId('collections-search');
      const queryNode = await query.elementHandle();
      await query.fill('  LeCtIoN 17  ');
      assert.deepEqual(await collectionIds(), [17, 170, 171, 172, 173, 174, 175, 176, 177, 178, 179]);
      assert.equal(await page.getByTestId('support-total').innerText(), '11 of 350 collections');
      assert.equal(await queryNode.evaluate((node) => node.isConnected && document.activeElement === node), true);
      assert.equal(new URLSearchParams(new URL(page.url()).hash.split('?')[1]).get('search'), '  LeCtIoN 17  ');
      await screenshot('collections-search.png');
      await query.fill('Norway || Iceland');
      assert.deepEqual(await collectionIds(), [], 'Collection search must use a literal substring, not Boolean tag grammar');
      assert.equal(await page.getByTestId('collections-empty').isVisible(), true);
      assert.equal(await page.getByTestId('support-total').innerText(), '0 of 350 collections');
      await page.getByTestId('collections-search-clear').click();
      assert.equal(await query.inputValue(), '');
      assert.equal((await collectionIds()).length, 350);
      assert.equal(await page.getByTestId('support-total').innerText(), 'Total: 350');
      await query.fill('oRw');
      assert.deepEqual(await collectionIds(), [1]);
      assert.equal(await page.getByTestId('collections-grid').locator('.collection-cover').count(), 1);
      await page.getByTestId('collections-grid').locator('.support-link').first().click(); await waitRoute('library');
      assert.equal((await state()).route.context.collection, 1);
      await page.goBack(); await waitRoute('collections');
      assert.equal(await page.getByTestId('collections-search').inputValue(), 'oRw');
      assert.deepEqual(await collectionIds(), [1]);
    } finally {
      await page.evaluate((collections) => { window.TFDemoFixtures.collections = collections; }, collectionsBackup);
      await page.reload({ waitUntil: 'load' });
    }
    await waitRoute('collections');
    assert.equal(await page.getByTestId('collections-search').inputValue(), 'oRw');
    assert.equal(await page.getByTestId('support-total').innerText(), '1 of 3 collections');
    await page.getByTestId('collections-search-clear').click();
    assert.equal(await page.getByTestId('support-total').innerText(), 'Total: 3');
    assert.deepEqual(report.externalRequests, []);
    assert.deepEqual(report.pageErrors, []);
    return { englishCopySurfaces: auditedSurfaces, fixtureCopyEnglish: true, appAndTemplateStringsEnglish: true, supportLayouts: { nativeTables: true, exactTagUsageCounts: true, compactSquareCollections: true, entityTotalsBelowContent: true, openAndRailWidths: [1440, 1024, 640], linksPreserveExistingScopes: true }, collectionSearch: { scale: 350, caseInsensitiveMiddleSubstring: true, literalQuery: true, clearAndNoMatch: true, inputDomAndFocusPreserved: true, filteredCoverScope: true, backAndReloadRestoreQuery: true }, categoriesApplyExistingSavedQuery: true, videoCategoryApplied: true, legacySavedSearchesAlias: true, unicodeInputCasesPassedInThisRun: report.tests.filter((entry) => [3, 11, 14].includes(entry.number) && entry.status === 'PASS').map((entry) => entry.number), offline: true, externalRequests: 0, pageErrors: 0 };
  });

  await test(17, 'One content-first viewer serves grid/list/related links without file metadata or preview mode', async () => {
    const surfaces = [];
    const namedMedia = await page.evaluate(() => [...window.TFDemoFixtures.items, ...window.TFDemo.getState().items]
      .filter((entry) => Object.hasOwn(entry, 'title') || Object.hasOwn(entry, 'filename')).map((entry) => entry.id));
    assert.deepEqual(namedMedia, [], 'Media fixtures and persisted metadata must not invent user-facing names');
    async function assertMinimalSurface(selector, name) {
      const evidence = await page.locator(selector).evaluate((element) => {
        const visibleText = element.innerText;
        const accessibleText = [...element.querySelectorAll('[aria-label], [title], img[alt]')]
          .map((node) => [node.getAttribute('aria-label'), node.getAttribute('title'), node.getAttribute('alt')].filter(Boolean).join(' ')).join('\n');
        const names = [...new Set(window.TFDemoFixtures.items.flatMap((entry) => [entry.title, entry.filename]).filter(Boolean))];
        return {
          visibleNames: names.filter((value) => visibleText.includes(value)),
          accessibleNames: names.filter((value) => accessibleText.includes(value)),
          technicalAccessibleValues: accessibleText.match(/[^\s]+\.(?:jpe?g|png|webp|avif|gif|heic|mov|mp4|mkv)\b|\b(?:MIME|filename|ID)\s*[:#]/giu) || [],
          technicalLabels: ['File size', 'Dimensions', 'Uploaded at', 'View count', 'Description', 'Read only', 'File information', 'About this media', 'Размер файла', 'Размеры', 'Добавлен', 'Просмотры', 'Описание', 'Только для чтения', 'Сведения', 'О материале']
            .filter((value) => visibleText.includes(value)),
          technicalValues: visibleText.match(/\b\d{2,5}\s*[×x]\s*\d{2,5}\b|\b\d+(?:[.,]\d+)?\s*(?:KB|MB|GB|bytes|КБ|МБ|ГБ|байт|px)\b|(?:image|video)\/(?:jpeg|png|mp4)|\b\d{2}\.\d{2}\.\d{4}\b/giu) || [],
          persistentHintNodes: [...element.querySelectorAll('.page-subtitle, .page-eyebrow, .search-hint, .query-help, .card-title, .card-meta, .metadata-grid, .description-readonly, .keyboard-hint')]
            .filter((node) => node.getBoundingClientRect().width > 0 && node.getBoundingClientRect().height > 0 && node.innerText.trim()).length,
        };
      });
      assert.deepEqual(evidence.visibleNames, [], `${name}: content names are visible`);
      assert.deepEqual(evidence.accessibleNames, [], `${name}: content names leak into accessibility labels`);
      assert.deepEqual(evidence.technicalAccessibleValues, [], `${name}: filenames or technical identifiers leak into accessibility labels`);
      assert.deepEqual(evidence.technicalLabels, [], `${name}: technical labels are visible`);
      assert.deepEqual(evidence.technicalValues, [], `${name}: technical values are visible`);
      assert.equal(evidence.persistentHintNodes, 0, `${name}: persistent explanatory copy is visible`);
      surfaces.push(name);
    }
    await assertMinimalSurface('#workspace', 'gallery');
    assert.equal(await page.locator('[data-testid="quick-preview"], [data-action="preview"], [data-action="open-preview"]').count(), 0, 'Library must not offer a second viewing mode');
    const tooltip = page.getByTestId('card-tags-tooltip');
    const firstLink = cards().first().locator('[data-content-link]');
    assert.equal(await cards().locator('.card-tags, .card-body').count(), 0, 'Grid must not reserve a tag row');
    const tagText = await page.evaluate(() => window.TFDemoFixtures.items[0].tagIds.map((id) => window.TFDemoFixtures.tags.find((tag) => tag.id === id).name).join(' · '));
    assert.equal((await item()).tagIds.length, 6, 'Fixture must expose a real six-tag example');
    const beforeHover = await cards().first().boundingBox();
    const nextRowBefore = await cards().nth(5).boundingBox();
    await screenshot('library.png');
    await firstLink.hover();
    await tooltip.waitFor();
    assert.equal((await tooltip.innerText()).trim(), tagText);
    assert.equal(await tooltip.evaluate((element) => element.childElementCount), 0, 'Tooltip must be plain text, without chips or links');
    const afterHover = await cards().first().boundingBox();
    const nextRowAfter = await cards().nth(5).boundingBox();
    assert.equal(afterHover.height, beforeHover.height);
    assert.equal(nextRowAfter.y, nextRowBefore.y, 'Hover must not reflow the grid');
    await screenshot('hover.png');
    const popup = await tooltip.boundingBox();
    await page.mouse.move(popup.x + popup.width / 2, popup.y + popup.height / 2);
    await wait(350);
    assert.equal(await tooltip.isVisible(), true, 'Moving onto tooltip must keep it visible');
    await firstLink.hover();
    await firstLink.focus();
    await page.keyboard.press('Escape');
    await page.mouse.move(beforeHover.x + beforeHover.width / 2 + 2, beforeHover.y + beforeHover.height / 2);
    await wait(150);
    assert.equal(await tooltip.isVisible(), false, 'Escape must suppress immediate reopening');
    await page.mouse.move(210, 10);
    await firstLink.evaluate((element) => element.blur());
    await firstLink.focus();
    await tooltip.waitFor();
    assert.equal((await tooltip.innerText()).trim(), tagText, 'Keyboard focus must reveal every tag');
    await page.keyboard.press('Escape');
    assert.equal(await page.locator('#workspace input[data-select], #selection-toolbar, .media-card.selected').count(), 0, 'Grid must not expose selection');
    await page.getByTestId('next-page').click();
    await page.waitForFunction(() => window.TFDemo.getState().route.context.page === 2);
    const untagged = page.locator('[data-testid="media-card"][data-id="27"] [data-content-link]');
    await untagged.hover();
    await untagged.focus();
    assert.equal(await tooltip.isVisible(), false, 'Untagged media must not show an empty tooltip');
    const originalTravel = await page.evaluate(() => { const tag = window.TFDemoFixtures.tags.find((entry) => entry.id === 1); const name = tag.name; tag.name = 'a-very-long-tag-'.repeat(18); window.TFDemo.reset(); return name; });
    await waitRoute('library');
    await page.setViewportSize({ width: 640, height: 1000 });
    const edgeLink = cards().last().locator('[data-content-link]');
    await edgeLink.evaluate((element) => { const bounds = element.getBoundingClientRect(); window.scrollBy(0, bounds.bottom - innerHeight + 2); });
    await edgeLink.hover({ position: { x: 10, y: 10 } });
    await tooltip.waitFor();
    const edgeBounds = await tooltip.boundingBox();
    assert.ok(edgeBounds.x >= 0 && edgeBounds.x + edgeBounds.width <= 641 && edgeBounds.y >= 0 && edgeBounds.y + edgeBounds.height <= 1001, 'Long tooltip must fit the narrow viewport at the edge');
    assert.equal(await tooltip.evaluate((element) => element.scrollWidth <= element.clientWidth + 1 && element.scrollHeight <= element.clientHeight + 1), true, 'Every long-tag character must remain visible');
    await page.evaluate((name) => { window.TFDemoFixtures.tags.find((entry) => entry.id === 1).name = name; }, originalTravel);
    await reset();
    const entrypoints = [];
    const assertCanonicalEntry = async (link, source, expectedId) => {
      const href = await link.getAttribute('href');
      const match = href?.match(/^#\/content\/(\d+)\?context=/);
      assert.ok(match, source + ': media must open the canonical content route');
      const id = Number(match[1]);
      if (expectedId !== undefined) assert.equal(id, expectedId);
      await link.click();
      await waitRoute('content', id);
      assert.equal(await page.getByTestId('viewer-stage').count(), 1);
      assert.equal(await page.getByRole('dialog').count(), 0, source + ': media must not open a preview dialog');
      assert.equal(await page.locator('[data-testid="quick-preview"], [data-action="preview"], [data-action="open-preview"]').count(), 0);
      entrypoints.push({ source, id });
      return id;
    };
    await assertCanonicalEntry(cards().first().locator('[data-content-link]').first(), 'grid', 1);
    const layout = await contentFirstLayout('canonical grid viewer');
    await assertMinimalSurface('#workspace', 'viewer');
    const collectionSection = page.getByTestId('collection-section');
    const collectionHeading = page.getByTestId('collection-heading');
    const sameTagsSection = page.getByTestId('same-tags-section');
    assert.equal((await collectionHeading.innerText()).trim(), 'Norway');
    assert.equal(await collectionHeading.evaluate((element) => element.tagName === 'A' && Boolean(element.closest('h2'))), true);
    assert.equal((await collectionSection.locator('h2').innerText()).trim(), 'Norway', 'Collection heading needs only the linked collection name');
    assert.equal(await collectionSection.getByRole('link', { name: 'All', exact: true }).count(), 0);
    assert.equal((await sameTagsSection.locator('h2').innerText()).trim(), 'Same tags');
    const headingBounds = await collectionHeading.boundingBox();
    assert.ok(headingBounds.y >= 0 && headingBounds.y + headingBounds.height <= 1000, 'Collection heading must be visible without scrolling on desktop');
    assert.equal(await collectionSection.evaluate((element) => Boolean(element.compareDocumentPosition(document.querySelector('[data-testid="same-tags-section"]')) & Node.DOCUMENT_POSITION_FOLLOWING)), true);
    const sameTagIds = await sameTagsSection.getByTestId('media-card').evaluateAll((elements) => elements.map((element) => Number(element.dataset.id)));
    assert.ok(sameTagIds.length > 0, 'Fixture must expose exact-tag matches');
    const ownTags = [...(await item()).tagIds].sort((a, b) => a - b);
    for (const id of sameTagIds) {
      assert.notEqual(id, 1);
      assert.deepEqual([...(await item(id)).tagIds].sort((a, b) => a - b), ownTags, 'Same tags must retain exact tag-set matching');
    }
    await screenshot('viewer.png');
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await imagesLoaded();
    await page.evaluate(() => window.scrollTo(0, 0));
    await screenshot('viewer-page.png', true);
    const collectionTotal = (await state()).items.filter((entry) => entry.collectionId === 1).length;
    await collectionHeading.click();
    await waitRoute('library');
    assert.equal((await state()).route.context.collection, 1);
    assert.equal(await visibleTotal(), collectionTotal);
    await page.goBack();
    await waitRoute('content', 1);
    const viewerTags = page.locator('.viewer-info .tag-list a.tag');
    assert.equal(await viewerTags.count(), (await item()).tagIds.length);
    await page.getByTestId('edit-item').click();
    await assertMinimalSurface('dialog[open]', 'editor');
    await page.getByTestId('editor-cancel').click();
    const viewerTag = (await viewerTags.first().innerText()).trim();
    await viewerTags.first().click();
    await waitRoute('library');
    assert.equal((await state()).route.context.q, viewerTag);
    await reset();
    await page.locator('[data-action="view-list"]').click();
    await page.locator('.media-list').waitFor();
    await assertMinimalSurface('#workspace', 'list');
    await search('travel');
    assert.deepEqual(await page.locator('.media-list thead th').allTextContents(), ['Media', 'Tags', 'Collection', 'Actions']);
    const row2 = page.locator('[data-testid="media-card"][data-id="2"]');
    const rowEdit = row2.getByTestId('list-edit');
    const rowCheckbox = row2.locator('input[data-select]');
    const actionStyle = (control) => control.evaluate((element) => ({ opacity: getComputedStyle(element).opacity, pointerEvents: getComputedStyle(element).pointerEvents }));
    await page.mouse.move(210, 10);
    await page.evaluate(() => document.activeElement.blur());
    assert.deepEqual(await actionStyle(rowEdit), { opacity: '0', pointerEvents: 'none' });
    assert.deepEqual(await actionStyle(rowCheckbox), { opacity: '0', pointerEvents: 'none' });
    await screenshot('list.png');
    const rowBefore = await row2.boundingBox();
    await row2.hover();
    await page.waitForFunction(() => getComputedStyle(document.querySelector('[data-testid="list-edit"][data-id="2"]')).opacity === '1');
    assert.deepEqual(await actionStyle(rowCheckbox), { opacity: '1', pointerEvents: 'auto' });
    assert.equal((await row2.boundingBox()).height, rowBefore.height);
    assert.equal(await row2.locator('.list-actions-cell').evaluate((element) => element.cellIndex), 3);
    const actionBounds = await row2.locator('.list-row-actions').boundingBox();
    assert.ok(actionBounds.x > rowBefore.x + rowBefore.width / 2);
    await screenshot('list-hover.png');
    await rowCheckbox.check();
    await rowCheckbox.evaluate((element) => element.blur());
    await page.mouse.move(210, 10);
    await wait(200);
    assert.equal((await actionStyle(rowCheckbox)).opacity, '1', 'Checked selection must stay visible outside hover/focus');
    assert.equal((await actionStyle(rowEdit)).opacity, '0', 'Edit must remain contextual even for checked rows');
    await row2.hover();
    await rowCheckbox.uncheck();
    await page.mouse.move(210, 10);
    await cards().first().locator('input[data-select]').focus();
    for (let step = 0; step < 8 && !await rowEdit.evaluate((element) => document.activeElement === element); step++) await page.keyboard.press('Tab');
    assert.equal(await rowEdit.evaluate((element) => document.activeElement === element), true, 'Native Tab must reach the contextual Edit button');
    await page.waitForFunction(() => getComputedStyle(document.querySelector('[data-testid="list-edit"][data-id="2"]')).opacity === '1');
    await page.evaluate(() => window.scrollTo(0, 100));
    const listOrigin = page.url();
    const listScroll = await page.evaluate(() => scrollY);
    assert.ok(listScroll > 50, 'List editor must preserve a real scroll position');
    const listMetadata = (await state()).items.map((entry) => ({ id: entry.id, ...metadata(entry) }));
    await rowEdit.press('Enter');
    assert.equal((await state()).route.type, 'library');
    assert.equal(await page.getByTestId('editor-tag-remove-2').count() > 0, true);
    assert.equal(await page.getByTestId('editor-tag-remove-3').count() > 0, false, 'Row2 Edit must not open the six-tag row1 draft');
    await editorTag(5);
    await page.getByTestId('editor-cancel').click();
    assert.equal(page.url(), listOrigin);
    assert.ok(Math.abs(await page.evaluate(() => scrollY) - listScroll) < 8);
    assert.equal(await rowEdit.evaluate((element) => document.activeElement === element), true);
    assert.deepEqual((await state()).items.filter((entry) => listMetadata.some((before) => before.id === entry.id)).map((entry) => ({ id: entry.id, ...metadata(entry) })), listMetadata);
    await rowEdit.click();
    await editorTag(5);
    await page.getByTestId('editor-save').click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    const changed = (await state()).items.filter((entry) => { const before = listMetadata.find((candidate) => candidate.id === entry.id); return before && JSON.stringify(metadata(before)) !== JSON.stringify(metadata(entry)); }).map((entry) => entry.id);
    assert.deepEqual(changed, [2]);
    assert.equal(page.url(), listOrigin);
    assert.ok(Math.abs(await page.evaluate(() => scrollY) - listScroll) < 8);
    assert.equal(await rowEdit.evaluate((element) => document.activeElement === element), true);
    await reset();
    await page.locator('[data-action="view-list"]').click();
    await page.locator('.media-list').waitFor();
    const bulkBefore = [metadata(await item(1)), metadata(await item(2))];
    await cards().nth(0).hover();
    await cards().nth(0).locator('input[data-select]').check();
    await cards().nth(1).hover();
    await cards().nth(1).locator('input[data-select]').check();
    await page.getByTestId('bulk-edit').click();
    await editorCollection(2);
    await page.getByTestId('editor-cancel').click();
    assert.deepEqual([metadata(await item(1)), metadata(await item(2))], bulkBefore);
    await page.locator('[data-action="view-grid"]').click();
    await page.locator('.media-grid').waitFor();
    assert.deepEqual((await state()).selected, []);
    assert.equal(await page.locator('#workspace input[data-select], #selection-toolbar, .media-card.selected').count(), 0);
    await page.locator('[data-action="view-list"]').click();
    await page.locator('.media-list').waitFor();
    assert.equal(await page.locator('input[data-select]:checked').count(), 0);
    const listTag = cards().first().locator('.tag-list a.tag').first();
    const listTagName = (await listTag.innerText()).trim();
    await listTag.click();
    await page.waitForFunction((name) => window.TFDemo.getState().route.context.q === name, listTagName);
    await assertCanonicalEntry(cards().first().locator('[data-content-link]').first(), 'list', 1);
    const related = page.getByTestId('same-tags-section').locator('[data-content-link]').first();
    assert.equal(await related.count(), 1, 'Fixture must expose a related-media entrypoint');
    await related.hover();
    await tooltip.waitFor();
    assert.equal((await tooltip.innerText()).trim(), tagText, 'Related thumbnails use the same complete plain-text tooltip');
    await assertCanonicalEntry(related, 'related');
    await contentFirstLayout('canonical related viewer');
    // Browser-only fixture extension exercises membership with no other items.
    // It is discarded with this isolated browser context and never edits sources.
    await page.evaluate(() => window.TFDemoFixtures.collections.push({ id: 99, name: 'QA single item collection', description: '' }));
    await page.getByTestId('edit-item').click();
    await editorCollection(99);
    await page.getByTestId('editor-save').click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    assert.equal((await collectionHeading.innerText()).trim(), 'QA single item collection');
    assert.equal(await collectionSection.getByTestId('media-card').count(), 0, 'Collection heading must remain when no siblings exist');
    assert.equal(await sameTagsSection.count(), 1);
    await page.getByTestId('edit-item').click();
    await editorCollection(null);
    await page.getByTestId('editor-save').click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    assert.equal(await collectionSection.count(), 0, 'An unassigned item must not show a collection group');
    assert.equal(await sameTagsSection.count(), 1);
    await reset();
    await openItem(2);
    await page.getByTestId('collection-heading').click();
    await waitRoute('library');
    await page.getByTestId('kind-image').click();
    await page.getByTestId('sort-filter').click();
    await page.locator('[role="menuitemradio"][data-sort="uploadedAsc"]').click();
    await page.waitForFunction(() => window.TFDemo.getState().route.context.kind === 'image' && window.TFDemo.getState().route.context.sort === 'uploadedAsc');
    const lastPage = Math.ceil(await visibleTotal() / 24);
    await page.getByTestId('page-' + lastPage).click();
    await page.waitForFunction((lastPage) => window.TFDemo.getState().route.context.page === lastPage, lastPage);
    if (!await page.locator('[data-testid="media-card"][data-id="2"]').count()) {
      await page.getByTestId('previous-page').click();
      await page.waitForFunction((lastPage) => window.TFDemo.getState().route.context.page === lastPage - 1, lastPage);
    }
    await openItem(2); // Two tags expose supersets that an exact-set preview omits.
    const sourceTags = (await item(2)).tagIds;
    const query = await page.evaluate((ids) => ids.map((id) => window.TFDemoFixtures.tags.find((tag) => tag.id === id).name).join(' && '), sourceTags);
    const sameTagsHeading = page.getByTestId('same-tags-heading');
    assert.equal((await sameTagsHeading.innerText()).trim(), 'Same tags');
    assert.equal(await sameTagsHeading.evaluate((element) => element.tagName === 'A' && Boolean(element.closest('h2'))), true);
    const previewIds = await sameTagsSection.getByTestId('media-card').evaluateAll((elements) => elements.map((element) => Number(element.dataset.id)));
    assert.equal(previewIds.includes(2), false);
    assert.equal(previewIds.includes(1), false, 'Exact-set preview must omit a strict superset');
    const expectedSearchIds = (await state()).items.filter((entry) => sourceTags.every((id) => entry.tagIds.includes(id))).map((entry) => entry.id);
    await sameTagsHeading.click();
    await waitRoute('library');
    const searched = (await state()).route.context;
    assert.equal(searched.q, query);
    assert.equal(searched.page, 1);
    assert.equal(searched.kind, '');
    assert.equal(searched.collection, null);
    assert.deepEqual(searched.u, []);
    assert.equal(searched.sort, 'uploadedDesc');
    assert.equal(await visibleTotal(), expectedSearchIds.length);
    assert.ok(expectedSearchIds.length > previewIds.length, 'Heading must search the library, not just the four related cards');
    const foundIds = await cardIds();
    assert.equal(foundIds.every((id) => expectedSearchIds.includes(id)), true);
    for (const id of [1, 2, 8, 18]) assert.equal(foundIds.includes(id), true, 'AND results must include self, supersets, other collections and videos');
    assert.equal(foundIds.includes(3), false, 'A missing required tag must exclude the item');
    return { checkedSurfaces: surfaces, namesAndTechnicalMetadata: 0, allAssignedTagsVisible: true, viewerTagNavigation: true, thumbnailTooltip: { sixTagsPlainText: true, hoverAndFocus: true, noReflow: true, pointerOverTooltipPersists: true, escapeSuppressesReopen: true, longTagNarrowEdgeFits: true, noTagsNoTooltip: true, relatedParity: true }, canonicalEntrypoints: entrypoints, previewViewingMode: false, contentFirstLayout: layout, collectionHeading: { name: 'Norway', visibleWithoutScrollDesktop: true, linksToCollection: 1, precedesExactSameTags: true, allLinkRemoved: true, singleItemMembershipVisible: true, unassignedGroupAbsent: true }, sameTagsHeading: { query, actualMatching: expectedSearchIds.length, previewCards: previewIds.length, includesSelfSupersetsAndOtherCollections: true, resetsLibraryRestrictions: true, previewRetainsExactSet: true } };
  });
} catch (error) {
  report.setupFailure = cleanError(error.stack || error);
  console.error(`SETUP FAIL: ${cleanError(error.message || error)}`);
} finally {
  report.finishedAt = new Date().toISOString();
  report.passed = report.tests.filter((entry) => entry.status === 'PASS').length;
  report.failed = report.tests.filter((entry) => entry.status === 'FAIL').length + (report.setupFailure ? 1 : 0);
  report.status = report.failed ? 'FAIL' : report.tests.length ? 'PASS' : 'NOT_RUN';
  await writeFile(path.join(directory, 'results.json'), JSON.stringify(report, null, 2) + '\n');
  await browser?.close();
  console.log(JSON.stringify({ status: report.status, passed: report.passed, failed: report.failed, results: path.join(directory, 'results.json') }));
  process.exitCode = report.status === 'PASS' ? 0 : 1;
}
