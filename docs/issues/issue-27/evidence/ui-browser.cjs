// 2026-09-30 WEBGIT-005/009：真實 Chromium DOM；fetch fixture 僅控制 UI 回應，授權另由 HTTP/IPC 測試保障。
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const source = fs.readFileSync(path.resolve(__dirname, '../../../../internal/web/page.go'), 'utf8');
const html = source.slice(source.indexOf('`') + 1, source.lastIndexOf('`'));

(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_BIN ? { executablePath: process.env.CHROME_BIN } : {}) });
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.setContent(html.replace('<script>', `<script>
      window.attack = 0; window.sent = []; window.confirms = 0; window.slow = {}; window.hold = ''; window.expire = false;
      window.confirm = () => { window.confirms++; return true; };
      window.fixture = { id: 'c1', workspace: '/a', repo_id: 'repo', ref_name: 'main', base_sha: 'base', head_sha: 'head', commit_count: 1, status: 'pending', is_truncated: false, draft_content: '<img src=x onerror="window.attack++">', facts_summary: '{"subject":"<script>window.attack++<\\/script>"}', created_at: '2026-09-30T00:00:00Z' };
      const reply = value => ({ status: 200, ok: true, json: async () => value });
      window.fetch = async (url, options = {}) => {
        if (url === '/api/projects') return reply(['/a', '/b']);
        if (url.startsWith('/api/memories?')) return reply({ items: [] });
        if (url === '/api/login') return reply({});
        if (url === '/api/logout') { if (window.logoutSlow) return new Promise(resolve => { window.logoutResolve = resolve; }); return reply({}); }
        if (url.includes('/api/git-memory/')) {
          window.sent.push({ url, body: options.body && JSON.parse(options.body), signal: options.signal });
          if (window.expire) { window.expire = false; return { status: 401, ok: false }; }
          if (window.hold && url.includes(window.hold)) return new Promise(resolve => { window.slow.resolve = resolve; window.slow.signal = options.signal; });
          if (url.includes('/capture')) return reply({ candidate_id: 'c1', outcome: 'Partial', reused: true, detail_available: true });
          if (url.includes('/approve')) return reply({ approved_memory_id: 'm1' });
          if (url.includes('/discard')) return reply({ status: 'discarded' });
          if (url.includes('/candidates?')) return reply({ items: [{ id: 'c1', status: 'pending', summary: '<script>window.attack++<\\/script>', is_truncated: false }], next_cursor: '' });
          return reply(window.fixture);
        }
        throw new Error('unexpected fetch ' + url);
      };
    <\/script><script>`));
    await page.waitForSelector('#app-section:not(.hidden)');
    assert.equal(await page.locator('#git-reload').count(), 1, 'Git review UI missing');
    await page.click('#git-reload');
    await page.locator('#git-list button').first().click();
    await page.waitForFunction(() => document.getElementById('git-draft').value.includes('<img'));
    assert.equal(await page.locator('#git-detail img,#git-list script,#git-facts script').count(), 0);
    assert.equal(await page.evaluate(() => window.attack), 0);
    assert.match(await page.locator('#git-facts').textContent(), /subject/);
    await page.fill('#git-draft', 'edited memory');
    await page.click('#git-approve');
    await page.waitForFunction(() => document.getElementById('git-msg').textContent.includes('m1'));
    const approved = await page.evaluate(() => window.sent.find(x => x.url.endsWith('/approve')));
    assert.deepEqual(approved.body, { workspace: '/a', content: 'edited memory' });
    assert.equal(await page.evaluate(() => window.confirms), 1);
    await page.click('#git-reload');
    await page.locator('#git-list button').first().click();
    await page.click('#git-discard');
    await page.waitForFunction(() => document.getElementById('git-msg').textContent.includes('已捨棄'));
    assert.equal(await page.evaluate(() => window.confirms), 2);
    await page.evaluate(() => { window.fixture.is_truncated = true; });
    await page.click('#git-reload'); await page.locator('#git-list button').first().click();
    await page.waitForFunction(() => document.getElementById('git-detail').textContent.includes('Partial'));
    assert.equal(await page.locator('#git-approve').isDisabled(), true);
    await page.fill('#git-range', 'HEAD~1..HEAD'); await page.click('#git-capture');
    await page.waitForFunction(() => document.getElementById('git-msg').textContent.includes('Partial'));
    assert.match(await page.locator('#git-msg').textContent(), /reused/);
    // 2026-09-30：斷線 AbortSignal 與遲到 response 的隔離是兩個獨立判準。
    await page.evaluate(() => { window.hold = '/c1?'; });
    await page.locator('#git-list button').first().click();
    await page.waitForFunction(() => typeof window.slow.resolve === 'function');
    await page.selectOption('#project-select', '/b');
    assert.equal(await page.locator('#git-draft').inputValue(), '');
    assert.equal(await page.locator('#git-range').inputValue(), '');
    assert.equal(await page.locator('#git-list').textContent(), '');
    assert.equal(await page.evaluate(() => window.slow.signal.aborted), true);
    await page.evaluate(() => { window.hold = ''; window.slow.resolve({ status: 200, ok: true, json: async () => ({ ...window.fixture, draft_content: 'LATE-A' }) }); });
    await page.waitForTimeout(100);
    assert.equal(await page.locator('#git-draft').inputValue(), '');
    // 2026-09-30：同 scope 不同 detail 請求也不得逆序覆寫。
    await page.selectOption('#project-select', '/a');await page.click('#git-reload');
    await page.evaluate(() => { window.hold = '/c1?'; window.slow = {}; });await page.locator('#git-list button').first().click();
    await page.waitForFunction(() => typeof window.slow.resolve === 'function');
    await page.evaluate(() => { window.hold = ''; window.fixture.draft_content = 'NEW'; });
    await page.click('#git-reload');await page.locator('#git-list button').first().click();
    await page.waitForFunction(() => document.getElementById('git-draft').value === 'NEW');
    await page.evaluate(() => window.slow.resolve({ status: 200, ok: true, json: async () => ({ ...window.fixture, draft_content: 'OLD' }) }));await page.waitForTimeout(100);
    assert.equal(await page.locator('#git-draft').inputValue(), 'NEW');
    await page.evaluate(() => { window.slow = {}; window.hold = '/capture'; });
    await page.fill('#git-range', 'HEAD~1..HEAD');await page.click('#git-capture');
    await page.waitForFunction(() => typeof window.slow.resolve === 'function');
    await page.selectOption('#project-select', '/b');
    assert.equal(await page.evaluate(() => window.slow.signal.aborted), true);
    assert.equal(await page.locator('#git-capture').isDisabled(), false);
    assert.equal(await page.locator('#git-range').inputValue(), '');
    await page.evaluate(() => { window.hold = ''; window.slow.resolve({ status: 200, ok: true, json: async () => ({ candidate_id: 'LATE-A', outcome: 'Complete' }) }); });
    await page.waitForTimeout(100);assert.equal(await page.locator('#git-msg').textContent(), '');
    await page.selectOption('#project-select', '/a');
    await page.click('#git-reload');
    await page.evaluate(() => { window.slow = {}; window.hold = '/c1?'; window.logoutSlow = true; });
    await page.locator('#git-list button').first().click();
    await page.waitForFunction(() => typeof window.slow.resolve === 'function');
    await page.click('#logout-btn');
    await page.waitForFunction(() => typeof window.logoutResolve === 'function');
    assert.equal(await page.locator('#git-draft').inputValue(), '');
    assert.equal(await page.locator('#git-list').textContent(), '');
    assert.equal(await page.evaluate(() => window.slow.signal.aborted), true);
    const count = await page.evaluate(() => window.sent.length);
    await page.evaluate(() => document.getElementById('git-capture').click());
    assert.equal(await page.evaluate(() => window.sent.length), count, 'logout allowed another Git action');
    await page.evaluate(() => { window.hold = ''; window.slow.resolve({ status: 200, ok: true, json: async () => ({ ...window.fixture, draft_content: 'LATE-LOGOUT' }) }); window.logoutResolve({ status: 200, ok: true }); });
    await page.waitForSelector('#login-section:not(.hidden)');
    assert.equal(await page.locator('#git-draft').inputValue(), '');
    await page.fill('#login-key', 'fixture');await page.click('#login-btn');
    await page.waitForSelector('#app-section:not(.hidden)');
    await page.evaluate(() => { window.expire = true; });await page.click('#git-reload');
    await page.waitForFunction(() => document.getElementById('app-section').classList.contains('hidden'));
    assert.equal(await page.locator('#git-draft').inputValue(), '');
    assert.equal(await page.locator('#git-list').textContent(), '');
    assert.deepEqual(errors, []);
    console.log('PASS real Chromium: text rendering, edited approval, discard confirmation, Partial, generation/abort, same-scope detail race, logout pending action gate, 401 reset');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
