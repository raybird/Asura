# Issue 41 — 需求與現況分析

2026-10-04 盤點；基準為已合併 PR #40 的 main `2f583c7f257fa9fd3b974ff94f5bbd82c787e4b5`。

## 需求與目標

依 [GitHub #41](https://github.com/raybird/Asura/issues/41) 的 R1～R18，建立有界、機器可讀、可持久化的 Control Plane Execution Audit。它回答 admission 決策與實際執行的關係；ExecutionContract / StageBindingPlan 繼續決定執行，Task lifecycle 繼續決定任務狀態。不擴充 routing、retry、failover 或 observability 平台。

## 現況

| 路徑 | 已有能力 | 本次缺口 |
|---|---|---|
| backend inventory / selection / stage_binding | runtime-inventory-v1 封存 inventory、可用 descriptors、各 stage candidates、priority 與 selection；Validate 使用封存資料 | payload 有多份 descriptors；沒有獨立 audit schema 或摘要完整度 |
| daemon handleConn submit | authorization → fingerprint/redelivery → Preflight → memories/capacity → CreateTask → queue/dispatch；rejection 隨 IPC error 回傳 inventory | 拒絕未建 Task、也未保存獨立 admission history；dispatch rollback 可能 DeleteTask |
| engine SubmitTask | local Preflight；已存在 pending/queued 任務沿用 sealed context；SQLite CAS 維護 running 與終態 | trace 與狀態分開寫；部分提早失敗／取消分支未保存 trace |
| pipeline Execute / guardStage | 整體 contract validation、所有 binding 的靜態前置查核，再於各 stage 查當前 sealed ID、能力及必要 availability/version；無重選 | guard errors 為字串；actual 與 planned 資訊混在 trace；沒有逐 stage durable observation |
| stage ExecutePrepared | sealed stage backend 再查核後呼叫 Backend.Run；Oracle/Critic 可於 Run 返回後解析失敗 | stage failed 不一定等於 runtime failed；Run 呼叫位置與 pipeline 呼叫計數不能混稱精確模型呼叫 |
| PipelineTrace | stage output/error、model_calls、duration、contract、guard、OraclePlan/CriticReview 等診斷 | engine 在整條 pipeline 返回後才保存；不能用 crash 後空 trace 推論沒有執行；含自由文字，不可直接轉存安全 audit |
| CancelTask | SQLite CAS → cancelling → cancel/reap/verify → cancelled；pending/queued 可直接 cancelled | terminal authority 在取消協調器；新增 recorder 不得搶先宣告取消成功 |
| RecoverInterruptedTasks / daemon Start | pending/queued/running/cancelling 全轉 failed，不 resume；Start 目前忽略 recovery error | 無 stage 歷史可復原；audit 寫入故障不能被忽略後繼續派送 |
| storage CreateTask | 在 sqlite_utility.go；有 bounded recall 時 Task 與 recall_events 同交易，其他情況直接 INSERT | 新 audit 的 admission 交易必須涵蓋既有 recall，不能另開不相容交易 |
| status / Web | 由 Task.ResolvedContext 與 PipelineTrace 投影 capability/inventory/selection/guard | 尚無 persisted-only GetTaskAudit；新的讀取不得使用 fresh runtime inventory API |

上述為 2026-10-04 靜態程式碼盤點，未執行新 audit 的測試，不能當作 audit 行為通過證據。

## 問題與邊界

最嚴重的缺口是 crash、取消與 storage failure 下的歷史可信度。僅在終態建立 snapshot 無法滿足 R9；直接把 trace 當 audit 會複製自由文字與巨大 contract，並混淆 stage 完成和 Task 成功。

原始需求有未決的保存／失敗政策，因此 technical-analysis 提出具體方案，README 保留待核准狀態。兩份分析均有觸發理由：現況必須讀程式才能確認；持久化方案與失敗政策有實質取捨。沒有 production code 修改，也未啟動待核准的實作。
