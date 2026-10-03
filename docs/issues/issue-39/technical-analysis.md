# Issue 39 — RI-TBD 技術提案

2026-10-04 提案；以下均待 README 的核准紀錄生效。

## 技術可行性與方案取捨

選擇獨立的 backend inventory 模組，Registry 保留唯一 adapter 登錄責任；probe 不透過 Run。相比把 availability 當 Registry 的長期狀態，每次 admission fresh snapshot 避免 TTL freshness 與 cache race。相較依序探測（32 × 3 秒），有界並行降低壞 runtime 對 admission budget 的影響。

```diff
 Registry authoritative snapshot
- sequential CaptureRuntime; any error aborts all
+ bounded concurrent probe; retain every result
+ one immutable inventory snapshot
+ only available descriptors
 capability filter -> deterministic priority -> StageBindingPlan
```

保留 StageBindingPlan 既有 candidate 值副本，上限固定，避免本期連帶重寫 selection contract。inventory descriptor 只在 inventory 中保存一次；重複資料仍受完整 contract 上限控制。若改成各 stage 僅保存 descriptor reference，需重寫舊 validator／audit，本期不採用。

## RI-TBD-1～15 定案提案

| ID | 提案 |
|---|---|
| RI-TBD-1 | InventorySnapshot：contract_version、policy_version、observed_at、entries。Entry：Registry runtime_id、registration_state、availability_state、descriptor?、observed_at、probe_kind、reason_code、diagnostic。身分沿用 map key；Registry 原子快照需同時提供 key 與 Backend reference，不建立第二 registry。 |
| RI-TBD-2 | Available：所有必要本機檢查成功且 descriptor 有效；Unavailable：確定的本機失敗、一般 error、panic、descriptor invalid；Unknown：未探測、timeout、取消或全域 budget 到期。僅 available 有 descriptor。無 transitional/ranking state。 |
| RI-TBD-3 | 獨立 inventory orchestrator；optional RuntimeProber.Probe(context.Context) 提供 adapter 本機探測，無此介面者用 bounded Version + Capabilities capture。保留 Backend 介面相容；四內建 adapter 實作專用 probe，非 production fallback 到 Run。 |
| RI-TBD-4 | version、capabilities、descriptor validation 都在單次 probe boundary 內；每個 runtime 各自 recover panic。Registry key 與 b.ID() 不符為 descriptor_invalid；不可由 probe 改名。 |
| RI-TBD-5 | 每個 runtime 3 秒、whole inventory 5 秒，上游較短 deadline 優先；固定政策常數，不新增使用者設定。全域 timeout 尚未完成者 unknown/inventory_timeout，仍可用已完成 available；上游取消則整次 admission 拒絕。 |
| RI-TBD-6 | 至多 32 runtimes，各 runtime 一個並行 worker。按 authoritative ID 收集／輸出，capabilities canonical sort；同份 snapshot 統一 UTC observed_at／CapturedAt，避免 completion order 影響序列化。不同真實時間的 fresh probes 不要求 JSON 完全相等。 |
| RI-TBD-7 | 每次 admission fresh；無 TTL、daemon scheduler 或 cache。standalone inventory 查詢也 fresh 且有時間戳；Task status 只看原封存 snapshot。 |
| RI-TBD-8 | 新 contract_version runtime-inventory-v1，policy_version capability-v1，inventory 自帶 inventory-v1 policy；StageBindingPlan 形狀維持。inventory 放 ExecutionContract 一次，stage candidates 仍是 available descriptor 的有界副本；Validate 從 snapshot 重建 candidate 集合與既有 planner 結果驗證一致性。 |
| RI-TBD-9 | Auto 排除 unavailable/unknown，再能力過濾與既有 deterministic priority。explicit 先確認已註冊，再 probe 該 ID；不可用返回 typed admission error，包含 bounded snapshot/reason；不因其他 ID 可用而 fallback。explicit 不 probe 無關 ID，snapshot 表明本次只觀測被指定 runtime。 |
| RI-TBD-10 | Local/daemon 共用 inventory service 與 adapter probe；四內建 ID 在 composition root 明確註冊，缺 binary 也有 unavailable entry。這是固定配置，不是 dynamic discovery；保留既有安全設定與 callbacks。 |
| RI-TBD-11 | bounded reasons：none、not_probed、executable_not_found、dependency_not_found、configuration_invalid、version_failed、descriptor_invalid、probe_error、probe_panic、probe_timeout、probe_cancelled、inventory_timeout。adapter 用 typed errors 分類，不靠 stderr 字串猜測。 |
| RI-TBD-12 | 32 entries；ID ≤64 bytes；version ≤256 bytes；diagnostic ≤512 bytes；inventory JSON ≤64 KiB；new ExecutionContract JSON ≤256 KiB。保留 selection ≤64 KiB 與 plan ≤192 KiB。超長 ID/version 或未知 enum 拒絕有效 descriptor／契約，不截斷 authoritative ID。diagnostic 僅固定安全模板，不持久化 raw error/stderr/environment。 |
| RI-TBD-13 | 每個 probe recover，panic unavailable/probe_panic，不保存 panic value；timeout unknown。worker buffered result，observer 不等永不合作的函式；內建 probe 必須 context-aware，外部子程序採進程組清理與有界 wait、bounded stdout/stderr。取消後不建立 Task，測試確認沒有 prompt/寫入。 |
| RI-TBD-14 | 新 asura runtime inventory（daemon 可用時 IPC，否則 local 共用 probe）；daemon inventory action；Web GET /api/runtime-inventory 唯讀 fresh snapshot；Task detail/status/trace 展示 sealed snapshot、availability exclusion、capability rejection、selection/binding/guard。不新增管理 UI。 |
| RI-TBD-15 | 保留 capability-v1／stage-binding-v1 validators 與 legacy 讀取行為；新契約辨識需同步所有以 StageBindingContractVersion 判斷的 engine/pipeline 分支。舊任務 inventory 顯示未封存，不 backfill/reprobe；queue/redelivery/restart 沿用封存契約及既有恢復政策。 |

## Probe 安全界線

2026-10-04 提案的 available 是本機 admission evidence，不保證 provider 登入、網路、配額或實際 Run 成功。不讀取／記錄 token，不使用 prompt。OpenCode 驗證兩個角色 adapter 必要設定與版本；Codex 納入 ExpectedVersion 比對；OMP 校驗 layout、CLI/Bun；Antigravity 校驗 CLI。缺配置或版本不符採 unavailable。

Version subprocess 只使用明確 --version 契約；不允許 prompt、workspace mutation 或 execution callbacks。constructor/setup 既有 isolated config 建立屬 composition，不在 probe 中新增。檔案 hash 讀取需在 chunks 檢查 context，避免現有非 context-aware hash 壓過 timeout。

Go context 無法強制殺掉任意不合作的 in-process 函式；inventory observer 可有界返回，但「所有 probe 結束」保證適用遵守契約的 adapter。內建 adapter 全部需證明取消後結束；不把 goroutine timeout 當作資源回收已完成。此限制不得被宣稱成可安全接納任意第三方程式碼。

## 架構與執行影響

admission snapshot 為註冊集合的同一次觀測批次，不承諾外部環境在整批探測期間完全靜止。Available descriptors 是唯一 selection 輸入；availability reason 不混入 capability missing list。新契約驗證查 enum、counts、sizes、descriptor identity、shared snapshot 與 StageBindingPlan 一致性。

execution guard 只查 sealed ID 與現在必要本機條件，禁止全 inventory 重選。版本錯誤必須被明確處理而非忽略；不新增版本相等鎖定政策，維持 Phase 4C 的安全能力／身份與 adapter 既有版本政策。adapter execution 錯誤仍是失敗，不 retry。

admission error 的 snapshot 隨錯誤返回，CLI/IPC/Web 可立即稽核；拒絕時無 Task，因此不假造已封存 Task，也不新增 rejected-task table。成功 Task 的 audit 由 ResolvedContext 與 trace 保存。無新資料庫 schema。

## 待釐清

README TBD-1～5 是核准入口，包含 auth 邊界、budget、size／normalization 與 audit surface。以上政策全部屬提案，2026-10-04 尚未寫入 production code。
