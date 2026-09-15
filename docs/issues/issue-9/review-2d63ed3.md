# 獨立程式碼審查報告 (Issue #9: 專案級基礎記憶)

- **審查日期**：2026-09-15
- **審查員**：Independent Reviewer (獨立審查員)
- **Reviewed BASE SHA**：`202f1b30e0c56aa087fd1c8f877c09bec1475019`
- **Reviewed HEAD SHA**：`2d63ed36ce51c0e39f0fa8932dcf573d387ff109`
- **Reviewed patch-id**：`256ae3160fa9647c72cfa31317968c8109624c83`
- **審查判定**：`PASS`

---

## 一、變更摘要與審查範圍

本審查針對 Issue #9（專案級基礎記憶）之實作成果進行獨立程式碼審查。本期核心目標為提供專案隔離、可追溯之記憶寫入、查詢與刪除，並具備同次解析授權、目錄邊界防護、撤權保留資料與重啟持久化。

審查範圍比對：`git diff 202f1b30e0c56aa087fd1c8f877c09bec1475019..2d63ed36ce51c0e39f0fa8932dcf573d387ff109`
主要變更涵蓋：
1. **CLI 記憶管理層**：`cmd/asura/memory.go` 實作 `memory` 子命令（`add`, `list`, `get`, `remove`），在 `cmd/asura/run.go` 與 `cmd/asura/run_test.go` 註冊與測試命令分派，各操作均經由 Unix socket 呼叫 Daemon IPC。
2. **授權與路徑正規化**：`internal/auth/policy.go` 新增 `AuthorizeCanonicalWorkspace`，同次完成路徑解析與授權校驗，杜絕 TOCTOU 時間差與符號連結越界；並於 `internal/auth/policy_test.go` 提供完整單元測試。
3. **Daemon IPC 處理層**：`internal/daemon/daemon.go` 新增 `memory_add`, `memory_list`, `memory_get`, `memory_remove` IPC 處理分支，由伺服端強制使用 canonical scope 與固定 `manual` 來源；於 `internal/daemon/memory_test.go` 驗證漏參數與越界拒絕。
4. **SQLite 儲存層**：`internal/storage/sqlite.go` 擴充加法式 `memories` 資料表 DDL 與索引；`internal/storage/sqlite_memory.go` 實作嚴格限定 `workspace` 與 `id` 之 CRUD；`internal/storage/sqlite_memory_test.go` 驗證跨專案資料列隔離、指定刪除與無損升級。
5. **外迴圈整合與驗收測試**：`tests/integration/memory_test.go` 建立真實 IPC 測試 fixture 與真實 CLI binary 端到端測試（`TestMEM001`～`TestMEM007`）。
6. **文件登記**：根目錄 `README.md` 新增 memory 使用說明與政策描述；`docs/agents/project.md` 登記常青文件更新責任。

---

## 二、規格修訂與需求符合度查核

### 1. 規格修訂查核
比對核准基準 `7eb9655` 至當前 HEAD `2d63ed3` 之 `docs/issues/issue-9/README.md`：
- Gherkin 原文無擅自篡改，條件、動作、預期結果與 Given/When/Then 完全一致。
- 唯一修訂為在全部核准後回填核准 commit 與更新任務進度狀態，符合 [acceptance.md](../../agents/acceptance.md) 規範。

### 2. 7 項核准 Gherkin 劇本符合度

| 劇本編號 | 劇本名稱 | 核心行為與邊界查核 | 審查結果 |
|---|---|---|---|
| **MEM-001** | 可追溯寫入與持久化 | CLI binary 寫入純文字記憶，Daemon 關閉重啟後，依回傳 ID 成功取得相同內容、正規化專案路徑、`manual` 來源與建立時間；繁中與多語文字完整未被截斷。 | **符合** |
| **MEM-002** | 授權 root 內仍隔離不同專案 | 同一授權 root 下之專案 A 與 B，在 A scope 下列出不含 B 記憶，以 B 之 ID 查詢或刪除皆遭拒絕（回傳 `memory not found`），B 記憶之內容與筆數保持不變。 | **符合** |
| **MEM-003** | 撤權及越界拒絕 | 輸入指向授權 root 外部之符號連結（`escapedLink`）或完全未授權之外部目錄，寫入、列出、查詢、刪除四項操作全數遭伺服端拒絕（回傳 `unauthorized workspace`），不洩漏內容亦不變更資料。 | **符合** |
| **MEM-004** | 精確 workspace 歸屬 | 專案 A 之符號連結別名經同次解析命中 A 之記憶集合；而子目錄 C 解析為獨立 scope，查詢 A 之記憶遭拒絕且清單為空，子目錄不繼承父目錄記憶。 | **符合** |
| **MEM-005** | 指定刪除與恢復 | 專案 A 保存兩筆記憶，透過 CLI 刪除其中一筆並重啟 Daemon，已刪除 ID 查無資料（exit code 1），未刪除之另一筆資料內容完好。 | **符合** |
| **MEM-006** | 失敗不冒充成功且管理不啟動模型 | Daemon 不可用時 CLI 回傳非零退出碼（code 1），錯誤輸出至 stderr，stdout 不輸出任何成功字樣；不寫入本地臨時或替代檔案；記憶管理操作 direct-to-storage，AI 模型呼叫保持為 0；既有任務提交之 prompt 原樣傳遞不被附加記憶。 | **符合** |
| **MEM-007** | 撤權保留資料並於重新授權後恢復操作 | 專案 A 撤除授權後，讀、寫、查、刪全數拒絕且原記憶完整保留；重新將 A 納入授權後，原記憶立即可查並恢復正常寫入與刪除。 | **符合** |

---

## 三、安全與架構查核

1. **同次正規化授權 (AuthorizeCanonicalWorkspace)**：
   - 實作於 `internal/auth/policy.go`，將 `resolveWorkspace`（解析符號連結與絕對路徑）、`filepath.Clean` 與 `allowedRoots` 範圍校驗封裝於單一函式內。
   - Daemon 在接收記憶 IPC 請求時，首要呼叫該方法取得唯一的 `canonicalScope`；若未授權或解析失敗立即回傳錯誤，完全杜絕符號連結置換的時間差競爭（TOCTOU）。
2. **專案隔離與儲存約束**：
   - 儲存層 `internal/storage/sqlite_memory.go` 所有查詢與刪除皆採用雙重條件：`WHERE workspace = ? AND id = ?`，無任何僅憑 ID 存取之全域 API。
   - 刪除操作嚴格檢查 `RowsAffected`，若為 0 則回傳 `ErrMemoryNotFound`，杜絕跨專案探測與誤刪。
3. **撤權與重新授權政策**：
   - 撤權時既有 SQLite 資料保留不作刪除；所有針對該專案之請求在 Daemon 授權閘門被即刻阻斷。
   - 重新授權後，由相同 `canonicalScope` 直接還原存取權，資料完整無損。
4. **失敗不冒充與儲存隔離**：
   - CLI 遇到連線異常或伺服端錯誤時，明確寫入 stderr 並返回非零退出碼，絕無 `stdout` 偽造已保存或已刪除情事。
   - 系統無任何替代儲存（如寫入工作區 `.memory.json` 等行為），純粹以 SQLite 作為單一持久化來源。
5. **AI 後端隔離**：
   - 記憶管理完全不經過 `internal/engine`、`WorkerPool` 或模型 runner；整合測試以計數器實測確認模型調用為 0。既有 ask 任務提交未注入記憶，符合本期純管理設計。
6. **五欄位完整性與多語支援**：
   - 記憶資料結構包含 `ID`、`Workspace`、`Content`、`Source`、`CreatedAt`。測試實測中文、特殊符號與英文混合長字串，無截斷或編碼破損問題。

---

## 四、Proof of Test (測試證據查核)

核對 `docs/issues/issue-9/evidence/` 下 22 份紅綠燈日誌，並由獨立審查員於目前工作區獨立執行 Go 測試，全數獲得實證通過：

| 驗收編號 | 可觀察結果摘要 | 規格依據 (commit:path) | 測試證據連結與指令 | 驗證結果 |
|---|---|---|---|---|
| **MEM-001** | CLI binary 寫入純文字記憶，重啟 Daemon 後依 ID 取得相同內容、專案、manual 來源與時間 | `7eb9655:docs/issues/issue-9/README.md#L42-L46` | [outer-cli-green.log](evidence/phase-3/outer-cli-green.log)<br>`go test -count=1 -v ./tests/integration -run '^TestMEM001$'` | **PASS** (4.29s) |
| **MEM-002** | 同 root 下 A/B 專案隔離，清單不含 B 記憶且以 B 之 ID 查詢/刪除皆被拒絕，B 內容不變 | `7eb9655:docs/issues/issue-9/README.md#L48-L54` | [outer-memory-green.log](evidence/phase-2/outer-memory-green.log)<br>`go test -count=1 -v ./tests/integration -run '^TestMEM002$'` | **PASS** (0.01s) |
| **MEM-003** | 未授權、已撤權或越界符號連結請求寫入、列出、查詢或刪除記憶全數拒絕，無資料洩漏或異動 | `7eb9655:docs/issues/issue-9/README.md#L56-L60` | [outer-memory-green.log](evidence/phase-2/outer-memory-green.log)<br>`go test -count=1 -v ./tests/integration -run '^TestMEM003$'` | **PASS** (0.02s) |
| **MEM-004** | 有效別名解析至同一專案並可查得記憶；子目錄 C 不繼承父目錄記憶 | `7eb9655:docs/issues/issue-9/README.md#L62-L66` | [outer-memory-green.log](evidence/phase-2/outer-memory-green.log)<br>`go test -count=1 -v ./tests/integration -run '^TestMEM004$'` | **PASS** (0.01s) |
| **MEM-005** | 指定刪除其中一筆記憶並重啟 Daemon，已刪 ID 查無資料且另一筆內容完好 | `7eb9655:docs/issues/issue-9/README.md#L68-L72` | [outer-cli-green.log](evidence/phase-3/outer-cli-green.log)<br>`go test -count=1 -v ./tests/integration -run '^TestMEM005$'` | **PASS** (3.97s) |
| **MEM-006** | Daemon 不可用時非零退出且 stderr 顯誤、stdout 無冒充成功、無替代儲存、零 AI 啟動、原 prompt 不變 | `7eb9655:docs/issues/issue-9/README.md#L74-L81` | [outer-cli-green.log](evidence/phase-3/outer-cli-green.log)<br>`go test -count=1 -v ./tests/integration -run '^TestMEM006$'` | **PASS** (2.15s) |
| **MEM-007** | 撤權期間所有操作拒絕且資料保留，重新授權後原記憶立即可查且可恢復寫入與刪除 | `7eb9655:docs/issues/issue-9/README.md#L83-L89` | [outer-memory-green.log](evidence/phase-2/outer-memory-green.log)<br>`go test -count=1 -v ./tests/integration -run '^TestMEM007$'` | **PASS** (1.67s) |

### 全專案回歸與競態檢測
- 獨立執行：`go test -count=1 -race ./...`
- 結果：所有套件全數通過，無任何失敗，無任何 Data Race 警報（PASS，總耗時約 110s）。
  - `asura/cmd/asura`：PASS
  - `asura/internal/auth`：PASS
  - `asura/internal/backend/opencode`：PASS
  - `asura/internal/cron`：PASS
  - `asura/internal/daemon`：PASS
  - `asura/internal/engine`：PASS
  - `asura/internal/storage`：PASS
  - `asura/internal/web`：PASS
  - `asura/tests/integration`：PASS

---

## 五、問題與風險評估

### MUST FIX (阻塞合併問題)
- **無**。

### SHOULD FIX (次要改進建議，不阻塞本次合併)
1. **Memory ID 產生格式在極高頻寫入時的碰撞防護**：
   - 目前 `generateMemoryID()` 使用 `fmt.Sprintf("mem-%d-%s", time.Now().Unix(), hex.EncodeToString(b))`（6 bytes 隨機數）。在一般人工 CLI 管理情境下完全足夠，但在日後若支援高頻並發或批次匯入時，可評估升級為微秒戳記或標準 UUIDv4 以徹底消除同秒衝突隱患。

### NICE TO HAVE (未來擴充建議)
1. **分頁查詢支援**：目前 `memory list` 回傳專案全部記憶，若單一專案積累成千上萬筆記憶時，可考慮在未來版本擴充 `--limit` 與 `--offset` 參數。
2. **多行輸入模式**：目前 `memory add` 僅支援 `--content <text>` 命令列旗標，日後若有長篇文字或結構化筆記，可評估支援從標準輸入（stdin）或檔案（`--file`）載入內容。

---

## 六、最終審查結論

- **判定**：`PASS`
- **結論說明**：
  Issue #9（專案級基礎記憶）之實作完全符合已核准之 7 項 Gherkin 驗收劇本（MEM-001 ~ MEM-007），未篡改規格，具備完整紅綠重構證據。同次正規化授權防禦到位，專案資料列隔離嚴格，撤權與重啟持久化邏輯正確，失敗路徑與零 AI 調用均具備實體驗證。全專案 Race 回歸測試通過，文件登記完備。無任何 MUST FIX 項目，本審查給予 `PASS`，准予進行後續合併作業。
