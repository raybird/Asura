# 權威實作計畫（Implementation Plan）

2026-09-25。分級：Large。本檔為 Phase 3H（Git-Aware Project Memory Capture）的唯一實作任務與進度追蹤來源。外部驗收契約見 [README Gherkin 驗收劇本](README.md#gherkin-驗收劇本)；技術決策與選型細節見 [技術分析](technical-analysis.md)。

> **核准門檻宣告**：依 [GitHub #23](https://github.com/raybird/Asura/issues/23) 規範，本 issue 目前授權「現況調查、方案分析與不造成新 production 行為的安全 probe」；具體 Git 擷取方式、checkpoint、AI 草稿、schema、上限與 GITMEM-001～010 驗收行為須待使用者定案核准後方可實作。因此，Phase 1 為立項已授權之非變更性探測，Phase 2～6 列為待核准任務。

---

## 設計方案摘要

### 1. 模組架構與邊界

```text
cmd/asura/ (CLI)
  └── memory capture | candidates | approve | discard
        │ Unix Socket IPC (有界 JSON)
        ▼
internal/daemon/ (Daemon)
  ├── 授權比對 (AuthorizeCanonicalWorkspace)
  ├── Git 子程序安全調度器 (internal/gitmem/git.go)
  │     ├── 唯讀參數封裝 (--no-replace-objects, -c core.fsmonitor=)
  │     ├── SHA 固定與祖先血統檢查
  │     └── 有界串流與截斷 (Hard bounds: 100 commits, 50 files, 256KB diff)
  └── 記憶庫與候選暫存管理器 (internal/gitmem/service.go)
        │
        ├── 候選暫存表 (memory_candidates) ─── 待審核草稿 (零召回污染)
        ├── Checkpoint 表 (git_checkpoints) ── 增量 SHA 紀錄 (交易保護)
        └── 正式記憶表 (memories) ───────────── 核准轉入 (source='git-approved')
```

### 2. 資料結構與 DTO 規格

```go
// internal/gitmem/types.go
type CandidateStatus string
const (
    StatusPending   CandidateStatus = "pending"
    StatusApproved  CandidateStatus = "approved"
    StatusDiscarded CandidateStatus = "discarded"
)

type MemoryCandidate struct {
    ID           string          `json:"id"`
    Workspace    string          `json:"workspace"`
    RefName      string          `json:"ref_name"`
    BaseSHA      string          `json:"base_sha"`
    HeadSHA      string          `json:"head_sha"`
    CommitCount  int             `json:"commit_count"`
    FactsSummary string          `json:"facts_summary"`
    DraftContent string          `json:"draft_content"`
    Status       CandidateStatus `json:"status"`
    CreatedAt    time.Time       `json:"created_at"`
    ApprovedAt   *time.Time      `json:"approved_at,omitempty"`
}

type GitCheckpoint struct {
    Workspace  string    `json:"workspace"`
    RepoRoot   string    `json:"repo_root"`
    RefName    string    `json:"ref_name"`
    LastHeadSHA string   `json:"last_head_sha"`
    UpdatedAt  time.Time `json:"updated_at"`
}
```

---

## 實作步驟（Phase / Task）

### Phase 1：現況調查、危險 Git 輸入矩陣與安全 Probe（已授權／不改變 production 行為）

- [ ] ⏳ **Task 1.1 — 建立測試 Git 負向樣本與指令邊界探測**
  - **產出**：`tests/integration/testdata/git_probe_repo` 輔助腳本與 `docs/issues/issue-23/evidence/probe-git-boundary.md`
  - **目標**：在隔離目錄構造 symlink 逃逸、submodule、二進位檔案、巨大 diff、無關歷史（unrelated history）、以及參數注入（如 `--upload-pack` 等）樣本；驗證 `git rev-parse`、`git log`、`git diff` 在唯讀旗標下是否保持原 repo 零修改。
  - **完成判準**：探測腳本確認原 repo HEAD/branch/index 無任何改動，且所有惡意邊界均能被靜態/命令封裝攔截。

- [ ] 📝 **Task 1.2 — 探測既有 SQLite Schema 與記憶隔離能力**
  - **產出**：`docs/issues/issue-23/evidence/probe-sqlite-isolation.md`
  - **目標**：測試既有 `memories` 查詢在加入 `source='git-approved'` 時之相容性，驗證 Phase 3F `recall` 與 Context Window 估算是否正常運作。
  - **完成判準**：證明正式記憶五欄位不需破壞性遷移即可自然相容新 source。

---

### Phase 2：有界 Git 唯讀讀取與 Repo 安全邊界（待核准）

> 依賴：H-TBD-1、H-TBD-2、H-TBD-3 核准，GITMEM-001～004 驗收確認。

- [ ] 📝 **Task 2.1 — 實作安全 Git CLI 執行器與 Repo 驗證 (`internal/gitmem/git.go`)**
  - **產出**：`internal/gitmem/git.go`, `internal/gitmem/git_test.go`
  - **對應驗收**：GITMEM-001, GITMEM-002
  - **完成判準**：通過非 Git 目錄拒絕、頂層路徑超出 allowed roots 拒絕測試；確認執行後原 repo 零暫存檔、零 index 變更。

- [ ] 📝 **Task 2.2 — 實作固定 Range 解析與祖先血統檢查**
  - **產出**：`internal/gitmem/range.go`, `internal/gitmem/range_test.go`
  - **對應驗收**：GITMEM-003
  - **完成判準**：單元測試驗證 base/head 存在性比對、merge 與 commit-graph 祖先驗證；unrelated history 拒絕混合。

- [ ] 📝 **Task 2.3 — 實作有界串流擷取與截斷防護 (Hard Bounds)**
  - **產出**：`internal/gitmem/capture.go`, `internal/gitmem/capture_test.go`
  - **對應驗收**：GITMEM-004
  - **完成判準**：超過 100 commits、超過 50 files、超過 256KB diff 時如實標記 truncated；binary 檔案自動略過。

---

### Phase 3：候選記憶儲存、Provenance 與 CLI 審核工作流（待核准）

> 依賴：H-TBD-4、H-TBD-5 核准，GITMEM-005、GITMEM-006 驗收確認。

- [ ] 📝 **Task 3.1 — 候選記憶與 Checkpoint 資料庫表實作 (`internal/storage/sqlite_gitmem.go`)**
  - **產出**：`internal/storage/sqlite_gitmem.go`, `internal/storage/sqlite_gitmem_test.go`
  - **對應驗收**：GITMEM-005
  - **完成判準**：建立 `memory_candidates` 與 `git_checkpoints` 表；草稿資料不被 `ListMemories` 讀出。

- [ ] 📝 **Task 3.2 — 客觀 Facts 生成與來源 Provenance 封裝**
  - **產出**：`internal/gitmem/facts.go`, `internal/gitmem/facts_test.go`
  - **對應驗收**：GITMEM-006
  - **完成判準**：結構化記錄 commit SHA、作者、統計數據，與草稿文字明確劃分。

- [ ] 📝 **Task 3.3 — Daemon IPC 與 CLI 審核命令列實作**
  - **產出**：`cmd/asura/memory_git.go`, `internal/daemon/gitmem_handlers.go`
  - **對應驗收**：GITMEM-005
  - **完成判準**：CLI 支援 `capture`, `candidates`, `approve`, `discard` 子命令；未審核草稿 0 寫入 `memories`。

---

### Phase 4：崩潰恢復、交易保證與 Checkpoint 冪等性（待核准）

> 依賴：H-TBD-6 核准，GITMEM-007 驗收確認。

- [ ] 📝 **Task 4.1 — 實作 Checkpoint 更新與原子核准交易**
  - **產出**：`internal/storage/sqlite_gitmem_tx.go`
  - **對應驗收**：GITMEM-007
  - **完成判準**：核准與 checkpoint 更新置於同一交易；注入 SQLite 失敗驗證 Rollback 零部分寫入。

- [ ] 📝 **Task 4.2 — 候選去重與重複請求冪等性**
  - **產出**：`internal/gitmem/dedup_test.go`
  - **對應驗收**：GITMEM-007
  - **完成判準**：相同 base..head 之重複 capture 回傳相同候選；重複 approve 回傳相同記憶 ID，無重複列。

---

### Phase 5：既有 Memory 與 Sealed Context 整合相容性（待核准）

> 依賴：GITMEM-008, GITMEM-009 驗收確認。

- [ ] 📝 **Task 5.1 — 正式記憶寫入與 Phase 3F 自動召回整合**
  - **產出**：`tests/integration/gitmem_recall_test.go`
  - **對應驗收**：GITMEM-008
  - **完成判準**：經核准的 `git-approved` 記憶可自然被 `asura run --memory-recall bounded` 召回；不影響已接受任務之密封快照。

- [ ] 📝 **Task 5.2 — AI 輔助草稿唯讀權限與安全邊界（若本期核准）**
  - **產出**：`internal/gitmem/ai_draft.go`, `internal/gitmem/ai_draft_test.go`
  - **對應驗收**：GITMEM-009
  - **完成判準**：模型以唯讀策略（deny mutating）執行；超限或惡意 prompt injection 時安全退回模式 1。

---

### Phase 6：端到端整合測試與完整回歸（待核准）

> 依賴：GITMEM-010 驗收確認。

- [ ] 📝 **Task 6.1 — 真實 Git Repo 端到端驗收測試**
  - **產出**：`tests/integration/git_memory_e2e_test.go`
  - **對應驗收**：GITMEM-001～009
  - **完成判準**：以真實多分支、帶 merge、帶刪檔之測試 Git Repo 跑通完整 capture -> review -> approve -> recall 流程。

- [ ] 📝 **Task 6.2 — 完整既有套件回歸測試**
  - **產出**：回歸測試紀錄
  - **對應驗收**：GITMEM-010
  - **完成判準**：`go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura` 全綠通過。

---

## 使用方式對照（變更前後）

| 操作場景 | 變更前 | 變更後（待核准目標） |
|---|---|---|
| **專案記憶來源** | 僅能由使用者以 `asura memory add --content "..."` 手動新增 | 支援手動新增，亦可針對 Git 差異執行 `asura memory capture` 自動擷取變更候選 |
| **Git 變更感知** | 無，完全無 Git 感知能力 | 透過安全封裝之 Git 子程序唯讀讀取固定 base..head 差異事實 |
| **審核機制** | 無審核概念，新增即正式生效 | 擷取之內容先存為 `pending` 候選草稿，使用者透過 CLI 查看／編修並明確 approve 後方轉為正式記憶 |
| **記憶召回** | 僅召回手動記憶 | 自動以相同權重與 lexical-v1 選擇器召回已審核之 `git-approved` 記憶 |

---

## 測試策略（Verification Strategy）

1. **單元測試（Unit Tests）**：
   - Git 指令參數組裝與安全注入防護（`internal/gitmem`）
   - Hard bounds 截斷（commit 數、diff bytes、檔案數）與二進位檢測
   - Keyset/Checkpoint 狀態與 SQLite 候選儲存交易
2. **負向測試與安全性測試（Negative & Security Tests）**：
   - 越界 symlink、非 Git 目錄、未授權 workspace 拒絕驗證（0 寫入、0 模型呼叫）
   - Git 惡意檔名、無關歷史（unrelated history）、force-push 增量拒絕
   - 崩潰與斷電注入（Crash injection）：驗證 Transaction 回滾與無孤立記錄
3. **整合測試（Integration Tests）**：
   - 透過 Unix Socket IPC 與 Daemon 進行完整生命週期測試
   - 結合 Phase 3F `recall` 與 Context Window 驗證端到端記憶召回
4. **全套件回歸（Regression）**：
   - `go vet ./...`
   - `go test -count=1 -race ./...`
   - `go build ./cmd/asura`

---

## 風險與首要驗證

- 引用 README 之 [風險與首要驗證](README.md#風險與首要驗證)。

---

## 檢查清單（Checklist）

- [ ] Phase 1 安全 Probe 產出完整紀錄，證實原 repo 零修改與指令邊界穩固。
- [ ] H-TBD-1～6 設計選型由使用者確認並正式核准。
- [ ] GITMEM-001～010 外部驗收劇本由使用者正式核准。
- [ ] 進入 Phase 2 實作前，以 decompose 將各 Phase 細化為具體 Task。
- [ ] 交付時更新根目錄 `README.md` CLI 使用手冊與架構常青文件。
