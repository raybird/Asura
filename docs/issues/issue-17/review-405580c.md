# 分析與規劃審查報告

- 審查日期：2026-09-21
- 範圍：上一個 commit 的局部文件 diff；僅審查分析與規劃，不代表程式驗收或完整 PR 通過。
- Reviewed BASE SHA：b153c012b5fd67f12cdb34f29610ef64edfbbaef
- Reviewed HEAD SHA：405580c3e623ad9ab1224dd511d7ae2962f9c86b
- Reviewed patch-id：5d900d9cf754462845cb7dd536c4a2a64bf06967
- 獨立 reviewer：`/root/independent_review`（宿主獨立 subagent，未參與原提交）；主代理核對並整理報告。
- Review artifact：`docs/issues/issue-17/review-405580c.md`

## 問題與風險

### MUST FIX — P2：TokenUsage 無法表示個別欄位缺失

位置：`implementation-plan.md:21–26`；相同設計見 `technical-analysis.md:85–105`。

所有用量欄位都是 `int64` 並使用 `omitempty`。只回報 input/output、缺少 cached/total 的資料，以及明確回報 cached/total 為 0 的資料，會得到相同欄位值與 JSON。整筆 `Usage` 指標及單一 `UsageSource` 無法保存個別欄位的 presence；這與 CTXWIN-002 的「缺失為 unknown、不以 0 假冒」及 Task 1.2 的 nil 判準矛盾。

建議：規劃可表示欄位是否存在的型別（例如 nullable 數值），並定義衍生 total 的來源；驗證缺失、明確 0、部分回報經 JSON／SQLite 往返後仍可區分。

### MUST FIX — P2：缺少實際 prompt 與 usage 的跨層傳遞計畫

位置：`implementation-plan.md:137–150`；README 涉及檔案亦未列 `stage_*.go` 與雙後端 `adapter.go`。

現有 `stage_oracle.go`、`stage_striker.go`、`stage_critic.go` 在 Execute 內組裝 prompt 並呼叫 Backend.Run；Orchestrator 只取得 plan、文字或 review，無法取得完整 ExecutionResult。雙後端的 `adapter.go` 才建立 ExecutionResult，僅改 parser 並不會把 usage 傳回上層。因此新增獨立 stage_estimator 與 Trace 欄位，仍缺少「實際送出的 prompt 與被估算內容相同」及「usage 確實到達 Trace」的必要接線；複製一套組裝邏輯還可能產生偏差。

建議：補上共享 prompt 組裝／准入位置，以及 adapter → stage → trace 的資料流、責任 Task 和必要檔案。驗證應攔取 Backend.Run 的實際 request 比對估算輸入，並由 fake CLI 原始事件一路驗證 usage 持久化；涵蓋有 usage 但執行或輸出解析失敗的情況。

### MUST FIX — P2：模型與預算設定沒有可執行的來源契約

位置：`implementation-plan.md:95`、`:129–142`；`technical-analysis.md:58–60`。

計畫使用 model、budgetConfig，並承諾呼叫端／環境變數覆蓋及靜態模型目錄，但未安排設定入口、解析、驗證或注入的 Task。現有 SubmitRequest／ExecutionContext 沒有 model 或 budget 欄位，Backend 介面也沒有模型解析能力；只憑 BackendID 無法辨識實際模型，尤其不同角色的 CLI 設定來源可能不同。依現有計畫，正式入口可能一直以 unknown bypass，或以未對應實際模型的上限判斷。

建議：選定首版最小設定入口，定義其優先序、有效值、作用範圍與決策保存方式；明列模型無法可靠辨識時的 unknown 行為，不強猜模型。補從正式組裝入口啟用明確低上限並阻擋 backend 呼叫的整合驗證。TBD-2 的預設政策仍待核准，這項發現不要求自行決定預設值。

### SHOULD FIX：分開 live 能力證據與 hermetic 驗收

位置：`implementation-plan.md:161–163`。

Task 6.1 宣稱全數 CTXWIN 劇本可於 hermetic/mock 環境驗收，但 CTXWIN-001 及 Task 1.1 要求固定版本的真實能力探測。應明列 hermetic 測試重用哪份固定版本 probe artifact，以及未執行 live probe 時不能宣稱能力已確認。現有文件沒有宣稱已執行測試，本項不是不實回報。

## 已查核維度

| 驗收 | 規劃覆蓋結論 |
|---|---|
| CTXWIN-001 | Task 1.1 採 probe first 合理；真實 CLI 能力與技術分析中的欄位敘述仍需探測確認，Task 6.1 判準需區分證據來源。 |
| CTXWIN-002 | 有來源標籤與估算策略，但個別用量缺失表示及模型來源有上述缺口。 |
| CTXWIN-003 | 已辨識動態 OraclePlan／StrikerOutput 膨脹；實際 prompt 共用與跨層接線不足。 |
| CTXWIN-004 | 已要求被拒絕 Stage 零呼叫、保留前序結果、不切換後端；設定入口及實際准入位置仍需補齊。 |
| CTXWIN-005 | 明列 sealed context 與 workspace 隔離保護，方向合理；實作時仍須驗證，不以文件視為已通過。 |
| CTXWIN-006 | Trace JSON 擴充與歷史缺欄位相容策略合理；usage 傳遞與 nullable 表示需修正。 |
| CTXWIN-007 | Task 5.2 已規劃重送不重跑；補設定契約時須維持既有決策保存與重送語意。 |
| CTXWIN-008 | 有既有回歸套件策略；尚未執行，亦未宣稱已執行。 |

重要失敗面已核對：部分 usage 缺失／真實零、動態 prompt 與送出值偏差、後端錯誤仍有 usage、未知模型與顯式限制、後續 Stage 被拒絕、歷史 Trace 缺欄位、同 ID 重送。前三項 MUST FIX 是規劃缺口，其餘已有規劃方向，須由實作證據完成驗收。

需求與核准：README 全數 Scenario 維持待核准，TBD-1／TBD-2 未解決；此狀態不構成初稿缺失，也不能當成實作授權。架構與安全方向（密封上下文、角色隔離、零自動 failover）合理；本提交只有文件，沒有引入執行期權限或依賴變更。

## 驗證證據與限制

- 2026-09-21：以 `git show 405580c3e623ad9ab1224dd511d7ae2962f9c86b -- docs/issues/issue-17` 查核四份新增文件完整 diff；以 `git diff HEAD^ HEAD | git patch-id --stable` 取得上述 patch-id（審查時 HEAD 為上述固定版本）。
- 2026-09-21：人工對照 `internal/backend/backend.go`、雙後端 parser／adapter、`internal/pipeline/types.go`／`orchestrator.go`／三個 stage 實作、`internal/engine/engine.go` 的型別與呼叫鏈，確認上述缺口。
- 2026-09-21：`git diff --check HEAD^ HEAD` 回報 README metadata 三行尾端空白；其為 Markdown 換行用途，不列為實質缺失。
- 本次為純文件審查，未跑 Go 回歸套件或 live CLI probe，不宣稱程式或外部 CLI 能力驗收通過；未查核遠端 issue 原始討論，核准狀態以本地文件為準。
- 工作區既有未追蹤 `asura` 不屬此次固定 diff，未修改。只新增本報告，未修改規格、程式或建立提交。

## 流程判定

**RETURN TO execute-task**：有三項 MUST FIX。此處表示退回修訂分析與實作計畫，不授權直接開始功能實作。報告已持久化；不能作為程式驗收 PASS。
