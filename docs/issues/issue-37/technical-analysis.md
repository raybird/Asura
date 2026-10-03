# Issue 37 — 技術提案

2026-10-04 使用者「核准」，完整提案已核准。以 requirement-analysis 的現況及 README 驗收為依據。

## 可行性與方案取捨

沿用 resolveSelection 純值函式、RequirementsForMode 及 JSON context persistence 即可，不需新增 router、database table 或 stage registry。

| 方案 | 取捨與提案 |
|---|---|
| 每 stage 重抓 registry 或共用值快照 | 採整 Task 一次 membership snapshot/capture；分次抓可能看到不同候選/version，且重複外部探測 |
| 完全取代舊 contract 或版本分流 | 新 contract `stage-binding-v1` 加 StageBindingPlan，舊 capability-v1 維持 single-runtime 執行；避免 queue 舊 Task 被重新 admission |
| 分離共用 candidates 或重用完整 decision | 採每 binding 重用完整 Phase 4B decision；最多三份、有總量 gate。去重 reference schema 會另建 selection representation，暫無必要 |
| Task.Backend 改 auto/mixed 或保留 primary | 採 Striker ID 相容投影，requested intent 留在 contract；不將 auto/mixed 當 registry ID |
| 每 stage policy 或共同 priority | 採同一有效 priority 及現有 policy；capability eligibility 即可形成 mixed，不增加設定面 |

```diff
 admission: authorize / redelivery lookup
- requirements union → one runtime contract
+ one registry value snapshot → per-stage requirements → all bindings or reject
 persistence: atomic resolved_context + intent fingerprint
- execution: Task.Backend → one BoundBackend for all stages
+ execution: validate complete plan → resolve/guard all bindings → stage-specific BoundBackend
+ immediately before each Run: recheck that stage identity/capability; never select/fallback
```

## SB-TBD 定案提案

| ID | 最小契約與實作方向 |
|---|---|
| SB-TBD-1 | `StageRuntimeBinding{Stage backend.Role, Requirements ExecutionRequirements, SelectionDecision RuntimeSelectionDecision}`；selected runtime/capability match 從 decision.Selected 及 selected candidate.Match 讀取，不再重複獨立 selected 欄位。`StageBindingPlan{Mode, Bindings}`，policy/version 在各 decision 封存，不新增 planner policy |
| SB-TBD-2 | 重用 bounded RoleOracle/RoleStriker/RoleCritic；驗證精確數量、role、順序且不得重複；Direct 僅 Striker，Three-Face 固定 Oracle/Striker/Critic |
| SB-TBD-3 | RequirementsForMode 的每個元素直接作 authoritative input，禁止降低 read-only、添加虛構 capability 或任意 stage |
| SB-TBD-4 | auto 一次 membership Snapshot，每個 backend 捕獲 descriptor 一次；explicit 只捕獲指定者一次。capture 完成後所有 stage 重用相同值，timestamp 不參與 selection |
| SB-TBD-5 | 各 stage 以單元素 requirements 呼叫 resolveSelection，不重寫 matcher/effectivePriority。Validate 用封存 candidates/priority 重建 decision，是結構查核，不是拿當前 registry 重選 |
| SB-TBD-6 | explicit 全 stage 指定同 ID，任何 stage 不相容在 Version capture/Run 前拒絕；省略仍 opencode |
| SB-TBD-7 | 沿 RequestedBackend + decision.Request explicit/auto；auto CLI/IPC/Web 原入口不變，不新增 per-stage override |
| SB-TBD-8 | Task.Backend、context.Backend、contract.Runtime 為 Striker selected 投影；contract.Match/SelectionDecision 同為 Striker 投影，Requirements 保留固定完整 stage 列表。stage-binding-v1 validation 必須核對投影一致，stage 執行只信 plan；舊 capability-v1 保留原 union Validate |
| SB-TBD-9 | StageBindingPlan additive 存 ExecutionContract，經 ResolvedContext 同次 CreateTask 封存。版本 stage-binding-v1 必須有 plan，capability-v1 不可夾帶新 plan，未知版本拒絕；禁止 plan 缺失後降級 legacy |
| SB-TBD-10 | orchestrator 在第一個 Run 前解析所有封存 bindings 並 guard；每 stage 用局部 context copy 設 BoundBackend。新契約若不能取得正確 binding，直接錯誤，不回 constructor/runner。每 stage Run 前再查 registry 身分及 capability，捕捉先前 stage 期間移除/退化；不 hold registry lock 跨 Run |
| SB-TBD-11 | 沿既有 safety semantics：current ID 必須一致、capability 仍滿足 stage requirement；版本更新不要求精確相等。trace 區分封存版本與實際可取得版本，查詢失敗不偽填歷史版本為 actual。未知 capability fail-closed；不新增 availability probe |
| SB-TBD-12 | 維持每 decision 32 candidates/64 KiB；plan 精確 1 或 3 bindings，plan JSON 最大 192 KiB，超限拒絕不截斷。舊欄位的 Striker decision 投影另至多 64 KiB；完整 contract 的 candidates 部分有限但不宣稱整份 context 有 192 KiB 上限 |
| SB-TBD-13 | additive stage guards/actual IDs 至 StageTrace；trace 帶完整 contract。CLI/Web 按固定 stage 順序呈現 intent、sealed selected、requirements、eligible/rejected、policy/priority、execution guard 與 actual runtime；查詢不呼叫 Version/Run |
| SB-TBD-14 | fingerprint 沿 requested backend intent，不納入 selected IDs/version/priority；已存在 request 先返回不 capture。正常 queue 讀回完整 plan，restart 沿 failed recovery 且保留 plan；cancel 不新建 binding，不引入 retry/resume |
| SB-TBD-15 | 新 Direct admission 同樣封存單 Striker plan，結果不變。舊無 plan capability-v1/無契約 runner 路徑維持既有相容，兩種契約明確版本分流，不以新 plan 缺失推定舊路徑 |

## 一致性查核與安全邊界

plan Validate 查核 bounded stages、canonical requirements、request intent、共用 candidates descriptors、封存有效 priority、各 decision 重建結果、selected match 與 Striker 投影。單欄改動、缺漏、重複與未知版本必須拒絕。這是現有 Phase 4A/4B 的一致性保護，不是 cryptographic tamper evidence；一致重寫完整 plan 與其所有依據仍可能自洽。若需求包含防任意 database attacker，須另核准可信摘要／金鑰管理設計，不能冒稱本提案已涵蓋。

所有必要 bindings 初始 guard 通過後才准第一個 Run；先前 stage 期間若後續 runtime 改變，只能在後續 Run 前失敗，不能承諾撤銷已完成的 side effects。共享 snapshot 固定 membership 與已捕獲值，不宣稱跨外部 binary 的版本捕獲在同一瞬間完成。

auto 任一 registered candidate Version failure 仍整次拒絕；不排除「不可用」候選、不 fallback、不提前 Phase 4D。plan 純函式 determinism 使用固定 descriptors，包括 timestamp；各次真實 capture 時間不同，不要求 JSON byte equality。

## 架構影響與待決事項

控制平面仍只決定 known stage runtime；stage 本身負責 prompt/result parsing，adapter sandbox 不變。要改 pipeline、admission、context、audit，因為目前都假設單一 runtime；不新增服務。常青文件在實作交付時更新。

2026-10-04 所有 SB-TBD-1～15 均已由使用者「核准」確認。README TBD-1～4 涵蓋新契約、上限、atomic fixture 與 restart/tamper 邊界；由 decompose 在原計畫補入可執行 Tasks。
