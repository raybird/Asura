# 實作計畫與任務清單

建立日期：2026-09-14。拆解完成日期：2026-09-14。規格與首要風險以 [README](README.md) 為準。本檔為唯一任務清單，沿用 P1-T1～P3-T2 編號；子任務直接細化於本檔，不另建 decomposition 文件。

2026-09-14 使用者要求「好直接更新同一份計畫」，授權本次文件拆解。MEM-001～007 已逐題核准；本次未修改功能程式碼、建立測試或執行功能驗收。

## 已確認的介面現況

2026-09-14 靜態檢核基準為 `96a50c1`，功能程式碼仍承接 main `202f1b3`。

| 查核項目 | 現況證據 | 本次實作決定 |
|---|---|---|
| IPC 格式與大小 | `internal/daemon/daemon.go` 的 IPCRequest／IPCResponse、handleConn 使用 json.Decoder／Encoder，未設定訊息大小上限 | 沿用 JSON 串流；不虛構既有長度限制、不新增任意截斷或固定前 N 筆 |
| 失敗回傳 | IPCResponse.Error 為字串；Client 將非空 Error 轉成 error；Cron CLI 將錯誤寫 stderr 並退出 1 | 記憶命令沿用，成功退出 0；必要回應缺欄位亦不得顯示成功 |
| 連線生命週期 | NewClient 使用 net.Dial；現有方法的 context 參數未自動設定 deadline | 每個 CLI 命令獨立連線並 Close；不宣稱已有逾時保障、不順帶重構所有 Client |
| 專案授權 | Policy.AuthorizeWorkspace 解析路徑；CanonicalWorkspace 另行解析且失敗會退回字串 | 新記憶路徑須取得同一次解析且授權通過的 canonical scope，解析失敗直接拒絕；不能把回退字串當成功 |
| 授權更新 | Policy 由啟動設定建立，沒有動態改授權 API | 撤權／恢復測試重啟 Daemon 並改用不同 Policy，保留同一資料庫；不新增熱更新入口 |
| 儲存與測試 | SQLiteStore 已有 Task／Cron；integration 測試以臨時 DB、Unix socket 與受控 runner 啟動真實 Daemon | 沿用此方式；mock 只用於故障注入與 runner 計數，不替代真實授權／SQLite |

以上為靜態證據，不能當作新記憶功能或路徑競態已通過測試。實作若發現確需新增長度、分頁或逾時等外部限制，先記錄具體需求及規格影響，不以拆解推定核准。

## 設計與使用方式

既有 CLI 無記憶命令；本期新增以下接線，所有參數名稱為最小實作選擇：

```text
asura memory add --workspace <path> --content <text>
asura memory list --workspace <path>
asura memory get --workspace <path> <memory-id>
asura memory remove --workspace <path> <memory-id>
```

各命令沿用 `--socket`。CLI 解析必要參數並將 workspace 轉為絕對路徑；Daemon 自行驗證，不相信客戶端傳入的 scope 或來源。內容保持原文，不自動摘要；查詢回傳完整文字及已核准五個欄位。列出限定單一 scope，不提供全域列出或搜尋；不默默省略記憶。

處理順序：解析請求 → 檢查必要欄位 → 一次解析並授權 workspace → 固定 scope → SQLite 操作 → 成功或 Error 回應。輸入缺漏、未知 action、查無指定記憶或儲存錯誤均不得顯示成功。scope 與 ID 不匹配視為查無可操作記憶，錯誤不帶其他專案內容。

擬新增 `Memory`（ID、Workspace、Content、Source、CreatedAt）與以下具體介面；Source 由 Daemon 固定 manual，不接受客戶端指定。

```go
CreateMemory(ctx context.Context, memory Memory) error
ListMemories(ctx context.Context, workspace string) ([]Memory, error)
GetMemory(ctx context.Context, workspace, id string) (*Memory, error)
DeleteMemory(ctx context.Context, workspace, id string) error
```

SQL 的列出必須帶 workspace，取得／刪除必須同時匹配 workspace 與 ID；刪除檢查影響筆數。新增資料表採加法式初始化，不改既有 Task／Cron。IPC 使用 memory_add/list/get/remove 與必要具型別欄位；沿用 Client，不建通用 repository 或新的服務。

記憶操作不呼叫 Engine、WorkerPool 或 AI，也不寫入替代檔案。傳輸中斷無法證明伺服器是否已提交時，只回報錯誤，不保證交易未發生、不自動重送寫入；成功僅在收到明確成功回應後輸出。這是失敗不冒充成功的必要區分，不新增寫入去重功能。

## Phase 1：核准查核與隔離紅燈

### P1-T1 — 介面檢核與核准基線

- **狀態**：✅ 2026-09-14 完成靜態檢核與規劃；無功能驗證。
- **相依／對應**：無；MEM-001～007。
- **產出**：本檔「已確認的介面現況」、錯誤映射與上述最小 API。
- **完成證據**：讀取 daemon.go 的 handleConn／Client、auth/policy.go、cmd/asura/cron.go；確認 JSON 無明訂上限、Error 映射及授權解析行為。README 七項 Scenario 核准表與來源齊備。
- **實作前再查核**：對 README 所列各核准 commit 比對實際工作區的對應 Scenario，包含未提交內容；語意差異才需重新確認，不以文件格式差異抹除既有核准。

### P1-T2 — 先建立風險可被測出的證據

- **狀態**：✅ 2026-09-14 完成測試 fixture、外迴圈紅燈與單元行為紅燈建立；無功能變更。**相依**：P1-T1；**對應**：MEM-002、003、004、007。
- **P1-T2.a 產出**：已於 `tests/integration/memory_test.go` 建立真實 IPC 測試 fixture（A/B/C/別名/越界，固定多語文字）；`TestP1T2a_FixtureAndRunnerBaseline` 驗證 runner 計數器由 0 變 1（證據：[fixture-baseline-green.log](evidence/phase-1/fixture-baseline-green.log)）。
- **P1-T2.b 產出**：已建立外迴圈測試（`TestMEM002`, `TestMEM003`, `TestMEM004`, `TestMEM007` 及預留骨架）；執行均取得清晰的「功能尚缺」紅燈（`unknown action: memory_add`），並防範假綠燈（證據：[outer-memory-red.log](evidence/phase-1/outer-memory-red.log)）。
- **P1-T2.c 產出**：在 `internal/auth/policy.go` 建立 `AuthorizeCanonicalWorkspace` 空殼，於 `internal/auth/policy_test.go` 取得 5 個 subtest 單元行為紅燈（證據：[inner-auth-red.log](evidence/phase-1/inner-auth-red.log)）；在 `internal/daemon/memory_test.go` 取得缺少 action 處理器之行為紅燈（證據：[inner-daemon-red.log](evidence/phase-1/inner-daemon-red.log)）；既有授權案例全綠保留刻畫（證據：[auth-regression-green.log](evidence/phase-1/auth-regression-green.log)）。
- **完成判準**：固定字面期望與實際 assertion 失敗原因清晰可讀；區分缺少操作與隔離缺口；既有案例全綠。
- **證據位置**：[evidence/phase-1/](evidence/phase-1/)。安全正反向全綠由 P2-T2 負責。

## Phase 2：儲存與服務端授權

### P2-T1 — scope 儲存、升級與刪除

- **狀態**：✅ 2026-09-14 完成 SQLite schema 加法式擴充、專案限定 CRUD 與無損升級驗證。**相依**：P1-T2；**對應**：MEM-001、002、005。
- **P2-T1.a 產出**：已於 `internal/storage/sqlite_memory_test.go` 建立 `TestSQLiteStore_MemoryCRUD`，先取得尚未實作之行為紅燈（證據：[inner-memory-red.log](evidence/phase-2/inner-memory-red.log)），實作後驗證固定內容、manual 來源、scope+ID 查詢隔離、指定刪除另一筆不變與重啟持久化全綠（證據：[inner-memory-green.log](evidence/phase-2/inner-memory-green.log)）。
- **P2-T1.b 產出**：在 `internal/storage/sqlite.go` 擴充加法式 memories DDL 與 workspace 索引；於 `internal/storage/sqlite_memory.go` 實作 `CreateMemory`、`ListMemories`、`GetMemory`、`DeleteMemory`，無全域讀取捷徑，刪除嚴格檢查影響筆數。
- **P2-T1.c 產出**：建立 `TestSQLiteStore_MemorySchemaUpgrade`，以明確舊 schema 建立歷史 Task/Cron 資料庫；升級後獨立 SQL 驗證既有資料 100% 無損且記憶功能正常，模組回歸含 race 全綠（證據：[storage-regression-green.log](evidence/phase-2/storage-regression-green.log)）。
- **完成判準**：單元全綠，異 scope 讀取／刪除失敗且另一專案不受影響；重啟保留未刪資料、已刪 ID 不恢復；重構確認為 no-op（證據：[refactor-noop.log](evidence/phase-2/refactor-noop.log)）。此階段綠燈僅代表儲存層，不代表 IPC 授權已完成。
- **證據位置**：[evidence/phase-2/](evidence/phase-2/)。

### P2-T2 — 同次解析授權與 IPC 管理

- **狀態**：✅ 2026-09-14 完成同次解析授權 helper、Daemon IPC 記憶操作管理與安全閘門外迴圈全綠。**相依**：P2-T1；**對應**：MEM-002、003、004、007，補 MEM-006 服務端錯誤。
- **P2-T2.a 產出**：在 `internal/auth/policy.go` 實作 `AuthorizeCanonicalWorkspace`，同次完成授權校驗與標準化路徑解析；單元測試全綠（證據：[auth-canonical-green.log](evidence/phase-2/auth-canonical-green.log)）。Daemon 每個記憶操作固定該 scope 後傳入 store，不再解析一次。
- **P2-T2.b 產出**：在 `internal/daemon/daemon.go` 實作 `memory_add`、`memory_list`、`memory_get`、`memory_remove` IPC 處理器與 `generateMemoryID`；在 `internal/daemon/memory_test.go` 驗證漏參數、未授權與越界拒絕、及正向 CRUD（證據：[daemon-ipc-green.log](evidence/phase-2/daemon-ipc-green.log)）。記憶操作零新增 runner 呼叫，不啟動模型後端。
- **P2-T2.c 產出**：在 `tests/integration/memory_test.go` 驗證 MEM-001～007 外迴圈安全閘門全數轉綠（含同 root 專案隔離、別名可查、子目錄不繼承、越界拒絕、以及「授權 A/B → 撤除 A → 恢復 A」完整撤權保留資料與重新授權恢復流程，證據：[outer-memory-green.log](evidence/phase-2/outer-memory-green.log)）；Phase 2 模組回歸含 race 全綠（證據：[phase2-regression-green.log](evidence/phase-2/phase2-regression-green.log)）。
- **完成判準**：A/B 同 root、別名、子目錄、越界及撤權案例全綠；拒絕後資料無變更、輸出無跨 scope 內容，管理零新增 runner 呼叫；重構確認為 no-op（證據：[p2t2-refactor-noop.log](evidence/phase-2/p2t2-refactor-noop.log)）。
- **證據位置**：[evidence/phase-2/](evidence/phase-2/)。已取得安全閘門全綠，准予推進至 Phase 3。

## Phase 3：CLI、整合與交付

### P3-T1 — CLI 操作與失敗路徑

- **狀態**：✅ 2026-09-15 完成 CLI memory 命令分派、真實在端 binary 驗證與失敗路徑安全防護。**相依**：P2-T2；**對應**：MEM-001、005、006。
- **P3-T1.a 產出**：已於 `cmd/asura/run_test.go` 建立單元紅燈（證據：[inner-cli-red.log](evidence/phase-3/inner-cli-red.log)），並在 `tests/integration/memory_test.go` 建立真實編譯 binary 外迴圈紅燈（證據：[outer-cli-red.log](evidence/phase-3/outer-cli-red.log)），涵蓋 add/list/get/remove、五欄位完整多語文字。
- **P3-T1.b 產出**：在 `cmd/asura/memory.go` 實作 `handleMemory`、旗標解析與順序相容 helper `reorderArgs`，在 `cmd/asura/run.go` 註冊子命令與 usage；每個操作直通 Daemon IPC，不直接開庫，成功回應前不輸出成功訊息；單元測試全綠（證據：[inner-cli-green.log](evidence/phase-3/inner-cli-green.log)）。
- **P3-T1.c 產出**：在 `TestMEM001`、`TestMEM005`、`TestMEM006` 驗證真實 CLI binary 端到端寫入重啟持久化、指定刪除隔離、健康對照成功、Daemon 不可用非零退出、輸出 stderr 且無成功字樣、無替代儲存寫入、零 AI 模型呼叫以及任務提交不自動注入記憶；外迴圈全綠（證據：[outer-cli-green.log](evidence/phase-3/outer-cli-green.log)、[outer-all-memory-green.log](evidence/phase-3/outer-all-memory-green.log)）；模組回歸含 race 全綠（證據：[p3t1-regression-green.log](evidence/phase-3/p3t1-regression-green.log)）。
- **完成判準**：正式 CLI 寫入／查詢／刪除／重啟可重現；全部五欄位正確且繁中多語內容不截斷；失敗不冒充成功、不改走替代儲存、零 runner 呼叫；重構確認為 no-op（證據：[p3t1-refactor-noop.log](evidence/phase-3/p3t1-refactor-noop.log)）。
- **證據位置**：[evidence/phase-3/](evidence/phase-3/)。已取得 CLI 外迴圈全綠，准予推進至 P3-T2。

### P3-T2 — 完整回歸、文件與審查交付

- **狀態**：📝 待實作；**相依**：P3-T1；**對應**：MEM-001～007。
- **P3-T2.a 產出**：七項外迴圈與低層回歸全綠；受控 runner 驗證正常 ask 可啟動且收到原 prompt，記憶命令不新增呼叫。既有 CLI／Web／Cron 測試仍通過。
- **P3-T2.b 產出**：根 README 新增四命令、五欄位、精確 scope、撤權與重啟政策、失敗回報；在 docs/agents/project.md 登記使用者功能 → 根 README。只更新本功能觸發的常青文件。
- **P3-T2.c 產出**：固定 BASE／HEAD、七項 Proof of Test 與規格基準，交由非實作者獨立 review，保存判定；依 [review-evidence](../../agents/review-evidence.md) 辦理。自我重讀不算獨立審查，本次拆解不宣稱 review PASS。
- **完成判準**：完整 race 回歸、文件查核及有效獨立 review 無 MUST FIX；所有實作與證據已提交。不自行把此狀態寫成 PR 已合併。
- **證據位置**：`evidence/phase-3/` 與實際 review artifact；尚未建立。

## Scenario 與測試層級對應

| Scenario | 主責 Task | 外迴圈（預定 TestMEM001～007） | 內迴圈 |
|---|---|---|---|
| MEM-001 | P2-T1、P3-T1 | CLI 寫入、重啟、依 ID 取得完整五欄位 | SQLite 持久化、Client 回應、CLI 輸出 |
| MEM-002 | P1-T2、P2-T2 | 同 root A/B、錯配 scope/ID、B 資料不變 | SQL scope 條件與授權 |
| MEM-003 | P1-T2、P2-T2 | 未授權、撤權、越界四操作拒絕 | Policy 解析與 handler 守門 |
| MEM-004 | P1-T2、P2-T2 | 別名可取、子目錄不繼承 | 單次解析及固定 scope |
| MEM-005 | P2-T1、P3-T1 | 刪除後重啟、另一筆保留 | 限定 scope 刪除與影響筆數 |
| MEM-006 | P2-T2、P3-T1、P3-T2 | 非零退出、無替代儲存、零 AI 呼叫、原 prompt | Client／CLI 錯誤映射與故障注入 |
| MEM-007 | P1-T2、P2-T2 | 授權 → 撤權保留 → 重新授權恢復 | Policy 與存取控制，撤權不刪資料 |

P3-T2 彙整全部 Scenario。外迴圈和內迴圈具有傳輸、授權及 SQL 的不同失敗面，保留雙迴圈；不另建第二份任務進度表。

## 執行命令與證據規則

預定每個外迴圈以 `TestMEM001`～`TestMEM007` 為頂層名稱，細例用 subtest。測試建立後才執行以下命令，確認七個頂層案例實際出現、零 FAIL／SKIP，而非僅看退出碼。

```bash
go test -count=1 -v ./tests/integration -run '^TestMEM00[1-7]$'
go test -count=1 -race ./internal/storage ./internal/auth ./internal/daemon ./cmd/asura
go test -count=1 -race ./...
```

每個 Task 的實際證據回填在原段落，包含系統日期、Scenario、工作區／commit、完整命令、退出碼與固定期望／實際值。預定 evidence 路徑不是已存在的證據。

保留外紅、內紅、最小實作綠燈及重構後綠燈；沒有必要重構時記 no-op 並重用同版綠燈。編譯、依賴、環境失敗不是行為紅燈。SQL 獨立對照用固定字面資料，不共用被測查詢作為唯一真相；未授權回應不能因正向操作也壞掉而假綠。

## 2026-09-14 本次拆解檢查

本次為純文件，功能紅燈／Go 測試不適用。檢查項目為六個既有 Task 編號保留、相依無循環、七個 Scenario 全覆蓋、相對連結及圍欄有效、README 已核准 Gherkin 未改動與 git diff --check。2026-09-14 實際結果：Python 靜態檢查確認六個 Task 保留、相依無循環、七項已核准 Scenario 與映射集合相等、連結與圍欄有效、只有本計畫變更且 README 與 HEAD 相同；退出碼 0。`git diff --check` 退出碼 0。未建立任何功能測試或 review artifact。
