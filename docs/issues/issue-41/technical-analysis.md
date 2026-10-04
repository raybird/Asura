# Issue 41 — EA-TBD 技術提案

2026-10-04 提案；全部內容待使用者核准，入口為 README。

## 方案選型

| 方案 | 取捨 |
|---|---|
| terminal snapshot | 最少寫入，但 crash 前完成的 Oracle 會遺失，無法滿足 R9 |
| append-only events + 每次寫 materialized projection | 可保留歷史，但投影又成為需交易同步、遷移與驗證的持久化形狀 |
| immutable admission header + append-only events + 唯讀 fold | 建議採用。每 Task 至多 32 events，可用有界 fold 重建，不需持久化第二份 projection |

```diff
 successful admission
- CreateTask (optionally with recall transaction)
+ atomic CreateTask + existing recall + immutable audit admission header
 stage lifecycle
- collect trace in memory; persist after pipeline returns
+ commit typed observation at each boundary; trace remains diagnostic
 task terminal CAS
- update Task status separately
+ same transaction: Task terminal CAS + unique terminal audit event
```

Audit 是觀測紀錄，不能提供 backend 綁定、替代 contract validator 或重新 selection。建議要求觀測寫入成功才開始下一個副作用；這是記錄可靠性的失敗政策，沒有賦予 audit 選擇 runtime 的權力，仍須核准。

## EA-TBD-1～16 提案

| ID | 提案 |
|---|---|
| EA-TBD-1 | schema execution-audit-v1。ExecutionAuditRecord = task identity、request correlation digest、mode/requested backend、admission 摘要、固定順序 stages、events、terminal?、completeness、integrity。StageAudit = sealed binding reference、guard observations、observed runtime identity/version?、Run invocation evidence、timestamps、outcome、reason code。Intent 僅指 mode/backend/requirements，不複製 prompt、workspace、memory 或模型內容。 |
| EA-TBD-2 | 兩個 SQLite table：task_execution_audits（每 Task 唯一 immutable header）與 task_execution_audit_events（task_id、seq、event_key、typed payload、prev_digest、digest）。無通用 event bus，無獨立 snapshot table。GetTaskAudit 有界讀取後 fold。 |
| EA-TBD-3 | 成功 admission 先 Validate 原 contract；SHA-256 canonical typed JSON 僅針對 ExecutionContract，不包括整個 ResolvedContext。Audit 中 inventory/runtime descriptors 正規化成一份 table，各 stage 只存 requirements、candidate refs、eligible/rejected enums、effective priority、selected ref、policy ID/version；移除任意 rejection reason 字串，保留 missing capability enum。封存 contract 保持原格式與既有上限。摘要必須足以離線解釋 selection，不能只保存 digest；所有 refs、集合與 digest 需與封存來源一致。 |
| EA-TBD-4 | sealed/planned ref 永不改寫。guard observed identity/version 與 actual Run runtime 分欄；失敗前看到錯誤 identity 不代表該 runtime 執行過。版本可與 admission 不同，沿用 adapter 既有政策，不新增版本相等鎖定。 |
| EA-TBD-5 | guard = not_reached/passed/failed；記 precheck 與 stage_execution 兩個 scope。固定原因 none/runtime_missing/runtime_unavailable/identity_mismatch/capability_mismatch/contract_invalid/cancelled；取消歸類用 typed/context 原因，不靠 error 字串猜測。guard failure 沒有 Run event。 |
| EA-TBD-6 | stage = not_started/running/completed/failed/cancelled/interrupted；另有 reachability = not_reached/reached。guard failure stage failed、Run=0；Prepare/context admission/response parse/critic verdict 各有 failure_layer 與安全 enum，不能一律叫 runtime failure。Task failed 可與三個 stage completed 同時成立（critic verdict、空輸出／結構化錯誤檢查）；terminal 由既有 lifecycle 得出。 |
| EA-TBD-7 | 建議不建立獨立 persisted AdmissionAudit。拒絕於原 submission response 附 bounded admission_rejection 摘要與 request correlation digest；無 Task、無 execution events；不保存 rejected prompt、也不承諾拒絕請求 redelivery cache。R10 適用已接受且有原 Task 的 request。CLI/Web submission 能顯示即時拒絕；GetTaskAudit 只讀有 Task 的 audit。 |
| EA-TBD-8 | accepted Task、recall 與 audit header 同交易；失敗全部 rollback、Run=0。guard/stage observation append 失敗停止後續 Run／stage，返回 audit_persistence_failed；不 retry/fallback。runtime 已造成的副作用不回滾。terminal Task CAS 與 audit event 同交易；若 DB 完全失效不得謊稱終態已持久化，保留最後 durable prefix 並回錯，重啟後依 recovery 處理。recovery 失敗讓 daemon Start 回錯、釋放 listener/lock，不開始派送。 |
| EA-TBD-9 | 每個 stage completion 必須先 durable append 才進下一 stage。重啟在同交易把原非終態 Task 轉 failed 並 append recovery/terminal；running stage → interrupted，未 reached stage 保留 not_started/not_reached，completed 不變。completeness=partial，說明 crash window；未開始 stage 不補 execution。無 resume。 |
| EA-TBD-10 | event identity 使用固定 task/stage/boundary key，seq 單調；UNIQUE(task_id,event_key) 與 UNIQUE(task_id,seq)。相同 key 的語意 payload 重送沿用原時間／digest；不同 payload 明確 conflict。Task CAS 和 events 的 ordering 在 SQLite transaction 線性化；concurrent delivery 沿既有 RequestID/fingerprint 策略，只有 accepted Task 勝者建立 header。event append retry 僅處理同一 observation，不是 runtime retry。 |
| EA-TBD-11 | before stage、during guard、during Run 各留 phase；context 取消不搶 CancelTask 的終態權，cancelling 不顯示 cancelled。process-group verification 後由取消協調器同交易寫唯一 terminal。完成與取消競態只允許 CAS 勝者 terminal；已完成 stage 不回寫 cancelled。 |
| EA-TBD-12 | 無新 header 的 capability-v1/stage-binding-v1/runtime-inventory-v1 Task 與無 contract 任務呈 legacy（非完整新 audit）。唯讀、驗證後可顯示原 sealed plan 摘要；歷史實際執行／guard 不從 trace 猜造 events。升級前 queued Task 可以記錄升級後新觀測但仍 partial，標記歷史不可得。完整度 complete 只指此 schema 的觀測已收齊，不承諾 runtime 內部模型呼叫已全部可知。 |
| EA-TBD-13 | 固定 typed structs、UTC timestamps、穩定 enum/ref 排序與 schema version；不使用任意 map 作 canonical authority。header digest 封存 contract digest 與 normalized admission；event chain 根植 header digest，查 seq、event identity、stage order、transition、terminal 唯一與 Task state 一致。新增／修改／刪除中間 events、contract digest 不符 → invalid，不顯示可信 completed。沒有金鑰或外部 anchor，不能偵測攻擊者完整重算 hash 或所有合法 prefix truncation，第一版不宣稱不可否認性。 |
| EA-TBD-14 | Store/daemon Client.GetTaskAudit(ctx,taskID)；IPC action task_audit；Web GET /api/tasks/{id}/audit；CLI asura audit --id ID（--json 可輸出 bounded record，daemon 不可用時讀本機 DB）。沿用 Task authorization、Web session/origin 防護；不新建可旁路權限的查詢。專用 response 不帶原 Task prompt/result/trace。Web task detail 僅新增 audit projection，不新增管理 dashboard。 |
| EA-TBD-15 | stages ≤3、inventory/candidates ≤32、events ≤32、單 event JSON ≤2 KiB、normalized header ≤64 KiB、完整 record ≤128 KiB、IPC/HTTP encoded response ≤160 KiB；runtime ID ≤64 bytes、version ≤256 bytes、Task identity ≤256 bytes，requestID 保存 SHA-256 correlation。diagnostic ≤128 bytes 且只用 enum 對應固定模板。超界拒絕、不截斷 authoritative identity；legacy 巨大／非法值回 bounded unavailable。計數與 byte budget 在 INSERT 和讀取前檢查，保留 terminal/recovery 的 event 空間，禁止逐 token/chunk events。 |
| EA-TBD-16 | PipelineTrace 保留既有診斷與 memory utility 使用；新 Audit 使用 typed execution observations，絕不由 trace error/output 反推。CLI audit / Web audit 完全讀 persisted record；既有 status 診斷可以保留但需明確區別，audit lifecycle 不由 UI 重新計算。 |

## Observation 與計數限制

2026-10-04 提案：在實際 Backend.Run 邊界，先持久化 run_intent，再呼叫，返回時記 run_returned（成功／失敗 enum），之後 stage parsing/validation 各自記 outcome。run_intent 是呼叫意圖，無法原子證明外部程序已開始；crash 在 append 與 Run 之間時必須 unknown/incomplete，不能寫成確定呼叫一次。guard 所觀測 runtime 只有 run_returned 確認後才能呈為已返回的 actual；running 時顯示 intended/observed，不能宣稱 completed。

Terminal summary 分開 Run intents、confirmed returns、completed stages；runtime invocations = confirmed 或 unknown，provider model calls 保留 unknown，除非 adapter 有可信獨立 evidence。不把 PipelineTrace.ModelCalls 當 provider calls；不儲存任意 model label。legacy RunnerFunc 只能記有來源標記的 runner observation，不能虛構 Backend.Run evidence。

建議 event vocabulary：task_started、guard_precheck（整批有界結果）、guard_started、guard_result、run_intent、run_returned、stage_outcome、cancel_requested、terminal、recovery。Direct 至多一 stage、Three-Face 至多三 stage；正常 Three-Face 小於 24 events，重複取消不 append 無界紀錄；最大 32 留有故障／recovery 空間。stage Prepare 與 context rejection 無 Run；後置解析失敗有 Run returned，但 stage failed。

## 原始需求覆蓋

| Requirement | 提案與驗收 |
|---|---|
| R1 Execution Audit Contract | EA-TBD-1；EXAUD-001、002 |
| R2 Admission Provenance | EA-TBD-3；EXAUD-003、004 |
| R3 Planned vs Actual | EA-TBD-4；EXAUD-007、019 |
| R4 Execution Guard Audit | EA-TBD-5；EXAUD-006、007 |
| R5 Stage Lifecycle Audit | EA-TBD-6；EXAUD-001、002、009、018 |
| R6 Terminal Task Outcome | EA-TBD-6、8；EXAUD-008、018、019 |
| R7 Admission Rejection Audit | EA-TBD-7；EXAUD-005 |
| R8 Persistence Boundary | EA-TBD-2、8；EXAUD-017 |
| R9 Crash / Restart | EA-TBD-9；EXAUD-010 |
| R10 Redelivery / Idempotency | EA-TBD-10；EXAUD-011 |
| R11 Cancellation Audit | EA-TBD-11；EXAUD-009 |
| R12 Legacy Contracts | EA-TBD-12；EXAUD-013 |
| R13 Audit Integrity | EA-TBD-13；EXAUD-015 |
| R14 Query API | EA-TBD-14；EXAUD-012 |
| R15 CLI Audit Surface | EA-TBD-14；EXAUD-001、002、012、014 |
| R16 Web Audit Surface | EA-TBD-14；EXAUD-001、002、012、014 |
| R17 Trace Relationship | EA-TBD-16；EXAUD-018、019 |
| R18 Security / Privacy / Bounds | EA-TBD-15；EXAUD-014、015 |

EXAUD-016 覆蓋所有實作的既有功能回歸。

## Security 與架構影響

2026-10-04 提案：新增 internal/audit 僅依 backend 安全的值型別，storage 與 pipeline/engine 都可依賴它，避免 storage → engine/pipeline 循環。以窄 typed observer 於 ExecutionContext 的非 JSON 欄位接線，非執行 authority；不得加入通用 observer framework。在 engine/daemon 的 task creation、CAS、取消與 recovery 邊界接入同交易 persistence。只改必要的 SQLite utility transaction 接線，不改 recall/utility 行為。

每一個 audit producer 只允許 identity、enum、bounded descriptors、counts、timestamps；不得接收 error.Error、stdout/stderr、panic value、prompt、environment 或 token。讀取也重新驗證 bytes、enum、digest 與結構，拒絕直接輸出被竄改的自由文字。敏感 sentinel fixture 必須涵蓋 runtime error/output、guard error、巨大 payload、IPC/API/CLI/Web，不能僅檢查 renderer。

## 待決與限制

2026-10-04 尚待核准上述保存與失敗政策，以及 README 的 EXAUD-001～019。特別是 rejection 僅即時回應、fail-closed 寫入和 crash 計數 unknown 均是可觀察行為選擇，不以 `/dev-cycle issue:41` 自動視為同意。raw PipelineTrace 與 Task.Result 既有保存不在本期擴大清理，安全保證對本期新增 audit persistence／query／projection 適用。
