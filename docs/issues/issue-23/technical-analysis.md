# 技術可行性分析與設計取捨

2026-09-25。本檔針對 Phase 3H（Git-Aware Project Memory Capture）提出的技術方案與待決設計點（H-TBD-1～6）進行可行性分析與取捨評估，做為使用者核准決策的依據。

## 技術可行性分析

1. **Git 互動方式**：
   - 方案 A（純 Go 函式庫，如 `go-git`）：無外部 CLI 依賴，但功能不完整（對特定 Git 擴展、複雜 merge、sparse checkout、commit-graph 等相容性較差），且記憶體開銷大。
   - 方案 B（安全封裝之系統 `git` CLI，**推薦**）：透過 `exec.CommandContext` 封裝系統 git 指令，以 argv 陣列嚴格隔離參數，顯式清空環境變數與關閉外部擴展工具，並以工作目錄錨定在 Daemon 驗證過的 canonical workspace。
   - **邊界說明**：`exec.CommandContext` 並非作業系統容器級沙盒。系統需透過嚴格的旗標配置、環境變數淨化、路徑校驗與行程群組（Process Group）終止機制，確保 Git 子程序在既定唯讀邊界內運作。

2. **記憶架構整合點**：
   - 現有 `memories` 資料表專用於「已生效的正式專案記憶」，直接參與 Phase 3F `recall`（詞彙重疊計數）與 Context Window 估算。
   - 若引入未審核的 Git 變更候選，必須採用獨立的「候選暫存（Candidates Storage）」架構，維持正式記憶與草稿候選的實體隔離，避免未審核內容污染自動召回結果。

---

## 方案選型與取捨（H-TBD-1 ～ H-TBD-6）

### H-TBD-1：Git Range、Repository Identity 與 Checkpoint 策略

#### 1. Repository Identity 定義
- **`repo_id`**：以 Git 根目錄之初始 Commit SHA（透過 `git rev-list --max-parents=0 --abbrev-commit=40 HEAD | tail -n 1` 取得）結合 Canonical Repo Root 路徑之 SHA-256 前 16 字元產生。
- **目的**：杜絕目錄更名、符號連結別名或不同專案共用工作區時的識別混淆。

#### 2. Checkpoint 複合鍵隔離
為支援多分支開發（例如 `main`、`develop`、`feature/*` 並存），Checkpoint 必須依分支與儲存庫嚴格隔離，不可僅以 `workspace` 為單一主鍵：
- 複合識別：`(workspace, repo_id, ref_name)`。

#### 3. 雙軌進度分離（Capture Checkpoint vs Review Status）
將「Git 變更擷取進度」與「人工審核狀態」完全解耦：
- **Capture Checkpoint（`git_checkpoints`）**：記錄某個分支在某個專案內，Git 變更已被**完整擷取並持久化至候選庫**的最高 Commit SHA。僅在「完整擷取（Complete Capture）」時前進。
- **Review Status（`memory_candidates.status`）**：記錄使用者對單一候選草稿的審核狀態（`pending`、`approved`、`discarded`）。
- **關鍵決策**：個別 Candidate 的核准或捨棄，**絕對不觸動、亦不代表整個 Git 區間的 Checkpoint 前進**。

| 方案 | 說明 | 優點 | 缺點 / 風險 | 取捨決策 |
|---|---|---|---|---|
| **方案 1A：純顯式指定 range** | 每次執行必須顯式指定 `--range <base>..<head>` 或 `<commit>` | 語意最清晰、無狀態、完全無分支/force-push 競態風險 | 使用者需手動查詢前次 commit SHA，無自動增量便利性 | **第一階段核心基底**：首版底層實作必須先支援此模式，提供最高確定性。 |
| **方案 1B：增量 Checkpoint + 顯式 range 混合** | 支援 `--range` 亦支援 `--since-last`。Checkpoint 以 `(workspace, repo_id, ref_name)` 隔離儲存 | 自動化程度高，使用者體驗佳 | 分支切換、force-push、merge、commit 重寫時若判斷不當可能混淆增量流 | **推薦實作**：以 1A 為底層驗證基礎，在上層提供 1B。若偵測到當前 HEAD 不是 checkpoint 的直接子孫（ancestor check 失敗）或 ref 改變，強制拒絕增量並提示使用者指定範圍。 |

---

### H-TBD-2：Git 子程序、外部擴展與安全防護矩陣

為防範受控儲存庫中的惡意 Git 配置、外部工具逃逸或未授權存取，建立嚴格的安全防護契約：

| 風險項目 | 潛在威脅 | 嚴格防護策略（實作規範） |
|---|---|---|
| **外部 Diff 程式** | Repo 的 `.git/config` 指定 `[diff] external = ...` 執行任意腳本 | 命令一律強制帶入旗標：`--no-ext-diff`、`--no-textconv`、`-c diff.external=`、`-c diff.noprefix=false`。 |
| **全域／系統配置污染** | 主機環境包含未預期 hook、alias 或 fsmonitor 背景進程 | 執行環境注入：`GIT_CONFIG_NOSYSTEM=1`、`GIT_CONFIG_GLOBAL=/dev/null`、`-c core.fsmonitor=`。 |
| **未授權 Git 目錄逃逸** | `.git` 檔案（如 submodules 或 linked worktree）指向 workspace 外目錄 | 執行前解析 `git rev-parse --git-dir` 與 `--show-toplevel`，兩者之真實絕對路徑均必須落在 `allowedRoots` 範圍內，否則直接拒絕。 |
| **外部物件庫參照** | `objects/info/alternates` 借用未受控的外部目錄 | 檢查或禁用 alternates；若 alternates 包含超出 allowed roots 之路徑，視為越界拒絕。 |
| **Partial Clone 自動拉取** | 讀取 missing objects 時自動觸發遠端網路連線或 prompt | 注入環境變數 `GIT_OPTIONAL_LOCKS=0`、`GIT_TERMINAL_PROMPT=0`，並傳入 `-c core.alternateRefsCommand=` 避免外送請求。 |
| **子程序取消與殘留** | 逾時（10s）或 Context 取消時，Git 子進程未釋放鎖或變殭屍進程 | 採用獨立 Process Group（`Setpgid: true`），取消時透過 `syscall.Kill(-pgid, syscall.SIGKILL)` 確保 Git 及其子行程完整回收。 |
| **輸出記憶體膨脹** | 惡意構造的巨大 commit message 或檔案列表導致 OOM | 對 stdout、stderr 採用 `io.LimitReader`，分別強制限制 512 KiB 與 64 KiB 上限。 |

---

### H-TBD-3：有界擷取、截斷政策與秘密過濾邊界

#### 1. 截斷政策與狀態劃分（Complete / Partial / Rejected）
針對超出 Hard bounds 的情境，嚴格劃分三種處理狀態，**絕不允許 Partial Capture 悄悄推進 Checkpoint**：

- **Complete Capture（完整擷取）**：
  - 條件：commits <= 100，files <= 50，diff <= 256 KiB。
  - 行為：生成候選草稿，`truncated = false`。若啟用增量模式，`git_checkpoints.last_captured_sha` 前進至 `head_sha`。
- **Partial Capture（部分擷取／截斷）**：
  - 條件：超過檔案數（>50）或 diff 位元組（>256 KiB）。
  - 行為：僅保留前 50 個檔案或前 256 KiB 內容，候選草稿明確標註 `truncated = true`，並在 Facts 中條列省略檔案清單。
  - **重要約束**：**不得自動推進 Checkpoint**，防止剩餘未處理檔案永久遺漏。使用者需顯式提供縮小的 `--range` 重新擷取，或傳入 `--allow-partial-checkpoint` 明確承擔遺漏風險。
- **Rejected Capture（拒絕擷取）**：
  - 條件：Commits 超過 100 個、執行逾時（>10s）、無關歷史（unrelated history）或祖先血統檢查失敗。
  - 行為：中止操作，回報錯誤，0 候選產生，Checkpoint 保持不變。

#### 2. 機密過濾邊界宣告
- 系統內建啟發式敏感檔案與關鍵字過濾（副檔名 `.env`、`.pem`、`.key`、`id_rsa`，以及 `API_KEY`、`PASSWORD` 等特徵）。
- **限制說明**：此機制屬於深度防禦（Defense-in-depth）的過濾網，**不能視為 100% 絕對的機密偵測器**。任何向外傳送（若未來啟用 AI 摘要）必須視為獨立授權事項。

---

### H-TBD-4：候選儲存、正式記憶與 Git Provenance 關聯

#### 1. 資料庫結構設計

```sql
-- 1. 候選記憶暫存表：完全隔離，不參與既有召回
CREATE TABLE memory_candidates (
    id TEXT PRIMARY KEY,
    workspace TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    ref_name TEXT NOT NULL,
    base_sha TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    commit_count INTEGER NOT NULL,
    facts_summary TEXT NOT NULL,      -- Git 客觀事實 (commits, files, stats, truncated 狀態)
    draft_content TEXT NOT NULL,      -- 待審核的專案記憶草稿
    status TEXT NOT NULL,             -- pending, approved, discarded
    policy_version TEXT NOT NULL DEFAULT 'gitmem-v1',
    approved_memory_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    approved_at DATETIME,
    UNIQUE(workspace, repo_id, ref_name, base_sha, head_sha, policy_version)
);

-- 2. Checkpoint 增量游標表：多分支與多儲存庫隔離
CREATE TABLE git_checkpoints (
    workspace TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    ref_name TEXT NOT NULL,
    last_captured_sha TEXT NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (workspace, repo_id, ref_name)
);

-- 3. 正式記憶來源關聯表（Provenance）：建立可追溯證據鏈
CREATE TABLE memory_provenance (
    memory_id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    ref_name TEXT NOT NULL,
    base_sha TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    commit_count INTEGER NOT NULL,
    commit_shas_json TEXT NOT NULL DEFAULT '[]',
    facts_summary TEXT NOT NULL DEFAULT '',
    is_user_edited INTEGER NOT NULL DEFAULT 0,
    approved_at DATETIME NOT NULL,
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
);
```

#### 2. 正式 Memory 內容純淨性與 Phase 3F 召回保護
- **Memory Content**：僅保留經過審核或編輯後的「高價值架構決策與實作約束」，**不將大量原始 Commit SHA、檔案列表或 Diff Stats 硬性拼接進 Content**，避免污染 Phase 3F 的詞彙重疊計數（term overlap count）。
- **來源保留政策**：當使用者手動呼叫 `asura memory remove` 刪除正式記憶時，`memory_provenance` 透過 `ON DELETE CASCADE` 同步清理，維持儲存庫整潔；而 `memory_candidates` 則永久保留 `approved` 審核歷史與 `approved_memory_id` 紀錄，作為不可變稽核痕跡。

---

### H-TBD-5：專案記憶價值與無模型最小交付

#### 1. 「Git 變更紀錄」vs「長期專案記憶」
- **變更紀錄（Change Log）**：僅描述「何時改了什麼檔案」（例如：`2026-09-25 新增 memory_candidates 表`）。對未來 AI Agent 進行架構決策時價值有限。
- **專案記憶（Project Memory）**：包含「架構決策 + 決策原由 + 技術限制」（例如：`Git 候選採獨立表隔離，避免未審核草稿進入 memories 表而污染 Phase 3F 自動召回`）。
- **目標**：從 Git 變更事實中提煉可長期重用的架構決策，輔助使用者審核並固化成正式記憶。

#### 2. 無模型最小交付（模式 1 優先）
- **第一版預設**：採用模式 1（確定性事實生成）。由 Git 子程序分析 Commit 訊息、影響模組與關鍵變更，產生包含結構化「變更摘要」與「待填寫決策理由」的草稿，由使用者在 CLI 編修核准。
- **好處**：零 Token 消耗、零外部網路呼叫、免除 Prompt Injection 風險，在斷網環境亦可 100% 運行。
- **AI 輔助模式（模式 2，選擇性增強）**：若本期核准 AI 草稿（GITMEM-009），僅作為選擇性旗標（`--ai-draft`）。模型僅獲唯讀分析權限（deny mutating/bash），並在固定 Token 預算下運作；生成失敗或超限時自動退回模式 1，絕不阻斷候選生成。

---

### H-TBD-6：候選去重、核准冪等性與交易恢復

#### 1. 擷取去重（Capture Dedup）
- 依 `(workspace, repo_id, ref_name, base_sha, head_sha, policy_version)` 判斷：
  - 若已有 `pending` 候選：直接回傳該候選 ID，不重複插入。
  - 若已有 `approved` 候選：回傳該候選資訊及既有之 `approved_memory_id`，防止重複產生候選。
  - 若已有 `discarded` 候選：回報已捨棄紀錄；除非顯式帶入 `--force`，否則不自動重開。

#### 2. 核准冪等（Approval Idempotency）
- 當對候選執行 `approve` 時：
  - 若候選狀態已為 `approved`：直接回傳既有的 `approved_memory_id`，維持冪等成功。
  - 若該 `approved_memory_id` 在 `memories` 中已被使用者刪除：候選維持 `approved` 狀態，回報錯誤告知該記憶已被手動刪除，**嚴禁悄悄重新插入**已刪除的記憶（防復活機制）。

#### 3. 原子性核准交易（Atomic Approval Transaction）
審核核准操作在單一 SQLite 交易內完成以下步驟：
1. `SELECT ... FROM memory_candidates WHERE id = ? FOR UPDATE`（驗證狀態為 `pending`）。
2. 生成新 `memory_id`，寫入 `memories`（`source = 'git-approved'`）。
3. 寫入 `memory_provenance`（關聯 `memory_id`、`candidate_id`、事實摘要與編輯標記）。
4. 更新 `memory_candidates`：`status = 'approved'`、`approved_memory_id = mem.ID`、`approved_at = now`。
5. 若為 Complete Capture 且由同一操作觸發 Checkpoint 更新，一併更新 `git_checkpoints`。
6. `COMMIT`（任一步驟失敗則整體回滾，保證 0 孤立紀錄）。

---

## 架構影響評估

1. **Daemon IPC 介面**：
   - `git_memory_capture`（回傳 candidate 或既有 ID，並標示 Complete/Partial）
   - `git_memory_candidates`（有界清單，僅回傳候選摘要）
   - `git_memory_approve`（原子寫入 memories + provenance，支援可選編修內文）
   - `git_memory_discard`（標記 candidate 為 discarded）
2. **CLI 命令列**：
   - `asura memory capture --workspace <path> [--range <base>..<head>] [--since-last]`
   - `asura memory candidates --workspace <path>`
   - `asura memory approve --workspace <path> --candidate <id> [--content <edited>]`
   - `asura memory discard --workspace <path> --candidate <id>`
3. **既有模組相容性**：
   - `cmd/asura/ask.go`：沿用 `--memory-recall bounded`，無縫召回 `git-approved` 記憶。
   - `internal/recall/recall.go`：現有詞彙重疊計數邏輯保持不變。
   - Web Console：保持相容，後續可平滑擴充候選審核介面。
