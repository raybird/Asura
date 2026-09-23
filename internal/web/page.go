package web

// 2026-09-13 P3-T1：內嵌單頁應用 (SPA)，包含密鑰登入、授權專案選擇、自然語言提交、Task ID 查詢與共同取消。
// 輸出顯示一律使用 textContent 防止腳本與 HTML 執行。
const indexHTML = `<!DOCTYPE html>
<html lang="zh-TW">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Asura 任務中樞控制台</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; margin: 2rem auto; max-width: 800px; padding: 0 1rem; color: #24292f; }
h1 { border-bottom: 1px solid #d0d7de; padding-bottom: 0.5rem; font-size: 1.5rem; }
.card { border: 1px solid #d0d7de; border-radius: 6px; padding: 1.25rem; margin-bottom: 1.5rem; background: #ffffff; box-shadow: 0 1px 3px rgba(0,0,0,0.04); }
label { display: block; margin-top: 0.75rem; font-weight: 600; font-size: 0.9rem; }
input, select, textarea, button { width: 100%; padding: 0.5rem 0.75rem; margin-top: 0.35rem; box-sizing: border-box; font-size: 0.95rem; border: 1px solid #d0d7de; border-radius: 6px; }
button { cursor: pointer; margin-top: 0.75rem; background-color: #0969da; color: white; border: none; font-weight: 600; }
button:hover { background-color: #0858b9; }
button.secondary { background-color: #6e7781; }
button.secondary:hover { background-color: #57606a; }
button.danger { background-color: #cf222e; }
button.danger:hover { background-color: #a40e26; }
pre { background: #f6f8fa; padding: 1rem; border-radius: 6px; overflow-x: auto; white-space: pre-wrap; word-break: break-all; border: 1px solid #d0d7de; min-height: 4rem; }
.hidden { display: none !important; }
.msg { padding: 0.5rem; border-radius: 4px; margin-top: 0.5rem; font-size: 0.9rem; }
.msg.error { background: #ffebe9; color: #cf222e; border: 1px solid #ff8182; }
.msg.success { background: #dafbe1; color: #1a7f37; border: 1px solid #4ac26b; }
.status-badge { display: inline-block; padding: 0.2rem 0.5rem; border-radius: 12px; font-weight: 600; font-size: 0.85rem; background: #eaeef2; }
</style>
</head>
<body>
<h1>Asura 任務中樞控制台</h1>

<div id="login-section" class="card">
  <h2>密鑰登入</h2>
  <div id="login-msg" class="msg error hidden"></div>
  <label for="login-key">存取密鑰 (Access Key)</label>
  <input type="password" id="login-key" placeholder="請輸入服務端存取密鑰">
  <button id="login-btn">登入</button>
</div>

<div id="app-section" class="card hidden">
  <div style="display:flex; justify-content:space-between; align-items:center;">
    <h2 style="margin:0;">任務操作</h2>
    <button id="logout-btn" class="secondary" style="width:auto; margin:0; padding:0.35rem 0.75rem;">登出</button>
  </div>

  <div id="op-msg" class="msg hidden"></div>

  <form id="submit-form" onsubmit="return false;">
    <label for="project-select">授權專案 (Workspace)</label>
    <select id="project-select"></select>

    <label for="prompt">任務提示詞 (Prompt)</label>
    <textarea id="prompt" rows="3" placeholder="請輸入欲交由本機執行的自然語言任務..."></textarea>

    <div id="memory-section" class="card" style="margin-top:1rem;">
      <h3>專案記憶</h3>
      <div id="memory-msg" class="msg hidden"></div>
      <button type="button" id="memory-reload" class="secondary">載入記憶</button>
      <div id="memory-list"></div>
      <button type="button" id="memory-next" class="secondary hidden">下一頁</button>
      <pre id="memory-detail">(選取記憶後可查看詳情)</pre>
      <label for="memory-content">新增手動記憶</label>
      <textarea id="memory-content" rows="3"></textarea>
      <button type="button" id="memory-add">新增記憶</button>
    </div>

    <label for="memory-recall">自動召回</label>
    <select id="memory-recall"><option value="off">關閉</option><option value="bounded">啟用 bounded 召回</option></select>
    <button type="button" id="submit-btn">提交任務</button>
    <div>Request ID：<code id="request-id-display">(尚未提交)</code></div>
    <button type="button" id="retry-btn" class="secondary hidden">原請求重試</button>
    <p>若送出後結果未知，請先保存 Request ID；重新登入後可用下方欄位查詢。關閉頁面後無法恢復待重試內容。</p>
  </form>

  <hr style="margin: 1.5rem 0; border: 0; border-top: 1px solid #d0d7de;">

  <div id="query-section">
    <h3>任務查詢與取消</h3>
    <label for="task-id">Task ID 或 Request ID</label>
    <input type="text" id="task-id" placeholder="輸入 Task ID 或 Request ID 查詢既有任務">
    <div style="display:flex; gap:0.5rem;">
      <button id="query-btn">查詢狀態</button>
      <button id="cancel-btn" class="danger">取消任務</button>
    </div>
  </div>

  <div id="status-section" style="margin-top: 1.5rem;">
    <h3>執行狀態與結果</h3>
    <div>狀態：<span id="task-status" class="status-badge">-</span></div>
    <div style="margin-top:0.5rem; font-weight:600; font-size:0.9rem;">任務輸出：</div>
    <pre id="task-result">(尚未查詢任務)</pre>
    <div style="margin-top:0.5rem; font-weight:600; font-size:0.9rem;">記憶與召回稽核：</div>
    <pre id="memory-audit">(尚未查詢任務)</pre>
  </div>
</div>

<script>
(function() {
  const loginSection = document.getElementById('login-section');
  const appSection = document.getElementById('app-section');
  const loginKey = document.getElementById('login-key');
  const loginBtn = document.getElementById('login-btn');
  const logoutBtn = document.getElementById('logout-btn');
  const loginMsg = document.getElementById('login-msg');
  const opMsg = document.getElementById('op-msg');

  const projectSelect = document.getElementById('project-select');
  const promptInput = document.getElementById('prompt');
  const submitBtn = document.getElementById('submit-btn');
  const recallSelect = document.getElementById('memory-recall');
  const retryBtn = document.getElementById('retry-btn');
  const requestIDDisplay = document.getElementById('request-id-display');

  const taskIdInput = document.getElementById('task-id');
  const queryBtn = document.getElementById('query-btn');
  const cancelBtn = document.getElementById('cancel-btn');

  const taskStatus = document.getElementById('task-status');
  const taskResult = document.getElementById('task-result');
  const memoryAudit = document.getElementById('memory-audit');

  const memoryList = document.getElementById('memory-list');
  const memoryDetail = document.getElementById('memory-detail');
  const memoryMessage = document.getElementById('memory-msg');
  const memoryContent = document.getElementById('memory-content');
  const memoryNext = document.getElementById('memory-next');
  const selectedMemories = new Set();
  let sessionGeneration = 0;
  let memoryCursor = '';
  let memoryGeneration = 0;

  function clearMemoryState() {
    memoryGeneration++;
    selectedMemories.clear();
    recallSelect.value = 'off';
    clearPendingRequest();
    stopPolling();
    taskStatus.textContent = '-';
    taskResult.textContent = '(尚未查詢任務)';
    memoryAudit.textContent = '(尚未查詢任務)';
    taskIdInput.value = '';
    requestIDDisplay.textContent = '(尚未提交)';
    memoryCursor = '';
    memoryList.replaceChildren();
    memoryDetail.textContent = '(選取記憶後可查看詳情)';
    memoryContent.value = '';
    memoryNext.classList.add('hidden');
    clearMessage(memoryMessage);
  }

  async function loadMemories(cursor) {
    const generation = memoryGeneration;
    const workspace = projectSelect.value;
    if (!workspace) return;
    if (!cursor) {
      memoryList.replaceChildren();
      memoryNext.classList.add('hidden');
    }
    try {
      const url = '/api/memories?workspace=' + encodeURIComponent(workspace) + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '');
      const res = await fetch(url);
      if (generation !== memoryGeneration || workspace !== projectSelect.value) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (!res.ok) { showMessage(memoryMessage, '記憶載入失敗：' + res.status, true); return; }
      const page = await res.json();
      if (generation !== memoryGeneration || workspace !== projectSelect.value) return;
      if (!cursor) memoryList.replaceChildren();
      for (const item of page.items || []) {
        const row = document.createElement('div');
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.style.width = 'auto';
        checkbox.checked = selectedMemories.has(item.id);
        checkbox.addEventListener('change', () => {
          if (checkbox.checked) {
            if (selectedMemories.size >= 32) {
              checkbox.checked = false;
              showMessage(memoryMessage, '最多選取 32 筆記憶', true);
              return;
            }
            selectedMemories.add(item.id);
          } else {
            selectedMemories.delete(item.id);
          }
        });
        row.appendChild(checkbox);
        const summary = document.createElement('span');
        summary.textContent = ' ' + item.id + '：' + item.summary + (item.truncated ? '…' : '');
        row.appendChild(summary);
        const detailButton = document.createElement('button');
        detailButton.type = 'button';
        detailButton.className = 'secondary';
        detailButton.textContent = '查看';
        detailButton.addEventListener('click', () => showMemoryDetail(item.id));
        row.appendChild(detailButton);
        const deleteButton = document.createElement('button');
        deleteButton.type = 'button';
        deleteButton.className = 'danger';
        deleteButton.textContent = '刪除';
        deleteButton.addEventListener('click', () => deleteMemory(item.id));
        row.appendChild(deleteButton);
        memoryList.appendChild(row);
      }
      memoryCursor = page.next_cursor || '';
      memoryNext.classList.toggle('hidden', !memoryCursor);
    } catch (err) {
      if (generation === memoryGeneration) showMessage(memoryMessage, '記憶載入失敗', true);
    }
  }

  async function showMemoryDetail(id) {
    const generation = memoryGeneration;
    const workspace = projectSelect.value;
    memoryDetail.textContent = '(讀取中...)';
    try {
      const res = await fetch('/api/memories/' + encodeURIComponent(id) + '?workspace=' + encodeURIComponent(workspace));
      if (generation !== memoryGeneration || workspace !== projectSelect.value) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (!res.ok) { showMessage(memoryMessage, '詳情讀取失敗：' + res.status, true); return; }
      const memory = await res.json();
      if (generation === memoryGeneration && workspace === projectSelect.value) memoryDetail.textContent = memory.content;
    } catch (err) {
      if (generation === memoryGeneration) showMessage(memoryMessage, '詳情讀取失敗', true);
    }
  }

  async function deleteMemory(id) {
    if (!window.confirm('確定刪除這筆記憶？已接受任務的快照不會改變。')) return;
    const generation = memoryGeneration;
    const workspace = projectSelect.value;
    try {
      const res = await fetch('/api/memories/' + encodeURIComponent(id) + '?workspace=' + encodeURIComponent(workspace), { method: 'DELETE' });
      if (generation !== memoryGeneration || workspace !== projectSelect.value) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (!res.ok) { showMessage(memoryMessage, '刪除失敗：' + res.status, true); return; }
      selectedMemories.delete(id);
      memoryDetail.textContent = '(選取記憶後可查看詳情)';
      await loadMemories('');
      if (generation === memoryGeneration) showMessage(memoryMessage, '記憶已刪除', false);
    } catch (err) {
      if (generation === memoryGeneration) showMessage(memoryMessage, '刪除失敗', true);
    }
  }

  document.getElementById('memory-reload').addEventListener('click', () => loadMemories(''));
  memoryNext.addEventListener('click', () => loadMemories(memoryCursor));
  projectSelect.addEventListener('change', () => { clearMemoryState(); loadMemories(''); });
  document.getElementById('memory-add').addEventListener('click', async () => {
    const generation = memoryGeneration;
    const workspace = projectSelect.value;
    const content = memoryContent.value;
    if (!workspace || !content) { showMessage(memoryMessage, '請選擇專案並輸入記憶內容', true); return; }
    try {
      const res = await fetch('/api/memories', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ workspace, content }) });
      if (generation !== memoryGeneration || workspace !== projectSelect.value) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (!res.ok) { showMessage(memoryMessage, '新增失敗：' + res.status, true); return; }
      memoryContent.value = '';
      await loadMemories('');
      if (generation === memoryGeneration) showMessage(memoryMessage, '記憶已新增', false);
    } catch (err) {
      if (generation === memoryGeneration) showMessage(memoryMessage, '新增失敗', true);
    }
  });

  let pendingRequest = null;

  function clearPendingRequest() {
    pendingRequest = null;
    retryBtn.classList.add('hidden');
  }

  function generateRequestID() {
    if (window.crypto && window.crypto.randomUUID) {
      return window.crypto.randomUUID();
    }
    return 'web-' + Date.now() + '-' + Math.random().toString(36).substring(2, 9);
  }

  function showMessage(el, text, isError) {
    el.textContent = text;
    el.className = 'msg ' + (isError ? 'error' : 'success');
    el.classList.remove('hidden');
  }

  function clearMessage(el) {
    el.textContent = '';
    el.classList.add('hidden');
  }

  function sessionExpired() {
    sessionGeneration++;
    clearMemoryState();
    appSection.classList.add('hidden');
    loginSection.classList.remove('hidden');
  }

  async function checkAuthAndLoad() {
    const generation = sessionGeneration;
    try {
      const res = await fetch('/api/projects');
      if (generation !== sessionGeneration) return;
      if (res.status === 200) {
        const projects = await res.json();
        if (generation !== sessionGeneration) return;
        renderProjects(projects);
        clearMemoryState();
        loadMemories('');
        loginSection.classList.add('hidden');
        appSection.classList.remove('hidden');
      } else {
        clearMemoryState();
        loginSection.classList.remove('hidden');
        appSection.classList.add('hidden');
      }
    } catch (e) {
      if (generation !== sessionGeneration) return;
      clearMemoryState();
      loginSection.classList.remove('hidden');
      appSection.classList.add('hidden');
    }
  }

  function renderProjects(projects) {
    projectSelect.innerHTML = '';
    if (!projects || projects.length === 0) {
      const opt = document.createElement('option');
      opt.value = '';
      opt.textContent = '(無授權專案)';
      projectSelect.appendChild(opt);
      return;
    }
    for (const p of projects) {
      const opt = document.createElement('option');
      opt.value = p;
      opt.textContent = p;
      projectSelect.appendChild(opt);
    }
  }

  loginBtn.addEventListener('click', async () => {
    clearMessage(loginMsg);
    const key = loginKey.value.trim();
    if (!key) {
      showMessage(loginMsg, '請輸入密鑰', true);
      return;
    }
    try {
      const res = await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key: key })
      });
      if (res.ok) {
        loginKey.value = '';
        sessionGeneration++;
        checkAuthAndLoad();
      } else {
        showMessage(loginMsg, '登入失敗：密鑰不正確', true);
      }
    } catch (err) {
      showMessage(loginMsg, '連線錯誤：' + err.message, true);
    }
  });

  logoutBtn.addEventListener('click', async () => {
    sessionGeneration++;
    clearMemoryState();
    try {
      await fetch('/api/logout', { method: 'POST' });
    } finally {
      appSection.classList.add('hidden');
      loginSection.classList.remove('hidden');
      clearMessage(opMsg);
    }
  });

  async function sendTask(payload) {
    const generation = memoryGeneration;
    submitBtn.disabled = true;
    retryBtn.disabled = true;
    try {
      const res = await fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (generation !== memoryGeneration || projectSelect.value !== payload.workspace) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (res.status === 202) {
        const data = await res.json();
        if (generation !== memoryGeneration || projectSelect.value !== payload.workspace) return;
        taskIdInput.value = data.id;
        taskStatus.textContent = data.status || 'pending';
        renderMemoryAudit(data);
        taskResult.textContent = data.result || '(等待執行中...)';
        showMessage(opMsg, '任務已接受：' + data.id, false);
        clearPendingRequest();
        pollTask(data.id);
      } else {
        const message = await res.text();
        if (generation !== memoryGeneration) return;
        if (res.status >= 400 && res.status < 500) {
          showMessage(opMsg, '提交被拒絕：' + message, true);
          clearPendingRequest();
        } else {
          retryBtn.classList.remove('hidden');
          showMessage(opMsg, '回應未確認：' + message + '；請保存 Request ID，或以原請求重試。', true);
        }
      }
    } catch (err) {
      if (generation === memoryGeneration) {
        retryBtn.classList.remove('hidden');
        showMessage(opMsg, '回應未知；請保存 Request ID，或以原請求重試。', true);
      }
    } finally {
      submitBtn.disabled = false;
      retryBtn.disabled = false;
    }
  }

  submitBtn.addEventListener('click', () => {
    clearMessage(opMsg);
    const workspace = projectSelect.value;
    const prompt = promptInput.value.trim();
    if (!workspace || !prompt) {
      showMessage(opMsg, '請選擇授權專案並輸入任務提示詞', true);
      return;
    }
    const payload = { id: generateRequestID(), workspace, prompt };
    if (selectedMemories.size) payload.memory_ids = Array.from(selectedMemories);
    if (recallSelect.value === 'bounded') payload.memory_recall = 'bounded';
    clearPendingRequest();
    pendingRequest = payload;
    requestIDDisplay.textContent = payload.id;
    taskIdInput.value = payload.id;
    sendTask(payload);
  });

  retryBtn.addEventListener('click', () => {
    if (pendingRequest) sendTask(pendingRequest);
  });

  function renderMemoryAudit(task) {
    if (!task.resolved_context) {
      memoryAudit.textContent = '舊任務：未提供記憶與召回資料';
      return;
    }
    let context;
    try { context = JSON.parse(task.resolved_context); }
    catch (err) { memoryAudit.textContent = '記憶稽核資料格式不支援'; return; }
    if (!context || typeof context !== 'object') { memoryAudit.textContent = '記憶稽核資料格式不支援'; return; }
    const refs = Array.isArray(context.memory_refs) ? context.memory_refs : (task.memory_refs || []);
    const recall = context.recall;
    const lines = [];
    if (recall && recall.mode === 'bounded') {
      lines.push('模式：bounded');
      lines.push('政策：' + (recall.policy_version || '未提供'));
      if (recall.policy) lines.push('政策限制：' + JSON.stringify(recall.policy));
      lines.push('explicit IDs：' + ((Array.isArray(recall.explicit_ids) ? recall.explicit_ids : []).join(', ') || '(無)'));
      lines.push('auto IDs：' + ((Array.isArray(recall.auto_ids) ? recall.auto_ids : []).join(', ') || '(0 筆)'));
      for (const item of Array.isArray(recall.selected) ? recall.selected : []) lines.push('選取：' + (item.id ?? '未提供') + '，分數 ' + (item.score ?? '未提供') + '，原因 ' + (item.reason ?? '未提供'));
      for (const [reason, count] of Object.entries(recall.skipped && typeof recall.skipped === 'object' ? recall.skipped : {})) lines.push('略過：' + reason + ' ' + count);
      if (recall.budget) lines.push('預算估算：' + (recall.budget.estimated_prompt_tokens ?? '未提供') + ' tokens（' + (recall.budget.estimation_method || '估算') + '）');
    } else if (recall && recall.mode) {
      lines.push('模式：未知（' + recall.mode + '）');
    } else if (refs.length) {
      lines.push('模式：explicit-only');
      lines.push('explicit IDs：' + refs.join(', '));
    } else {
      lines.push('模式：off（無自動召回）');
    }
    let trace;
    try { trace = task.pipeline_trace ? JSON.parse(task.pipeline_trace) : null; }
    catch (err) { trace = null; }
    if (trace && Array.isArray(trace.stages)) {
      for (const stage of trace.stages) {
        const admission = stage.admission;
        lines.push('Stage ' + (stage.name || '未提供') + '：' + (stage.status || '未知') + '；准入 ' + (admission ? admission.verdict : '未提供') + '；context window ' + (admission && admission.max_tokens ? admission.max_tokens : '未知'));
        if (admission && admission.estimate) lines.push('估算：' + (admission.estimate.total_estimated_tokens ?? '未提供') + ' tokens（' + (admission.estimate.provenance || '估算') + '）');
      }
    } else {
      lines.push('Stage trace：未提供');
    }
    memoryAudit.textContent = lines.join('\n');
  }

  let pollTimer = null;

  function stopPolling() {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  async function loadFullResultPaged(taskID) {
    const generation = memoryGeneration;
    taskResult.textContent = '';
    let offset = 0;
    const limit = 1000;
    while (true) {
      try {
        const res = await fetch('/api/tasks/' + encodeURIComponent(taskID) + '/result?offset=' + offset + '&limit=' + limit);
        if (generation !== memoryGeneration) return;
        if (res.status === 401) { sessionExpired(); return; }
        if (!res.ok) {
          break;
        }
        const page = await res.json();
        if (generation !== memoryGeneration) return;
        taskResult.textContent += page.data || '';
        if (!page.has_more) {
          break;
        }
        offset += (page.data ? Array.from(page.data).length : 0);
      } catch (e) {
        break;
      }
    }
  }

  async function pollTask(tid) {
    stopPolling();
    taskStatus.textContent = '-';
    taskResult.textContent = '(查詢中...)';
    memoryAudit.textContent = '(查詢中...)';
    const generation = memoryGeneration;
    try {
      const res = await fetch('/api/tasks/' + encodeURIComponent(tid));
      if (generation !== memoryGeneration) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (res.ok) {
        const task = await res.json();
        if (generation !== memoryGeneration) return;
        taskStatus.textContent = task.status || '-';
        renderMemoryAudit(task);
        if (task.status === 'completed') {
          if (task.result && task.result.length > 500) {
            await loadFullResultPaged(task.id);
          } else {
            taskResult.textContent = task.result || '(無輸出)';
          }
          return;
        } else if (task.status === 'failed' || task.status === 'cancelled') {
          taskResult.textContent = task.result || (task.error_message ? '錯誤: ' + task.error_message : '(無輸出)');
          return;
        } else {
          taskResult.textContent = task.result || '(任務執行中...)';
          pollTimer = setTimeout(() => pollTask(tid), 1000);
        }
      }
    } catch (err) {
      // 網路中斷不誤判完成，停止輪詢並提示
      stopPolling();
    }
  }

  queryBtn.addEventListener('click', async () => {
    clearMessage(opMsg);
    const tid = taskIdInput.value.trim();
    if (!tid) {
      showMessage(opMsg, '請輸入 Task ID 或 Request ID', true);
      return;
    }
    stopPolling();
    taskStatus.textContent = '-';
    taskResult.textContent = '(查詢中...)';
    memoryAudit.textContent = '(查詢中...)';
    const generation = memoryGeneration;
    try {
      const res = await fetch('/api/tasks/' + encodeURIComponent(tid));
      if (generation !== memoryGeneration) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (res.ok) {
        const task = await res.json();
        if (generation !== memoryGeneration) return;
        taskStatus.textContent = task.status || '-';
        renderMemoryAudit(task);
        if (task.status === 'completed' && task.result && task.result.length > 500) {
          await loadFullResultPaged(task.id);
        } else {
          taskResult.textContent = task.result || (task.error_message ? '錯誤: ' + task.error_message : '(尚無輸出)');
        }
        showMessage(opMsg, '查詢成功', false);
        if (task.status === 'pending' || task.status === 'running') {
          pollTimer = setTimeout(() => pollTask(task.id), 1000);
        }
      } else {
        const errText = await res.text();
        if (generation !== memoryGeneration) return;
        showMessage(opMsg, '查詢失敗：' + errText, true);
      }
    } catch (err) {
      if (generation === memoryGeneration) showMessage(opMsg, '查詢出錯：' + err.message, true);
    }
  });

  cancelBtn.addEventListener('click', async () => {
    clearMessage(opMsg);
    stopPolling();
    const tid = taskIdInput.value.trim();
    if (!tid) {
      showMessage(opMsg, '請輸入欲取消的 Task ID', true);
      return;
    }
    const generation = memoryGeneration;
    try {
      const res = await fetch('/api/tasks/' + encodeURIComponent(tid) + '/cancel', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}'
      });
      if (generation !== memoryGeneration) return;
      if (res.status === 401) { sessionExpired(); return; }
      if (res.ok) {
        const task = await res.json();
        if (generation !== memoryGeneration) return;
        taskStatus.textContent = task.status || 'cancelled';
        renderMemoryAudit(task);
        showMessage(opMsg, '任務已成功取消：' + task.id, false);
      } else {
        const errText = await res.text();
        if (generation !== memoryGeneration) return;
        showMessage(opMsg, '取消失敗：' + errText, true);
      }
    } catch (err) {
      if (generation === memoryGeneration) showMessage(opMsg, '取消請求出錯：' + err.message, true);
    }
  });

  // 初始載入
  checkAuthAndLoad();
})();
</script>
</body>
</html>
`
