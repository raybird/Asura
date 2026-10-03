# Issue 37 — 現況盤點

2026-10-04 以 Phase 4B merge `d3a40e6ead080d7ab74accf02aadba192aedb48e` 為基準，唯讀盤點。

## 需求與目標

沿 GitHub #37 R1～R12，讓已知 stages 各自依 requirements 綁定 runtime，admission 原子封存，execution 不 selection、不 fallback；保持 explicit 與 Direct 相容。最小修改邊界見 README。

## 現況分析

- `backend.RequirementsForMode` 已固定 Direct=[Striker]，Three-Face=[Oracle,Striker,Critic]。Oracle/Critic 均唯讀，Striker 為 workspace-write。
- `backend.AdmitRuntime` 對整份 requirements 做 union match。auto 呼叫 Registry.Snapshot 一次，鎖外 CaptureRuntime 後交 resolveSelection；explicit 先能力拒絕再捕獲版本。
- `resolveSelection` 以 ID canonical ordering 列候選，以既有 priority 選 eligible；最多 32 candidates、decision JSON 最多 64 KiB，descriptor Version 失敗使 auto 整次拒絕。
- daemon IPC 在授權、redelivery lookup 後 preflight，封存進 `ExecutionContext.ExecutionContract`，以 `Task.ResolvedContext` 原子寫入。`Task.Backend` 與 context.Backend 存 selected ID。
- engine 的新 Task 走 preflight；已存在 Task 由 stored requested intent 檢查身分，讀回 context 執行。queue wake 會從 context 取 auto intent，避免把 selected ID 當重送 intent。
- orchestrator Execute 用 context.Backend 找單一 backend，Validate 原封存 contract，對 requirements union 做一次 current capability guard；BoundBackend 傳給所有 stage。各 stage 若無 BoundBackend 還能使用 registry/context.Backend 或 constructor/runner。
- runtime current version 由 Version 查詢，但不要求等於封存版本；目前不以 Version 查詢錯誤作版本一致 gate。封存 version 及 match 則須結構一致。
- CLI status 及 Web page 讀 contract/decision，現有 stage trace 全部記同一 backendID/version。
- daemon Start 呼叫 RecoverInterruptedTasks，將 running/pending/queued/cancelling 標 failed，保留 resolved_context；沒有自動 resume。
- OMP/Antigravity 真實 adapter 僅有 workspace-write、structured events、process cancellation；Codex/OpenCode 另有 read-only sandbox。預設排序 opencode 優先、omp 最後，環境可調。

## 問題點與規格前提修正

1. 單一 runtime union admission 無法讓 write-only runtime 只接 Striker；pipeline/context 需要 stage binding 唯一來源。
2. Oracle/Critic requirements 相同：同 snapshot/priority 下 eligibility 相同。GitHub STGBIND-004 的「只有 Critic 無候選」無法在本期契約構造；改用唯讀 stages 無候選，證明即使 Striker 有候選仍整次拒絕。
3. Codex/OpenCode 都符合三階段需求：共同 priority 下兩者本身無法混搭。以真實 OMP/Codex capabilities 和 omp,codex priority 證明 mixed 可發生，不新增 stage-specific priority。
4. restart stability 是 plan 保留且不 reselection；不能將既有 failed recovery 說成恢復執行。
5. 現有 Validate 能抓結構矛盾，無簽章或可信外部摘要；不能承諾識別攻擊者一致重寫所有欄位。提案明列此邊界。

上述結論為程式碼盤點，不是 Phase 4C 行為測試通過證據。SB-TBD 的具體設計見 technical-analysis。
