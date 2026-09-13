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

    <button type="button" id="submit-btn">提交任務</button>
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

  const taskIdInput = document.getElementById('task-id');
  const queryBtn = document.getElementById('query-btn');
  const cancelBtn = document.getElementById('cancel-btn');

  const taskStatus = document.getElementById('task-status');
  const taskResult = document.getElementById('task-result');

  let currentRequestID = '';

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

  async function checkAuthAndLoad() {
    try {
      const res = await fetch('/api/projects');
      if (res.status === 200) {
        const projects = await res.json();
        renderProjects(projects);
        loginSection.classList.add('hidden');
        appSection.classList.remove('hidden');
      } else {
        loginSection.classList.remove('hidden');
        appSection.classList.add('hidden');
      }
    } catch (e) {
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
        checkAuthAndLoad();
      } else {
        showMessage(loginMsg, '登入失敗：密鑰不正確', true);
      }
    } catch (err) {
      showMessage(loginMsg, '連線錯誤：' + err.message, true);
    }
  });

  logoutBtn.addEventListener('click', async () => {
    try {
      await fetch('/api/logout', { method: 'POST' });
    } finally {
      appSection.classList.add('hidden');
      loginSection.classList.remove('hidden');
      clearMessage(opMsg);
    }
  });

  submitBtn.addEventListener('click', async () => {
    clearMessage(opMsg);
    const ws = projectSelect.value;
    const prompt = promptInput.value.trim();
    if (!ws) {
      showMessage(opMsg, '請選擇有效的授權專案', true);
      return;
    }
    if (!prompt) {
      showMessage(opMsg, '請輸入任務提示詞', true);
      return;
    }

    if (!currentRequestID) {
      currentRequestID = generateRequestID();
    }

    submitBtn.disabled = true;
    try {
      const res = await fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          id: currentRequestID,
          workspace: ws,
          prompt: prompt
        })
      });
      const data = await res.json();
      if (res.status === 202) {
        taskIdInput.value = data.id;
        taskStatus.textContent = data.status || 'pending';
        taskResult.textContent = data.result || '(等待執行中...)';
        showMessage(opMsg, '任務已接受：' + data.id, false);
        currentRequestID = ''; // 提交成功後重置識別，供下次新任務使用
      } else {
        showMessage(opMsg, '提交被拒絕：' + (data.error || res.statusText), true);
      }
    } catch (err) {
      showMessage(opMsg, '請求失敗：' + err.message, true);
    } finally {
      submitBtn.disabled = false;
    }
  });

  queryBtn.addEventListener('click', async () => {
    clearMessage(opMsg);
    const tid = taskIdInput.value.trim();
    if (!tid) {
      showMessage(opMsg, '請輸入 Task ID 或 Request ID', true);
      return;
    }
    try {
      const res = await fetch('/api/tasks/' + encodeURIComponent(tid));
      if (res.ok) {
        const task = await res.json();
        taskStatus.textContent = task.status || '-';
        taskResult.textContent = task.result || '(尚無輸出)';
        showMessage(opMsg, '查詢成功', false);
      } else {
        const errText = await res.text();
        showMessage(opMsg, '查詢失敗：' + errText, true);
      }
    } catch (err) {
      showMessage(opMsg, '查詢出錯：' + err.message, true);
    }
  });

  cancelBtn.addEventListener('click', async () => {
    clearMessage(opMsg);
    const tid = taskIdInput.value.trim();
    if (!tid) {
      showMessage(opMsg, '請輸入欲取消的 Task ID', true);
      return;
    }
    try {
      const res = await fetch('/api/tasks/' + encodeURIComponent(tid) + '/cancel', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}'
      });
      if (res.ok) {
        const task = await res.json();
        taskStatus.textContent = task.status || 'cancelled';
        showMessage(opMsg, '任務已成功取消：' + task.id, false);
      } else {
        const errText = await res.text();
        showMessage(opMsg, '取消失敗：' + errText, true);
      }
    } catch (err) {
      showMessage(opMsg, '取消請求出錯：' + err.message, true);
    }
  });

  // 初始載入
  checkAuthAndLoad();
})();
</script>
</body>
</html>
`
