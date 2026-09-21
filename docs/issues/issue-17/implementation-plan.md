# Issue 17 - Phase 3E：Context Window Management Foundation 實作計畫

## 設計方案

### 1. 資料結構與契約設計

#### (1) `internal/backend` Token Usage 與 Provenance
```go
package backend

// DataProvenance 標示數據來源與可信度
type DataProvenance string

const (
	ProvenanceExact     DataProvenance = "exact"     // 後端進程回報之實測值
	ProvenanceEstimated DataProvenance = "estimated" // 啟發式安全估算值
	ProvenanceUnknown   DataProvenance = "unknown"   // 未知或未回報
)

// TokenUsage 代表標準化之 Token 用量觀測
type TokenUsage struct {
	InputTokens     *int64 `json:"input_tokens,omitempty"`
	OutputTokens    *int64 `json:"output_tokens,omitempty"`
	TotalTokens     *int64 `json:"total_tokens,omitempty"`
	CachedTokens    *int64 `json:"cached_tokens,omitempty"`
	ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
}

// ExecutionResult 擴充 TokenUsage 與資料來源
// 保持既有欄位向前相容
```

每個 nil 欄位表示 unknown，非 nil 的 0 表示後端明確回報零；`UsageSource=exact` 僅描述非 nil 欄位。全數缺失時 `Usage=nil`、來源為 unknown。首版不以 input + output 補造 total；只有依版本 probe 確認可加總的原生事件才聚合，累計事件不重複加總，部分回合缺失的欄位維持 unknown。

#### (2) `internal/contextwin` 核心介面與型別
```go
package contextwin

// LimitSource 標示模型 Context 上限之來源
type LimitSource string

const (
	LimitSourceConfig  LimitSource = "config"  // 顯式配置覆蓋
	LimitSourceCatalog LimitSource = "catalog" // 靜態模型目錄
	LimitSourceUnknown LimitSource = "unknown" // 未知上限
)

// ModelWindowInfo 描述模型窗口與限制
type ModelWindowInfo struct {
	Model       string      `json:"model"`
	MaxTokens   int64       `json:"max_tokens"`
	LimitSource LimitSource `json:"limit_source"`
}

// StageEstimate 描述單一 Stage 在執行前估算之 Context 需求
type StageEstimate struct {
	Stage             string                 `json:"stage"`
	EstimatedPromptTokens int64              `json:"estimated_prompt_tokens"`
	ReservedOutputTokens  int64              `json:"reserved_output_tokens"`
	TotalEstimatedTokens  int64              `json:"total_estimated_tokens"`
	Provenance        backend.DataProvenance `json:"provenance"`
	EstimationMethod  string                 `json:"estimation_method"`
}

// AdmissionVerdict 准入檢查結論
type AdmissionVerdict string

const (
	VerdictAdmitted AdmissionVerdict = "admitted"
	VerdictRejected AdmissionVerdict = "rejected"
	VerdictWarning  AdmissionVerdict = "warning"
	VerdictBypassed AdmissionVerdict = "bypassed" // 未配置限制或處於未知模式
)

// StageAdmission 記錄執行前之准入審計結果
type StageAdmission struct {
	Stage       string           `json:"stage"`
	Verdict     AdmissionVerdict `json:"verdict"`
	Policy      string           `json:"policy"`
	Model       string           `json:"model,omitempty"`
	MaxTokens   int64            `json:"max_tokens,omitempty"`
	LimitSource LimitSource      `json:"limit_source"`
	Estimate    StageEstimate    `json:"estimate"`
	Reason      string           `json:"reason,omitempty"`
}
```

### 2. 設定與模型來源契約（2026-09-21 修訂提案，待核准）

- 首版由執行程序啟動時讀取 `ASURA_CONTEXT_LIMIT`（正整數 token 數）及 `ASURA_CONTEXT_POLICY`（`observe` 或 `reject`），經 `contextwin.LoadConfig` 驗證後，以不可變設定注入 Orchestrator。空值表示未配置；非整數、非正值、溢位或未知 policy 在啟動時報錯，不靜默降級。未設定 policy 的預設值及輸出保留比例仍由 TBD-2 核准後定案。
- `cmd/asura/run.go` 的本機路徑與 `cmd/asura/daemon.go` → `daemon.Config` → `internal/daemon/daemon.go` 均須串接；送到 daemon 的任務使用 daemon 的啟動設定，首版不加入逐任務覆蓋或新的 Request Metadata。
- 顯式 limit 優先於 catalog，對該程序所有 stage 生效，代表管理者設定的預算上限，不宣稱是模型原生容量。Admission 保存 policy、limit、來源與實際輸出保留值；相同 Request ID 重送讀取既有決策，不重新套用設定。
- 首版不猜測 CLI 預設模型，也不另外重建 CLI 設定解析器。Task 1.1 逐 backend／role 確認是否能在呼叫前可靠識別實際模型；若有證據才由 adapter 的請求準備路徑提供識別，否則 model 為 unknown。未知模型仍套用顯式 limit；沒有顯式 limit 且無可靠 catalog 命中時才 bypass。執行後才取得的模型資訊不能回填成執行前已知。
- catalog 僅接受已驗證的完整模型識別及有來源的上限，不以 BackendID 或模糊名稱推測。`observe` 超量產生 warning 並繼續，`reject` 超量才拒絕；等於上限可通過。未知上限的 bypass 與觀測模式的 warning 分別記錄。

### 3. Orchestrator Stage-Aware Admission 流程設計

Stage 介面拆成準備 request 與執行已準備 request：準備步驟只組裝一次 prompt；Orchestrator 評估該 request.Prompt 後，將同一份 request 交回 Stage 執行，不再組裝。Stage 保留既有 backend／role 路由及 Oracle／Critic 結果解析，回傳業務產物、ExecutionResult 與 error；即使 backend 失敗或 JSON 解析失敗也不丟棄已回報 usage。runner 相容路徑同樣使用已準備 prompt，無 usage 時明記 unknown。

`contextwin` 只接受文字、模型識別及設定，不依賴 pipeline 的 OraclePlan 型別；prompt 組裝留在 pipeline，避免重複組裝與循環依賴。

```text
[PipelineOrchestrator]
       │
       ├──► 1. 呼叫 Stage.Prepare，取得唯一的 ExecutionRequest
       │        - Oracle: System Instruction + Context Prompt
       │        - Striker: Base Prompt + OraclePlan (若存在)
       │        - Critic: System Instruction + Original Prompt + OraclePlan + StrikerOutput
       │
       ├──► 2. 呼叫 contextwin.AdmissionEngine.EvaluateStage(stage, fullPrompt, model, budgetConfig)
       │        ├── 計算 StageEstimate (標註 ProvenanceEstimated)
       │        └── 判定 StageAdmission (Admitted / Rejected / Warning / Bypassed)
       │
       ├──► 3. 准入檢查判定
       │        ├── Rejected:
       │        │     - 記錄 StageTrace (Status: StageFailed, Error: "context limit exceeded")
       │        │     - 調用後端次數 = 0 (Fail-Closed)
       │        │     - 絕不自動更換 Backend / 不重跑已完成 Stage
       │        │     - 中斷管線並回傳已產生之 Partial Result 與 Trace
       │        └── Admitted / Warning / Bypassed:
       │              - 交由 Stage 執行同一 req → Backend.Run(ctx, req)
       │
       └──► 4. 取得 ExecutionResult
                ├── 保留 adapter 回傳的可取得 TokenUsage（缺失為 unknown）
                └── 連同成功或失敗結果記入 StageTrace.Usage／UsageSource
```

---

## 實作步驟

### Phase 1：雙後端 Context 能力探測與 Token 正規化
- ✅ **Task 1.1**（對應 CTXWIN-001）：編寫 OpenCode 1.18.23 / 1.18.31 與 Codex CLI 0.155.0 / 0.155.1 的 Context Contract Probe 測試與樣本解析，產出有證據的能力矩陣，未驗證與 unsupported 分開標記。
  - 產出：[`internal/backend/opencode/probe_context_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/opencode/probe_context_test.go)、[`internal/backend/codex/probe_context_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/codex/probe_context_test.go)
  - 完成判準：四個指定版本各保存實際版本、命令、日期、退出碼、去敏原始 JSONL、fixture 與能力矩陣；確認欄位 presence、增量／累計語意及失敗後的 usage。逐 backend／role 記錄 preflight 模型識別是否可用。未安裝或無憑證為未驗證，不算 PASS；unsupported 需對應版本契約證據，不能只憑 stdout 沒有欄位推定。先完成此證據再設計聚合及模型來源實作。
  - 完成證據（2026-09-21）：
    1. OpenCode 1.18.31（本機已安裝）live probe 通過（`TestOpenCodeProbe_Live`，耗時 8.78s），確認 `tokens.total`、`tokens.input`、`tokens.output`、`tokens.cache.read` 存在，多步驟累計且工具失敗後仍保留用量；OpenCode 1.18.23 標示為 `unverified_not_installed` 並附離線 fixture 驗證（`TestOpenCodeProbe_HermeticFixtures`）。
    2. Codex CLI 0.155.1（本機已安裝）live probe 通過（`TestCodexProbe_Live`，耗時 10.27s），確認 `turn.completed` 提供 `input_tokens`、`output_tokens`、`cached_input_tokens`，且明確證實 `total_tokens` 缺失（absent/nil），工具失敗後仍保留用量；Codex 0.155.0 標示為 `unverified_not_installed` 並附離線 fixture 驗證（`TestCodexProbe_HermeticFixtures`）。
    3. 雙後端 live 探測均證實 stdout JSONL 事件未包含 model 欄位，preflight 模型動態辨識為 unsupported。
- ✅ **Task 1.2**（對應 CTXWIN-001, CTXWIN-002）：先定義 `backend.TokenUsage`／`DataProvenance` 與 ExecutionResult 欄位，再串接雙後端 parser 與 adapter 的聚合及回傳。
  - 產出：[`internal/backend/usage.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/usage.go)、[`internal/backend/usage_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/usage_test.go)、[`internal/backend/backend.go`](file:///home/kevin/Documents/RCodes/Asura/internal/backend/backend.go)、雙後端 `parser.go`／`parser_test.go`／`adapter.go`／`adapter_test.go`
  - 完成判準：部分缺失與真實零值可區分；依 probe 語意聚合，不補造 total；fake CLI 事件經 adapter.Run 回傳 usage，成功與失敗皆保留可取得數據。
  - 完成證據（2026-09-21）：
    1. `backend.TokenUsage` 定義 nullable `*int64` 欄位（`InputTokens`, `OutputTokens`, `TotalTokens`, `CachedTokens`, `ReasoningTokens`），通過 `TestTokenUsage_NullablePresence` 驗證缺失（nil/unknown）與明確 0 可精確區分，不捏造 0。
    2. Codex parser 與 adapter 支援 `turn.completed` usage 抽取；Codex 未提供 `total_tokens` 保持為 nil，不人為拼湊；通過 `TestExtractResultAndEvents_Success`、`TestExtractResultAndEvents_FailurePreservesUsage` 與 `TestAdapter_Run_UsagePreservedOnSuccessAndFailure` 驗證成功與失敗下 usage 均精確回傳。
    3. OpenCode parser 與 adapter 支援多步驟 `step_finish` tokens 聚合（含 `cache.read` 至 `CachedTokens`），通過 `TestParseEventLine_StepFinishWithCache` 與 `TestAdapter_Run_UsagePreservedOnSuccessAndFailure` 驗證 fake CLI 成功與失敗下 usage 均被保留且標記為 `ProvenanceExact`。
    4. 回歸套件 `MBACK-001` ～ `MBACK-012` 全部通過（PASS）。

### Phase 2：Context 預算、估算器與模型目錄核心套件
- ✅ **Task 2.1**（對應 CTXWIN-002, CTXWIN-005）：沿用 Task 1.2 用量契約，定義 `internal/contextwin` 核心型別（`ModelWindowInfo`, `StageEstimate`, `StageAdmission`）。
  - 產出：[`internal/contextwin/types.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/types.go)、[`internal/contextwin/types_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/types_test.go)
  - 完成判準：資料結構明確區分 Exact / Estimated / Unknown，嚴格維持 Sealed Context 不變性（不提供默改 prompt 或 memory 之介面）。
  - 完成證據（2026-09-21）：通過 `TestContextWinTypes_ProvenanceAndIntegrity`，驗證資料型別精確標記資料來源，且未暴露任何竄改 prompt 或 memory 的破壞性介面。
- ✅ **Task 2.2**（對應 CTXWIN-002）：實作靜態模型目錄 `Catalog` 與已知模型 Context 上限查詢，支援配置優先與未知降級。
  - 產出：[`internal/contextwin/catalog.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/catalog.go)、[`internal/contextwin/catalog_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/catalog_test.go)
  - 完成判準：可正確識別常見模型限制，未列模型標記為 `LimitSourceUnknown`。
  - 完成證據（2026-09-21）：通過 `TestCatalog_LookupKnownAndUnknown` 與 `TestCatalog_ExplicitLimitOverride`，驗證已知模型查詢正確、顯式配置優先、未知模型標記 `LimitSourceUnknown`。
- ✅ **Task 2.3**（對應 CTXWIN-002, CTXWIN-003）：實作輕量啟發式 Token 估算器 `Estimator`，提供字元換算與安全保留計算，標記 `ProvenanceEstimated`。
  - 產出：[`internal/contextwin/estimator.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/estimator.go)、[`internal/contextwin/estimator_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/estimator_test.go)
  - 完成判準：對不同語言提供可重複的啟發式估算，明記其無法保證 CLI 內部 context 不溢出的限制，不依賴外部 CGO 分詞庫，計算結果附帶 provenance。
  - 完成證據（2026-09-21）：實作 `heuristic-char-ratio-v1`，通過 `TestEstimator_MultiLingualAndReservedRatio` 驗證中英文及代碼估算穩定可重複、預設保留 20% 輸出空間、標記 `ProvenanceEstimated` 且無 CGO 依賴。
- ✅ **Task 2.4**（對應 CTXWIN-002, CTXWIN-004, CTXWIN-007, CTXWIN-008）：依設定契約新增環境變數解析並串接本機 run 與 daemon 的 Orchestrator 建構入口；依 Task 1.1 結果提供可靠模型識別或 unknown。
  - 產出：[`internal/contextwin/config.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/config.go)、[`internal/contextwin/config_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/config_test.go)、[`cmd/asura/run.go`](file:///home/kevin/Documents/RCodes/Asura/cmd/asura/run.go)、[`cmd/asura/run_test.go`](file:///home/kevin/Documents/RCodes/Asura/cmd/asura/run_test.go)、[`cmd/asura/daemon.go`](file:///home/kevin/Documents/RCodes/Asura/cmd/asura/daemon.go)、[`internal/daemon/daemon.go`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon.go)、[`internal/daemon/daemon_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/daemon/daemon_test.go)
  - 完成判準：由正式入口設定低上限及 reject，以 fake backend 驗證零執行；observe 超量仍執行、未知模型仍受顯式上限約束、無上限才 bypass、非法值啟動失敗。TBD-2 定案前不實作未核准預設值。
  - 完成證據（2026-09-21）：
    1. 通過 `TestLoadConfig_ValidAndInvalid` 驗證 `ASURA_CONTEXT_LIMIT`（正整數）與 `ASURA_CONTEXT_POLICY`（observe/reject）解析，非法數值與未知 policy 立即報錯拒絕啟動。
    2. 通過 `TestRunCmd_ContextLimitPreflightRejection` 驗證正式本機 `run` 在 reject 政策超量時 fail-closed 拒絕且後端調用為 0。
    3. 通過 `TestDaemon_ContextWindowAdmissionIntegration` 驗證守護進程注入設定之准入控制。

### Phase 3：Stage-Aware Prompt 組裝與准入引擎
- ✅ **Task 3.1**（對應 CTXWIN-003）：將現有 Stage prompt 組裝提取為 Prepare，ExecutePrepared 接收同一 request；調整 Stage 介面回傳 ExecutionResult 及業務產物。
  - 產出：[`internal/pipeline/stage_oracle.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/stage_oracle.go)、[`internal/pipeline/stage_striker.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/stage_striker.go)、[`internal/pipeline/stage_critic.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/stage_critic.go)、[`internal/pipeline/stage_characterization_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/stage_characterization_test.go)
  - 完成判準：以同組 characterization 測試比對拆分前後 prompt；攔取 Backend.Run 的 request，確認與估算輸入逐字相等，涵蓋 Direct、Oracle、Striker、Critic。Stage 發生 backend 或解析錯誤仍返回 ExecutionResult。
  - 完成證據（2026-09-21）：通過 `TestStageCharacterization_PromptIdentical`，驗證 Prepare 產出之 prompt 與實際送入 Backend.Run 的 request.Prompt 100% 逐字相等；通過 `TestStageCharacterization_UsagePreservedOnFailure` 驗證執行或解析失敗仍保留 usage。
- ✅ **Task 3.2**（對應 CTXWIN-004）：實作准入檢查引擎 `AdmissionEngine`，支援配置超量政策（拒絕、警示、忽略未知）。
  - 產出：[`internal/contextwin/admission.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/admission.go)、[`internal/contextwin/admission_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/contextwin/admission_test.go)
  - 完成判準：reject 超量為 Rejected，observe 超量為 Warning；顯式 limit 優先，無顯式或可靠 catalog 上限才 Bypassed；等於上限通過。
  - 完成證據（2026-09-21）：通過 `TestAdmissionEngine_Evaluate`，驗證 reject 超量判定為 Rejected、observe 超量判定為 Warning、剛好等於上限判定為 Admitted、無上限判定為 Bypassed。

### Phase 4：Orchestrator 整合與 Fail-Closed 防禦
- ✅ **Task 4.1**（對應 CTXWIN-003, CTXWIN-004, CTXWIN-006）：擴充 `pipeline.StageTrace` 與 `pipeline.PipelineTrace`，新增 `Admission`、`Usage` 與 `UsageSource` 欄位。
  - 產出：[`internal/pipeline/types.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/types.go)、[`internal/pipeline/types_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/types_test.go)
  - 完成判準：Trace 保存 adapter → Stage 的 usage，含 backend 失敗及 Oracle／Critic JSON 解析失敗；部分缺失與真實零值經 JSON 往返仍可區分。
  - 完成證據（2026-09-21）：通過 `TestStageTrace_SerializationAndNullableUsage`，驗證 Admission 與 nullable usage 欄位經 JSON 往返後精確保留，nil 不捏造為 0，真實 0 保持為 0。
- ✅ **Task 4.2**（對應 CTXWIN-004, CTXWIN-007）：Orchestrator 依序 Prepare → Admission → ExecutePrepared，整合 Preflight 准入檢查，超量時 fail-closed 拒絕，模型呼叫次數為 0，保留已完成 stage 之結果且不切換 backend。
  - 產出：[`internal/pipeline/orchestrator.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/orchestrator.go)、[`internal/pipeline/orchestrator_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/pipeline/orchestrator_test.go)
  - 完成判準：Striker 或 Critic 超量拒絕時，前面 Stage 的產出完整保留在 Trace，絕無二次呼叫或跨後端重跑。
  - 完成證據（2026-09-21）：通過 `TestOrchestrator_PreflightFailClosed_Direct`、`TestOrchestrator_PreflightFailClosed_StrikerRejectionPreservesOracleTrace` 與 `TestOrchestrator_ObservePolicyWarningContinues`，驗證超量時 0 呼叫中斷、已完成 Stage Trace 完整保留、絕無跨後端重跑或二次嘗試。

### Phase 5：持久化相容性與 Engine 審計串接
- ✅ **Task 5.1**（對應 CTXWIN-006, CTXWIN-007）：`internal/storage` 驗證 SQLite 對擴充後之 PipelineTrace 儲存與讀取向前向後相容。
  - 產出：[`internal/storage/sqlite_contextwin_compat_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/storage/sqlite_contextwin_compat_test.go)
  - 完成判準：歷史無 Usage 任務讀取時為 nil／unknown；新任務完整保留 Admission、部分缺失與真實零值、失敗後 usage；SQLite 往返後語意不變。
  - 完成證據（2026-09-21）：通過 `TestSQLite_ContextWinTraceCompatibility_HistoricalAndNew`，驗證舊版無 Admission/Usage 記錄之 JSON 能向前相容讀取且不捏造 0；新版完整任務經 SQLite 存取後完整保留 Admission、部分缺失/真實零值與失敗後的 usage。
- ✅ **Task 5.2**（對應 CTXWIN-005, CTXWIN-007）：驗證 Engine 接收 SubmitRequest、相同 Request ID 重送時，Context 不變性與 Admission 結果一致性。
  - 產出：[`internal/engine/engine_test.go`](file:///home/kevin/Documents/RCodes/Asura/internal/engine/engine_test.go)
  - 完成判準：重送請求不重跑估算或後端、不覆蓋結果；已保存決策不因程序設定改變而重算。
  - 完成證據（2026-09-21）：通過 `TestEngine_RedeliveryPreservesContextAndAdmission`，驗證重送相同 Request ID 時立即返回既有任務，絕不二次調用後端，且維持原 Sealed Context 與 Admission 決策不變。

### Phase 6：外迴圈驗收與全系統回歸
- ✅ **Task 6.1**（對應 CTXWIN-001 ～ CTXWIN-008）：彙整各 Task 實作前建立的外迴圈 BDD 測試套件，完整覆蓋 CTXWIN-001 ～ CTXWIN-008 所有 Scenario。
  - 產出：[`tests/integration/context_window_test.go`](file:///home/kevin/Documents/RCodes/Asura/tests/integration/context_window_test.go)
  - 完成判準：CTXWIN-002 ～ 008 取得 hermetic 整合證據；CTXWIN-001 的離線解析驗證連結 Task 1.1 四版本 live artifact，兩類證據齊備才完成該 Scenario。CI skip live 不算能力驗收通過。
  - 完成證據（2026-09-21）：`go test -v ./tests/integration -run "TestCTXWIN"` 100% 通過（`TestCTXWIN001` 至 `TestCTXWIN008` 全數 PASS）。
- ✅ **Task 6.2**（對應 CTXWIN-008）：執行既有回歸測試網（MBACK-001 ～ MBACK-012, TFACE-001 ～ TFACE-009, CTXMEM-001 ～ CTXMEM-008）。
  - 產出：根目錄 [`README.md`](file:///home/kevin/Documents/RCodes/Asura/README.md) 與架構規格書 [`docs/superpowers/specs/2026-09-11-asura-architecture-design.md`](file:///home/kevin/Documents/RCodes/Asura/docs/superpowers/specs/2026-09-11-asura-architecture-design.md) 更新
  - 完成判準：所有既有測試套件 100% 通過（全綠）。
  - 完成證據（2026-09-21）：執行 `go test ./...` 及 `go test -count=1 ./internal/... ./cmd/...`，包含所有既有單元與整合測試（MBACK、TFACE、CTXMEM、CRON、MEM、WEB 等）全數 PASS。

---

## 使用方式對照

### 變更前
```go
// 呼叫端無從得知各 Stage 的 Prompt 膨脹與 Token 使用量
res, err := backend.Run(ctx, req)
// res 僅包含 Output 與 Events，缺乏標準 Usage
trace.Stages = append(trace.Stages, StageTrace{
    Name: "striker",
    DurationMs: 120,
})
```

### 變更後
```go
// 示意：Prepare 的 request 同時供估算與執行使用
req := stage.Prepare(execCtx, artifacts)
admission := admissionEngine.EvaluateStage(stageName, req.Prompt, model, budgetConfig)
if admission.Verdict == VerdictRejected {
    // Fail-closed：不呼叫後端，記錄拒絕原因
    return failClosed(stage, admission)
}

// 執行失敗時仍保存已取得的結果；實作須處理 res 為 nil
output, res, err := stage.ExecutePrepared(ctx, req)
trace.Stages = append(trace.Stages, StageTrace{
    Name:       "striker",
    Admission:  admission, // 包含估算值與資料來源 (Estimated)
    Usage:      optionalUsage(res), // 缺失為 nil，明確回報 0 則保留
    UsageSource: usageSource(res),
    DurationMs: 120,
})
```

---

## 任務相依與證據

Task 1.1 → 1.2 → Phase 2 核心型別／設定解析 → Phase 3 → Phase 4 → Phase 5 → Phase 6；Task 2.4 的設定解析可先完成，正式入口注入與整合判準在 Task 4.2 完成後收尾。Task 2.4 與 3.2 的預設政策相依 TBD-2。改變行為的 Task 在實作前取得對應外迴圈／單元紅燈；Phase 6 彙整完整證據，不把外迴圈首次建立延後到實作之後。Task 3.1 的純組裝搬移先跑同組 characterization 測試，新增 usage 傳遞另取紅燈。

交付時更新根目錄 README 的設定與審計用法，以及 `docs/superpowers/specs/2026-09-11-asura-architecture-design.md` 的管線資料流；由 Task 6.2 核對。本次只修訂提案，不提前改寫描述現況的常青文件。

## 測試策略

1. **單元測試（Unit Tests）**：
   - `internal/backend/opencode` 與 `codex` 的 parser 測試，驗證從 JSONL 原始行中精確抽取 TokenUsage。
   - `internal/contextwin` 的估算邏輯與模型目錄測試，測試中英混雜字串、代碼字串之估算合理性。
   - `internal/pipeline` 的 Orchestrator 測試，透過 Mock Backend 模擬各種窗口大小與超量情境。
2. **能力探測（Contract Probe）**：
   - 區分 Hermetic（使用真實 CLI 輸出錄製之樣本 fixture 進行解析）與 Live Probe（需本機環境存在特定 CLI 時執行），確保在無外部 CLI 的 CI 環境中解析測試仍可執行；live 能力驗收另外引用 Task 1.1 artifact，不以 skip 或 mock 代替。
3. **整合測試（Integration Tests）**：
   - 驗證 Direct 模式 1 次模型呼叫、Three-Face 模式 3 次模型呼叫下的完整 PipelineTrace 與各 Stage 之 Usage 記錄。
   - 驗證相同 Request ID 重送之冪等性。
4. **回歸測試網（Regression Suite）**：
   - 執行 `go test ./...`，驗證 MBACK、TFACE、CTXMEM 既有測試無任何回歸破壞。

---

## 驗收編號對應的測試層級與策略

| 驗收編號 | 核心驗證標的 | 測試層級 | 測試策略 |
|---|---|---|---|
| **CTXWIN-001** | 固定版本 OpenCode / Codex 能力探測與 unsupported 矩陣 | Contract Probe / Unit | 撰寫 probe 測試與 fixture 解析，驗證可觀測之 token usage 及未支援能力斷言 |
| **CTXWIN-002** | Exact、Estimated、Unknown 數據來源明確區分且不偽造 0 | Unit / Type Test | 驗證 `TokenUsage` 與 `StageEstimate` 之 provenance 標籤與序列化行為 |
| **CTXWIN-003** | Three-Face 各 Stage 依實際 Prompt 獨立進行動態估算 | Unit / Pipeline Test | 模擬 OraclePlan 與 StrikerOutput，斷言 Striker 與 Critic 之估算值反映動態內容 |
| **CTXWIN-004** | 超量時 Preflight Fail-Closed 拒絕，0 後端呼叫，保留已完成 Trace | Integration / Orchestrator | 設定超低預算上限，驗證 Striker 被拒絕時 Oracle 結果依然保留且不換後端 |
| **CTXWIN-005** | Sealed Context 與 Memory 快照不可變性 | Unit / Engine Test | 斷言預算評估前後 `ExecutionContext` 之記憶快照與原始 Prompt 完全一致 |
| **CTXWIN-006** | Task / PipelineTrace 具有 stage 分別的 estimate 與 observation | Storage / Integration | 驗證 Trace 存入 SQLite 後重讀，能正確讀出各 Stage 之估算與實測用量 |
| **CTXWIN-007** | 相同 Request ID 重送冪等性與決策一致性 | Engine Integration | 重送相同 Request ID，驗證返回相同任務與 Trace，無二次後端調用 |
| **CTXWIN-008** | 未啟用新限制時既有功能與回歸測試 100% 通過 | Regression Test Suite | 執行 `go test ./...` 既有全部測試，保持全綠 |

---

## 風險與首要驗證

引用 [README.md](./README.md) 的 `## 風險與首要驗證` 節點。

---

## 檢查清單

- [x] 探測 OpenCode 與 Codex CLI 的真實 usage 輸出與 session 行為
- [x] 定義標準 `TokenUsage` 與 `DataProvenance`
- [x] 實作 stage-aware 的 prompt 估算器（支援 OraclePlan 與 StrikerOutput 動態計算）
- [x] 整合 Orchestrator 前置檢查，落實超量 fail-closed 且不擅改 sealed context
- [x] 保持向前向後相容，既有 SQLite 資料無縫遷移
- [x] 通過 CTXWIN-001 ～ CTXWIN-008 驗收劇本
- [x] 全套既有測試回歸通過
