# 端到端驗收與全套回歸測試報告（Task 7.1 & Task 7.2）

2026-09-25 11:58 CST。工作區基準：Commit `abd99d8`。
本報告涵蓋 Phase 3H: Git-Aware Project Memory Capture（Issue #23）之真實 Git 儲存庫端到端整合驗收測試與全專案既有套件之完整回歸驗證。

---

## 一、驗收目標與涵蓋範圍

本次驗收依據 Issue #23 規範與核准之技術設計，針對下列驗收標準進行最終整合與回歸檢核：
1. **GITMEM-001 ~ GITMEM-003（唯讀來源與增量邊界）**：
   - 安全 Git CLI 隔離執行、唯讀保護、父行程群組超時終止。
   - 工作區邊界與外部 gitdir 逃逸阻斷。
   - 固定 Range 與增量 `--since-last` Checkpoint 前進機制。
2. **GITMEM-004 ~ GITMEM-006（可稽核候選與人工審核）**：
   - 候選記憶與正式記憶實體儲存隔離（`git_memory_candidates` vs `memories`）。
   - 客觀變更事實抽取、Provenance 結構（`git_ref`, `git_range`, `commit_shas`, `file_paths`）。
   - 人工審核工作流（review、approve、reject、clear），支援 CLI 與 Daemon IPC。
3. **GITMEM-007 ~ GITMEM-010（可靠性、生命週期與隔離）**：
   - 核准交易之原子性、草稿就地覆寫、去重與 Checkpoint 前進。
   - 不可變稽核痕跡與防復活機制（拒絕或核准後不可被相同 Range 重複擷取或復活）。
   - 刪除連動與歷史任務密封保護（歷史 Task 之快照記憶純淨無污染）。
   - Phase 3F 記憶召回整合（`asura ask` 僅能檢索已核准之正式記憶，候選庫不外洩）。

---

## 二、端到端整合測試（Task 7.1）

執行命令：
```bash
go test -v -count=1 ./tests/integration -run TestGitMemory_EndToEnd_FullWorkflow
```

### 1. 測試情境設計
在獨立臨時目錄建立具備多分支、非 Fast-Forward 合併（Merge Commit）、新增檔案與刪除檔案之複雜真實 Git 儲存庫，模擬專案實務開發歷程：
1. 初始化 main 分支並提交 `README.md`（Root Commit）。
2. 在 main 提交 `auth.go`。
3. 建立 `feature/auth` 分支，新增 `auth_oauth.go` 並刪除 `README.md`。
4. 切回 main 執行 `--no-ff` merge，產生 Merge Commit。
5. 啟動 Asura Daemon 與 Engine，以 IPC 進行擷取、候選檢視、核准、Phase 3F Ask 召回、二次擷取增量驗證、正式記憶刪除與防復活檢核。

### 2. 執行輸出日誌
```text
=== RUN   TestGitMemory_EndToEnd_FullWorkflow
--- PASS: TestGitMemory_EndToEnd_FullWorkflow (0.12s)
PASS
ok  	asura/tests/integration	0.118s
```

### 3. 實證結果
- **擷取與候選隔離**：成功擷取 3 個候選事實（包含新增檔案、刪除檔案與變更概要），此時 `store.ListMemories` 筆數為 0，正式記憶未受任何污染。
- **原子核准與草稿編輯**：管理員編輯候選草稿內容為「feat(auth): 新增 OAuth 認證機制並移除舊 README」，執行核准後正式記憶庫產生該筆記憶，候選狀態轉為 `approved`。
- **Phase 3F 召回整合**：執行 `asura ask`（透過 Mock Engine 模擬問答），產生的 Task 成功召回剛核准的正式 Git 記憶，其 `Task.MemoryRefs` 精確包含該記憶 ID。
- **增量 Checkpoint 推進**：首次擷取後 Checkpoint 之 `LastCapturedSHA` 更新至當前 HEAD；在未產生新 Commit 情況下再次執行 `--since-last` 擷取，系統精確回報「No new commits since last checkpoint」，未產生任何重複候選。
- **刪除連動與防復活**：刪除已核准之正式記憶後，正式記憶庫筆數歸零，但稽核紀錄與候選庫之審核狀態保持密封；再次執行相同 Range 擷取，去重機制阻斷復活，候選庫筆數不變。

---

## 三、全專案既有套件完整回歸測試（Task 7.2）

### 1. 靜態分析與型別檢查
```bash
go vet ./...
```
**結果**：無任何警告或錯誤輸出，完全通過（Exit Code 0）。

### 2. CLI 應用程式二進位編譯
```bash
go build -o /dev/null ./cmd/asura
```
**結果**：編譯成功，無任何語法或符號解析錯誤（Exit Code 0）。

### 3. 全專案測試套件執行
```bash
go test -count=1 ./...
```

**測試結果匯總表**：

| 套件路徑 | 執行耗時 | 狀態 | 測試重點 |
| :--- | :--- | :--- | :--- |
| `asura/cmd/asura` | 0.064s | PASS | CLI 命令介面、參數解析、記憶管理子命令 |
| `asura/docs/issues/issue-19/evidence` | 0.106s | PASS | Issue 驗證輔助腳本與證據驗證 |
| `asura/internal/auth` | 0.005s | PASS | 安全政策、工作區路徑白名單驗證 |
| `asura/internal/backend` | 0.006s | PASS | LLM 後端介面抽象 |
| `asura/internal/backend/codex` | 1.043s | PASS | Codex 後端整合 |
| `asura/internal/backend/opencode` | 25.204s | PASS | OpenCode 後端整合與流式回應 |
| `asura/internal/contextwin` | 0.009s | PASS | 上下文視窗滑動計算與 Token 控制 |
| `asura/internal/cron` | 0.114s | PASS | 排程與定時任務觸發 |
| `asura/internal/daemon` | 13.143s | PASS | Daemon 核心、IPC 處理常式、工作池 |
| `asura/internal/engine` | 0.999s | PASS | 任務執行引擎、記憶注入與密封保護 |
| `asura/internal/gitmem` | 3.330s | PASS | 安全 Git CLI、Range 解析、有界擷取、客觀事實、去重、AI 草稿 |
| `asura/internal/pipeline` | 0.005s | PASS | 任務流水線排程 |
| `asura/internal/recall` | 0.003s | PASS | Phase 3F 記憶檢索與關鍵字比對 |
| `asura/internal/storage` | 1.699s | PASS | SQLite 儲存層、Git 候選庫、原子交易、Checkpoint |
| `asura/internal/web` | 0.010s | PASS | Web Console 靜態資產與 API |
| `asura/tests/integration` | 41.688s | PASS | 端到端整合測試（邊界探測、IPC、Recall、密封性快照、真實 Git 流程） |

**總結**：16 個套件全數 PASS，總執行時間約 87 秒，無任何回歸錯誤。

### 4. 併發防護測試（Data Race Check）
```bash
go test -race ./internal/gitmem/... ./internal/storage/...
```

**輸出結果**：
```text
ok  	asura/internal/gitmem	2.595s
ok  	asura/internal/storage	15.561s
```
**結果**：無任何 Data Race 警報（0 race detected）。

---

## 四、資料完整性與孤立資料盤點

透過整合測試與單元測試之嚴格斷言，驗證系統在異常與邊界條件下無孤立資料產生：
1. **Partial Capture（有界截斷）**：
   - 截斷時 `truncated == true`，系統嚴格禁止更新 Checkpoint（H-TBD-3）。
   - 保留之候選事實帶有明確截斷標記，避免不完整歷史被誤判為全量同步。
2. **外部 Git 邊界違規**：
   - 若偵測到惡意 `diff.external`、`core.fsmonitor` 或跨越允許目錄之 `gitdir` 符號連結，立即在記憶體層級攔截並回傳錯誤，不寫入任何 SQLite 紀錄。
3. **交易復原（Transaction Rollback）**：
   - 核准交易在單一 SQLite 交易內完成（候選狀態更新、正式記憶建立、Checkpoint 推進），任一步驟失敗全數回滾，絕無「核准成功但正式記憶未建立」或「正式記憶建立但 Checkpoint 游標遺失」之孤立狀態。

---

## 五、結論

Issue #23（Phase 3H: Git-Aware Project Memory Capture）之所有核心功能、安全邊界、資料模型、生命週期與端到端工作流均已全數實作完畢並取得客觀實證。全專案測試無任何回歸，符合專案標準，可正式交付。
