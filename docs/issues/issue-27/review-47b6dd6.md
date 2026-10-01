# Issue 27 獨立審查報告

- 審查日期：2026-09-30。
- 範圍：完整 PR；全部 `BASE..HEAD` 的 50 個檔案，包含核准文件、實作、測試、證據、CI 與常青文件。
- TARGET SHA：`231d846e15a1a3aa8e227fa87fde2e1a4c476c73`。
- Reviewed BASE SHA：`231d846e15a1a3aa8e227fa87fde2e1a4c476c73`。
- Reviewed HEAD SHA：`47b6dd6fa183a339db9ab4d2e90dc590080e71b5`
- Reviewed patch-id：`987754146c59e4638b7a1acaec172f582d79808d`（`git diff BASE HEAD | git patch-id --stable`）。
- 獨立 reviewer：Codex 隔離 reviewer task `/root/independent_review`；非原實作者，未再委派，未修改 production 或測試，未 commit。
- Review artifact：`docs/issues/issue-27/review-47b6dd6.md`。
- 風險：High；以授權隔離、SQL/IPC 容量、原子審核與 capture 取消為重點。

## 問題與風險

- MUST FIX：無。
- SHOULD FIX（證據持久力）：`internal/storage/sqlite_gitmem_bounded_test.go:159` 的並行 review 測試目前忽略各 goroutine 回傳錯誤，只檢查最終終態與 memory 筆數。此斷言真實保障不雙寫與不逆轉，不是假綠燈；建議收集每次結果，另外斷言已知終態的回應為既有結果或 conflict，讓未來錯誤分類退化更容易被測試攔截。本次重送與 conflict 已由 HTTP workflow 及交易測試分別保障，不阻塞。
- NICE TO HAVE（證據持久力）：補上 `HeadersOnly:true` 的 Partial→Complete 重試專屬案例，包含已人工編修 draft。既有 CaptureService Partial 升級與 SQL CASE 保留人工草稿測試，加上本次 Web metadata-only 重用、真實 Partial capture 與靜態追蹤，已提供本次有效證據；專屬案例能直接守住新增選項與原狀態機的組合。沒有發現 header 空 draft 被寫回人工 draft 的缺陷。

## 核准與範圍

已讀根目錄 AGENTS、docs/AGENTS 及 project/acceptance/verification/review-evidence，流程契約相容。已讀 issue README、全部分析、Phase/Task 計畫與 evidence；核對協調者傳入的 2026-09-30 使用者核准來源。WEBGIT-001～012 與 J-TBD-1～8 全部核准，基準 `147954b`。核准表與劇本集合一致。

以 `git diff 147954b 47b6dd6 -- docs/issues/issue-27/README.md docs/issues/issue-27/technical-analysis.md` 比對，Gherkin 無變更。新增證據/交付檔案連結、狀態與核准 SHA 回填不改變驗收；GET 帶明確跨 Origin 時拒絕的補述落實原 WEBGIT-001，不引入未授權外部行為。新增 bounded IPC 與 storage query 保留 CLI 完整 detail 契約，不新增 schema、scanner、recall 政策或模型草稿功能。

## 驗收與證據

固定版本規格為 `47b6dd6fa183a339db9ab4d2e90dc590080e71b5:docs/issues/issue-27/README.md`；下表各證據與測試也屬同一固定 HEAD。

| 驗收 | 查核與結果 |
|---|---|
| WEBGIT-001 | `web_git_cancel_test.go:19` 五條路由的未登入/非法 Host/POST Origin 拒絕，真實 process marker 與 DB 計數為零；`TestWEBGITCrossOriginRead` 驗 GET，expired-session unit 驗期限。共同 middleware 位於 dispatch 前。通過。 |
| WEBGIT-002 | `TestWEBGITIPCReauthorization` 真實 symlink alias/escape、Daemon 重啟撤權；HTTP parent/child 與跨 scope not-found，SQL WHERE workspace+id。授權先於 store/Git。通過。 |
| WEBGIT-003 | `TestWEBGITHTTPWorkflow` fixed range/reused；`TestWEBGITCaptureModesAndLegacyPayload` since-last、501 files Partial、非法 range、legacy 2 MiB draft 重用與 Git HEAD/branch/index/worktree/config 前後比對。capture 呼叫原 service。通過。 |
| WEBGIT-004 | `TestWEBGITHTTPPaging` 真實 10,000 candidates 完整遍歷；storage 同時間戳 keyset/UTF-8 summary/cursor scope-status，detail sentinel、escaped JSON 與 IPC reader 超限均拒絕。無全量讀後切頁、無截短完整 draft。通過。 |
| WEBGIT-005 | 真實 Chromium probe 驗 list/facts/draft 惡意字串未執行、純文字 DOM 與分區；production 採 textContent/value、createElement，未將候選注入 HTML。通過。 |
| WEBGIT-006 | HTTP workflow 核准編修內容後查真實 git-approved memory 與 provenance；既有 Memory route 可查批准 ID。Chromium 確認與送出 payload。通過。 |
| WEBGIT-007 | bounded approval 交易內 scope/status/Complete/content gate；Partial、legacy approved Partial、discarded、invalid、missing/cross-scope 拒絕且零正式寫入。直接 API 不依賴 UI。通過。 |
| WEBGIT-008 | HTTP approve/discard 重送與終態 conflict；storage 16 並行 review 的終態/筆數一致；正式 memory 刪除後重送保留 ID、不復活。SQL transaction/CAS 保護。通過；錯誤分類測試持久力建議如上。 |
| WEBGIT-009 | Chromium 控制 detail/capture 遲到、同 scope detail 逆序、workspace 切換、pending logout、401；清除 list/detail/edit/form/action 並 AbortSignal。session/workspace generation 與 token 判斷有效。通過。 |
| WEBGIT-010 | `TestWEBGITRecallAndSealedContext` 真實 Task 接受前候選不 recall，批准後 explicit/bounded recall，刪除正式 memory 與捨棄後逐筆比較 sealed resolved context 原字串。通過。 |
| WEBGIT-011 | HTTP disconnect/10 秒 timeout→Unix→真實 Git process group，先驗 process 存活，再驗 ESRCH 與零 candidate/memory/checkpoint；既有 Unix probe 成功對照與半關閉回歸。已提交交易遵循原子 commit，不倒退；UI 告知重新查詢。通過。 |
| WEBGIT-012 | manifest 對應完整 vet/race/build 及原 UI 回歸本機全綠；正式 CI run 36695493914 進行中，保留平台交付 gate，不宣稱完整 CI 已通過。 |

詳細 raw logs 與實際命令見 `evidence/verification.md`，紅燈為缺 bounded 介面、unknown IPC、未掛路由 404、錯誤批准寫入、legacy Partial 重送繞過、GET Origin 未阻擋、reader 未分類，以及缺 browser UI。編譯錯誤未採為功能紅燈。Storage 同層合併紅燈理由明確，HTTP/SQL/IPC/browser 不同失敗面保留各自證據。精煉以最終回歸驗證，no-op 理由已保存。測試 expectation 來自固定字面結果、DB 實際資料、process 狀態與獨立前後比對，沒有與 production 公式同義重算或 mock 掉核心授權/原子寫入。

## 重要失敗面

| 輸入／狀態 | 預期與覆蓋結論 |
|---|---|
| 非法 Host/session/Origin；symlink 逸出；撤權 | middleware/Daemon policy 拒絕先於副作用；marker、零 DB 計數與原 candidate 不變，成功對照存在。 |
| 既知 ID 在另一 canonical scope／父子 scope | 同 scope SQL lookup，跨 scope 與不存在 ID 同 not-found；不回 raw Git stderr/路徑。 |
| 10k rows、相同 timestamp、錯 scope/status cursor | SQL LIMIT limit+1 與 created_at DESC/id ASC keyset；固定資料集無重複/遺漏；游標參數化，錯 cursor 拒絕。 |
| legacy 2 MiB payload、邊界 UTF-8、JSON escaping | SQL BLOB sentinel 最大上限+1；memory 前載入有界，detail 完整或拒絕；encoder 有界及 Web reader hard bound，超限 413。 |
| omitted/empty/過大 edit；Partial/終態 candidate | omitted 沿原 draft，明確 edit 非空且最多32 KiB；同交易驗證後寫 memories/provenance，再 CAS status；失敗 rollback。 |
| approve/discard 並行；批准 memory 被刪除後重送 | 終態不逆轉、最多一筆 memory；批准 ID 保留，不新增 memory。 |
| HeadersOnly Partial 升級及人工 draft | Header 保留 status/IsTruncated/IsUserEdited；upgrade SQL CASE 依當前列保護人工 draft，無法因 header 空 draft 覆寫。save transaction 的 metadata-only reload 不寫回人工 draft。舊升級/並行編修/rollback 測試與本次回歸通過。 |
| capture 於 repo preflight/Git/交易前斷線或逾時 | request context 關閉專用 Unix socket、connCtx 傳下層；process 被回收、checkpoint不前進。已提交後仍可查持久化狀態，不將失去回應視為未寫入。 |
| 同 scope detail 逆序／A→B／logout pending | 舊 response 由 token/generation 丟棄；abort、清空編修與隱藏 app 阻止登出中的新 action。真實 DOM probe 證明。 |
| candidate 或正式記憶後續變動 | Recall 僅正式 memory，Task resolved context 原封保存，不由 Web 審核重寫。 |

## 架構、品質與限制

Web 僅為 DTO/error/UI adapter，Git、canonical authorization、capture/store ownership 留在 Daemon，status transition 留在原 SQLite 交易。新增跨 Web/Daemon/storage/gitmem 的檔案各自對應有界入口與必要 capture metadata-only 選項；無跨 backend/pipeline 的非需求改動。

跨 Task 查核：identity/payload/review SQL 投影已共用；approve/discard 重用原交易/狀態轉移，UI 共用 gitFetch/generation，不存在分歧的第二套 capture/checkpoint。HTTP 與 Daemon 重複入口檢查屬兩個信任邊界，HTTP/SQL content 驗證也各有必要性；未發現應列 MUST FIX 的重複邏輯分歧或過度設計。

`git diff --check BASE HEAD` 僅列 issue README metadata 的 Markdown 雙空白換行（154～156行），屬文件刻意格式，不影響行為。既有 SQLite busy timeout 取消延遲、並行分頁非跨 HTTP snapshot、browser fetch fixture 與真實 backend 分層驗證均已揭露；不當作假綠燈或豁免。

無驗收豁免、無未解決需求決策。2026-09-30 協調者已建立 PR https://github.com/raybird/Asura/pull/28，遠端 source HEAD 仍為 `47b6dd6`；正式 CI run 36695493914 正在執行完整 race 測試，是尚未完成的外部交付檢查，須取得相同 source HEAD 的成功 checks；本報告不授權合併，也不將本機測試替代正式 CI。

## 版本與獨立驗證

2026-09-30 審查時工作區 HEAD 為 Reviewed HEAD，merge-base 等於 BASE，無未提交交付程式差異。以 `git show HEAD:path` 的 SHA-256 核對 source-manifest.json 全部169個 production/Go tests/CI/UI probe 檔案，0 mismatch，既有完整回歸證據可重用。

獨立重跑（2026-09-30，固定 HEAD 工作區）：

```bash
go test -count=1 ./internal/storage ./internal/daemon ./internal/web ./internal/gitmem ./tests/integration -run 'TestWEBGIT|TestCaptureService_PartialCandidate|TestSaveCandidateAndCheckpointTx'
```

exit 0：storage 0.392s、daemon 3.857s、web 0.013s、gitmem 0.293s、integration 18.053s，五套件全部通過。

```bash
PLAYWRIGHT_MODULE=/home/kevin/.cache/ms-playwright-go/1.50.1/package CHROME_BIN=/usr/bin/google-chrome node docs/issues/issue-27/evidence/ui-browser.cjs
```

exit 0：`PASS real Chromium: text rendering, edited approval, discard confirmation, Partial, generation/abort, same-scope detail race, logout pending action gate, 401 reset`。

完整 `go vet ./...`、`go test -count=1 -race ./...`、`go build -o /tmp/asura-27-build ./cmd/asura` 依無 mismatch 的 manifest 重用 `evidence/vet.log`、`race.log`、`build.log`：exit 0，race 全部19套件通過。空 vet/build log 為命令成功無輸出，exit code 已在 verification 保存；不是僅由空檔推論成功。

## 流程判定

**PASS**：完整固定範圍獨立審查已完成，報告已持久化，無 MUST FIX。程式與本機證據審查通過；WEBGIT-012 正式 CI 部分仍為交付前必要 gate，須取得 checks 後才完成整體交付。不得以本報告宣稱仍進行中的正式 CI 已通過。

報告如新增為唯一直接後繼提交，需依 review-evidence.md 與 verify-artifact.py 檢查新增例外；其他程式/測試/規格變更不沿用本次 PASS。
