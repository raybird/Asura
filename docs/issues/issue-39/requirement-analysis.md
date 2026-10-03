# Issue 39 — 需求與現況分析

2026-10-04 盤點基準：main `625e62d`（Phase 4C PR #38 已合併）。

## 需求描述

registered 只代表配置身分；新 Task 必須使用一次 coherent admission inventory 決定可用候選。runtime 探測失敗應被隔離，explicit 必須保持 authoritative，sealed Task 禁止 fallback。健康僅表達 admission eligibility，不引入品質、成本、load 或延遲排名。

## 現況分析

1. `InMemoryRegistry` 以註冊時的 ID 為 map key，Snapshot 在鎖內按 key 排序，但只回傳 Backend；後續重新呼叫 b.ID()，無獨立 authoritative key 欄位。
2. `CaptureRuntime` 先取 capabilities，再以 5 秒 context 呼叫 Version；一般 error 或空版本會返回錯誤。沒有 inventory 狀態、reason、panic recovery 或 snapshot 總 budget。context 本身不能強制停止不合作的函式。
3. `admitAutoRuntime` 逐一 capture 所有 registered runtimes，任一失敗即返回，無法保留其他 available candidates。
4. explicit `AdmitRuntime` 先用能力拒絕，再 capture 版本；#39 要 availability → capability，會調整同時不可用且缺能力時的錯誤優先順序。
5. `planStageBindings` 已讓三個 stage 共用 descriptors；每個 decision 保存自己的 candidate 值副本。plan 上限 192 KiB，selection 上限 64 KiB，candidate 上限 32。
6. `ExecutionContract.Validate` 分 capability-v1／stage-binding-v1；`ResolvedContext` 與 PipelineTrace 保存完整契約，無 inventory 欄位。
7. local auto 一律註冊 OpenCode，但 Codex/OMP/Antigravity 依 LookPath 才註冊；daemon 一律註冊 OpenCode/OMP/Antigravity，Codex 依 LookPath。相同電腦缺 binary 時，local 與 daemon 觀察的 registered 集合可能不同。
8. OpenCode Version 用 --version 並驗證固定版本；Codex Version 執行 --version，但 ExpectedVersion 比對位於另一個 ValidateVersion。兩者 Output() 先收集完整輸出，尚無 probe output 上限。
9. OMP Version 校驗 package layout、CLI 與 Bun hash；Antigravity Version 校驗 CLI hash。兩者現有檔案 hash 路徑沒有 context-aware 讀取，Version 不直接驗證登入。故 available 不能被描述成遠端 auth 成功。
10. OpenCode RoleRoutingBackend 的 Version 只優先檢查 Striker；要保證宣告的唯讀能力對應必要本機配置，需要 probe 同時檢查兩個 underlying adapter。
11. `guardStage` 使用 sealed ID 與當前能力，但版本檢查會忽略 Version error；實際 adapter Run 尚有自己的 preflight。#39 的 execution disappearance 必須在 guard／Run 邊界做負向驗證，不能只驗 ID 存在。
12. CLI status 已讀 sealed contract 與 trace；Web 經 daemon status 取得 Task。新 audit 應沿用此讀取路徑，status 不重新 probe。
13. queue/redelivery 保留封存 context；restart 保留既有 failed 恢復政策，本期不增加 retry。

## 問題點總結

- Registered 被直接當 descriptor candidate；一個不可用 runtime 會使整次 auto admission fail-closed。
- 探測缺乏總 budget、隔離、bounded output 與安全 diagnostic。
- audit 無法區分 availability rejection 與 capability rejection。
- 註冊集合與 adapter 本機驗證需要對齊，不能依健康結果動態註冊。

## 目標與邊界

解決 #39 R1～R16；具體政策見 technical-analysis，驗收及核准唯一來源為 README。維持既有優先序、Task.Backend Striker projection、Memory/Cron 等外部行為。本期不做 remote health、auth/token 探測、發送 prompt、background cache、runtime management 或 retry/failover。
