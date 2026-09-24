# Issue #21 PR #22 獨立審查報告

- 日期：2026-09-23
- 範圍：完整 PR，TARGET `main`；[PR #22](https://github.com/raybird/Asura/pull/22)
- Reviewed BASE SHA：`9d5c2a3ecae3a59341ec63b69fb9e573ef1b5c79`
- Reviewed HEAD SHA：`682193c5f5e332303c85dda0ef918fcd8823672b`
- Reviewed patch-id：`2eca1afba10da3d7e3f4eb32c3a8bab31f0db0d7`
- 獨立 reviewer：Codex 隔離 reviewer `/root/issue21_independent_review`（非原實作者）
- Review artifact：`docs/issues/issue-21/review-682193c.md`
- 版本核對：本機 HEAD 與 PR source HEAD 均為上述 SHA；工作區在建立本報告前無交付內容差異。固定 BASE..HEAD 含 21 個異動檔；歷史 `review-9d5c2a3.md` 不作本次通過證據。

## 問題與風險

### MUST FIX

1. **HTTP 5xx 後無法從 UI 重送原請求，違反 G4／WEBMEM-007。** [page.go](../../../internal/web/page.go#L380) 的 `sendTask` 只在 `fetch` 拋例外時顯示 retry 按鈕；HTTP 503／500 進入 `else`，留下 `pendingRequest` 卻不解除按鈕的 `hidden` 類別（第 402～411 行）。使用者無法透過核准的「原請求重試」操作再次送出相同 Request ID／payload，只能另按「提交任務」建立新 ID；若第一次請求實際已被接受而回應失敗，可能重複執行。以現有 [UI 探測](evidence/ui-probe.cjs#L75) 暫時將 `/api/tasks` 的 `throw` 換成 `{status:503,ok:false,text:...}`，並在首次提交後斷言 retry 按鈕可見，實際結果 `true !== false`，Node assertion 失敗。原探測的 retry 模式只覆蓋網路例外，且程式化 `.click()` 不檢查按鈕是否隱藏。請讓 5xx／回應未定狀態顯示可用的原 payload 重試入口，保留已知 4xx 的衝突處理，並將 503 可見性納入 UI 回歸。

### SHOULD FIX

- [UI 探測](evidence/ui-probe.cjs#L94) 對 retry 按鈕直接觸發 `.click()`，即使處於 `hidden` 也能通過 payload 相等斷言。增加可見與可操作斷言可提高日後守門持久力。現有網路例外路徑的綠燈有效，這是覆蓋缺口，非假綠燈指控。

### NICE TO HAVE

- 無。

## 已查核維度

### 驗收與證據

固定規格：[README](README.md#gherkin-驗收劇本)、[G1～G5](technical-analysis.md)；Task 證據：[implementation-plan.md](implementation-plan.md)。核准來源為 2026-09-23 對話中的「$dev-cycle 核准」及「核准 G-TBD-1～5 現行提案」。核准基線 `21aafa8` 到被審查 HEAD 的 Gherkin 行為未見語意修訂；G-TBD-1～5 均有明確核准紀錄。PR Proof of Test 列出 WEBMEM-001～010 全集，無豁免。

| Scenario | 實作與證據核對 | 結論 |
|---|---|---|
| WEBMEM-001 | `ServeHTTP` 共用 session／Host／變更方法 Origin；`TestWEBMEMHTTPBoundary` 用真實 HTTP→IPC→SQLite 驗證拒絕後資料與 backend 計數，合法 GET 可讀；SameSite／同源讀取限制按 G1 區分。 | 通過 |
| WEBMEM-002 | Daemon 新 action 每次 canonical 授權；`TestDaemonBoundedMemoryIPC` 涵蓋父子、別名、越界、撤權與跨 scope ID 同 404。 | 通過 |
| WEBMEM-003 | SQLite keyset、50+1、欄位 sentinel；Daemon 編碼前及 Web JSON 上限；storage／IPC／10k HTTP 測試與 query plan 證據。 | 通過，保留 G2 的 SQLite 內部 I/O 證據限制 |
| WEBMEM-004 | Web CRUD、`manual` 來源、失敗不回 204；SQLite 重啟與已密封 Task 快照沿用既有測試，UI 確認刪除 probe。 | 通過 |
| WEBMEM-005 | Web DTO 僅轉送 IDs／mode，Daemon 解析並密封；跨 scope／缺失／已刪除 ID 在 backend 前拒絕，有 integration 計數。 | 通過 |
| WEBMEM-006 | 空選取及未啟用維持 off；明確 bounded 走既有 selector，`TestWEBMEMBoundedOnly` 觀察保存的 mode／policy。 | 通過 |
| WEBMEM-007 | Daemon 重送同 Task、指紋衝突 409 及網路例外的 UI 原 payload 重試有證據；HTTP 5xx UI 分支缺少可用重試入口。 | **未通過** |
| WEBMEM-008 | UI 從 Task 保存的 context／trace 顯示 IDs、policy、score、skip、stage admission；probe 覆蓋 legacy／off／explicit／0 auto／unknown／estimated，刪除後快照由整合與既有 Daemon 測試支援。 | 通過；G5 既有 status 大小限制已揭露 |
| WEBMEM-009 | 記憶及稽核使用 `textContent`；generation 丟棄舊 workspace／session 的 detail／Task 回應，清除選取與待重送 payload；UI probe 及 session HTTP 測試。 | 通過 |
| WEBMEM-010 | 計畫記錄固定 `0869a60` 的 vet、race 全套、build、UI probe exit 0；本次又執行 `go test ./tests/integration -run '^TestWEBMEM' -count=1` exit 0、`node docs/issues/issue-21/evidence/ui-probe.cjs` exit 0。 | 回歸命令有證據；整體仍受 007 缺口阻擋 |

### 相關失敗面

| 輸入／狀態 | 預期 | 覆蓋與判定 |
|---|---|---|
| 無 session、跨 Origin POST／DELETE、跨站 GET | 拒絕變更與無 cookie 讀取；GET 不被誤寫成服務端 Origin 拒絕 | Web 入口、integration、SameSite 設定；通過。真實瀏覽器未自動化，已在 PR 揭露。 |
| A/B、父子、symlink 改指向、缺失 ID | 每次 canonical 授權，精確 scope，缺失與跨 scope 同外部結果 | 真實 IPC／HTTP fixture；通過。 |
| 10k 筆、同時間戳、長 content、超限 metadata/detail／body、JSON 逸出 | 50+1 keyset 與 bytes 上限；detail 在 IPC 前拒絕 | storage／IPC／HTTP 測試及編碼前長度檢查；通過。SQL 投影界線不證明 SQLite 引擎內部 BLOB I/O，計畫已如實揭露。 |
| 回應遺失後同 ID／變更 payload／已刪 ID | 原 payload 同 Task、不同 payload 409，無額外 backend | integration 與 UI 網路例外 probe；通過。 |
| **HTTP 503／500 後待重送** | 顯示可用的原請求重試，避免建立新 ID | 暫存 UI 503 變體可重現隱藏按鈕；**未通過**。 |
| 切換 workspace／失效後遲到回應與腳本樣式內容 | 不回填或執行，不誤送選取 | generation、`textContent` 及 UI probe；通過。 |
| 歷史 Task context／trace 很大 | G5 核准沿用既有 status 路徑並揭露限制 | 文件與程式一致；非本次阻塞項。 |

需求與架構：HTTP 不直接存取 SQLite，Daemon 保持 canonical 授權，CLI 全量 `memory_list/get` 契約未變；README／架構文件對應新增資料流。安全與權限：未發現新增跨 scope 讀寫或 client 密封 context 進入執行路徑。品質與測試真偽：紅燈記錄對應新增行為，真實 SQLite／IPC／HTTP 測試沒有 mock 核心授權；UI 最小 DOM 限制已揭露。5xx 未覆蓋屬必要失敗面缺口，現有綠燈不能證明該分支。

## 豁免、待確認與限制

- 無未核准 Scenario 或 gate 豁免。
- G5 首版沿用既有 Task status IPC，完整 `resolved_context`／result 大小不受新 memory page/detail 512 KiB 限制；此為使用者已核准的已知界線。
- 暫存 503 探測與無效 UTF-8 探測均未保留在交付樹；前者重現上述 MUST FIX，後者僅顯示長 content 的第 514 byte 之後不屬於清單摘要檢查範圍，未據此判為新增缺失。

## 流程判定

**RETURN TO execute-task**。請修復 WEBMEM-007 的 5xx 原請求重試 UI 與回歸證據，提交新 HEAD 後以固定 BASE／新 HEAD 重新審查。本報告只對上述 Reviewed HEAD 有效，不能作為新提交的 PASS。
