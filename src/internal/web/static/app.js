// litefeed web UI.
//
// The feed is loaded day by day from /api/entries/brief in the browser's
// time zone. Every response carries "prev_day" (the closest earlier day
// with entries), which becomes the next day to load when the user scrolls
// to the bottom. Feed selection is stored in localStorage as the list of
// *disabled* feeds, so feeds added to the config later are enabled.
//
// All data from the API is inserted with textContent; only the entry body
// (sanitized by the server's markdown renderer) is inserted as HTML.
'use strict';

const $ = (sel, root = document) => root.querySelector(sel);

const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

// Maximum entries per day requested from the API (the API maximum).
const DAY_LIMIT = 1000;

// Distance (px) from the bottom of the page at which the next day is loaded.
const PRELOAD_MARGIN = 600;

const state = {
  feeds: [],            // [{name, title, ...}] from /api/feeds
  disabled: new Set(),  // names of feeds unchecked by the user
  nextDay: null,        // next day to load (YYYY-MM-DD) or null when done
  loading: false,
  generation: 0,        // bumped on reset to drop stale responses
};

// ---------------------------------------------------------------- storage

function loadSetting(key, fallback) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : JSON.parse(v);
  } catch (e) {
    return fallback;
  }
}

function saveSetting(key, value) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch (e) { /* storage may be unavailable (private mode) */ }
}

// ------------------------------------------------------------------ theme

// Themes cycle: system -> light -> dark -> system.
const THEMES = {
  system: { icon: '◐', label: 'Theme: system', next: 'light' },
  light: { icon: '☀', label: 'Theme: light', next: 'dark' },
  dark: { icon: '☾', label: 'Theme: dark', next: 'system' },
};

function currentTheme() {
  const t = document.documentElement.dataset.theme;
  return t === 'light' || t === 'dark' ? t : 'system';
}

function renderThemeButton() {
  const btn = $('#theme-toggle');
  const t = THEMES[currentTheme()];
  btn.textContent = t.icon;
  btn.title = t.label;
  btn.setAttribute('aria-label', t.label);
}

function toggleTheme() {
  const next = THEMES[currentTheme()].next;
  if (next === 'system') {
    delete document.documentElement.dataset.theme;
    try { localStorage.removeItem('theme'); } catch (e) { /* ignore */ }
  } else {
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem('theme', next); } catch (e) { /* ignore */ }
  }
  renderThemeButton();
}

// ------------------------------------------------------------------ dates

// toDay formats a Date as YYYY-MM-DD in the local time zone.
function toDay(d) {
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// parseDay converts YYYY-MM-DD into a local midnight Date.
function parseDay(s) {
  const [y, m, d] = s.split('-').map(Number);
  return new Date(y, m - 1, d);
}

const dayFormat = new Intl.DateTimeFormat(undefined, {
  weekday: 'long', day: 'numeric', month: 'long', year: 'numeric',
});
const relativeFormat = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
// 24-hour clock regardless of the browser locale.
const timeFormat = new Intl.DateTimeFormat(undefined, {
  hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
});

// dayTitle returns "Today"/"Yesterday" (localized) or a full date.
function dayTitle(s) {
  const diff = Math.round((parseDay(toDay(new Date())) - parseDay(s)) / 86400000);
  const text = diff === 0 || diff === 1
    ? relativeFormat.format(-diff, 'day')
    : dayFormat.format(parseDay(s));
  return text.charAt(0).toUpperCase() + text.slice(1);
}

// ------------------------------------------------------------------ feeds

function enabledFeeds() {
  return state.feeds.filter((f) => !state.disabled.has(f.name)).map((f) => f.name);
}

function feedTitle(name) {
  const f = state.feeds.find((x) => x.name === name);
  return f ? f.title : name;
}

function renderFeedsCount() {
  const n = enabledFeeds().length;
  $('#feeds-count').textContent = n === state.feeds.length ? '' : `${n}/${state.feeds.length}`;
}

function renderFeedsList() {
  const list = $('#feeds-list');
  list.replaceChildren();
  for (const f of state.feeds) {
    const li = document.createElement('li');
    const label = document.createElement('label');
    const box = document.createElement('input');
    box.type = 'checkbox';
    box.checked = !state.disabled.has(f.name);
    box.addEventListener('change', () => {
      if (box.checked) state.disabled.delete(f.name);
      else state.disabled.add(f.name);
      onFeedsChanged();
    });
    label.append(box, document.createTextNode(f.title));
    li.append(label);
    list.append(li);
  }
  renderFeedsCount();
}

function setAllFeeds(enabled) {
  state.disabled = enabled ? new Set() : new Set(state.feeds.map((f) => f.name));
  renderFeedsList();
  onFeedsChanged();
}

function onFeedsChanged() {
  saveSetting('disabledFeeds', [...state.disabled]);
  renderFeedsCount();
  reset();
}

function toggleFeedsPanel() {
  const panel = $('#feeds-panel');
  panel.hidden = !panel.hidden;
  $('#feeds-toggle').setAttribute('aria-expanded', String(!panel.hidden));
}

// ---------------------------------------------------------------- loading

async function fetchJSON(url) {
  const resp = await fetch(url, { headers: { Accept: 'application/json' } });
  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`;
    try { msg = (await resp.json()).error || msg; } catch (e) { /* not JSON */ }
    throw new Error(msg);
  }
  return resp.json();
}

function setStatus(text, retry) {
  const status = $('#status');
  status.replaceChildren(document.createTextNode(text));
  if (retry) {
    const btn = document.createElement('button');
    btn.className = 'link';
    btn.type = 'button';
    btn.textContent = 'Retry';
    btn.addEventListener('click', loadNext);
    status.append(btn);
  }
}

// reset clears the feed and starts loading from today.
function reset() {
  state.generation++;
  state.loading = false;
  $('#list').replaceChildren();
  if (enabledFeeds().length === 0) {
    state.nextDay = null;
    setStatus('No feeds selected.');
    return;
  }
  state.nextDay = toDay(new Date());
  loadNext();
}

// loadNext loads state.nextDay and keeps going while the bottom of the
// page stays visible (e.g. when days are short or empty).
async function loadNext() {
  if (state.loading || state.nextDay === null) return;
  state.loading = true;
  const generation = state.generation;
  const day = state.nextDay;
  setStatus('Loading…');

  const params = new URLSearchParams({ day, tz, limit: String(DAY_LIMIT) });
  const enabled = enabledFeeds();
  if (enabled.length !== state.feeds.length) params.set('feed', enabled.join(','));

  let data;
  try {
    data = await fetchJSON(`/api/entries/brief?${params}`);
  } catch (e) {
    if (generation !== state.generation) return;
    state.loading = false;
    setStatus(`Failed to load: ${e.message}`, true);
    return;
  }
  if (generation !== state.generation) return; // filters changed meanwhile

  if (data.entries.length > 0) renderDay(day, data.entries, data.more);
  state.nextDay = data.prev_day || null;
  state.loading = false;

  if (state.nextDay === null) {
    setStatus($('#list').children.length ? 'That is all.' : 'No entries yet.');
  } else {
    setStatus('');
    // Keep loading when nothing was added (the observer would not fire
    // again) or the bottom of the page is still close.
    if (data.entries.length === 0 || nearBottom()) loadNext();
  }
}

// nearBottom reports whether the sentinel is within the preload margin.
// The position is measured directly instead of relying on the observer
// state, which is not updated in background tabs.
function nearBottom() {
  return $('#sentinel').getBoundingClientRect().top < window.innerHeight + PRELOAD_MARGIN;
}

// ------------------------------------------------------------- rendering

function renderDay(day, entries, more) {
  const section = $('#tpl-day').content.firstElementChild.cloneNode(true);
  $('.day-title', section).textContent = dayTitle(day);
  const box = $('.day-entries', section);
  for (const e of entries) box.append(renderEntry(e));
  if (more) {
    const note = document.createElement('p');
    note.className = 'status';
    note.textContent = `Only the latest ${entries.length} entries of this day are shown.`;
    box.append(note);
  }
  $('#list').append(section);
}

function renderEntry(e) {
  const el = $('#tpl-entry').content.firstElementChild.cloneNode(true);
  $('.feed', el).textContent = feedTitle(e.feed);
  const time = $('.time', el);
  time.dateTime = e.published;
  time.textContent = timeFormat.format(new Date(e.published));
  $('.author', el).textContent = e.author || '';

  const link = $('.title a', el);
  link.textContent = e.title || '(untitled)';
  if (e.link) link.href = e.link;

  $('.summary', el).textContent = e.summary || '';

  const btn = $('.expand', el);
  btn.addEventListener('click', () => toggleContent(e.id, el, btn));
  return el;
}

// toggleContent loads the rendered body once and then shows/hides it.
async function toggleContent(id, el, btn) {
  const content = $('.content', el);
  const summary = $('.summary', el);
  if (content.dataset.loaded) {
    content.hidden = !content.hidden;
    summary.hidden = !content.hidden;
    btn.textContent = content.hidden ? 'Read here' : 'Collapse';
    return;
  }
  btn.disabled = true;
  btn.textContent = 'Loading…';
  try {
    const resp = await fetch(`/entries/${encodeURIComponent(id)}/content`);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    content.innerHTML = await resp.text(); // sanitized by the server
    for (const a of content.querySelectorAll('a')) {
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
    }
    for (const img of content.querySelectorAll('img')) img.loading = 'lazy';
    content.dataset.loaded = '1';
    content.hidden = false;
    summary.hidden = true;
    btn.textContent = 'Collapse';
  } catch (err) {
    btn.textContent = 'Failed to load, retry';
  } finally {
    btn.disabled = false;
  }
}

// ------------------------------------------------------------------- init

async function init() {
  renderThemeButton();
  $('#theme-toggle').addEventListener('click', toggleTheme);
  $('#feeds-toggle').addEventListener('click', toggleFeedsPanel);
  $('#feeds-all').addEventListener('click', () => setAllFeeds(true));
  $('#feeds-none').addEventListener('click', () => setAllFeeds(false));

  // Infinite scroll: load the next day when the bottom gets close.
  new IntersectionObserver((records) => {
    if (records.some((r) => r.isIntersecting)) loadNext();
  }, { rootMargin: `${PRELOAD_MARGIN}px 0px` }).observe($('#sentinel'));

  try {
    state.feeds = await fetchJSON('/api/feeds');
  } catch (e) {
    setStatus(`Failed to load feeds: ${e.message}`);
    return;
  }
  state.disabled = new Set(loadSetting('disabledFeeds', []));
  renderFeedsList();
  reset();
}

init();
