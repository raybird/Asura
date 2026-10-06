# Runtime 與執行控制

Asura 如何決定一個任務由哪個 runtime 執行、執行前檢查什麼，以及事後如何稽核。命令語法見 [CLI 參考](cli-reference.md)。

## Runtime adapter

抽象化 Runner 契約，支援 OpenCode、Codex、OMP、Antigravity 四種固定版本 runtime。OpenCode 固定 1.18.23、Codex CLI 固定 0.155.0。

2026-09-30：OMP 固定支援 CLI 18.4.4、Bun 1.4.2 與 `openai-codex/gpt-6-luna`；Antigravity 固定支援 CLI 1.2.13 與 `gemini-3.8-flash-low`。OMP 還會驗證並複製固定的完整 node_modules 樹（約 1.2 GB 暫存空間）；增加其他 global 套件也會使摘要不符，需使用相符的獨立安裝。兩者需要 Linux `bwrap` 的 PID／IPC namespace；版本或隔離條件不符會在模型呼叫前拒絕。沿用原 CLI 登入，只傳入 access-only 快照，登入過期須在原 CLI 手動更新。OMP 不傳入可用的 refresh token；Antigravity 先讀原 CLI keyring，再讀原 CLI token 檔。

2026-09-30：這兩個 backend 目前僅支援 Single Striker，不宣告唯讀 sandbox 能力，Three-Face 在 Stage 0 拒絕且不呼叫模型。Context window 未知時沿用既有政策；可用 `ASURA_CONTEXT_LIMIT` 指定限制。`status` 顯示任務保存的 backend 與 trace 中的版本。

## 固定三面管線 (Three-Face Fixed Pipeline)

2026-09-17：
- **模式語意與向後相容**：
  - `direct`（預設）：直通既有單一 Striker / Runner，完全維持 Phase 3B 的 Prompt、Memory 與 Redelivery 契約，僅產生 1 次模型呼叫。
  - `three-face`：固定依序執行 Oracle（規劃）→ Striker（執行）→ Critic（審查），各執行 1 次，總計 3 次模型呼叫。
- **共用不可變 Sealed ExecutionContext**：三角色共用同一 Task execution identity 與快照記憶，執行期間外部新增、修改或刪除記憶絕不破壞既有任務上下文。
- **結構化契約與 Fail-Closed**：
  - Oracle 產出結構化 `OraclePlan`（包含 Goal, Constraints, Steps, ExpectedOutcome），Striker 依據該 Plan 執行。
  - Critic 審查 Striker 產出並產出結構化 `CriticReview`（包含 Verdict, Findings, Summary）。
  - 嚴格 Fail-Closed：任一 Stage 失敗（或 Critic Verdict 判定 Failed）立即標記任務為 Failed，絕不靜默 fallback，保存已發生 Stage 的 partial output 與錯誤原因。
- **審計與成本追蹤**：所有任務持久化 `ExecutionMode` 與 `PipelineTrace`（包含各 Stage 狀態、DurationMs、呼叫次數與結構化 Plan/Review），支援 CLI `status` 格式化檢視。
- **Redelivery 冪等防禦**：請求指紋計算納入 `Mode` 參數；相同 Request ID 與相同 Payload 重送時立即回傳既有 Task，絕不重複觸發三角色執行。

## 能力感知 admission

2026-10-03：真正使用 backend runtime 的 Daemon／IPC／Web／Cron 與本機 runtime pipeline，在接受新任務前以共同 deterministic requirements／capability matcher 預檢。Direct 要求 Striker 的 workspace_write；Three-Face 另要求 Oracle／Critic 的 read_only_sandbox。缺少能力、未知 runtime／capability 或版本取得失敗時接受前拒絕，沒有模型呼叫或自動 fallback。

2026-10-04：新任務以 runtime-inventory-v1 契約保存於 Task resolved_context，包含各 stage requirements、requested／resolved runtime、版本、能力集合、UTC snapshot 與 match；舊 capability-v1 Task 沿原單一 runtime 路徑執行。無記憶任務也封存；重送先查原 Task，不重新探測。status 與 Web 稽核顯示保存值；舊 Task 未提供快照時明示 legacy/unavailable，不回填。執行前能力退化的拒絕分開記錄在 execution guard，不改寫接受時快照。

2026-10-03：本機 `run --backend opencode/codex` 保留既有 shell 命令執行，不呼叫 runtime 或封存其能力；CLI status 與 Web 稽核明示 `legacy/unavailable`。真正 runtime 的 engine、Daemon／IPC／Web／Cron 入口才封存 runtime 契約。

## 自動選擇與優先序

2026-10-03：`ask --backend auto` 與 `run --backend auto` 明確啟用能力感知 runtime 選擇；`run auto` 的參數是 AI prompt，其他既有 shell run 行為保持。先以共同 matcher 篩選，再依 `opencode` 優先、其他 ID 字典序、`omp` 最後的 deterministic policy 選擇一個 runtime；2026-10-04：Three-Face auto 改為同一份候選快照、共同排序按 stage requirements 各自選擇；explicit 仍讓全部 stage 使用指定 runtime，不相容不 fallback。

2026-10-03：可用 `ASURA_RUNTIME_PRIORITY=codex,opencode,antigravity,omp asura daemon` 或同環境變數啟動 `asura run --backend auto 'prompt'` 調整偏好。未列的候選按預設規則補齊，未註冊的偏好不新增候選；空項、重複或非四種已知 ID 拒絕。`ask` 使用 daemon 啟動設定，變更需重啟 daemon；不以 client 環境覆蓋。排序只能作用於 eligible set。

## Stage 級綁定

2026-10-04：`status`、pipeline trace 與 Web audit 按 stage 顯示封存 intent、candidate/match、eligible/rejected、selected runtime、policy/version、有效排序及 actual runtime／guard 結果。Task.Backend 代表 Striker 相容投影，execution 以完整 StageBindingPlan 為準。Direct 僅一個 Striker binding；Three-Face 順序固定 Oracle/Striker/Critic。plan 最多三個 binding、每 decision 32 candidates／64 KiB、整份 plan 192 KiB，超限拒絕不截斷。

2026-10-04：例如 daemon 以 `ASURA_RUNTIME_PRIORITY=omp,codex` 啟動，已註冊 OMP 僅支援寫入、Codex 支援唯讀與寫入時，auto Three-Face 可綁定 Codex → OMP → Codex（各綁定 runtime 必須 available）。全部階段在接受時一次封存，任何必要 stage 無候選整次拒絕；執行前檢查所有 bindings，逐 stage Run 前再檢查安全能力，缺失或退化不重選、不 fallback。版本更新不要求與歷史 snapshot 完全相等。registry／priority 變更不改已接受 Task，queue／redelivery 使用原 plan；daemon 重啟沿既有規則將殘留未完成 Task 標 failed，保留 plan 而不自動續跑。契約一致性檢查不等同防止完整資料庫內容被一致重寫的密碼學驗證。

## 可用性清冊

```bash
./bin/asura runtime inventory
./bin/asura runtime inventory --socket /path/to/asura.sock
```

2026-10-04：Daemon 與 local auto 明確註冊 OpenCode/Codex/OMP/Antigravity，缺 binary 也保留 registered entry。每次 admission 使用 fresh inventory，先排除 unavailable/unknown，再做 capability filter 與原 deterministic priority；單一版本失敗／逾時不污染其他候選。explicit 只探測指定 runtime，不可用則帶 reason 拒絕，不 fallback。最多 32 entries、每 probe 3 秒、整批 5 秒，timeout 為 unknown；panic/error 為 unavailable。snapshot 排序與序列化不受完成順序影響。Web task API 支援 additive `backend:"auto"`，沒有新增設定編輯頁。

2026-10-04：Web 的「Runtime 可用性清冊」與 `GET /api/runtime-inventory` 提供已登入使用者唯讀查詢。Task status/audit 顯示該次封存 inventory、availability reason、capability rejection、stage selection/binding 與實際 guard；不把 fresh 狀態回填到舊 Task。legacy capability-v1／stage-binding-v1 保留原讀取語意，inventory 顯示未封存。

2026-10-04：Available 僅代表必要本機 executable、固定版本／hash、配置及依賴有效；probe 不呼叫模型、不讀取 token、不保證遠端登入、配額或 Run 成功。sealed runtime 消失或本機條件失效，執行前拒絕且不重新選擇；版本更新仍沿用 adapter 既有政策，不要求等於歷史版本。Inventory JSON 最多 64 KiB，完整新契約最多 256 KiB；diagnostic 為固定安全訊息（最多 512 bytes），不保存原始 stderr/error/environment。

Runtime inventory／binding 最後驗證日期：2026-10-04；證據見 [Issue 39](../issues/issue-39/implementation-plan.md)。

## 執行稽核

```bash
./bin/asura audit --id <task-id> --db asura.db
./bin/asura audit --id <task-id> --socket /path/to/asura.sock --json
```

2026-10-04：`audit` 讀取 `execution-audit-v1` 持久化資料，顯示 intent、接受時 availability／能力篩選／priority／policy、各 stage 的 sealed／guard observed／confirmed actual runtime，以及獨立的 stage outcome 與 Task terminal。Web 任務頁提供相同摘要，HTTP 入口為已登入的 `GET /api/tasks/{id}/audit`。查詢不重新 probe、選擇或執行 runtime；daemon 路徑沿用 workspace 授權，授權失敗不改走本機 DB。

2026-10-04：run intent 只代表執行意圖；confirmed return 才代表已確認的 runtime 返回，未返回顯示 unknown。模型內部呼叫數固定標示 unknown。crash recovery 保留已完成 stage 與未完成 prefix，呈現 partial；升級前 Task 無 audit header 時呈現 legacy，不從 trace 補造歷史；capability-v1 的候選 match 是原本全 Task 的一次決策，不能解讀為新的 per-stage 選擇。稽核只保存有界 enum、descriptor、digest 與 lifecycle，不保存 prompt、memory、output 或任意 error。結構與 hash chain 偵測不一致，不提供不可否認性；CLI 本機 DB 的存取權由作業系統控制。

## 上下文窗口管理與 Token 審計

2026-09-21：
- **能力探測優先與真實邊界**：固定版本 OpenCode 與 Codex CLI 經過 Contract Probe 驗證，明確標記 unsupported 能力，不推測或假裝擁有模型內部 Context 編輯權。
- **動態 Stage-Aware 預估與准入 (Admission)**：
  - Oracle、Striker（帶入動態 OraclePlan）與 Critic（帶入 OraclePlan + StrikerOutput）各自進行執行前（Preflight）獨立上下文預估。
  - 透過環境變數 `ASURA_CONTEXT_LIMIT`（正整數 Token）與 `ASURA_CONTEXT_POLICY`（`observe` 觀測警示 或 `reject` 拒絕）進行程序級預算控制。
- **Fail-Closed 阻擋防禦**：在 `reject` 政策下，超量 Stage 於調用後端前直接拒絕，模型呼叫次數為 0，且絕不自動更換 Backend，前面已完成之 Stage 產物與 Trace 完整保留。
- **數據來源可信度 (Provenance) 與不可變性**：
  - 嚴格區分 `exact`（後端實測）、`estimated`（啟發式估算）與 `unknown`（缺失），絕不將未回報數值捏造為 0。
  - 預算檢查維持 Sealed Context 不變性，絕不私自刪剪記憶或 Prompt。
