# Issue 27 驗證證據

驗證日期：2026-09-30。核准基準 `147954b`，測試對象為本分支待交付工作區；production、Go tests、CI 與 UI probe 內容雜湊見 [source-manifest.json](source-manifest.json)。完整提交後以該 manifest 與固定交付 HEAD 核對；不將編譯錯誤當功能紅燈。

## 前置查核（Task 0.1）

```bash
go test -count=1 -v ./internal/daemon ./internal/storage ./internal/gitmem -run 'TestWEBGIT.*Probe|TestApproveCandidateTx_PartialCandidateRejected|TestCapture_SubdirectoryScopeIsolation'
```

exit 0，[p0.log](p0.log)。未授權 capture 0 Git subprocess；授權成功對照啟動真實 process，Unix 斷線後回收，0 candidate/memory writes；恢復 Git 後可正常 capture。SQLite 1,000 candidates 清單仍最多 50，但原 detail 載入 2 MiB draft+2 MiB facts，證明 Web 需有界投影。Partial approval 與子目錄隔離既有契約通過。這是既有行為 characterization，不是新 Web 完整驗收；無 production 變更，不需缺功能紅燈。

## 紅燈與最小實作綠燈

| 層級／驗收 | 真實紅燈 | 最小實作與最終綠燈 |
|---|---|---|
| Storage page/detail（004） | `go test -count=1 -v ./internal/storage -run TestWEBGITBoundedPageAndDetail` exit 1，尚無 bounded page/detail 介面：[storage-red.log](storage-red.log) | 同命令及 `-run 'TestWEBGITBounded|TestApproveCandidateTx'` exit 0：[storage-green.log](storage-green.log)，同時間戳 keyset、scope/status cursor、欄位 sentinel |
| Daemon IPC（002、007） | `go test -count=1 -v ./internal/daemon -run TestWEBGITBoundedIPC` exit 1，unknown bounded action：[ipc-red.log](ipc-red.log) | `-run 'TestWEBGITBoundedIPC|TestWEBGITIPCDisconnectProbe'` exit 0：[ipc-green.log](ipc-green.log) |
| HTTP 外迴圈（001～004、006～008） | HTTP fixture 初次有未使用 import，未採為紅燈；修正 fixture 並重放尚未掛載 Git route 的狀態，`go test -count=1 -v ./tests/integration -run '^TestWEBGITHTTP'` exit 1，404：[http-red.log](http-red.log) | 掛載 route 後同測試 exit 0；最終擴充命令 `go test -count=1 -v ./tests/integration -run '^TestWEBGIT'` exit 0：[http-green.log](http-green.log) |
| Bounded approval（007、008） | 暫時停用新增 Web bounds gate、保留原 CLI 行為，`go test -count=1 -v ./internal/storage -run TestWEBGITBoundedApproval` exit 1，過大/空編修建立 memory，Partial/終態錯誤不符分類：[approval-red.log](approval-red.log) | 恢復交易內 bounded gate 後同命令 exit 0：[approval-green.log](approval-green.log) |
| Legacy approved Partial（007） | `go test -count=1 -v ./internal/storage -run TestWEBGITApprovedPartialRejected` exit 1，approved Partial 先走重送成功：[partial-retry-red.log](partial-retry-red.log) | 把 Partial gate 移至重送分支前，同命令 exit 0：[partial-retry-green.log](partial-retry-green.log) |
| Candidate GET Origin（001） | `go test -count=1 -v ./tests/integration -run TestWEBGITCrossOriginRead` exit 1，明確跨 Origin GET 回 200：[read-origin-red.log](read-origin-red.log) | 新 Git route 補同源檢查後同命令 exit 0：[read-origin-green.log](read-origin-green.log) |
| IPC reader（004） | `go test -count=1 -v ./internal/web -run TestWEBGITIPCReaderBound` exit 1，超大回應分類 503：[reader-red.log](reader-red.log) | 設有限 reader 並分類 413，`-run TestWEBGIT` exit 0：[reader-green.log](reader-green.log) |
| Chromium UI（005、009） | `PLAYWRIGHT_MODULE=/home/kevin/.cache/ms-playwright-go/1.50.1/package CHROME_BIN=/usr/bin/google-chrome node docs/issues/issue-27/evidence/ui-browser.cjs` exit 1，缺 Git review UI：[ui-red.log](ui-red.log) | 同命令 exit 0：[ui-green.log](ui-green.log)；真實 DOM、確認、編修、Partial、workspace/401 reset、同 scope detail 逆序、capture AbortSignal 及登出 pending action gate |

純 UI 與 API/DB 各有不同失敗面，保留 browser 外層與 Go 契約層；page/detail、approval 的 store 行為由同層單迴圈保障，沒有額外 store 內部整合層需要重複紅燈。沒有以 mock 代替核心授權、Git service 或原子 SQLite 寫入。Browser fixture 僅控制 fetch/延遲；真實 HTTP 到 Daemon 路徑另由整合測試覆蓋。

## 最終行為查核與精煉

- `go test -count=1 -v ./internal/storage ./internal/daemon ./internal/web -run 'TestWEBGIT|TestApproveCandidateTx'` exit 0：[storage-ipc-final.log](storage-ipc-final.log)。包含 revoke/restart、canonical symlink alias/escape、交易 content gate、重送與 expired session。
- `go test -count=1 ./internal/gitmem ./internal/storage ./internal/daemon -run 'TestWEBGIT|TestCaptureService|TestSaveCandidateAndCheckpointTx'` exit 0：[capture-bounds-green.log](capture-bounds-green.log)。Web capture 改用有界 metadata；原 #23 capture/交易契約保持通過。
- `go test -count=1 -v ./tests/integration -run '^TestWEBGIT'` exit 0：[http-green.log](http-green.log)。10,000 候選全部遍歷無重複/遺漏；HTTP reader/escaped JSON 超限拒絕；500 files 上限真實 capture 回 Partial，checkpoint 不前進；HEAD、branch、index、worktree/config 不變；2 MiB legacy draft 重用如實回報且 detail unavailable。capture 中斷及 10 秒 timeout 回收 process，0 candidate、memory、checkpoint writes；timeout 已修正為 504。
- `node docs/issues/issue-21/evidence/ui-probe.cjs`（以及 audit、taskRace、retry503 模式）exit 0：[existing-ui.log](existing-ui.log)。原 Web memory/Task UI 回歸通過。
- code-simplify：共享 bounded SQL identity/payload/review 投影，移除依 SQL 字串 replacement 產生 header 的脆弱寫法；只動本次範圍。其餘 handler/UI 依既有流程，沒有值得新增抽象的重構，no-op。精煉後以上必要檢查及下方完整回歸確認最終內容。

## 完整回歸（WEBGIT-012）

以下命令於 2026-09-30 對 manifest 對應的最終內容執行完成。

| 命令 | 結果 | 輸出 |
|---|---|---|
| `go vet ./...` | PASS，exit 0 | [vet.log](vet.log) |
| `go test -count=1 -race ./...` | PASS，exit 0 | [race.log](race.log) |
| `go build -o /tmp/asura-27-build ./cmd/asura` | PASS，exit 0 | [build.log](build.log) |

## 驗收對應

| 驗收 | 真實證據與結果 |
|---|---|
| WEBGIT-001 | HTTP denied session/Host/Origin 0 subprocess/writes；expired session unit；http-green + storage-ipc-final |
| WEBGIT-002 | exact parent/child、跨 scope ID、canonical alias/escape、重啟撤權；http-green + storage-ipc-final |
| WEBGIT-003 | fixed range/reused、since-last Partial、invalid range、Git 狀態不變；http-green |
| WEBGIT-004 | 10k pages、field/JSON/IPC reader bounds、cursor scope/status；http-green + storage-green + reader-green |
| WEBGIT-005 | 真實 Chromium 無 script/img 注入、facts/draft/status 分區；ui-green |
| WEBGIT-006 | edited content 進 git-approved、原 provenance 不變、existing Web memory 可讀；http-green + ui-green |
| WEBGIT-007 | Partial/legacy approved Partial/discarded/invalid/cross scope/missing 拒絕；storage-ipc-final + partial-retry-green + http-green |
| WEBGIT-008 | approve/discard 重送與並行終態、正式 memory 刪除後不復活；storage final/race + http-green |
| WEBGIT-009 | generation/token、同 scope 遲到 detail、workspace/401/capture abort；ui-green，登出 pending HTTP 期間也拒絕新 Git action，重登入後可重新查詢 |
| WEBGIT-010 | pending 永不 recall，Web approved memory 可 explicit/bounded；後續 delete/discard 不改已接受快照；http-green |
| WEBGIT-011 | 真實 HTTP/Unix/Git disconnect/timeout，process reap、0 checkpoint/write；http-green + p0 |
| WEBGIT-012 | 完整 vet/race/build、原 UI 回歸；正式 CI 以 PR checks 為準 |

## 限制

分頁不是跨 HTTP snapshot；並行新增/狀態變更時重新整理。SQLite 既有 busy timeout 的取消延遲限制保持原契約；capture process 的取消已由真實路徑驗證。沒有自動模型草稿或 force/bulk API。正式 CI 與獨立 review 不以本機自我檢查替代，結果由 PR 與持久化 review artifact 記錄。
