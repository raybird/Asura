# 技術可行性分析與設計取捨

2026-09-25。本檔針對 Phase 3H（Git-Aware Project Memory Capture）提出的技術方案與待決設計點（H-TBD-1～6）進行可行性分析與取捨評估，做為使用者核准決策的依據。

## 技術可行性分析

1. **Git 互動方式**：
   - 方案 A（純 Go 函式庫，如 `go-git`）：無外部 CLI 依賴，但功能不完整（對特定 Git 擴展、複雜 merge、sparse checkout、commit-graph 等相容性較差），且記憶體開銷大。
   - 方案 B（安全封裝之系統 `git` CLI，**推薦**）：透過 `exec.CommandContext` 封裝系統 git 指令，顯式指定安全參數（如 `-c core.fsmonitor=`、`--no-replace-objects`、`-c diff.noprefix=false`）、清空可能逃逸的環境變數（如 `GIT_DIR`、`GIT_WORK_TREE`），並以工作目錄錨定在 Daemon 驗證過的 canonical workspace。
   - **結論**：採用方案 B。既能保證 100% Git 語意相容性與高效串流處理，又能藉由進程沙盒與環境變數隔離達成嚴密安全控制。

2. **記憶架構整合點**：
   - 現有 `memories` 資料表專用於「已生效的正式專案記憶」，直接參與 Phase 3F `recall` 與 Context Window 估算。
   - 若引入未審核的 Git 變更候選，必須採用獨立的「候選暫存（Candidates Storage）」架構，維持正式記憶與草稿候選的實體隔離。

---

## 方案選型與取捨（H-TBD-1 ～ H-TBD-6）

### H-TBD-1：第一版 Git Range 與 Checkpoint 策略

| 方案 | 說明 | 優點 | 缺點 / 風險 | 取捨決策 |
|---|---|---|---|---|
| **方案 1A：純顯式指定 range** | 每次執行必須顯式指定 `--range <base>..<head>` 或 `<commit>` | 語意最清晰、無狀態、完全無分支/force-push 競態風險 | 使用者需手動查詢前次 commit SHA，無自動增量便利性 | **第一階段基礎核心**：首版底層實作必須先支援此模式，提供最高確定性。 |
| **方案 1B：增量 Checkpoint + 顯式 range 混合** | 支援 `--range` 亦支援 `--since-last`。Checkpoint 以 `(canonical_workspace, git_root_sha, ref_name)` 為複合鍵儲存於 SQLite | 自動化程度高，使用者體驗佳 | 分支切換、force-push、merge、commit 重寫時若判斷不當可能混淆增量流 | **推薦實作**：以 1A 為底層驗證基礎，在上層提供 1B。若偵測到當前 HEAD 不是 checkpoint 的直接子孫（ancestor check 失敗）或 ref 改變，強制拒絕增量並提示使用者指定範圍。 |

### H-TBD-2：Repo Identity、路徑授權與 Git 子程序安全

| 考量點 | 風險分析 | 防護策略（建議核准） |
|---|---|---|
| **工作區與 Git Root 關係** | workspace 可能是 Git 子目錄或頂層目錄，甚至 symlink 逃逸 | 先執行 `git rev-parse --show-toplevel` 取得絕對路徑，比對 `AuthorizeCanonicalWorkspace`；若頂層超出 allowed roots 則嚴格拒絕。 |
| **TOCTOU 競爭條件** | 檢查路徑後分支被外部變更、或 HEAD 在讀取過程中前進 | 進入擷取前先解析出具體的完整 40-byte commit SHA，後續所有 log、diff、show 操作一律使用固定 SHA，禁止使用動態 ref 名稱（如 HEAD）。 |
| **Git 子程序安全** | 惡意 commit message、特殊 branch 命名或路徑包含 `--argument` 導致參數注入 | 嚴禁字串拼接 shell，所有參數作為 argv 陣列傳遞；使用 `--` 分隔旗標與路徑；注入環境變數 `GIT_CONFIG_NOSYSTEM=1`、`GIT_TERMINAL_PROMPT=0` 防止互動掛起。 |

### H-TBD-3：有界擷取與資源防護限制

為避免巨型倉庫、大量 commit 或二進位檔耗盡資源，建議核准下列硬性邊界（Hard Bounds）：

```text
硬性限制指標 (Conservative Hard Bounds)：
├── 單次擷取 Commits 數量上限：最多 100 個 commits（超出時報錯或由使用者縮小範圍）
├── 變更檔案數量上限：最多 50 個檔案（超出時僅保留前 50 個，並標記 truncated: true）
├── Diff 總位元組上限：最多 256 KiB（超出時截斷並提示）
├── 單一檔案 Diff 上限：最多 32 KiB
├── 執行逾時上限：Git 子程序總耗時上限 10 秒
└── 特殊檔案政策：
    ├── Binary 檔案：檢測到二進位（\0 位元組）自動略過，僅記錄 "binary file modified"
    ├── Submodule / Symlink：僅記錄指標或路徑變更，不遞迴讀取內容
    └── 機密過濾：過濾常見密鑰副檔名（.env, .pem, .key, credentials）與憑證關鍵字
```

### H-TBD-4：候選儲存架構與 Schema 設計

```diff
 現有結構 (SQLite):
   memories (id, workspace, content, source, created_at)
 
 提議新增獨立候選表:
+  memory_candidates (
+    id TEXT PRIMARY KEY,
+    workspace TEXT NOT NULL,
+    ref_name TEXT NOT NULL,
+    base_sha TEXT NOT NULL,
+    head_sha TEXT NOT NULL,
+    commit_count INTEGER NOT NULL,
+    facts_summary TEXT NOT NULL,      -- Git 客觀結構事實 (commits, files, stats)
+    draft_content TEXT NOT NULL,      -- 待審核的專案記憶草稿內文
+    status TEXT NOT NULL,             -- pending, approved, discarded
+    created_at DATETIME NOT NULL,
+    approved_at DATETIME
+  );
+  git_checkpoints (
+    workspace TEXT PRIMARY KEY,
+    repo_root TEXT NOT NULL,
+    ref_name TEXT NOT NULL,
+    last_head_sha TEXT NOT NULL,
+    updated_at DATETIME NOT NULL
+  );
```

- **取捨理由**：若將候選寫入既有 `memories`，需要修改既有所有 `ListMemories`、`PageMemories` 與 `recall` 的 SQL（增加 `WHERE status='approved'`），不僅有相容性破壞風險，且草稿刪除時易造成索引膨脹。獨立表能確保 100% 隔離，零污染既有召回流程。
- **正式記憶轉入**：核准後，將 `draft_content` 寫入 `memories`，其 `source` 設定為 `git-approved`，並在正式記憶內容中附帶來源 commit SHA 標記。舊有五欄位 API 完全不受影響。

### H-TBD-5：AI 草稿生成 vs 確定性/手動草稿（最小交付路徑）

| 模式 | 描述 | 安全與資源邊界 |
|---|---|---|
| **模式 1：確定性結構化摘要（無模型最小交付，優先推薦）** | 由 Git 子程序提取客觀事實（Commit subjects, Authors, Affected files, Added/Deleted stats），直接生成格式化草稿供人工編修 | 0 token 消耗、0 模型調用成本、0 Prompt Injection 風險、可離線執行。 |
| **模式 2：AI 輔助決策摘要（可選增強模組）** | 將模式 1 的結構化 Facts 送入後端模型，請模型提煉「架構決策與實作約束」 | 需核准固定 Token 預算（<= 4096 tokens）、唯讀權限邊界（deny bash/write）、防範 Commit message 注入攻擊；失敗時退回模式 1。 |

- **建議取捨**：第一版交付優先跑通「模式 1」完整閉環（GITMEM-001～008），確保即使在無模型環境下也能產出可審核記憶；若本期核准 AI 草稿（GITMEM-009），則將模式 2 作為選擇性旗標（`--ai-draft`），其失敗不阻塞候選生成。

### H-TBD-6：候選去重、核准冪等與崩潰恢復

1. **候選去重（Dedup）**：
   - 相同 `workspace + base_sha + head_sha` 若已有狀態為 `pending` 的候選，重複擷取時直接回傳既有候選 ID，不重複新增。
2. **核准冪等（Approval Idempotency）**：
   - 若候選已被 `approved`，再次呼叫 approve 時保持冪等，回傳已建立之正式記憶，不重複插入 `memories`。
3. **交易與 Checkpoint 恢復**：
   - 核准轉入正式記憶與 Checkpoint 更新置於同一 SQLite Transaction 內；若其中一步失敗，整體 Rollback，保證資料與狀態的原子性（Atomicity）。

---

## 架構影響評估

1. **Daemon IPC**：
   - 新增 IPC 動作：`git_memory_capture`、`git_memory_candidates`、`git_memory_approve`、`git_memory_discard`。
   - 所有動作嚴格經由 `AuthorizeCanonicalWorkspace` 進行專案隔離。
2. **CLI 命令擴充**：
   - `asura memory capture --workspace <path> [--range <base>..<head>] [--since-last]`
   - `asura memory candidates --workspace <path>`
   - `asura memory approve --workspace <path> --candidate <id> [--content <edited>]`
   - `asura memory discard --workspace <path> --candidate <id>`
3. **既有模組相容性**：
   - `internal/storage/sqlite_memory.go`：維持既有五欄位與查詢邏輯不變，新增 `git-approved` 來源值。
   - `internal/recall/recall.go`：既有 `lexical-v1` 政策完全不需修改，自然召回 `git-approved` 記憶。
   - Web Console：首版無 Web 入口，不影響 Web 運作。
