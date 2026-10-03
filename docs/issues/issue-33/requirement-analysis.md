# Issue 33 — 需求與現況分析

調查日期：2026-10-03；被讀取版本 `41b5220b2100ad85fdb4913c7c8e73ea57657464`。

## 需求描述與目標

R1～R8 要求 deterministic execution requirements、runtime descriptor、共同 matcher、Direct／Three-Face 整合、immutable admission snapshot 與既有入口相容。explicit backend selection 與單 backend Three-Face 保持；Control Plane 不理解 runtime skills／MCP workflow。

## 現況分析

1. `internal/backend/backend.go` 已有 Role、四種 Capability 與 `Backend.ID/Version/Capabilities/HasCapability/Run`。Registry 僅管理 registration／lookup；核心預設在 engine、daemon、pipeline 固定為 opencode，並非 registry Default。
2. `internal/pipeline/orchestrator.go` 的 Execute 先 lookup、讀 Version（忽略錯誤），再要求 workspace_write；Three-Face 額外要求 read_only_sandbox。沒有獨立 requirement/match 型別；capability 拒絕回傳 nil trace。
3. OpenCode RoleRoutingBackend 將 Oracle／Critic 派往唯讀 adapter，Striker 派往寫入 adapter；仍是同一邏輯 backend。新契約不得破壞此安全角色分派。OMP／Antigravity 不宣告 read_only_sandbox。
4. `internal/engine/engine.go` ExecutionContext 包含 backend、記憶與建立時間，但沒有 mode、capability 或 policy snapshot。SubmitTask 建立 Task 並轉 running 後才呼叫 Pipeline。已有 ID 的 terminal Task 直接回傳；pending／queued 仍可能進入執行，不能把 engine worker dispatch 當成一般 IPC redelivery。
5. `internal/daemon/daemon.go` submit 在 workspace 授權、mode 與 fingerprint 建立後先做 request 去重；再解析記憶、取得容量、建立 Task、取得 workspace queue、派送。只有有記憶或 recall 時接受階段才序列化 resolved_context；無記憶只有記憶體 context。
6. 同檔 dispatchCronTask 在容量取得後建立 direct／opencode Task，沒有 sealed context；queued wake 經 engine 讀 persisted Task。共同 admission 必須覆蓋 Cron，避免只修互動入口。
7. `internal/storage/sqlite.go` Task 已有 resolved_context 與 pipeline_trace TEXT；CreateTask 在單次 INSERT 保存它們。可用 additive JSON 欄位擴充，無須另建 audit table。
8. `cmd/asura/status.go` 顯示 Task backend、mode 與各 stage version；Web page 的既有稽核區解析 resolved_context 與 pipeline_trace。現有 IPC 回傳 storage.Task，無須新使用者必填參數。

## 問題點總結

- 安全需求隱含在 Pipeline，缺乏可重用的共同集合判定。
- capability 預檢不在新任務接受點；無記憶／Cron 沒有接受時快照。
- stage trace 的 backend version 是執行時資料，不能代表接受時判定。
- 若將快照取得放到去重前，runtime 改變或移除會破壞原本能回傳的 redelivery。
- version 讀取錯誤原本被忽略；改成可信完整 descriptor 必須明確決定失敗語意。

## 調查證據與限制

既有刻畫測試命令與結果保存在 [planning-baseline.log](./evidence/planning-baseline.log)。新 contract 尚未實作，因此不能把此基線當成 RTCAP-001～016 的完成證據。檔案邊界與風險只在 README 維護。
