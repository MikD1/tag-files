(function () {
  'use strict';

  var F = window.TFDemoFixtures;
  if (!F || !Array.isArray(F.items)) return;
  var STORAGE_KEY = 'tagfiles-ui-demo-v3';
  var PAGE_SIZE = 24;
  var sidebar = document.getElementById('sidebar');
  var workspace = document.getElementById('workspace');
  var overlayRoot = document.getElementById('overlay-root');
  var toastRoot = document.getElementById('toast-root');
  var stored = readStorage();
  var prefs = Object.assign({ view: 'grid', density: 4, sidebarOpen: true }, stored.prefs || {});
  var overrides = stored.overrides || {};
  var extraItems = Array.isArray(stored.extraItems) ? stored.extraItems : [];
  var jobs = (Array.isArray(stored.jobs) ? stored.jobs : seedJobs()).filter(function (job) { return ['queued', 'uploading', 'processing', 'failed'].indexOf(job.status) >= 0; });
  var sequence = Number(stored.sequence) || 1;
  var selected = new Set();
  var scrollPositions = Object.create(null);
  var contextSnapshots = Object.create(null);
  var activeContext = '';
  var currentRoute;
  var currentResults = [];
  var currentPageItems = [];
  var queryDraft = null;
  var queryError = '';
  var modal = null;
  var dialogElement = null;
  var previousFocus = null;
  var pendingSave = false;
  var saveGeneration = 0;
  var failNextSave = false;
  var viewTimer = null;
  var viewSession = null;
  var mediaTransform = { scale: 1, rotation: 0 };
  var filePickerOpen = false;
  var uploadMessage = '';
  var toastTimer;
  var cardTagsTooltip = document.createElement('div');
  cardTagsTooltip.id = 'card-tags-tooltip';
  cardTagsTooltip.className = 'card-tags-tooltip';
  cardTagsTooltip.dataset.testid = 'card-tags-tooltip';
  cardTagsTooltip.setAttribute('role', 'tooltip');
  cardTagsTooltip.hidden = true;
  document.body.appendChild(cardTagsTooltip);
  var tooltipTrigger = null;
  var suppressedTooltipTrigger = null;
  var tooltipHideTimer;

  function hideCardTags(suppress) {
    clearTimeout(tooltipHideTimer);
    if (tooltipTrigger) {
      var descriptions = (tooltipTrigger.getAttribute('aria-describedby') || '').split(/\s+/).filter(function (id) { return id && id !== cardTagsTooltip.id; });
      if (descriptions.length) tooltipTrigger.setAttribute('aria-describedby', descriptions.join(' '));
      else tooltipTrigger.removeAttribute('aria-describedby');
      if (suppress) suppressedTooltipTrigger = tooltipTrigger;
    }
    tooltipTrigger = null;
    cardTagsTooltip.hidden = true;
    cardTagsTooltip.textContent = '';
  }
  function resetCardTags() { hideCardTags(false); suppressedTooltipTrigger = null; }
  function positionCardTags() {
    if (!tooltipTrigger) return;
    var media = tooltipTrigger.closest('.card-media');
    if (!media || !document.contains(media)) { resetCardTags(); return; }
    var rect = media.getBoundingClientRect();
    if (rect.bottom < 0 || rect.top > innerHeight) { resetCardTags(); return; }
    var tooltipRect = cardTagsTooltip.getBoundingClientRect();
    cardTagsTooltip.style.left = Math.max(12, Math.min(rect.left, innerWidth - tooltipRect.width - 12)) + 'px';
    var top = rect.bottom + 6;
    if (top + tooltipRect.height > innerHeight - 12) top = rect.top - tooltipRect.height - 6;
    top = Math.max(12, Math.min(top, innerHeight - tooltipRect.height - 12));
    cardTagsTooltip.style.top = top + 'px';
  }
  function showCardTags(trigger) {
    clearTimeout(tooltipHideTimer);
    if (modal || suppressedTooltipTrigger === trigger) return;
    if (!trigger.dataset.mediaTags) { hideCardTags(false); return; }
    if (tooltipTrigger !== trigger) hideCardTags(false);
    tooltipTrigger = trigger;
    cardTagsTooltip.textContent = trigger.dataset.mediaTags;
    cardTagsTooltip.hidden = false;
    var descriptions = (trigger.getAttribute('aria-describedby') || '').split(/\s+/).filter(Boolean);
    if (descriptions.indexOf(cardTagsTooltip.id) < 0) descriptions.push(cardTagsTooltip.id);
    trigger.setAttribute('aria-describedby', descriptions.join(' '));
    positionCardTags();
  }
  function delayHideCardTags() {
    clearTimeout(tooltipHideTimer);
    tooltipHideTimer = setTimeout(function () {
      if (tooltipTrigger && !tooltipTrigger.matches(':hover') && !tooltipTrigger.contains(document.activeElement) && !cardTagsTooltip.matches(':hover')) hideCardTags(false);
    }, 160);
  }

  function readStorage() {
    try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}') || {}; }
    catch (_) { return {}; }
  }
  function persist() {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ prefs: prefs, overrides: overrides, extraItems: extraItems, jobs: jobs, sequence: sequence })); }
    catch (_) { /* The demo also works when local storage is unavailable. */ }
  }
  function esc(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function icon(name) {
    var paths = {
      library: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
      search: '<circle cx="10.8" cy="10.8" r="7.2"/><path d="m16 16 5 5"/>',
      upload: '<path d="M12 16V3m-5 5 5-5 5 5M4 16v4a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-4"/>',
      tag: '<path d="M20 13 13 20a2 2 0 0 1-3 0L3 13V3h10l7 7a2 2 0 0 1 0 3Z"/><circle cx="7.5" cy="7.5" r="1"/>',
      folder: '<path d="M3 7V5a2 2 0 0 1 2-2h5l3 3h6a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z"/>',
      bookmark: '<path d="M6 21V5a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v16l-6-4Z"/>',
      grid: '<rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>',
      list: '<path d="M9 5h12M9 12h12M9 19h12M3 5h1M3 12h1M3 19h1"/>',
      chevron: '<path d="m9 5 7 7-7 7"/>',
      previous: '<path d="m15 5-7 7 7 7"/>',
      back: '<path d="m14 5-7 7 7 7M7 12h14"/>',
      close: '<path d="m6 6 12 12M18 6 6 18"/>',
      play: '<path d="m9 5 11 7-11 7Z"/>',
      eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/>',
      star: '<path d="m12 3 2.8 5.7 6.3.9-4.6 4.4 1.1 6.3-5.6-3-5.6 3 1.1-6.3L3 9.6l6.2-.9Z"/>',
      edit: '<path d="m16 3 5 5-12 12-6 1 1-6Z M13 6l5 5"/>',
      plus: '<path d="M12 5v14M5 12h14"/>',
      minus: '<path d="M5 12h14"/>',
      rotate: '<path d="M3 10a9 9 0 1 1 2 8M3 4v6h6"/>',
      check: '<path d="m5 12 4 4L19 6"/>',
      clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
      retry: '<path d="M3 10a9 9 0 1 1 2 8M3 4v6h6"/>',
      image: '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8" cy="8" r="2"/><path d="m3 17 5-5 4 4 4-7 5 8"/>',
      video: '<rect x="3" y="5" width="13" height="14" rx="2"/><path d="m16 10 5-3v10l-5-3Z"/>',
      shuffle: '<path d="m3 5 4 0 10 14h4m-4-4 4 4-4 4M3 19h4L17 5h4m-4-4 4 4-4 4"/>',
      history: '<path d="M3 11a9 9 0 1 1 2 7M3 5v6h6M12 7v5l3 2"/>',
      adjust: '<path d="M3 7h4m4 0h10M3 17h10m4 0h4"/><circle cx="9" cy="7" r="2"/><circle cx="15" cy="17" r="2"/>',
      more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>'
    };
    return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + (paths[name] || paths.library) + '</svg>';
  }
  function button(action, label, options) {
    options = options || {};
    return '<button type="button" class="button ' + esc(options.className || 'secondary') + '" data-action="' + esc(action) + '"' + (options.id ? ' data-id="' + esc(options.id) + '"' : '') + (options.testid ? ' data-testid="' + esc(options.testid) + '"' : '') + (options.title ? ' title="' + esc(options.title) + '" aria-label="' + esc(options.title) + '"' : '') + (options.disabled ? ' disabled' : '') + '>' + (options.icon ? icon(options.icon) : '') + (label ? '<span>' + esc(label) + '</span>' : '') + '</button>';
  }
  function tagName(id) { var tag = F.tags.find(function (t) { return t.id === id; }); return tag ? tag.name : ''; }
  function mediaLabel(item) { return (item.kind === 'video' ? 'Video' : 'Photo') + (item.tagIds.length ? ': ' + item.tagIds.map(tagName).join(', ') : ' without tags'); }
  function collectionName(id) { var col = F.collections.find(function (c) { return c.id === id; }); return col ? col.name : 'No collection'; }
  function allItems() {
    var added = extraItems.map(function (record) {
      var base = F.items.find(function (item) { return item.id === record.baseId; }) || F.items[0];
      return Object.assign({}, base, record, { src: base.src, poster: base.poster, videoSrc: base.videoSrc || F.videoSrc });
    });
    return F.items.concat(added).map(function (item) {
      return Object.assign({ viewCount: item.id % 17 }, item, overrides[item.id] || {});
    });
  }
  function itemById(id) { return allItems().find(function (item) { return item.id === Number(id); }); }
  function duration(value) { if (value == null) return '—'; return Math.floor(value / 60) + ':' + String(Math.floor(value % 60)).padStart(2, '0'); }
  function countLabel(n) { return n.toLocaleString('en-US') + (n === 1 ? ' item' : ' items'); }
  function hashNumber(value) { var h = 2166136261; for (var i = 0; i < value.length; i++) h = Math.imul(h ^ value.charCodeAt(i), 16777619); return h >>> 0; }

  // Tag names are compared exactly, including case and Unicode spelling.
  function compileQuery(query) {
    if (!query.trim()) return function () { return true; };
    var tokens = [], at = 0;
    while (at < query.length) {
      if (/\s/.test(query[at])) { at++; continue; }
      var pair = query.slice(at, at + 2), char = query[at];
      if (pair === '&&' || pair === '||') { tokens.push(pair); at += 2; continue; }
      if (char === '!' || char === '(' || char === ')') { tokens.push(char); at++; continue; }
      if (char === '&' || char === '|') throw new Error('Use two characters: && or ||.');
      var start = at;
      while (at < query.length && !/[!()&|]/.test(query[at])) at++;
      var name = query.slice(start, at).trim();
      if (!name) throw new Error('Enter the exact tag name after the operator.');
      tokens.push({ tag: name });
    }
    var position = 0;
    function primary() {
      var token = tokens[position++];
      if (!token) throw new Error('Incomplete expression: enter a tag.');
      if (token === '!') { var negated = primary(); return function (item) { return !negated(item); }; }
      if (token === '(') { var inside = or(); if (tokens[position++] !== ')') throw new Error('Add a closing parenthesis.'); return inside; }
      if (typeof token === 'object') return function (item) { return item.tagIds.some(function (id) { return tagName(id) === token.tag; }); };
      throw new Error('Enter a tag name or an opening parenthesis.');
    }
    function and() {
      var left = primary();
      while (tokens[position] === '&&') { position++; var right = primary(); left = combine(left, right, true); }
      return left;
    }
    function or() {
      var left = and();
      while (tokens[position] === '||') { position++; var right = and(); left = combine(left, right, false); }
      return left;
    }
    function combine(left, right, conjunction) { return function (item) { return conjunction ? left(item) && right(item) : left(item) || right(item); }; }
    var result = or();
    if (position !== tokens.length) throw new Error('Use && or || between tags. Check the parentheses.');
    return result;
  }
  function parseLibrary(hash) {
    var query = (hash || '').split('?')[1] || '';
    var params = new URLSearchParams(query);
    var kind = params.get('kind') || '';
    var bothTypes = params.get('types') === 'image,video';
    var sort = params.get('sort') || 'uploadedDesc';
    if (['uploadedDesc', 'uploadedAsc', 'durationDesc', 'durationAsc', 'viewsDesc', 'viewsAsc', 'random'].indexOf(sort) < 0) sort = 'uploadedDesc';
    return {
      q: params.get('q') || '', kind: bothTypes ? '' : kind === 'image' || kind === 'video' ? kind : '', sort: sort,
      imageSelected: bothTypes || kind === 'image', videoSelected: bothTypes || kind === 'video',
      page: Math.max(1, Number(params.get('page')) || 1), view: params.get('view') === 'list' ? 'list' : params.get('view') === 'grid' ? 'grid' : prefs.view,
      seed: params.get('seed') || '426', u: (params.get('u') || '').split(',').map(Number).filter(Boolean),
      collection: params.has('collection') ? Number(params.get('collection')) : null
    };
  }
  function libraryURL(context) {
    var params = new URLSearchParams();
    if (context.q) params.set('q', context.q);
    if (context.imageSelected && context.videoSelected) params.set('types', 'image,video');
    else if (context.kind) params.set('kind', context.kind);
    params.set('sort', context.sort || 'uploadedDesc');
    params.set('page', context.page || 1);
    params.set('view', context.view || prefs.view);
    if (context.sort === 'random') params.set('seed', context.seed || '426');
    if (context.u && context.u.length) params.set('u', context.u.join(','));
    if (context.collection !== null && context.collection !== undefined) params.set('collection', context.collection);
    return '#/library?' + params.toString();
  }
  function contentURL(id, context) { return '#/content/' + Number(id) + '?context=' + encodeURIComponent(context || activeContext || libraryURL(parseLibrary(''))); }
  function captureContext(context) {
    try { contextSnapshots[context] = resultsFor(parseLibrary(context)).map(function (item) { return item.id; }); }
    catch (_) { /* A malformed direct-link query uses the viewer fallback. */ }
  }
  function resultsFor(context) {
    var predicate = compileQuery(context.q);
    var result = allItems().filter(function (item) {
      return predicate(item) && (!context.kind || item.kind === context.kind) && (!context.u.length || context.u.indexOf(item.id) >= 0) && (context.collection == null || item.collectionId === context.collection);
    });
    result.sort(function (a, b) {
      var difference = 0;
      switch (context.sort) {
        case 'uploadedAsc': difference = new Date(a.uploadedAt) - new Date(b.uploadedAt); break;
        case 'durationDesc': difference = a.durationSeconds == null ? (b.durationSeconds == null ? 0 : 1) : b.durationSeconds == null ? -1 : b.durationSeconds - a.durationSeconds; break;
        case 'durationAsc': difference = a.durationSeconds == null ? (b.durationSeconds == null ? 0 : -1) : b.durationSeconds == null ? 1 : a.durationSeconds - b.durationSeconds; break;
        case 'viewsDesc': difference = b.viewCount - a.viewCount; break;
        case 'viewsAsc': difference = a.viewCount - b.viewCount; break;
        case 'random': difference = hashNumber(context.seed + ':' + a.id) - hashNumber(context.seed + ':' + b.id); break;
        default: difference = new Date(b.uploadedAt) - new Date(a.uploadedAt);
      }
      return difference || a.id - b.id;
    });
    return result;
  }
  function navigate(hash) {
    if (currentRoute && currentRoute.type === 'library') scrollPositions[activeContext] = window.scrollY;
    if (location.hash === hash) render(); else location.hash = hash;
  }
  function toast(message) {
    clearTimeout(toastTimer);
    toastRoot.innerHTML = '<div class="toast">' + icon('check') + '<span>' + esc(message) + '</span></div>';
    toastTimer = setTimeout(function () { toastRoot.innerHTML = ''; }, 4400);
  }
  function applySidebarState() {
    document.documentElement.classList.toggle('sidebar-collapsed', !prefs.sidebarOpen);
    sidebar.hidden = false;
    var toggle = document.getElementById('sidebar-toggle');
    var label = prefs.sidebarOpen ? 'Collapse sidebar' : 'Expand sidebar';
    toggle.setAttribute('aria-expanded', String(!!prefs.sidebarOpen));
    toggle.setAttribute('aria-label', label);
    toggle.title = label;
  }
  function renderSidebar() {
    var type = currentRoute.type;
    var activeJobs = jobs.filter(function (job) { return job.status !== 'failed'; }).length;
    function link(path, name, glyph, count, selectedType) {
      return '<a class="nav-link' + (type === (selectedType || path) ? ' active' : '') + '" href="#/' + path + '" aria-label="' + esc(name) + '" title="' + esc(name) + '"' + (type === (selectedType || path) ? ' aria-current="page"' : '') + '>' + icon(glyph) + '<span class="nav-label">' + esc(name) + '</span>' + (count != null ? '<span class="nav-count">' + count + '</span>' : '') + '</a>';
    }
    sidebar.innerHTML = '<a class="nav-brand" href="#/library" aria-label="TagFiles"><span>TagFiles<span class="brand-dot">.</span></span></a><nav class="nav-section nav-primary" aria-label="Library navigation">' + link('library', 'Library', 'library', null, type === 'content' ? 'content' : 'library') + link('collections', 'Collections', 'folder') + link('tags', 'Tags', 'tag') + link('categories', 'Categories', 'bookmark', null, 'saved-searches') + '</nav><div class="nav-section nav-bottom" data-testid="sidebar-bottom">' + link('uploads', 'Uploads', 'upload', activeJobs || null) + '</div>';
    applySidebarState();
  }
  function header(eyebrow, title, subtitle, actions) {
    return '<header class="page-header"><h1 class="page-title">' + esc(title) + '</h1><div class="header-actions">' + (actions || '') + '</div></header>';
  }
  function refreshSidebarCounts() {
    var uploadLink = sidebar.querySelector('a[href="#/uploads"]');
    var activeJobs = jobs.filter(function (job) { return job.status !== 'failed'; }).length;
    if (uploadLink) {
      var badge = uploadLink.querySelector('.nav-count');
      if (activeJobs && !badge) { badge = document.createElement('span'); badge.className = 'nav-count'; uploadLink.appendChild(badge); }
      if (badge && activeJobs) badge.textContent = activeJobs;
      else if (badge) badge.remove();
    }
  }
  var sortChoices = [
    ['uploadedDesc', 'Uploaded ↓', 'history'], ['uploadedAsc', 'Uploaded ↑', 'history'],
    ['durationDesc', 'Duration ↓', 'clock'], ['durationAsc', 'Duration ↑', 'clock'],
    ['viewsDesc', 'Views ↓', 'eye'], ['viewsAsc', 'Views ↑', 'eye'], ['random', 'Random', 'shuffle']
  ];
  function sortGlyph(choice) {
    return '<span class="sort-glyph">' + icon(choice[2]) + (choice[0] === 'random' ? '' : '<span class="sort-direction" aria-hidden="true">' + (choice[0].endsWith('Asc') ? '↑' : '↓') + '</span>') + '</span>';
  }
  function sortControl(sort) {
    var chosen = sortChoices.find(function (entry) { return entry[0] === sort; }) || sortChoices[0];
    return '<div class="sort-control"><button type="button" class="button ghost icon-button" data-action="sort-toggle" data-testid="sort-filter" aria-label="Sort: ' + esc(chosen[1]) + '" title="Sort: ' + esc(chosen[1]) + '" aria-haspopup="menu" aria-controls="sort-menu" aria-expanded="false">' + sortGlyph(chosen) + '</button><div id="sort-menu" class="sort-menu" role="menu" aria-label="Sort media" hidden>' + sortChoices.map(function (entry) { return '<button type="button" class="sort-option" role="menuitemradio" aria-checked="' + (entry[0] === sort) + '" data-action="choose-sort" data-sort="' + entry[0] + '">' + icon(entry[2]) + '<span>' + esc(entry[1]) + '</span>' + (entry[0] === sort ? icon('check') : '') + '</button>'; }).join('') + '</div></div>';
  }
  function closeSortMenu(restoreFocus) {
    var menu = document.getElementById('sort-menu');
    var trigger = document.querySelector('[data-action="sort-toggle"]');
    if (menu) menu.hidden = true;
    if (trigger) { trigger.setAttribute('aria-expanded', 'false'); if (restoreFocus) trigger.focus(); }
  }
  function updateSearch(patch) {
    var input = document.getElementById('tag-query');
    var query = input ? input.value : (currentRoute.context || {}).q || '';
    try { compileQuery(query); }
    catch (error) {
      queryDraft = query;
      var existing = document.getElementById('query-error');
      if (!existing) { existing = document.createElement('p'); existing.id = 'query-error'; existing.className = 'form-error query-error'; existing.setAttribute('role', 'alert'); workspace.querySelector('.search-toolbar').after(existing); }
      existing.textContent = error.message + ' Previous results are unchanged.';
      if (input) { input.setAttribute('aria-invalid', 'true'); input.setAttribute('aria-describedby', 'query-error'); }
      return;
    }
    changeLibrary(Object.assign({ q: query, page: 1, seed: Date.now().toString(36) }, patch || {}));
  }
  function renderLibrary() {
    resetCardTags();
    var context = parseLibrary(location.hash);
    queryError = '';
    try { currentResults = resultsFor(context); }
    catch (error) { queryError = error.message; currentResults = resultsFor(Object.assign({}, context, { q: '' })); }
    var pages = Math.max(1, Math.ceil(currentResults.length / PAGE_SIZE));
    context.page = Math.min(context.page, pages);
    activeContext = libraryURL(context);
    currentRoute.context = context;
    currentPageItems = currentResults.slice((context.page - 1) * PAGE_SIZE, context.page * PAGE_SIZE);
    selected = context.view === 'list' ? new Set(Array.from(selected).filter(function (id) { return currentPageItems.some(function (item) { return item.id === id; }); })) : new Set();
    var title = context.u.length ? 'Added media' : context.collection != null ? collectionName(context.collection) : 'Library';
    var searchValue = queryDraft != null ? queryDraft : context.q;
    var filters = '';
    if (context.u.length) filters += '<span class="chip">From uploads</span>';
    if (context.collection != null) filters += '<span class="chip">' + esc(collectionName(context.collection)) + '</span>';
    workspace.innerHTML = '<h1 class="' + (context.u.length || context.collection != null ? 'context-title' : 'sr-only') + '">' + esc(title) + '</h1>' +
      '<section class="search-toolbar" aria-label="Search and filters"><form id="search-form" class="search-box" role="search"><button class="button ghost icon-button" type="submit" data-testid="apply-search" aria-label="Search" title="Search">' + icon('search') + '</button><input id="tag-query" data-testid="tag-query" name="q" type="text" autocomplete="off" spellcheck="false" aria-label="Search by tags"' + (queryError ? ' aria-describedby="query-error" aria-invalid="true"' : '') + ' placeholder="Search by tags" value="' + esc(searchValue) + '"><div class="search-actions"><div class="filters" role="group" aria-label="Media types"><button type="button" class="button ghost icon-button" data-action="toggle-image" data-testid="kind-image" aria-label="Photos" title="Photos" aria-pressed="' + context.imageSelected + '">' + icon('image') + '</button><button type="button" class="button ghost icon-button" data-action="toggle-video" data-testid="kind-video" aria-label="Videos" title="Videos" aria-pressed="' + context.videoSelected + '">' + icon('video') + '</button></div>' + sortControl(context.sort) + button('clear-search', '', { className: 'ghost icon-button', icon: 'close', title: 'Clear filters', testid: 'clear-filters' }) + '</div></form><div class="library-view-controls">' + (context.view === 'grid' ? thumbnailControls() : '') + '<div class="view-switch" aria-label="View"><button class="button icon-button ' + (context.view === 'grid' ? 'active' : '') + '" data-action="view-grid" aria-label="Grid" title="Grid" aria-pressed="' + (context.view === 'grid') + '">' + icon('grid') + '</button><button class="button icon-button ' + (context.view === 'list' ? 'active' : '') + '" data-action="view-list" aria-label="List" title="List" aria-pressed="' + (context.view === 'list') + '">' + icon('list') + '</button></div></div></section>' +
      (queryError ? '<p id="query-error" class="form-error query-error" role="alert">' + esc(queryError) + ' Showing results without this expression.</p>' : '') +
      (filters ? '<div class="active-filters">' + filters + '</div>' : '') +
      (context.view === 'list' ? '<div id="selection-toolbar">' + selectionToolbar() + '</div>' : '') +
      (currentPageItems.length ? context.view === 'grid' ? '<div class="media-grid density-' + prefs.density + '" style="--card-min:' + densityWidth() + 'px" data-density="' + prefs.density + '">' + currentPageItems.map(function (item) { return mediaCard(item, activeContext); }).join('') + '</div>' : renderList(currentPageItems, activeContext) : '<div class="empty-state">' + icon('search') + '<h2>No results</h2><p>Try another tag or clear filters.</p></div>') +
      '<footer class="library-footer" data-testid="library-footer"><strong data-testid="results-total">' + countLabel(currentResults.length) + '</strong>' + pagination(context, pages) + '</footer>';
  }
  function densityWidth() { return [360, 310, 270, 220, 185, 150, 120][Math.max(0, Math.min(6, prefs.density - 1))]; }
  function isLibraryList() { return currentRoute && currentRoute.type === 'library' && currentRoute.context && currentRoute.context.view === 'list'; }
  function thumbnailControls() {
    return '<div class="density-control" role="group" aria-label="Thumbnail size">' + button('thumbnail-larger', '', { icon: 'plus', title: 'Larger thumbnails', className: 'ghost icon-button', testid: 'thumbnail-larger', disabled: prefs.density <= 1 }) + button('thumbnail-smaller', '', { icon: 'minus', title: 'Smaller thumbnails', className: 'ghost icon-button', testid: 'thumbnail-smaller', disabled: prefs.density >= 7 }) + '</div>';
  }
  function resizeThumbnails(delta) {
    if (!currentRoute || currentRoute.type !== 'library' || currentRoute.context.view !== 'grid') return;
    prefs.density = Math.max(1, Math.min(7, prefs.density + delta));
    persist();
    var grid = workspace.querySelector('.media-grid');
    if (grid) { grid.className = 'media-grid density-' + prefs.density; grid.dataset.density = prefs.density; grid.style.setProperty('--card-min', densityWidth() + 'px'); }
    workspace.querySelector('[data-testid="thumbnail-larger"]').disabled = prefs.density <= 1;
    workspace.querySelector('[data-testid="thumbnail-smaller"]').disabled = prefs.density >= 7;
    positionCardTags();
  }
  function selectionToolbar() {
    if (!isLibraryList() || !selected.size) return '';
    return '<div class="selection-toolbar"><strong>Selected: ' + selected.size + '</strong>' + button('bulk-edit', 'Tags and collection', { icon: 'edit', className: 'secondary small', testid: 'bulk-edit' }) + button('clear-selection', '', { icon: 'close', title: 'Clear selection', className: 'ghost icon-button' }) + '</div>';
  }
  function mediaCard(item, context) {
    var label = mediaLabel(item);
    var tagText = item.tagIds.map(tagName).join(' · ');
    return '<article class="media-card" data-testid="media-card" data-id="' + item.id + '" data-card-tags="' + esc(tagText) + '"><div class="card-media"><a href="' + esc(contentURL(item.id, context)) + '" data-content-link data-id="' + item.id + '" data-media-tags="' + esc(tagText) + '" aria-label="Open ' + esc(label) + '"><img src="' + esc(item.poster || item.src) + '" alt="' + esc(label) + '" loading="lazy" draggable="false"></a>' + (item.kind === 'video' ? '<span class="video-badge" aria-label="Video">' + icon('play') + '</span>' : '') + (item.isFavorite ? '<span class="favorite-marker" aria-label="Favorite" title="Favorite">' + icon('star') + '</span>' : '') + '</div></article>';
  }
  function renderList(items, context) {
    return '<div class="table-scroll"><table class="data-table media-list"><thead class="sr-only"><tr><th>Media</th><th>Tags</th><th>Collection</th><th>Actions</th></tr></thead><tbody>' + items.map(function (item) {
      var label = mediaLabel(item);
      return '<tr data-testid="media-card" data-id="' + item.id + '"><td><a class="list-media" href="' + esc(contentURL(item.id, context)) + '" data-content-link data-id="' + item.id + '" aria-label="Open ' + esc(label) + '"><img src="' + esc(item.poster || item.src) + '" alt="">' + (item.kind === 'video' ? '<span class="list-play" aria-label="Video">' + icon('play') + '</span>' : '') + (item.isFavorite ? '<span class="favorite-marker" title="Favorite" aria-label="Favorite">' + icon('star') + '</span>' : '') + '</a></td><td><div class="tag-list">' + item.tagIds.map(function (id) { return '<a class="tag" href="' + esc(libraryURL(Object.assign(parseLibrary(''), { q: tagName(id) }))) + '">' + esc(tagName(id)) + '</a>'; }).join('') + '</div></td><td>' + (item.collectionId != null ? '<a class="list-collection" href="' + esc(libraryURL(Object.assign(parseLibrary(''), { collection: item.collectionId }))) + '">' + esc(collectionName(item.collectionId)) + '</a>' : '') + '</td><td class="list-actions-cell"><div class="list-row-actions">' + button('edit-item', '', { id: item.id, icon: 'edit', title: 'Edit media', className: 'ghost icon-button list-edit', testid: 'list-edit' }) + '<label class="list-select-control"><input class="list-select" type="checkbox" data-select="' + item.id + '" aria-label="Select ' + esc(label) + '"' + (selected.has(item.id) ? ' checked' : '') + '></label></div></td></tr>';
    }).join('') + '</tbody></table></div>';
  }
  function pagination(context, pages) {
    var numbers = [], start = Math.max(1, context.page - 2), end = Math.min(pages, context.page + 2);
    if (start > 1) numbers.push(1);
    if (start > 2) numbers.push('…');
    for (var n = start; n <= end; n++) numbers.push(n);
    if (end < pages - 1) numbers.push('…');
    if (end < pages) numbers.push(pages);
    return '<nav class="pagination" aria-label="Result pages"><span>Page ' + context.page + ' of ' + pages + '</span><div>' + button('previous-page', 'Previous', { disabled: context.page <= 1, className: 'ghost small', testid: 'previous-page' }) + numbers.map(function (n) { return typeof n === 'number' ? '<a class="button small ' + (n === context.page ? 'primary' : 'ghost') + '" href="' + esc(libraryURL(Object.assign({}, context, { page: n }))) + '"' + (n === context.page ? ' aria-current="page"' : '') + ' data-testid="page-' + n + '">' + n + '</a>' : '<span>…</span>'; }).join('') + button('next-page', 'Next', { disabled: context.page >= pages, className: 'ghost small', testid: 'next-page' }) + '</div></nav>';
  }
  function viewerContext() {
    var params = new URLSearchParams(location.hash.split('?')[1] || '');
    var raw = params.get('context') || activeContext || libraryURL(parseLibrary(''));
    if (raw.indexOf('#/library') !== 0) raw = libraryURL(parseLibrary(''));
    return raw;
  }
  function renderViewer() {
    var item = itemById(currentRoute.id);
    if (!item) {
      workspace.innerHTML = header('VIEWER', 'Media not found', 'This item is not in the demo library.', '<a class="button secondary" href="#/library">Back to library</a>');
      return;
    }
    var context = viewerContext();
    activeContext = context;
    var contextItems;
    if (contextSnapshots[context]) {
      var indexedItems = Object.create(null);
      allItems().forEach(function (entry) { indexedItems[entry.id] = entry; });
      contextItems = contextSnapshots[context].map(function (id) { return indexedItems[id]; }).filter(Boolean);
    } else {
      try { contextItems = resultsFor(parseLibrary(context)); } catch (_) { contextItems = allItems(); }
      contextSnapshots[context] = contextItems.map(function (entry) { return entry.id; });
    }
    currentResults = contextItems;
    var index = contextItems.findIndex(function (entry) { return entry.id === item.id; });
    var previous = index > 0 ? contextItems[index - 1] : null;
    var next = index >= 0 && index < contextItems.length - 1 ? contextItems[index + 1] : null;
    var tags = item.tagIds.map(function (id) { return '<a class="tag" href="' + esc(libraryURL(Object.assign(parseLibrary(''), { q: tagName(id) }))) + '">' + esc(tagName(id)) + '</a>'; }).join('');
    var primaryActions = '<div class="viewer-primary-actions"><a class="button ghost icon-button" href="' + esc(context) + '" data-testid="back-to-library" aria-label="Back to results" title="Back to results">' + icon('back') + '</a><div class="viewer-info-actions" role="group" aria-label="Organize media">' + button('edit-item', '', { id: item.id, icon: 'edit', title: 'Edit tags and collection', className: 'ghost icon-button', testid: 'edit-item' }) + button('favorite', '', { id: item.id, icon: 'star', title: item.isFavorite ? 'Remove from favorites' : 'Add to favorites', className: 'ghost icon-button favorite-button', testid: 'favorite-toggle' }) + '</div></div>';
    var neighbors = '<div class="viewer-nav">' + (previous ? '<a class="button icon-button ghost" href="' + esc(contentURL(previous.id, context)) + '" aria-label="Previous item" title="Previous item" data-testid="viewer-previous">' + icon('previous') + '</a>' : button('noop', '', { icon: 'previous', title: 'Previous item', className: 'icon-button ghost', disabled: true })) + '<span class="viewer-position">' + (index >= 0 ? (index + 1) + ' / ' + contextItems.length : '') + '</span>' + (next ? '<a class="button icon-button ghost" href="' + esc(contentURL(next.id, context)) + '" aria-label="Next item" title="Next item" data-testid="viewer-next">' + icon('chevron') + '</a>' : button('noop', '', { icon: 'chevron', title: 'Next item', className: 'icon-button ghost', disabled: true })) + '</div>';
    workspace.innerHTML = '<h1 class="sr-only">Media viewer</h1><section class="viewer-main" aria-label="Media"><div class="viewer-stage" data-testid="viewer-stage">' + mediaMarkup(item) + '</div></section>' +
      '<section class="viewer-info" data-testid="viewer-secondary" aria-label="Media controls, tags and collection"><div class="viewer-navigation" data-testid="viewer-navigation">' + primaryActions + neighbors + viewerControls(item) + '</div>' +
      '<div class="viewer-organizing"><div class="tag-list">' + tags + '</div></div></section>' + relatedMarkup(item);
    workspace.querySelector('[data-action="favorite"]').setAttribute('aria-pressed', String(!!item.isFavorite));
    applyMediaTransform();
    startViewTimer(item.id);
  }
  function mediaMarkup(item) {
    if (item.kind === 'video') return '<video controls preload="metadata" poster="' + esc(item.poster || item.src) + '" src="' + esc(item.videoSrc || F.videoSrc || '') + '" tabindex="0" aria-label="' + esc(mediaLabel(item)) + '" data-testid="viewer-video">Your browser does not support video.</video>';
    return '<img class="viewer-image" src="' + esc(item.src) + '" alt="' + esc(mediaLabel(item)) + '" data-testid="viewer-image" draggable="false">';
  }
  function viewerControls(item) {
    if (item.kind === 'video') return '';
    return '<details class="image-tools"><summary class="button ghost icon-button" data-action="image-tools-toggle" data-testid="image-tools-toggle" aria-label="Image tools" title="Image tools" aria-controls="image-tools-panel" aria-expanded="false">' + icon('adjust') + '</summary><div class="viewer-controls" id="image-tools-panel" data-testid="image-tools-panel" role="group" aria-label="Image tools">' + button('zoom-out', '', { icon: 'minus', title: 'Zoom out', className: 'ghost icon-button' }) + '<button type="button" class="button ghost zoom-reset" data-action="zoom-reset" aria-label="Reset zoom and rotation" title="Reset zoom and rotation"><span data-zoom-label>100%</span></button>' + button('zoom-in', '', { icon: 'plus', title: 'Zoom in', className: 'ghost icon-button', testid: 'zoom-in' }) + button('rotate', '', { icon: 'rotate', title: 'Rotate 90°', className: 'ghost icon-button', testid: 'rotate-image' }) + '</div></details>';
  }
  function closeImageTools(restoreFocus) {
    var details = workspace.querySelector('.image-tools');
    if (!details) return;
    details.open = false;
    var trigger = details.querySelector('summary');
    trigger.setAttribute('aria-expanded', 'false');
    if (restoreFocus) trigger.focus();
  }
  function relatedMarkup(item) {
    var exact = item.tagIds.slice().sort(function (a, b) { return a - b; }).join(',');
    var tagSearch = libraryURL(Object.assign(parseLibrary(''), { q: item.tagIds.map(tagName).join(' && ') }));
    var related = exact ? allItems().filter(function (candidate) { return candidate.id !== item.id && candidate.tagIds.slice().sort(function (a, b) { return a - b; }).join(',') === exact; }).sort(function (a, b) { return new Date(b.uploadedAt) - new Date(a.uploadedAt) || a.id - b.id; }).slice(0, 4) : [];
    var siblings = item.collectionId != null ? allItems().filter(function (candidate) { return candidate.id !== item.id && candidate.collectionId === item.collectionId; }).slice(0, 4) : [];
    var output = '';
    if (item.collectionId != null) output += '<section class="related-section" data-testid="collection-section"><div class="section-heading"><h2><a class="collection-heading" data-testid="collection-heading" href="' + esc(libraryURL(Object.assign(parseLibrary(''), { collection: item.collectionId }))) + '">' + esc(collectionName(item.collectionId)) + '</a></h2></div>' + (siblings.length ? '<div class="media-grid related-grid">' + siblings.map(function (entry) { return mediaCard(entry, libraryURL(Object.assign(parseLibrary(''), { collection: item.collectionId })), true); }).join('') + '</div>' : '') + '</section>';
    if (related.length) output += '<section class="related-section" data-testid="same-tags-section"><div class="section-heading"><h2><a class="related-heading" data-testid="same-tags-heading" href="' + esc(tagSearch) + '">Same tags</a></h2></div><div class="media-grid related-grid">' + related.map(function (entry) { return mediaCard(entry, libraryURL(Object.assign(parseLibrary(''), { u: related.map(function (r) { return r.id; }) })), true); }).join('') + '</div></section>';
    return output;
  }
  function startViewTimer(id) {
    if (viewSession && viewSession.id === id) return;
    clearTimeout(viewTimer);
    viewSession = { id: id, eventId: 'demo-view-' + id + '-' + Date.now() + '-' + Math.random().toString(36).slice(2), committed: false };
    var session = viewSession;
    viewTimer = setTimeout(function () {
      if (viewSession !== session || session.committed || currentRoute.type !== 'content' || currentRoute.id !== id) return;
      session.committed = true;
      var item = itemById(id);
      overrides[id] = Object.assign({}, overrides[id], { viewCount: item.viewCount + 1 });
      persist();
    }, 3000);
  }
  function applyMediaTransform() {
    var image = workspace.querySelector('.viewer-image');
    if (image) image.style.transform = 'scale(' + mediaTransform.scale + ') rotate(' + mediaTransform.rotation + 'deg)';
    var label = workspace.querySelector('[data-zoom-label]');
    if (label) label.textContent = Math.round(mediaTransform.scale * 100) + '%';
  }
  function showDialog(html, label, className) {
    resetCardTags();
    previousFocus = document.activeElement;
    overlayRoot.innerHTML = '<dialog class="modal ' + (className || '') + '" aria-label="' + esc(label) + '">' + html + '</dialog>';
    dialogElement = overlayRoot.querySelector('dialog');
    dialogElement.addEventListener('cancel', function (event) { event.preventDefault(); if (pendingSave) return; if (modal && modal.type === 'editor' && modal.optionKind) closeEditorOptions(); else closeDialog(); });
    dialogElement.addEventListener('click', function (event) { if (event.target === dialogElement && !pendingSave) { var rect = dialogElement.getBoundingClientRect(); if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) closeDialog(); } });
    if (dialogElement.showModal) dialogElement.showModal(); else { dialogElement.setAttribute('open', ''); overlayRoot.classList.add('modal-backdrop'); }
    var focusTarget = dialogElement.querySelector('[autofocus]') || dialogElement.querySelector('input, select, textarea') || dialogElement.querySelector('button, a');
    if (focusTarget) focusTarget.focus();
  }
  function closeDialog() {
    if (pendingSave) return;
    var focus = previousFocus;
    if (dialogElement && dialogElement.open && dialogElement.close) dialogElement.close();
    overlayRoot.innerHTML = '';
    overlayRoot.classList.remove('modal-backdrop');
    modal = null;
    dialogElement = null;
    if (focus && document.contains(focus)) focus.focus();
  }
  function closeEditorOptions() {
    if (!modal || modal.type !== 'editor') return;
    modal.optionKind = null;
    modal.activeOption = -1;
    modal.collectionEditing = false;
    modal.collectionQuery = '';
    var collectionInput = overlayRoot.querySelector('[data-testid="editor-collection-search"]');
    if (collectionInput) collectionInput.value = modal.draftCollection === '' ? '' : collectionName(Number(modal.draftCollection));
    ['tag', 'collection'].forEach(function (kind) {
      var input = overlayRoot.querySelector('[data-testid="editor-' + kind + '-search"]');
      var list = overlayRoot.querySelector('#editor-' + kind + '-options');
      if (input) { input.setAttribute('aria-expanded', 'false'); input.removeAttribute('aria-activedescendant'); }
      if (list) list.hidden = true;
    });
  }
  function editorMatches(kind) {
    var input = overlayRoot.querySelector('[data-testid="editor-' + kind + '-search"]');
    var query = kind === 'collection' ? (modal.collectionQuery || '').trim().toLowerCase() : input ? input.value.trim().toLowerCase() : '';
    return (kind === 'tag' ? F.tags.filter(function (tag) { return modal.draftTags.indexOf(tag.id) < 0; }) : F.collections).filter(function (entry) { return entry.name.toLowerCase().includes(query); });
  }
  function renderEditorSelection() {
    if (!modal || modal.type !== 'editor') return;
    overlayRoot.querySelector('[data-testid="editor-selected-tags"]').innerHTML = modal.draftTags.map(function (id) {
      return '<span class="selected-tag" data-tag-id="' + id + '"><span>' + esc(tagName(id)) + '</span>' + button('editor-tag-remove', '', { id: id, icon: 'close', title: 'Remove tag ' + tagName(id), className: 'ghost icon-button', testid: 'editor-tag-remove-' + id, disabled: pendingSave }) + '</span>';
    }).join('');
    var collection = overlayRoot.querySelector('[data-testid="editor-selected-collection"]');
    collection.dataset.collectionId = modal.draftCollection;
    var input = collection.querySelector('[data-testid="editor-collection-search"]');
    if (!modal.collectionEditing) input.value = modal.draftCollection === '' ? '' : collectionName(Number(modal.draftCollection));
    var clear = collection.querySelector('[data-testid="editor-collection-clear"]');
    clear.hidden = modal.draftCollection === '';
    clear.disabled = pendingSave;
  }
  function renderEditorOptions(kind, activeIndex, queryValue) {
    if (!modal || modal.type !== 'editor' || pendingSave) return;
    if (modal.optionKind !== kind) closeEditorOptions();
    modal.optionKind = kind;
    if (kind === 'collection' && !modal.collectionEditing) { modal.collectionEditing = true; modal.collectionQuery = ''; }
    if (kind === 'collection' && queryValue !== undefined) { modal.collectionQuery = queryValue; overlayRoot.querySelector('[data-testid="editor-collection-search"]').value = queryValue; }
    var matches = editorMatches(kind);
    modal.activeOption = activeIndex == null ? -1 : Math.max(-1, Math.min(matches.length - 1, activeIndex));
    var input = overlayRoot.querySelector('[data-testid="editor-' + kind + '-search"]');
    var list = overlayRoot.querySelector('#editor-' + kind + '-options');
    list.innerHTML = matches.length ? matches.map(function (entry, index) {
      return '<button type="button" tabindex="-1" class="editor-option' + (index === modal.activeOption ? ' is-active' : '') + '" role="option" id="editor-' + kind + '-option-' + entry.id + '" data-action="editor-' + kind + '-pick" data-id="' + entry.id + '" data-testid="editor-' + kind + '-option-' + entry.id + '" aria-selected="' + (kind === 'collection' ? String(entry.id) === modal.draftCollection : index === modal.activeOption) + '">' + esc(entry.name) + '</button>';
    }).join('') : '<div class="editor-no-match">' + (kind === 'tag' ? 'No matching tags' : 'No matching collections') + '</div>';
    list.hidden = false;
    input.setAttribute('aria-expanded', 'true');
    positionEditorOptions();
    if (modal.activeOption >= 0) {
      var optionId = 'editor-' + kind + '-option-' + matches[modal.activeOption].id;
      input.setAttribute('aria-activedescendant', optionId);
      var option = list.querySelector('#' + optionId);
      var optionRect = option.getBoundingClientRect();
      var listRect = list.getBoundingClientRect();
      if (optionRect.top < listRect.top + 4) list.scrollTop -= listRect.top + 4 - optionRect.top;
      else if (optionRect.bottom > listRect.bottom - 4) list.scrollTop += optionRect.bottom - listRect.bottom + 4;
    } else input.removeAttribute('aria-activedescendant');
  }
  function positionEditorOptions() {
    if (!modal || modal.type !== 'editor' || !modal.optionKind) return;
    var input = overlayRoot.querySelector('[data-testid="editor-' + modal.optionKind + '-search"]');
    var list = overlayRoot.querySelector('#editor-' + modal.optionKind + '-options');
    if (!input || !list || list.hidden) return;
    var rect = input.getBoundingClientRect();
    var body = input.closest('.modal-body').getBoundingClientRect();
    if (rect.bottom < body.top || rect.top > body.bottom) { closeEditorOptions(); return; }
    var footer = input.closest('form').querySelector('.modal-footer').getBoundingClientRect();
    var below = Math.min(innerHeight - 12, footer.top - 4) - rect.bottom - 6;
    var above = rect.top - Math.max(12, body.top + 4) - 6;
    var height = Math.min(220, list.scrollHeight);
    var flip = below < height && above > below;
    list.style.width = Math.min(rect.width, innerWidth - 24) + 'px';
    list.style.maxHeight = Math.max(36, Math.min(220, flip ? above : below)) + 'px';
    list.style.left = Math.max(12, Math.min(rect.left, innerWidth - rect.width - 12)) + 'px';
    var actualHeight = list.getBoundingClientRect().height;
    var top = flip ? rect.top - actualHeight - 6 : rect.bottom + 6;
    list.style.top = Math.max(12, Math.min(top, innerHeight - actualHeight - 12)) + 'px';
  }
  function pickEditorOption(kind, id) {
    if (!modal || modal.type !== 'editor' || pendingSave) return;
    var existing = (kind === 'tag' ? F.tags : F.collections).find(function (entry) { return entry.id === Number(id); });
    if (!existing) return;
    if (kind === 'tag') { if (modal.draftTags.indexOf(existing.id) < 0) modal.draftTags.push(existing.id); }
    else { modal.draftCollection = String(existing.id); modal.collectionEditing = false; }
    var input = overlayRoot.querySelector('[data-testid="editor-' + kind + '-search"]');
    if (kind === 'tag') input.value = '';
    renderEditorSelection();
    input.focus({ preventScroll: true });
    if (kind === 'tag') renderEditorOptions(kind); else closeEditorOptions();
  }
  function handleEditorKey(event) {
    if (!modal || modal.type !== 'editor') return false;
    var input = event.target.closest('[data-editor-search]');
    if (event.isComposing || modal.composing || event.keyCode === 229) return !!input;
    if (event.key === 'Escape' && modal.optionKind) { event.preventDefault(); closeEditorOptions(); return true; }
    if (!input) return false;
    if (event.key === 'Tab') { closeEditorOptions(); return false; }
    if (pendingSave) { if (event.key === 'Enter' || event.key === ',') event.preventDefault(); return true; }
    var kind = input.dataset.editorSearch;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      var matches = editorMatches(kind);
      var index = modal.optionKind === kind ? modal.activeOption : -1;
      index = index < 0 ? (event.key === 'ArrowUp' ? matches.length - 1 : 0) : (index + (event.key === 'ArrowUp' ? -1 : 1) + matches.length) % matches.length;
      renderEditorOptions(kind, index);
      return true;
    }
    if (event.key === 'Enter' || (event.key === ',' && kind === 'tag')) {
      event.preventDefault();
      var options = editorMatches(kind);
      var chosen = event.key === 'Enter' && modal.optionKind === kind && modal.activeOption >= 0 ? options[modal.activeOption] : options.find(function (entry) { return entry.name.toLowerCase() === input.value.trim().toLowerCase(); });
      if (chosen) pickEditorOption(kind, chosen.id); else renderEditorOptions(kind);
      return true;
    }
    return false;
  }
  function openEditor(ids, bulk) {
    if (bulk && !isLibraryList()) return;
    var item = itemById(ids[0]);
    modal = { type: 'editor', ids: ids.slice(), bulk: !!bulk, draftTags: bulk ? [] : Array.from(new Set(item.tagIds)), draftCollection: bulk ? '' : item.collectionId == null ? '' : String(item.collectionId), optionKind: null, activeOption: -1, composing: false, collectionEditing: false, collectionQuery: '' };
    showDialog('<form id="editor-form"><div class="modal-header"><h2>' + (bulk ? 'Tags · ' + ids.length : 'Tags') + '</h2>' + button('close-modal', '', { icon: 'close', title: 'Close without saving', className: 'ghost icon-button' }) + '</div><div class="modal-body">' + (bulk ? '<p class="bulk-warning">Replaces tags and collection for ' + ids.length + ' items.</p>' : '') + '<div class="field"><label class="sr-only" for="editor-tag-search">Tags</label><div class="editor-selected-tags" data-testid="editor-selected-tags"></div><div class="editor-combobox"><input id="editor-tag-search" data-testid="editor-tag-search" data-editor-search="tag" role="combobox" aria-autocomplete="list" aria-expanded="false" aria-controls="editor-tag-options" autocomplete="off" placeholder="Search tags" spellcheck="false"><div id="editor-tag-options" data-testid="editor-tag-options" class="editor-options" role="listbox" aria-label="Tags" hidden></div></div></div><div class="field"><label class="field-label" for="editor-collection-search">Collection</label><div class="editor-single-select editor-combobox" data-testid="editor-selected-collection"><input id="editor-collection-search" data-testid="editor-collection-search" data-editor-search="collection" role="combobox" aria-autocomplete="list" aria-expanded="false" aria-controls="editor-collection-options" autocomplete="off" placeholder="No collection" spellcheck="false">' + button('editor-collection-clear', '', { icon: 'close', title: 'Clear collection', className: 'ghost icon-button', testid: 'editor-collection-clear' }) + '<span class="single-select-chevron" aria-hidden="true">' + icon('chevron') + '</span><div id="editor-collection-options" data-testid="editor-collection-options" class="editor-options" role="listbox" aria-label="Collections" hidden></div></div></div><p class="form-error" id="save-error" role="alert" hidden></p></div><div class="modal-footer">' + button('close-modal', 'Cancel', { className: 'secondary', testid: 'editor-cancel' }) + '<button class="button primary" type="submit" data-testid="editor-save"><span>Save</span></button></div></form>', bulk ? 'Replace tags and collection for ' + ids.length + ' items' : 'Edit tags and collection', 'editor-modal');
    renderEditorSelection();
    overlayRoot.querySelector('[data-testid="editor-tag-search"]').focus({ preventScroll: true });
  }
  function saveEditor(event) {
    event.preventDefault();
    if (pendingSave || !modal || modal.type !== 'editor' || modal.composing || event.isComposing) return;
    var form = event.target;
    var tags = F.tags.filter(function (tag) { return modal.draftTags.indexOf(tag.id) >= 0; }).map(function (tag) { return tag.id; });
    var collection = modal.draftCollection === '' ? null : Number(modal.draftCollection);
    closeEditorOptions();
    var ids = modal.ids.slice();
    var restoreListEditFocus = isLibraryList() && !modal.bulk;
    var generation = ++saveGeneration;
    pendingSave = true;
    form.querySelectorAll('button, input, select').forEach(function (control) { control.disabled = true; });
    var saveButton = form.querySelector('[data-testid="editor-save"]');
    saveButton.innerHTML = '<span>Saving…</span>';
    form.querySelector('#save-error').hidden = true;
    setTimeout(function () {
      if (generation !== saveGeneration) return;
      pendingSave = false;
      if (failNextSave) {
        failNextSave = false;
        form.querySelectorAll('button, input, select').forEach(function (control) { control.disabled = false; });
        saveButton.innerHTML = icon('check') + '<span>Save' + (ids.length > 1 ? ' for ' + ids.length : '') + '</span>';
        var error = form.querySelector('#save-error');
        error.textContent = 'Could not save. Your changes are still here. Try again.';
        error.hidden = false;
        error.focus();
        return;
      }
      ids.forEach(function (id) { overrides[id] = Object.assign({}, overrides[id], { tagIds: tags.slice(), collectionId: collection }); });
      persist();
      closeDialog();
      selected.clear();
      render();
      if (restoreListEditFocus && isLibraryList()) {
        var rowEdit = workspace.querySelector('[data-testid="list-edit"][data-id="' + ids[0] + '"]');
        (rowEdit || workspace.querySelector('#tag-query')).focus({ preventScroll: true });
      }
      toast('Saved');
    }, 350);
  }
  function seedJobs() {
    return [
      { id: 'demo-seed-3', name: 'Morning at the lake.mp4', kind: 'video', sizeBytes: 34800000, status: 'processing', progress: 100, itemId: null, attempt: 1, tick: 0 },
      { id: 'demo-seed-4', name: 'Forest trail.jpg', kind: 'image', sizeBytes: 7240000, status: 'uploading', progress: 42, itemId: null, attempt: 1, tick: 0 },
      { id: 'demo-seed-5', name: 'Mountain sunset.jpg', kind: 'image', sizeBytes: 5310000, status: 'failed', progress: 100, itemId: null, attempt: 1, tick: 0, error: 'Could not process. Try again.' }
    ];
  }
  function renderUploads() {
    var busy = jobs.filter(function (job) { return job.status !== 'failed'; });
    var failed = jobs.filter(function (job) { return job.status === 'failed'; });
    workspace.innerHTML = header('', 'Uploads', '', button('add-demo-batch', '', { icon: 'plus', title: 'Add demo batch', className: 'ghost icon-button', testid: 'add-demo-batch' }) + button('choose-files', '', { icon: 'upload', title: 'Add media', className: 'ghost icon-button', testid: 'choose-files' })) + '<input id="upload-files" type="file" accept="image/*,video/*" multiple hidden data-testid="upload-input"><div class="upload-summary"><div class="summary-tile"><span>Total</span><strong data-testid="upload-total">' + jobs.length + '</strong></div><div class="summary-tile"><span>In progress</span><strong data-testid="upload-in-progress">' + busy.length + '</strong></div><div class="summary-tile failed"><span>Errors</span><strong data-testid="upload-errors">' + failed.length + '</strong></div></div><section class="upload-queue" aria-label="Upload queue">' + (uploadMessage ? '<p class="form-error" role="alert">' + esc(uploadMessage) + '</p>' : '') + '<div class="table-scroll" id="upload-table-wrap"' + (!jobs.length ? ' hidden' : '') + '><table class="data-table upload-table"><thead><tr><th>Media</th><th>Uploading</th><th>Status</th><th><span class="sr-only">Action</span></th></tr></thead><tbody>' + jobs.map(uploadRow).join('') + '</tbody></table></div><div class="upload-empty" data-testid="upload-empty" role="status"' + (jobs.length ? ' hidden' : '') + '>Queue is empty</div></section>';
  }
  function uploadRow(job) {
    var statusLabels = { queued: 'Queued', uploading: 'Uploading', processing: 'Processing', failed: 'Error' };
    return '<tr class="upload-row" data-testid="upload-job" data-job-id="' + esc(job.id) + '" data-status="' + job.status + '"><td><div class="upload-file-icon">' + icon(job.kind === 'video' ? 'video' : 'image') + '</div><div class="upload-file-name"><strong>' + esc(job.name) + '</strong></div></td><td><div class="progress-label"><span>' + job.progress + '%</span>' + (job.progress === 100 ? icon('check') : '') + '</div><div class="progress-track"><div class="progress-fill" style="width:' + job.progress + '%"></div></div></td><td><span class="status-badge ' + job.status + '">' + (job.status === 'processing' ? icon('clock') : '') + statusLabels[job.status] + '</span>' + (job.status === 'failed' ? '<small class="upload-status-detail">' + esc(job.error || 'Could not process.') + '</small>' : '') + '</td><td><div class="upload-actions">' + (job.status === 'failed' ? button('retry-upload', 'Retry', { id: job.id, icon: 'retry', className: 'secondary small', testid: 'retry-upload' }) : '') + button('cancel-upload', '', { id: job.id, icon: 'close', title: 'Cancel upload', className: 'ghost icon-button', testid: 'cancel-upload' }) + '</div></td></tr>';
  }
  function addJobs(files) {
    var accepted = [], rejected = [];
    files.forEach(function (file) {
      if (!file.size || !/^(image|video)\//.test(file.type)) rejected.push(file.name);
      else accepted.push(file);
    });
    accepted.forEach(function (file) {
      jobs.push({ id: 'demo-upload-' + Date.now() + '-' + sequence++, name: file.name, kind: file.type.indexOf('video/') === 0 ? 'video' : 'image', sizeBytes: file.size, status: 'queued', progress: 0, itemId: null, attempt: 1, tick: 0 });
    });
    uploadMessage = rejected.length ? 'Not added: ' + rejected.join(', ') + '. Choose nonempty photos or videos.' : '';
    persist(); renderSidebar();
    if (currentRoute.type === 'uploads') renderUploads();
    if (accepted.length) toast('Added to queue: ' + accepted.length);
  }
  function completeJob(job) {
    if (!jobs.some(function (entry) { return entry.id === job.id; })) return;
    if (!job.itemId) {
      var existing = extraItems.find(function (item) { return item.uploadJobId === job.id; });
      if (existing) job.itemId = existing.id;
      else {
        var base = F.items.find(function (item) { return item.kind === job.kind; }) || F.items[0];
        var id = Math.max.apply(null, F.items.map(function (item) { return item.id; }).concat(extraItems.map(function (item) { return item.id; }))) + 1;
        extraItems.push({ id: id, baseId: base.id, uploadJobId: job.id, kind: job.kind, sizeBytes: job.sizeBytes, uploadedAt: new Date().toISOString(), tagIds: [], collectionId: null, isFavorite: false, viewCount: 0 });
        job.itemId = id;
      }
    }
    jobs = jobs.filter(function (entry) { return entry.id !== job.id; });
  }
  function refreshUploadProgress() {
    if (currentRoute.type !== 'uploads' || modal || filePickerOpen) return;
    var active = document.activeElement;
    var lostFocus = false;
    var rows = Array.from(workspace.querySelectorAll('[data-job-id]'));
    rows.forEach(function (row) {
      if (!jobs.some(function (job) { return job.id === row.dataset.jobId; })) {
        if (row.contains(active)) lostFocus = true;
        row.remove();
      }
    });
    jobs.forEach(function (job) {
      var row = rows.find(function (entry) { return entry.dataset.jobId === job.id; });
      var temporaryBody = document.createElement('tbody');
      temporaryBody.innerHTML = uploadRow(job);
      var updated = temporaryBody.firstElementChild;
      if (!row) { workspace.querySelector('.upload-table tbody').appendChild(updated); return; }
      Array.from(row.children).forEach(function (cell, index) {
        if (!cell.contains(active)) cell.innerHTML = updated.children[index].innerHTML;
        else if (active.dataset.action && !updated.children[index].querySelector('[data-action="' + active.dataset.action + '"]')) {
          cell.innerHTML = updated.children[index].innerHTML;
          var replacement = cell.querySelector('button');
          if (replacement) replacement.focus({ preventScroll: true });
        }
      });
      row.dataset.status = job.status;
    });
    workspace.querySelector('[data-testid="upload-total"]').textContent = jobs.length;
    workspace.querySelector('[data-testid="upload-in-progress"]').textContent = jobs.filter(function (job) { return job.status !== 'failed'; }).length;
    workspace.querySelector('[data-testid="upload-errors"]').textContent = jobs.filter(function (job) { return job.status === 'failed'; }).length;
    workspace.querySelector('#upload-table-wrap').hidden = !jobs.length;
    workspace.querySelector('[data-testid="upload-empty"]').hidden = !!jobs.length;
    if (lostFocus) workspace.querySelector('[data-testid="choose-files"]').focus({ preventScroll: true });
  }
  function advanceJobs() {
    var changed = false;
    jobs.slice().forEach(function (job) {
      if (job.status === 'failed') return;
      job.tick = (job.tick || 0) + 1;
      changed = true;
      if (job.status === 'queued') { job.status = 'uploading'; job.progress = 4; job.tick = 0; }
      else if (job.status === 'uploading') { job.progress = Math.min(100, job.progress + 11 + (hashNumber(job.id) % 13)); if (job.progress === 100) { job.status = 'processing'; job.tick = 0; } }
      else if (job.status === 'processing' && job.tick >= 5) completeJob(job);
    });
    if (!changed) return;
    persist();
    refreshSidebarCounts();
    refreshUploadProgress();
  }
  function filterCollections(query, updateURL) {
    var normalized = query.trim().toLowerCase();
    var matches = F.collections.filter(function (collection) { return collection.name.toLowerCase().includes(normalized); });
    var library = allItems();
    workspace.querySelector('[data-testid="collections-grid"]').innerHTML = matches.map(function (collection) {
      var cover = library.find(function (item) { return item.collectionId === collection.id; });
      return '<a class="collection-card support-link" href="' + esc(libraryURL(Object.assign(parseLibrary(''), { collection: collection.id }))) + '">' + (cover ? '<img class="collection-cover" src="' + esc(cover.poster || cover.src) + '" alt="" loading="lazy">' : '<span class="collection-cover collection-cover-empty" aria-hidden="true">' + icon('folder') + '</span>') + '<h2 class="collection-title">' + esc(collection.name) + '</h2></a>';
    }).join('');
    workspace.querySelector('[data-testid="collections-empty"]').hidden = !!matches.length;
    workspace.querySelector('[data-testid="support-total"]').textContent = normalized ? matches.length.toLocaleString('en-US') + ' of ' + F.collections.length.toLocaleString('en-US') + ' collections' : 'Total: ' + F.collections.length.toLocaleString('en-US');
    workspace.querySelector('[data-testid="collections-search-clear"]').disabled = !query;
    if (updateURL) { var params = new URLSearchParams(); if (normalized) params.set('search', query); history.replaceState(history.state, '', '#/collections' + (params.size ? '?' + params.toString() : '')); }
  }
  function renderSupport(type) {
    if (type === 'tags') {
      var items = allItems();
      workspace.innerHTML = header('', 'Tags', '') + '<table class="support-table tags-table" data-testid="tags-list"><thead><tr><th scope="col">Tag</th><th scope="col">Usage count</th></tr></thead><tbody>' + F.tags.map(function (tag) {
        var count = items.filter(function (item) { return item.tagIds.indexOf(tag.id) >= 0; }).length;
        return '<tr><td><a class="tag support-link" href="' + esc(libraryURL(Object.assign(parseLibrary(''), { q: tag.name }))) + '">' + esc(tag.name) + '</a></td><td>' + count.toLocaleString('en-US') + '</td></tr>';
      }).join('') + '</tbody></table><p class="support-total" data-testid="support-total">Total: ' + F.tags.length.toLocaleString('en-US') + '</p>';
    } else if (type === 'collections') {
      var search = new URLSearchParams(location.hash.split('?')[1] || '').get('search') || '';
      workspace.innerHTML = header('', 'Collections', '') + '<div class="collections-search"><label class="sr-only" for="collections-search">Search collections</label><input id="collections-search" data-testid="collections-search" type="search" aria-label="Search collections" placeholder="Search collections" autocomplete="off" value="' + esc(search) + '">' + button('collections-search-clear', '', { icon: 'close', title: 'Clear collection search', className: 'ghost icon-button', testid: 'collections-search-clear', disabled: !search }) + '</div><div class="collections-grid" data-testid="collections-grid"></div><div class="collections-empty" data-testid="collections-empty" hidden>No collections found</div><p class="support-total" data-testid="support-total"></p>';
      filterCollections(search, false);
    } else {
      workspace.innerHTML = header('', 'Categories', '') + '<table class="support-table categories-table" data-testid="categories-list"><thead><tr><th scope="col">Name</th><th scope="col">Query</th><th scope="col">Items type</th></tr></thead><tbody>' + F.savedSearches.map(function (search) {
        var context = Object.assign(parseLibrary(''), { q: search.query || '', kind: search.kind || '' });
        return '<tr><td><a class="tag support-link" href="' + esc(libraryURL(context)) + '">' + esc(search.name) + '</a></td><td><code>' + esc(search.query || '') + '</code></td><td>' + (search.kind === 'video' ? 'Video' : search.kind === 'image' ? 'Photo' : 'All types') + '</td></tr>';
      }).join('') + '</tbody></table><p class="support-total" data-testid="support-total">Total: ' + F.savedSearches.length.toLocaleString('en-US') + '</p>';
    }
  }
  function render() {
    resetCardTags();
    var hash = location.hash || '#/library';
    var oldType = currentRoute && currentRoute.type;
    var match = hash.match(/^#\/content\/(\d+)/);
    currentRoute = match ? { type: 'content', id: Number(match[1]) } : { type: hash.indexOf('#/uploads') === 0 ? 'uploads' : hash.indexOf('#/tags') === 0 ? 'tags' : hash.indexOf('#/collections') === 0 ? 'collections' : (hash.indexOf('#/categories') === 0 || hash.indexOf('#/saved-searches') === 0) ? 'saved-searches' : 'library' };
    if (currentRoute.type !== 'content') { clearTimeout(viewTimer); viewSession = null; }
    renderSidebar();
    if (currentRoute.type === 'library') renderLibrary();
    else if (currentRoute.type === 'content') renderViewer();
    else if (currentRoute.type === 'uploads') renderUploads();
    else renderSupport(currentRoute.type);
    document.title = (currentRoute.type === 'content' ? 'Viewer' : ({ library: 'Library', uploads: 'Uploads', tags: 'Tags', collections: 'Collections', 'saved-searches': 'Categories' })[currentRoute.type]) + ' — TagFiles';
    if (currentRoute.type === 'library' && oldType === 'content') requestAnimationFrame(function () { window.scrollTo(0, scrollPositions[activeContext] || 0); });
    else if (oldType !== currentRoute.type || currentRoute.type === 'content') window.scrollTo(0, 0);
  }
  function changeLibrary(patch) {
    var context = Object.assign({}, currentRoute.context || parseLibrary(activeContext), patch);
    selected.clear(); queryDraft = null; queryError = '';
    navigate(libraryURL(context));
  }
  function runAction(action, target) {
    var id = target.dataset.id;
    if (action === 'noop') return;
    if (action === 'collections-search-clear') { var search = workspace.querySelector('[data-testid="collections-search"]'); search.value = ''; filterCollections('', true); search.focus({ preventScroll: true }); return; }
    if (action === 'editor-tag-pick' || action === 'editor-collection-pick') { pickEditorOption(action === 'editor-tag-pick' ? 'tag' : 'collection', id); return; }
    if (action === 'editor-tag-remove') {
      if (!pendingSave && modal && modal.type === 'editor') { modal.draftTags = modal.draftTags.filter(function (tagId) { return tagId !== Number(id); }); renderEditorSelection(); overlayRoot.querySelector('[data-testid="editor-tag-search"]').focus({ preventScroll: true }); renderEditorOptions('tag'); }
      return;
    }
    if (action === 'editor-collection-clear') {
      if (!pendingSave && modal && modal.type === 'editor') { modal.draftCollection = ''; modal.collectionEditing = false; renderEditorSelection(); overlayRoot.querySelector('[data-testid="editor-collection-search"]').focus({ preventScroll: true }); }
      return;
    }

    if (action === 'toggle-sidebar') { prefs.sidebarOpen = !prefs.sidebarOpen; persist(); applySidebarState(); positionCardTags(); return; }
    if (action === 'image-tools-toggle') { var details = target.closest('.image-tools'); details.open = !details.open; target.setAttribute('aria-expanded', String(details.open)); return; }
    if (action === 'close-modal') { closeDialog(); return; }
    if (action === 'edit-item') { openEditor([Number(id)], false); return; }
    if (action === 'bulk-edit') { if (isLibraryList() && selected.size) openEditor(Array.from(selected), true); return; }
    if (action === 'toggle-image' || action === 'toggle-video') {
      var context = currentRoute.context || parseLibrary(activeContext);
      var imageSelected = action === 'toggle-image' ? !context.imageSelected : context.imageSelected;
      var videoSelected = action === 'toggle-video' ? !context.videoSelected : context.videoSelected;
      updateSearch({ imageSelected: imageSelected, videoSelected: videoSelected, kind: imageSelected === videoSelected ? '' : imageSelected ? 'image' : 'video' });
      return;
    }
    if (action === 'sort-toggle') {
      var menu = document.getElementById('sort-menu');
      if (!menu.hidden) closeSortMenu(true);
      else { menu.hidden = false; target.setAttribute('aria-expanded', 'true'); (menu.querySelector('[aria-checked="true"]') || menu.querySelector('button')).focus(); }
      return;
    }
    if (action === 'choose-sort') { closeSortMenu(false); updateSearch({ sort: target.dataset.sort }); requestAnimationFrame(function () { var trigger = document.querySelector('[data-action="sort-toggle"]'); if (trigger) trigger.focus(); }); return; }
    if (action === 'clear-selection') { if (isLibraryList()) { selected.clear(); renderLibrary(); } return; }
    if (action === 'clear-search') { changeLibrary({ q: '', kind: '', imageSelected: false, videoSelected: false, page: 1, u: [], collection: null }); return; }
    if (action === 'view-grid' || action === 'view-list') { prefs.view = action === 'view-grid' ? 'grid' : 'list'; persist(); changeLibrary({ view: prefs.view }); return; }
    if (action === 'thumbnail-larger' || action === 'thumbnail-smaller') { resizeThumbnails(action === 'thumbnail-larger' ? -1 : 1); return; }
    if (action === 'next-page') { changeLibrary({ page: currentRoute.context.page + 1 }); window.scrollTo(0, 0); return; }
    if (action === 'previous-page') { changeLibrary({ page: Math.max(1, currentRoute.context.page - 1) }); window.scrollTo(0, 0); return; }
    if (action === 'zoom-in') mediaTransform.scale = Math.min(4, mediaTransform.scale + 0.25);
    if (action === 'zoom-out') mediaTransform.scale = Math.max(0.25, mediaTransform.scale - 0.25);
    if (action === 'zoom-reset') mediaTransform = { scale: 1, rotation: 0 };
    if (action === 'rotate') mediaTransform.rotation = (mediaTransform.rotation + 90) % 360;
    if (action.indexOf('zoom-') === 0 || action === 'rotate') { applyMediaTransform(); return; }
    if (action === 'favorite') {
      var item = itemById(id);
      overrides[id] = Object.assign({}, overrides[id], { isFavorite: !item.isFavorite }); persist();
      target.setAttribute('aria-pressed', String(!item.isFavorite));
      target.setAttribute('aria-label', item.isFavorite ? 'Add to favorites' : 'Remove from favorites');
      target.title = target.getAttribute('aria-label');
      return;
    }
    if (action === 'choose-files') { filePickerOpen = true; document.getElementById('upload-files').click(); return; }
    if (action === 'add-demo-batch') { addJobs([{ name: 'New horizons.jpg', type: 'image/jpeg', size: 6200000 }, { name: 'Sea and wind.mp4', type: 'video/mp4', size: 28400000 }, { name: 'City details.jpg', type: 'image/jpeg', size: 4700000 }]); return; }
    if (action === 'retry-upload') {
      var job = jobs.find(function (entry) { return entry.id === id; });
      if (job && job.status === 'failed') { job.status = 'queued'; job.progress = 0; job.tick = 0; job.attempt++; job.error = ''; persist(); refreshSidebarCounts(); refreshUploadProgress(); toast('Retrying'); }
      return;
    }
    if (action === 'cancel-upload') { jobs = jobs.filter(function (job) { return job.id !== id; }); persist(); refreshSidebarCounts(); refreshUploadProgress(); return; }
  }
  document.addEventListener('click', function (event) {
    hideCardTags(true);
    if (modal && modal.type === 'editor' && !event.target.closest('.editor-combobox')) closeEditorOptions();
    if (!event.target.closest('.sort-control')) closeSortMenu(false);
    if (!event.target.closest('.image-tools')) closeImageTools(false);
    var action = event.target.closest('[data-action]');
    if (action && !action.disabled) { event.preventDefault(); runAction(action.dataset.action, action); return; }
    var contentLink = event.target.closest('[data-content-link]');
    if (contentLink && currentRoute.type === 'library') { scrollPositions[activeContext] = window.scrollY; captureContext(activeContext); }
    var paginationLink = event.target.closest('.pagination a');
    if (paginationLink && !event.metaKey && !event.ctrlKey) { selected.clear(); queryDraft = null; window.scrollTo(0, 0); }
  });
  document.addEventListener('toggle', function (event) {
    if (event.target.matches && event.target.matches('.image-tools')) event.target.querySelector('summary').setAttribute('aria-expanded', String(event.target.open));
  }, true);
  document.addEventListener('pointerover', function (event) {
    if (cardTagsTooltip.contains(event.target)) { clearTimeout(tooltipHideTimer); return; }
    var trigger = event.target.closest('.card-media a[data-media-tags]');
    if (trigger && (!event.relatedTarget || !trigger.contains(event.relatedTarget))) showCardTags(trigger);
  });
  document.addEventListener('pointerout', function (event) {
    var trigger = event.target.closest('.card-media a[data-media-tags]');
    if (trigger && (!event.relatedTarget || !trigger.contains(event.relatedTarget))) {
      if (suppressedTooltipTrigger === trigger) suppressedTooltipTrigger = null;
      if (!event.relatedTarget || !cardTagsTooltip.contains(event.relatedTarget)) delayHideCardTags();
    }
    if (cardTagsTooltip.contains(event.target) && (!event.relatedTarget || !cardTagsTooltip.contains(event.relatedTarget))) delayHideCardTags();
  });
  document.addEventListener('focusin', function (event) {
    if (event.target.dataset.editorSearch) { renderEditorOptions(event.target.dataset.editorSearch); if (event.target.dataset.editorSearch === 'collection') event.target.select(); }
    var trigger = event.target.closest('.card-media a[data-media-tags]');
    if (trigger) showCardTags(trigger);
  });
  document.addEventListener('focusout', function (event) {
    if (event.target.dataset.editorSearch) { var kind = event.target.dataset.editorSearch; setTimeout(function () { if (modal && modal.type === 'editor' && modal.optionKind === kind && !overlayRoot.querySelector('[data-testid="editor-' + kind + '-search"]').closest('.editor-combobox').contains(document.activeElement)) closeEditorOptions(); }, 0); }
    if (suppressedTooltipTrigger === event.target) suppressedTooltipTrigger = null;
    if (tooltipTrigger && tooltipTrigger.contains(event.target)) delayHideCardTags();
  });
  window.addEventListener('resize', positionCardTags);
  window.addEventListener('resize', positionEditorOptions);
  document.addEventListener('scroll', positionEditorOptions, { capture: true, passive: true });
  window.addEventListener('scroll', positionCardTags, { passive: true });
  document.addEventListener('submit', function (event) {
    if (event.target.id === 'editor-form') return saveEditor(event);
    if (event.target.id !== 'search-form') return;
    event.preventDefault();
    updateSearch();
  });
  document.addEventListener('change', function (event) {
    var target = event.target;
    if (target.dataset.select) {
      if (!isLibraryList()) return;
      var id = Number(target.dataset.select);
      if (target.checked) selected.add(id); else selected.delete(id);
      var toolbar = document.getElementById('selection-toolbar'); if (toolbar) toolbar.innerHTML = selectionToolbar();
      return;
    }
    if (target.id === 'upload-files') { filePickerOpen = false; var files = Array.from(target.files || []); if (files.length) addJobs(files); else refreshUploadProgress(); }
  });
  document.addEventListener('input', function (event) {
    if (event.target.id === 'tag-query') queryDraft = event.target.value;
    if (event.target.dataset.editorSearch && !event.isComposing) renderEditorOptions(event.target.dataset.editorSearch, undefined, event.target.dataset.editorSearch === 'collection' ? event.target.value : undefined);
    if (event.target.id === 'collections-search') filterCollections(event.target.value, true);
  });
  document.addEventListener('mousedown', function (event) { if (event.target.closest('.editor-option')) event.preventDefault(); });
  document.addEventListener('compositionstart', function (event) { if (event.target.dataset.editorSearch && modal) modal.composing = true; });
  document.addEventListener('compositionend', function (event) { if (event.target.dataset.editorSearch && modal) { modal.composing = false; renderEditorOptions(event.target.dataset.editorSearch, undefined, event.target.dataset.editorSearch === 'collection' ? event.target.value : undefined); } });
  document.addEventListener('focusout', function () { setTimeout(refreshUploadProgress, 0); });
  function handleVideoKey(event) {
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    if (modal || currentRoute.type !== 'content') return;
    var video = workspace.querySelector('video');
    if (!video || document.activeElement !== video) return;
    var isSpace = event.key === ' ' || event.code === 'Space';
    if (!isSpace && ['ArrowRight', 'ArrowLeft', 'ArrowUp', 'ArrowDown'].indexOf(event.key) < 0) return;
    // Capture before the native control handles the same key. Own both phases
    // so a browser cannot toggle playback again when Space is released.
    event.preventDefault();
    event.stopPropagation();
    if (event.type === 'keyup' || (isSpace && event.repeat)) return;
    if (isSpace) { if (video.paused) video.play().catch(function () {}); else video.pause(); }
    if (event.key === 'ArrowRight') video.currentTime = Math.min(video.duration || Infinity, video.currentTime + 5);
    if (event.key === 'ArrowLeft') video.currentTime = Math.max(0, video.currentTime - 3);
    if (event.key === 'ArrowUp') video.volume = Math.min(1, video.volume + 0.1);
    if (event.key === 'ArrowDown') video.volume = Math.max(0, video.volume - 0.1);
  }
  document.addEventListener('keydown', handleVideoKey, { capture: true, passive: false });
  document.addEventListener('keyup', handleVideoKey, { capture: true, passive: false });
  document.addEventListener('keydown', function (event) {
    if (handleEditorKey(event)) return;
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    if (event.key === 'Escape' && tooltipTrigger) { event.preventDefault(); hideCardTags(true); return; }
    var imageTools = workspace.querySelector('.image-tools[open]');
    if (imageTools && event.key === 'Escape') { event.preventDefault(); closeImageTools(true); return; }
    var menu = document.getElementById('sort-menu');
    if (menu && !menu.hidden) {
      if (event.key === 'Escape') { event.preventDefault(); closeSortMenu(true); return; }
      if (event.key === 'Tab') { closeSortMenu(false); return; }
      if (['ArrowDown', 'ArrowUp', 'Home', 'End'].indexOf(event.key) >= 0) {
        event.preventDefault();
        var choices = Array.from(menu.querySelectorAll('button'));
        var index = choices.indexOf(document.activeElement);
        var next = event.key === 'Home' ? 0 : event.key === 'End' ? choices.length - 1 : (index + (event.key === 'ArrowUp' ? -1 : 1) + choices.length) % choices.length;
        choices[next].focus();
        return;
      }
    }
    if (event.key === 'Escape' && modal) { if (!pendingSave) closeDialog(); return; }
    var active = document.activeElement;
    if (active && (/^(INPUT|TEXTAREA|SELECT|BUTTON|A|SUMMARY|VIDEO|AUDIO)$/.test(active.tagName) || active.isContentEditable)) return;
    if (modal || currentRoute.type !== 'content' || !workspace.querySelector('.viewer-image')) return;
    if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
      var link = workspace.querySelector('[data-testid="viewer-' + (event.key === 'ArrowRight' ? 'next' : 'previous') + '"]');
      if (link) { event.preventDefault(); navigate(link.getAttribute('href')); }
    }
  });
  window.addEventListener('hashchange', function () {
    if (modal && !pendingSave) closeDialog();
    queryDraft = null; selected.clear(); mediaTransform = { scale: 1, rotation: 0 }; render();
  });
  window.addEventListener('focus', function () { if (filePickerOpen) setTimeout(function () { filePickerOpen = false; refreshUploadProgress(); }, 400); });
  window.TFDemo = {
    reset: function () {
      clearTimeout(viewTimer); saveGeneration++; pendingSave = false; closeDialog(); failNextSave = false; overrides = {}; extraItems = []; jobs = seedJobs(); sequence = 1; prefs = { view: 'grid', density: 4, sidebarOpen: true }; selected.clear(); queryDraft = null; uploadMessage = ''; viewSession = null; scrollPositions = Object.create(null); contextSnapshots = Object.create(null); persist();
      if (location.hash === '#/library') render(); else location.hash = '#/library'; toast('Demo reset');
    },
    getState: function () { return JSON.parse(JSON.stringify({ prefs: prefs, overrides: overrides, items: allItems().map(function (item) { return { id: item.id, kind: item.kind, tagIds: item.tagIds, collectionId: item.collectionId, isFavorite: item.isFavorite, viewCount: item.viewCount }; }), jobs: jobs, selected: Array.from(selected), route: currentRoute, viewEvent: viewSession })); },
    debug: { failNextSave: function () { failNextSave = true; } }
  };
  persist();
  render();
  setInterval(advanceJobs, 1600);
}());
