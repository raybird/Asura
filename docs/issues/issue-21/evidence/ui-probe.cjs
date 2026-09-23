// 2026-09-23：以最小 DOM 探測 Web 記憶純文字、切換與同 ID 原 payload 重試。
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const path = require('node:path');

const root = path.resolve(__dirname, '../../../..');
const source = fs.readFileSync(path.join(root, 'internal/web/page.go'), 'utf8');
const script = source.split('<script>')[1].split('</script>')[0];

class Element {
  constructor(tag = 'div') {
    this.tag = tag;
    this.children = [];
    this.listeners = {};
    this.value = '';
    this.textContent = '';
    this.style = {};
    this.classes = new Set();
    this.classList = {
      add: name => this.classes.add(name),
      remove: name => this.classes.delete(name),
      toggle: (name, force) => force ? this.classes.add(name) : this.classes.delete(name)
    };
  }
  set innerHTML(value) { if (value === '') this.children = []; }
  appendChild(child) {
    this.children.push(child);
    if (this.tag === 'select' && this.value === '') this.value = child.value;
  }
  replaceChildren() { this.children = []; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  click() { return this.listeners.click?.(); }
  change() { return this.listeners.change?.(); }
}

const elements = new Map();
const document = {
  getElementById(id) { if (!elements.has(id)) elements.set(id, new Element(id === 'project-select' ? 'select' : 'div')); return elements.get(id); },
  createElement(tag) { return new Element(tag); }
};
const el = id => document.getElementById(id);
const tick = () => new Promise(resolve => setTimeout(resolve, 10));
let requestCounter = 0;
const submitted = [];
let deletes = 0;
let confirms = 0;
let detailReply;
let delayDetail = false;
let queryTask = null;
let delayTask = false;
let taskReply;
let expireNextPage = false;
const window = {
  crypto: { randomUUID: () => `request-${++requestCounter}` },
  confirm: () => { confirms++; return true; }
};
const fetch = async (url, options = {}) => {
  if (url === '/api/projects') return { status: 200, ok: true, json: async () => ['/a', '/b'] };
  if (url === '/api/logout') return { status: 204, ok: true };
  if (url.startsWith('/api/memories?')) {
    if (expireNextPage) { expireNextPage = false; return { status: 401, ok: false }; }
    return { status: 200, ok: true, json: async () => ({ items: [{ id: 'm1', summary: '<script>attack</script>', truncated: false }], next_cursor: '' }) };
  }
  if (url.startsWith('/api/memories/m1') && options.method === 'DELETE') { deletes++; return { status: 204, ok: true }; }
  if (url.startsWith('/api/memories/m1')) {
    if (delayDetail) return new Promise(resolve => { detailReply = resolve; });
    return { status: 200, ok: true, json: async () => ({ content: '<img src=x onerror=alert(1)>' }) };
  }
  if (url === '/api/memories' && options.method === 'POST') return { status: 201, ok: true, json: async () => ({ id: 'created' }) };
  if (String(url).startsWith('/api/tasks/') && queryTask) {
    if (delayTask) return new Promise(resolve => { taskReply = resolve; });
    return { status: 200, ok: true, json: async () => queryTask };
  }
  if (url === '/api/tasks') {
    const body = options.body;
    submitted.push({ body, shownID: el('request-id-display').textContent });
    throw new Error('response lost');
  }
  throw new Error(`unexpected fetch ${url}`);
};

(async () => {
  vm.runInNewContext(script, { document, window, fetch, setTimeout, clearTimeout });
  await tick();
  if (process.argv[2] === 'audit') {
    queryTask = { id: 'legacy', status: 'completed', result: '', resolved_context: '' };
    el('task-id').value = 'legacy';
    el('query-btn').click();
    await tick();
    assert.equal(el('memory-audit').textContent, '舊任務：未提供記憶與召回資料');
    return;
  }
  if (process.argv[2] === 'retry') {
    el('prompt').value = 'first prompt';
    el('submit-btn').click();
    await tick();
    assert.equal(submitted[0].shownID, 'request-1');
    el('prompt').value = 'changed prompt';
    el('retry-btn').click();
    await tick();
    assert.equal(submitted[1].body, submitted[0].body);
    return;
  }
  if (process.argv[2] === 'race') {
    queryTask = { id: 'old', status: 'completed', result: 'old-project-secret' };
    delayTask = true;
    el('task-id').value = 'old';
    el('query-btn').click();
    el('project-select').value = '/b';
    el('project-select').change();
    taskReply({ status: 200, ok: true, json: async () => queryTask });
    await tick();
    assert.equal(el('task-status').textContent, '-');
    return;
  }
  assert.equal(el('memory-list').children.length, 1);
  const row = el('memory-list').children[0];
  assert.equal(row.children[1].textContent, ' m1：<script>attack</script>');
  assert.equal(row.children.some(child => child.tag === 'script'), false);
  row.children[2].click();
  await tick();
  assert.equal(el('memory-detail').textContent, '<img src=x onerror=alert(1)>');
  row.children[3].click();
  await tick();
  assert.equal(deletes, 1);
  assert.equal(confirms, 1);
  el('memory-content').value = 'manual';
  el('memory-add').click();
  await tick();
  assert.equal(el('memory-content').value, '');

  const selected = el('memory-list').children[0].children[0];
  selected.checked = true;
  selected.change();
  el('memory-recall').value = 'bounded';
  el('prompt').value = 'first prompt';
  el('submit-btn').click();
  await tick();
  assert.equal(submitted[0].shownID, 'request-1');
  assert.equal(JSON.parse(submitted[0].body).memory_ids[0], 'm1');
  assert.equal(JSON.parse(submitted[0].body).memory_recall, 'bounded');
  el('prompt').value = 'changed prompt';
  el('retry-btn').click();
  await tick();
  assert.equal(submitted[1].body, submitted[0].body);
  el('submit-btn').click();
  await tick();
  assert.equal(JSON.parse(submitted[2].body).id, 'request-2');
  assert.equal(JSON.parse(submitted[2].body).prompt, 'changed prompt');

  queryTask = { id: 'legacy', status: 'completed', result: '', resolved_context: '' };
  el('task-id').value = 'legacy';
  el('query-btn').click();
  await tick();
  assert.equal(el('memory-audit').textContent, '舊任務：未提供記憶與召回資料');
  queryTask = { id: 'off', status: 'completed', result: '', resolved_context: JSON.stringify({ memory_refs: [] }) };
  el('task-id').value = 'off';
  el('query-btn').click();
  await tick();
  assert.match(el('memory-audit').textContent, /模式：off/);
  queryTask = { id: 'explicit', status: 'completed', result: '', resolved_context: JSON.stringify({ memory_refs: ['id1'] }) };
  el('task-id').value = 'explicit';
  el('query-btn').click();
  await tick();
  assert.match(el('memory-audit').textContent, /模式：explicit-only/);
  queryTask = { id: 'unknown', status: 'completed', result: '', resolved_context: JSON.stringify({ recall: { mode: 'future' } }) };
  el('task-id').value = 'unknown';
  el('query-btn').click();
  await tick();
  assert.match(el('memory-audit').textContent, /模式：未知/);
  assert.match(el('memory-audit').textContent, /Stage trace：未提供/);
  queryTask = { id: 'bounded', status: 'completed', result: '', resolved_context: JSON.stringify({ memory_refs: ['id1'], project_memories: [{ id: 'id1', content: 'private snapshot body' }], recall: { mode: 'bounded', policy_version: 'lexical-v1', policy: { max_items: 4 }, explicit_ids: ['id1'], auto_ids: [], selected: [{ id: 'id1', score: 2, reason: 'lexical_overlap' }], skipped: { score_zero: 2 }, budget: { estimated_prompt_tokens: 12, estimation_method: 'heuristic' } } }), pipeline_trace: JSON.stringify({ stages: [{ name: 'striker', status: 'completed', admission: { verdict: 'admitted', max_tokens: 100, estimate: { total_estimated_tokens: 12, provenance: 'estimated' } } }] }) };
  el('task-id').value = 'bounded';
  el('query-btn').click();
  await tick();
  assert.equal(el('memory-audit').textContent.includes('auto IDs：(0 筆)'), true);
  assert.match(el('memory-audit').textContent, /略過：score_zero 2/);
  assert.match(el('memory-audit').textContent, /分數 2/);
  assert.match(el('memory-audit').textContent, /max_items/);
  assert.match(el('memory-audit').textContent, /estimated/);
  assert.equal(el('memory-audit').textContent.includes('private snapshot body'), false);
  assert.match(el('memory-audit').textContent, /准入 admitted/);
  assert.equal(el('memory-audit').textContent.includes('old-project-secret'), false);

  delayDetail = true;
  el('memory-list').children[0].children[2].click();
  el('project-select').value = '/b';
  el('project-select').change();
  detailReply({ status: 200, ok: true, json: async () => ({ content: 'old-project-secret' }) });
  await tick();
  assert.equal(el('memory-detail').textContent.includes('old-project-secret'), false);
  assert.equal(el('memory-recall').value, 'off');
  delayTask = true;
  el('task-id').value = 'bounded';
  el('query-btn').click();
  el('project-select').value = '/a';
  el('project-select').change();
  taskReply({ status: 200, ok: true, json: async () => queryTask });
  await tick();
  assert.equal(el('task-status').textContent, '-');
  assert.equal(el('memory-audit').textContent, '(尚未查詢任務)');
  expireNextPage = true;
  el('memory-reload').click();
  await tick();
  assert.equal(el('memory-list').children.length, 0);
  assert.equal(el('app-section').classes.has('hidden'), true);
  assert.equal(el('retry-btn').classes.has('hidden'), true);
  console.log('WEBMEM UI probe: text, CRUD confirmation, audit, retry payload, workspace race, session expiry OK');
})().catch(err => { console.error(err); process.exitCode = 1; });
