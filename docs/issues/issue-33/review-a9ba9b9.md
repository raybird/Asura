# Issue 33 獨立審查報告

- 審查日期：2026-10-03。
- 範圍：局部功能審查；讀取下列固定 BASE..HEAD 的完整變更，判定限於 RTCAP-001～015 與 RTCAP-016 的真正 runtime 路徑。
- Reviewed BASE SHA：`41b5220b2100ad85fdb4913c7c8e73ea57657464`。
- Reviewed HEAD SHA：`a9ba9b9723ec7d48144aec7cf42ce47392aa638d`。
- Reviewed patch-id：`c5ea8fdee75a422e526fd39fbb3fb3518f2d9d24`，由完整 `git diff BASE HEAD | git patch-id --stable` 取得。
- 獨立 reviewer：Codex 隔離 reviewer task `/root/review_runtime_contract`；不是原實作者。
- Review artifact：`docs/issues/issue-33/review-a9ba9b9.md`。
- 風險：High；接受前唯讀能力拒絕、immutable admission snapshot、執行前安全退化與版本 subprocess 期限。

## 問題與風險

### MUST FIX：OpenCode admission 版本探測實際未受 caller deadline 約束

位置：`internal/backend/requirements.go:140`、`internal/backend/opencode/adapter.go:190`、`:223`、`:339`（固定被審查 HEAD）。

`CaptureRuntime` 建立五秒 probe context，但 `Adapter.Version` 先呼叫 `ResolveOpenCodeBinary`，後者透過 `ValidateOpenCodeVersion` 使用不帶 context 的 `exec.Command(..., "--version").Output()`。只有第二次版本呼叫使用 `exec.CommandContext`。因此第一個 subprocess 若停住，新的同步 admission 仍可無限等待；caller 取消也不能結束這段探測。這違反已核准 RC-TBD-4「沿 caller context、有有限期限」的契約，影響 Daemon／IPC／Web／Cron 的預設 OpenCode 新任務接受路徑。雖然 adapter helper 原本存在，新增同步接受點使此缺口成為本次交付的必要整合責任。

獨立靜態查核已確認上述完整呼叫鏈。協調者另於原 HEAD 加入未提交的針對性測試，以 `--version` 執行 `exec sleep 2` 的可執行 fixture 與 50ms caller deadline 重現：`go test -count=1 ./internal/backend/opencode -run '^TestCapabilityVersionProbeDeadline'` exit 1，實際耗時 `2.00130274s`。原始紅燈保存於 `evidence/version-deadline-red.log`；測試與該 log 是原 HEAD 之後取得的補充重現證據，不冒充已提交 HEAD 的既有測試。

建議讓 Version 的 resolve／validate 全程使用 caller context，保留既有公開 helper 的相容介面；以真 subprocess 取消測試證明取消及 timeout，修復後固定新 HEAD 重新查核。此報告不包含後續修復的審查結論。

### SHOULD FIX／NICE TO HAVE

- NICE TO HAVE：`internal/web/page.go:654` 的稽核 renderer 僅驗 version 與 runtime/match 存在，CLI 則透過 `ExecutionContract.Validate` 查核完整結構。可增加壞掉的 role、requirements、match fixture，讓 Web 同樣清楚顯示 unavailable。正常 admission 產生的 JSON 已受共同驗證；此處未證明可由產品提交入口產生不實契約，列稽核防禦與證據持久力建議，非 MUST FIX。
- NICE TO HAVE：`implementation-plan.md` 上方 Phase 表仍標 P1 待實作，但下方 P1-T1 已完成；交付時同步摘要，避免讀者誤解狀態。
- 三個 stage 的 BoundBackend 優先選擇分支為相同短邏輯，尚無分歧或額外抽象需求；未列為阻塞問題。

## 已查核維度

### 核准、範圍與證據

已讀專案 AGENTS、流程契約 2.0、acceptance、verification、review-evidence、project 與 issue README／technical-analysis／implementation-plan。2026-10-03 使用者對 `665e409` 方案與 16 項驗收的明確核准由協調者提供，文件核准來源相符。RC-TBD-11 與 RTCAP-016 shell 子範圍維持待決／待重新核准；既有 opencode/codex 本機 shell run 與其決策不在本次 PASS 可適用範圍。誠實揭露該限制不構成不實回報。

審查初期核對 `tested-source.sha256`，18 個被修改 Go source／test 檔全數 OK，與當時固定 HEAD 的提交內容一致。`full-race.log` 記錄 19 個 package 成功，命令及 exit 0 由 `implementation-notes.md` 記錄；`full-vet.log`／`full-build.log` 為成功無診斷輸出。未自行重跑全 suite。後續協調者修復產生的工作區差異與 final-* 證據不套用到本報告的舊 HEAD。

| 驗收 | 已查核證據／程式責任 | 判定 |
|---|---|---|
| RTCAP-001 | engine local runtime 測試、既有 Direct regression；接受前共同 preflight，Striker 單次 | 已查核，未發現其他阻塞 |
| RTCAP-002／010 | backend policy 與 matcher 字面值期望、排序／去重／slice mutation | 已查核 |
| RTCAP-003／006／009 | 真 IPC／SQLite rejects、sentinel、全部 backend Run=0、raw requested identity、無 fallback | 已查核 |
| RTCAP-004／005 | Three-Face union、既有 OMP／Antigravity 能力矩陣、三 stage BoundBackend 測試與角色序列 | 已查核 |
| RTCAP-007／008 | persisted context 優先、restart／redelivery／queue bytes equality、並行唯一 winner 及單次 Run | 已查核 |
| RTCAP-011 | CLI SQLite 前後 JSON 不變、Web legacy renderer、查询不探測 runtime | 已查核；Web malformed 結構建議如上 |
| RTCAP-012 | 固定被測來源 checksum、full race／vet／build 與 notes 的實際命令／結果 | 已查核既有紀錄；不代表版本期限案例已覆蓋 |
| RTCAP-013 | matcher unknown role/access/capability、fixed JSON 未知 contract/policy 與 invalid match guard | 已查核 |
| RTCAP-014 | 版本 error／empty 拒絕的實作與現有 descriptor 測試；真實 OpenCode subprocess deadline | MUST FIX：期限不成立 |
| RTCAP-015 | runtime 消失、能力退化、相容升級；Prepare／Run 前 guard 與保留 admission JSON | 已查核 |
| RTCAP-016 真正 runtime 部分 | 無記憶 IPC、engine、Cron 封存；status／IPC Task／Web 保存值 | 已查核；shell 子範圍明示排除 |

### 重要失敗面與品質

- 輸入未知 mode／role／workspace access／capability：共同純 policy／matcher 拒絕；執行契約 version、policy、identity、requirements、match 重新驗證，失敗在 stage Prepare 前停止。
- explicit A 能力不足而 B 足夠：lookup 固定 A，不呼叫 Registry.Default；缺能力拒絕帶 required／available／missing，所有 Run=0。
- 佇列接受後 V1→V2／能力退化：worker 解碼 persisted context；guard 使用目前能力查封存需求，成功升級不改 admission，退化保存獨立 guard error。
- 同 request 並行／已存在 request 重送：lookup 在 descriptor 前；SQLite unique insert 敗者回傳 winner，不覆寫 snapshot。授權、fingerprint conflict、cancel 保護仍存在；現有全回歸涵蓋這些責任。
- Cron：先授權 canonical workspace 與 preflight，再取得 capacity／CreateTask，無記憶也保存 JSON；共用 worker guard。
- 三舞台間 registry 替換：BoundBackend 固定同一 instance，真 stage backend mock 驗證 Oracle／Striker／Critic 均呼叫 A，B=0。
- 狀態查詢：CLI 與 IPC 無 backend probe；Web 以 textContent 寫入，未引入 HTML 注入。舊資料不回填。
- 測試真偽：guard 使用獨立固定 JSON，IPC storage 與 matcher 未被 mock；既有測試改為同步拒絕符合核准新行為且保留零 Run 判準。Web Node DOM seam 未替代 renderer，自行揭露真人瀏覽器未涵蓋；非假綠燈。
- 架構／過度設計：需求型別集中 backend，policy/matcher 共用，使用既有 JSON storage，沒有 schema／routing／health／MCP discovery 擴張。daemon、engine 的序列化責任對應各接受入口，未發現造成行為分歧的跨 Task 重複。

### 未完成與限制

Reviewer 的額外 `/tmp` 最小實際 adapter 探測因本機 snap sandbox 限制無法執行，升權呼叫已中止，未把它當成成功證據；上述期限重現由協調者實際測試 log 支持。沒有進行真人瀏覽器操作、真模型呼叫或新的 live binary／認證探測；原 suite 的 live probe 可用條件仍沿原測試。除此之外未新增額外壓力／大量並行實驗。這些未執行項目不宣稱完成。

## 流程判定

**RETURN TO execute-task**：原固定 HEAD 存在版本探測期限的 MUST FIX，須修復並補針對性驗證，再由獨立 reviewer 查核新固定 HEAD。報告已持久化，但不構成任何範圍的 PASS。

即使之後 runtime 局部範圍通過，也不能拿該結論作為整個 issue／PR 閉環 PASS；RTCAP-016 shell 子範圍仍須依 RC-TBD-11 的實際核准狀態處理。
