# 權威實作計畫（Implementation Plan）

2026-09-25。分級：Large。本檔為 Phase 3H（Git-Aware Project Memory Capture）的唯一實作任務與進度追蹤來源。外部驗收契約見 [README Gherkin 驗收劇本](README.md#gherkin-驗收劇本)；技術決策與選型細節見 [技術分析](technical-analysis.md)。

> **核准狀態**：GITMEM-001～010 驗收劇本與 H-TBD-1～6 設計選型已於 2026-09-25 由使用者正式核准。本計畫各 Phase 與 Task 已具備完整相依與完成判準，依風險優先原則先執行 Phase 1 安全探測，再推進核心實作。

---

## 設計方案摘要

### 1. 模組架構與邊界

```text
cmd/asura/ (CLI)
  └── memory capture | candidates | approve | discard
        │ Unix Socket IPC (有界 JSON)
        ▼
internal/daemon/ (Daemon)
  ├── 授權比對 (AuthorizeCanonicalWorkspace & Repo Root 包含性校驗)
  ├── Git 子程序安全調度器 (internal/gitmem/git.go)
  │     ├── 禁用外部 Diff 與擴展工具 (--no-ext-diff, -c diff.external=, -c core.fsmonitor=)
  │     ├── SHA 固定、祖先血統檢查與 Process Group 安全回收
  │     └── 截斷狀態分流 (Complete: 前進 Checkpoint / Partial: 阻斷 Checkpoint / Rejected)
  └── 記憶庫與來源追溯管理器 (internal/gitmem/service.go)
        │
        ├── 候選暫存表 (memory_candidates) ─── 待審核草稿 (零召回污染)
        ├── 增量游標表 (git_checkpoints) ──── 分支/儲存庫複合鍵 (Complete 推進)
        ├── 來源關聯表 (memory_provenance) ─── 結構化客觀事實 (ON DELETE CASCADE)
        └── 正式記憶表 (memories) ───────────── 高價值決策純淨內文 (source='git-approved')
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

type CaptureOutcome string
const (
    OutcomeComplete CaptureOutcome = "complete" // 完整擷取，可推進 Checkpoint
    OutcomePartial  CaptureOutcome = "partial"  // 超出 Hard bounds 截斷，禁止自動推進 Checkpoint
    OutcomeRejected CaptureOutcome = "rejected" // 驗證失敗、逾時或血統異常，0 候選產生
)

type MemoryCandidate struct {
    ID               string          `json:"id"`
    Workspace        string          `json:"workspace"`
    RepoID           string          `json:"repo_id"`
    RefName          string          `json:"ref_name"`
    BaseSHA          string          `json:"base_sha"`
    HeadSHA          string          `json:"head_sha"`
    CommitCount      int             `json:"commit_count"`
    FactsSummary     string          `json:"facts_summary"`
    DraftContent     string          `json:"draft_content"`
    Status           CandidateStatus `json:"status"`
    PolicyVersion    string          `json:"policy_version"`
    ApprovedMemoryID string          `json:"approved_memory_id,omitempty"`
    CreatedAt        time.Time       `json:"created_at"`
    ApprovedAt       *time.Time      `json:"approved_at,omitempty"`
}

type GitCheckpoint struct {
    Workspace       string    `json:"workspace"`
    RepoID          string    `json:"repo_id"`
    RefName         string    `json:"ref_name"`
    LastCapturedSHA string    `json:"last_captured_sha"`
    UpdatedAt       time.Time `json:"updated_at"`
}

type MemoryProvenance struct {
    MemoryID       string    `json:"memory_id"`
    CandidateID    string    `json:"candidate_id"`
    Workspace      string    `json:"workspace"`
    RepoID         string    `json:"repo_id"`
    RefName        string    `json:"ref_name"`
    BaseSHA        string    `json:"base_sha"`
    HeadSHA        string    `json:"head_sha"`
    CommitCount    int       `json:"commit_count"`
    CommitSHAsJSON string    `json:"commit_shas_json"`
    FactsSummary   string    `json:"facts_summary"`
    IsUserEdited   bool      `json:"is_user_edited"`
    ApprovedAt     time.Time `json:"approved_at"`
}
```

---

## 實作步驟（Phase / Task，資料模型優先）

### Phase 1：Git 安全與授權 Probe（首要驗證）

- [x] ✅ **Task 1.1 — 建立測試 Git 負向樣本與指令邊界探測**
  - **產出與範圍**：`tests/integration/probe_git_boundary_test.go` 與 `docs/issues/issue-23/evidence/probe-git-boundary.md`
  - **相依**：無
  - **驗收編號**：GITMEM-001, GITMEM-002, GITMEM-004
  - **目標**：在隔離目錄構造 symlink 逃逸、submodule、二進位檔案、巨大 diff、無關歷史（unrelated history）、外部 diff 配置與參數注入；驗證 `git rev-parse`、`git log`、`git diff` 在嚴格安全旗標下是否保持原 repo 零修改與零外部程式調用。
  - **完成判準**：探測報告證實原 repo HEAD/branch/index 無任何改動，且所有惡意邊界均能被靜態/命令封裝攔截。
  - **驗證**：`go test -v -run TestProbe_GitBoundary ./tests/integration/` 通過（6/6 pass）。
  - **狀態**：✅ 已完成
  - **證據**：[probe-git-boundary.md](evidence/probe-git-boundary.md)

- [x] ✅ **Task 1.2 — 探測既有 SQLite Schema 與記憶隔離能力**
  - **產出與範圍**：`tests/integration/probe_sqlite_isolation_test.go` 與 `docs/issues/issue-23/evidence/probe-sqlite-isolation.md`
  - **相依**：Task 1.1
  - **驗收編號**：GITMEM-005, GITMEM-008, GITMEM-010
  - **目標**：測試既有 `memories` 查詢在加入 `source='git-approved'` 時之相容性，驗證 Phase 3F `recall`（詞彙重疊計數）與 Context Window 估算是否正常運作。
  - **完成判準**：證明正式記憶五欄位不需破壞性遷移即可自然相容新 source。
  - **驗證**：`go test -v -run TestProbe_SQLite ./tests/integration/` 通過（2/2 pass）。
  - **狀態**：✅ 已完成
  - **證據**：[probe-sqlite-isolation.md](evidence/probe-sqlite-isolation.md)

---

### Phase 2：Git Range、血統驗證與截斷策略

> 依賴：Phase 1 探測完成。

- [x] ✅ **Task 2.1 — 實作安全 Git CLI 執行器與 Repo 邊界校驗 (`internal/gitmem/git.go`)**
  - **產出與範圍**：`internal/gitmem/git.go`, `internal/gitmem/git_test.go`
  - **相依**：Phase 1
  - **驗收編號**：GITMEM-001, GITMEM-002
  - **完成判準**：通過非 Git 目錄拒絕、頂層路徑超出 allowed roots 拒絕、外部 diff 禁用測試；Process Group 逾時安全回收。
  - **驗證**：`go test -v ./internal/gitmem`（7/7 pass，包含 stress 10 次零失敗）。
  - **狀態**：✅ 已完成
  - **證據**：單元測試覆蓋 ValidateAndResolveRepo (Success, Subdir, NotGit, OutsideAllowedRoots, GitDirEscape)、ExternalDiffBlocked、TimeoutWithProcessGroup。

- [x] ✅ **Task 2.2 — 實作固定 Range 解析與祖先血統檢查 (`internal/gitmem/range.go`)**
  - **產出與範圍**：`internal/gitmem/range.go`, `internal/gitmem/range_test.go`
  - **相依**：Task 2.1
  - **驗收編號**：GITMEM-003
  - **完成判準**：單元測試驗證 base/head 存在性比對、merge 與 commit-graph 祖先驗證；unrelated history 拒絕混合。
  - **驗證**：`go test -v ./internal/gitmem`（14/14 pass）。
  - **狀態**：✅ 已完成
  - **證據**：單元測試覆蓋 ResolveRange (ValidRange, SingleCommit, InvalidRef, NotAncestor, UnrelatedHistory, EmptyRange, TooManyCommits)。

- [x] ✅ **Task 2.3 — 實作有界串流擷取與截斷分流政策 (`internal/gitmem/capture.go`)**
  - **產出與範圍**：`internal/gitmem/capture.go`, `internal/gitmem/capture_test.go`
  - **相依**：Task 2.2
  - **驗收編號**：GITMEM-004
  - **完成判準**：明確分流 Complete / Partial (50 檔案、256 KiB diff) / Rejected；Partial Capture 標記 `truncated: true` 且明確禁止推進 Checkpoint；機密檔案過濾。
  - **驗證**：`go test -v ./internal/gitmem`（18/18 pass）。
  - **狀態**：✅ 已完成
  - **證據**：單元測試覆蓋 Capture (Complete, Partial_FileLimit, Partial_DiffSizeLimit, SensitiveFileFiltering)。

---

### Phase 3：Candidate / Provenance / Checkpoint Schema 與資料模型

> 依賴：Phase 2 完成。

- [x] ✅ **Task 3.1 — 資料庫 Schema 實作 (`internal/storage/sqlite_gitmem.go`)**
  - **產出與範圍**：`internal/storage/sqlite_gitmem.go`, `internal/storage/sqlite_gitmem_test.go`
  - **相依**：Phase 2
  - **驗收編號**：GITMEM-005, GITMEM-006
  - **完成判準**：建立 `memory_candidates`（含 `approved_memory_id`）、`git_checkpoints`（複合 PK）與 `memory_provenance`（ON DELETE CASCADE）表；草稿資料 100% 隔離，不被 `ListMemories` 或 `recall` 讀出。
  - **驗證**：`go test -v -run TestGitMem ./internal/storage`（4/4 pass）。
  - **狀態**：✅ 已完成
  - **證據**：單元測試覆蓋 SchemaAndIsolation (正式記憶與 Recall 零污染)、CandidateCRUD (複合 Range 唯一索引)、CheckpointCRUD (分支/repo 隔離)、ProvenanceCascadeDelete (外鍵連動刪除與不可變稽核)。

- [x] ✅ **Task 3.2 — 客觀 Facts 生成與 Provenance 封裝 (`internal/gitmem/facts.go`)**
  - **產出與範圍**：`internal/gitmem/facts.go`, `internal/gitmem/facts_test.go`
  - **相依**：Task 3.1
  - **驗收編號**：GITMEM-006
  - **完成判準**：結構化記錄 commit SHA 清單、變更統計與截斷資訊，與專案記憶草稿內文劃分；保持正式記憶內文純淨。
  - **驗證**：`go test -v ./internal/gitmem`（21/21 pass）。
  - **狀態**：✅ 已完成
  - **證據**：單元測試覆蓋 FormatFactsSummary、GenerateDraftContent (純淨架構決策模板，無 raw diff 污染)、BuildProvenance (JSON SHA 清單、使用者編輯判定)。

---

### Phase 4：Atomic Approval、去重與冪等性

> 依賴：Phase 3 完成。

- [x] ✅ **Task 4.1 — 擷取去重與 Checkpoint 增量游標管理**
  - **產出與範圍**：`internal/gitmem/checkpoint.go`, `internal/gitmem/dedup_test.go`
  - **相依**：Phase 3
  - **驗收編號**：GITMEM-007
  - **完成判準**：相同 `(workspace, repo_id, ref_name, base_sha, head_sha, policy_version)` 重用候選；僅 Complete Capture 推進 `git_checkpoints`；Partial Capture 嚴格禁止前進 Checkpoint；捨棄候選需 force 重開。
  - **驗證**：`go test -v -run TestCaptureService ./internal/gitmem`（3/3 pass）。
  - **狀態**：✅ 已完成
  - **證據**：單元測試覆蓋 DedupPendingAndApproved、DiscardedRequiresForce、CheckpointProgressionAndPartialBlocking (Partial 阻斷)。

- [ ] 📝 **Task 4.2 — 原子性核准交易與冪等性保證 (`internal/storage/sqlite_gitmem_tx.go`)**
  - **產出與範圍**：`internal/storage/sqlite_gitmem_tx.go`, `internal/storage/sqlite_gitmem_tx_test.go`
  - **相依**：Task 4.1
  - **驗收編號**：GITMEM-007
  - **完成判準**：核准操作於單一 SQLite Transaction 寫入 `memories`、`memory_provenance` 並更新 `memory_candidates`；重複核准回傳既有 `approved_memory_id`。
  - **驗證**：交易崩潰注入與冪等測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

- [ ] 📝 **Task 4.3 — 防復活機制與刪除行為驗證**
  - **產出與範圍**：`internal/gitmem/lifecycle_test.go`
  - **相依**：Task 4.2
  - **驗收編號**：GITMEM-007, GITMEM-008
  - **完成判準**：正式記憶被手動刪除後，再次 approve 原 Candidate 明確報錯，禁止悄悄復活已刪除記憶。
  - **驗證**：生命週期整合測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

---

### Phase 5：CLI Capture / Review 審核工作流

> 依賴：Phase 4 完成。

- [ ] 📝 **Task 5.1 — Daemon IPC 動作擴充 (`internal/daemon/gitmem_handlers.go`)**
  - **產出與範圍**：`internal/daemon/gitmem_handlers.go`, `internal/daemon/gitmem_test.go`
  - **相依**：Phase 4
  - **驗收編號**：GITMEM-005
  - **完成判準**：實作 `git_memory_capture`、`git_memory_candidates`、`git_memory_approve`、`git_memory_discard` 動作並驗證 canonical scope。
  - **驗證**：IPC 整合測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

- [ ] 📝 **Task 5.2 — CLI 子命令介面實作 (`cmd/asura/memory_git.go`)**
  - **產出與範圍**：`cmd/asura/memory_git.go`, `cmd/asura/memory_git_test.go`
  - **相依**：Task 5.1
  - **驗收編號**：GITMEM-001, GITMEM-005
  - **完成判準**：CLI 提供 `capture`, `candidates`, `approve`, `discard` 操作閉環；支援 `--content` 編修。
  - **驗證**：CLI 整合測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

- [ ] 📝 **Task 5.3 — AI 輔助草稿唯讀權限與安全邊界（可選增強模組）**
  - **產出與範圍**：`internal/gitmem/ai_draft.go`, `internal/gitmem/ai_draft_test.go`
  - **相依**：Task 5.2
  - **驗收編號**：GITMEM-009
  - **完成判準**：模型以唯讀策略（deny mutating）執行；超限或注入異常時安全退回確定性草稿。
  - **驗證**：Prompt injection 與權限拒絕測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

---

### Phase 6：Memory Recall 整合

> 依賴：Phase 5 完成。

- [ ] 📝 **Task 6.1 — 正式記憶與 Phase 3F 自動召回整合驗證**
  - **產出與範圍**：`tests/integration/gitmem_recall_test.go`
  - **相依**：Phase 5
  - **驗收編號**：GITMEM-008
  - **完成判準**：經核准的 `git-approved` 記憶可自然被 `asura ask --memory-recall bounded` 召回；未核准草稿 0 召回。
  - **驗證**：Recall 整合測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

- [ ] 📝 **Task 6.2 — 任務密封性保護驗證**
  - **產出與範圍**：`tests/integration/sealed_compat_test.go`
  - **相依**：Task 6.1
  - **驗收編號**：GITMEM-008
  - **完成判準**：Git 記憶的新增、刪除或重新擷取不改寫歷史已接受任務之密封快照與 fingerprint。
  - **驗證**：密封快照比對測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

---

### Phase 7：端到端整合測試與完整回歸

> 依賴：Phase 6 完成。

- [ ] 📝 **Task 7.1 — 真實 Git Repo 端到端驗收測試**
  - **產出與範圍**：`tests/integration/git_memory_e2e_test.go`
  - **相依**：Phase 6
  - **驗收編號**：GITMEM-001～009
  - **完成判準**：以真實多分支、帶 merge、帶刪檔之測試 Git Repo 跑通完整 capture -> review -> approve -> recall 流程。
  - **驗證**：端到端整合測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

- [ ] 📝 **Task 7.2 — 完整既有套件回歸測試**
  - **產出與範圍**：回歸測試紀錄
  - **相依**：Task 7.1
  - **驗收編號**：GITMEM-010
  - **完成判準**：`go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura` 全綠通過。
  - **驗證**：全專案編譯與回歸測試。
  - **狀態**：📝 待實作
  - **證據**：待填入

---

## 使用方式對照（變更前後）

| 操作場景 | 變更前 | 變更後（待核准目標） |
|---|---|---|
| **專案記憶來源** | 僅能由使用者以 `asura memory add --content "..."` 手動新增 | 支援手動新增，亦可針對 Git 差異執行 `asura memory capture` 自動擷取變更候選 |
| **Git 變更感知** | 無，完全無 Git 感知能力 | 透過安全封裝之 Git 子程序唯讀讀取固定 base..head 差異事實 |
| **審核機制** | 無審核概念，新增即正式生效 | 擷取之內容先存為 `pending` 候選草稿，使用者透過 CLI 查看／編修並明確 approve 後方轉為正式記憶 |
| **記憶召回** | 僅召回手動記憶 | 透過 `asura ask --memory-recall bounded` 自動以相同權重與 lexical-v1 選擇器召回已審核之 `git-approved` 記憶 |

---

## 測試策略（Verification Strategy）

1. **單元測試（Unit Tests）**：
   - Git 指令參數組裝與安全注入防護（`internal/gitmem`）
   - Complete / Partial / Rejected 截斷分流與二進位檢測
   - Keyset/Checkpoint 複合鍵隔離與 SQLite 候選儲存交易
2. **負向測試與安全性測試（Negative & Security Tests）**：
   - 越界 symlink、非 Git 目錄、未授權 workspace 拒絕驗證（0 寫入、0 模型呼叫）
   - 外部 diff 注入、Git 惡意檔名、無關歷史（unrelated history）、force-push 增量拒絕
   - 崩潰與斷電注入（Crash injection）：驗證 Transaction 回滾與無孤立記錄
   - 防復活測試：驗證手動刪除記憶後重複核准被拒絕
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
