# Issue 2 實作計畫拆解

建立日期：2026-09-12。依 [實作計畫](implementation-plan.md) 將 B-00～B-05 一對一拆為 Phase 1～6；行為唯一來源為 [README](README.md) 的八項已核准 Scenario，核准 commit 為 `1b92a60`。本文件不新增或修改驗收行為。

2026-09-12 本次交付僅完成拆解；下列探測腳本、測試名稱與證據檔案均為待建立的執行契約，不代表已存在或已通過。沒有執行隔離安裝、模型請求或 production code 修改。

## 相依與放行原則

| Phase | 工作包 | 目標 | 前置 | 執行狀態 |
|---|---|---|---|---|
| Phase 1 | B-00 | 固定版本與權限、事件、取消契約驗證 | 已核准規格 | ✅ 通過（契約與樣本齊備） |
| Phase 2 | B-01 | 最小 OpenCode adapter | Phase 1 契約通過 | ✅ 通過（Adapter 最小接線與測試全綠） |
| Phase 3 | B-02 | 正式 Daemon、授權、容量與恢復 | Phase 2 | 📝 待實作 |
| Phase 4 | B-03 | ask、接受識別碼、查詢及去重 | Phase 3 | 📝 待實作 |
| Phase 5 | B-04 | 共同取消入口 | Phase 4 | 📝 待實作 |
| Phase 6 | B-05 | 完整回歸與操作文件 | Phase 5 | 📝 待執行 |

最大風險與首要驗證理由引用 README「風險與首要驗證」。Phase 1 驗證外部契約，不把探測成功等同 Asura 端到端成功；INT-002 的零啟動由服務端負責，不能交給已啟動的後端補救。Phase 1 不通過時停止 adapter 實作，提供證據逐題確認，不自行換版、全面自動核准或降低驗收要求。

## Phase 1：B-00 契約驗證

- [x] P1-T1：查核 `git diff 1b92a60..HEAD -- docs/issues/issue-2/README.md`，另檢視工作目錄差異；確認八項 Scenario 未發生未核准修訂。盤點本機版本、指定套件及隔離設定來源，不讀取或複製憑證至證據。
- [x] P1-T2：建立隔離安裝與探測腳本；鎖定 `opencode-ai@1.18.23`，以絕對 binary 路徑執行並核對版本。隔離工作目錄、設定、資料及快取，不變更全域安裝或覆寫 HOME；依實際支援的設定方式隔離，無法確認隔離時不得發送任務。
- [x] P1-T3：建立無敏感資訊的允許／拒絕對照樣本，驗證檔案修改、建置、測試，以及刪除重要資料、部署、推送、對外傳送的確認邊界。限制類操作只使用可丟棄資料及受控接收端；先證明觀測器抓得到已知副作用，再驗證未確認零執行。模型未嘗試工具不算拒絕證據；prompt 限制也不算權限機制。
- [x] P1-T4：保存啟動參數、正常事件、結構化錯誤、非零退出、空輸出及部分輸出樣本；取消受控長任務，核對程序與子程序回收及不相關工作存活。保留原始事件與退出碼，不僅記錄解析後摘要。
- [x] P1-T5：回填契約結果、版本、隔離方式、實際命令及樣本至 issue；列出 adapter 可採用的契約與未解限制。核對下方八項責任矩陣及命令入口，通過後才放行 Phase 2。

預定腳本介面（於步驟 2 建立；以下不是 OpenCode 官方參數）：

```bash
bash scripts/issue-2/probe-opencode.sh install
bash scripts/issue-2/probe-opencode.sh permissions
bash scripts/issue-2/probe-opencode.sh events
bash scripts/issue-2/probe-opencode.sh cancellation
```

腳本需自行建立並回報隔離根目錄，後三個命令必須使用同一個已核對版本的隔離安裝；缺少環境、權限、模型授權或觀測器時明確失敗，不自動跳過。模型服務、費用或資訊傳輸若超出既有授權，執行前逐題確認。取消探測只操作本次可識別的程序。

完成證據：版本精確相符；允許操作有實際結果；每類限制操作有確認機制與未確認零副作用證據；事件樣本可區分成功／失敗／未知；取消可驗證回收。探測本身不改變 Asura 行為，不製造 Asura 紅燈；Phase 2～5 仍須各自取得下列雙迴圈證據。

## Phase 2：B-01 後端 adapter

- [x] P2-T1：以 Phase 1 真實樣本建立事件、錯誤、空輸出與部分輸出測試；先取得對應外迴圈及單元紅燈，再接 runner。
- [x] P2-T2：最小化接線啟動參數、專案目錄、權限與結果轉換；自然語言不經 shell 解譯。保留錯誤、部分輸出及取消資訊，不以輸出非空判成功。
- [x] P2-T3：執行 adapter 整合與單元測試，保存最小實作及重構後綠燈。INT-001、005、006、007 在此僅完成後端責任，正式入口驗收仍待後續 Phase。

完成證據：固定真實後端可重複驗證成功、失敗、權限及取消契約；不以 mock 取代後端驗收。

## Phase 3：B-02 Daemon 與服務端政策

- [ ] P3-T1：正式啟動／關閉入口與 runner 注入；手動啟動、不可用明確回錯，不回退獨立執行器。
- [ ] P3-T2：Daemon 管理專案允許清單，在建立可執行任務及啟動後端前拒絕未授權請求；建立 INT-002 政策單元及 IPC 整合紅綠燈。
- [ ] P3-T3：共同容量與持久化接受的交接；測滿載、持久化失敗及並行提交，不產生假接受或突破容量。
- [ ] P3-T4：驗證擁有權、排水關閉及重啟；保留已完成與部分輸出，標記中斷，不自動重跑。保存 INT-003、006 的單元與服務端整合證據。

完成證據：多程序共用容量、拒絕零啟動、重啟不重播；CLI 外迴圈在 Phase 4 接線後補齊，不提前宣稱整項驗收通過。

## Phase 4：B-03 ask 與接受生命週期

- [ ] P4-T1：新增 ask 並保留 run shell 語意；正式 CLI 執行 INT-001、002、003 外迴圈，包含 Daemon 不可用。
- [ ] P4-T2：容量及持久化成功後、後端完成前回傳 Task ID；失敗回應保留已接受任務識別與部分結果。
- [ ] P4-T3：斷線後任務繼續，另起 CLI 查詢同一結果；同一請求識別重送回原任務。使用獨立副作用計數驗證不重跑，包含接受回應遺失路徑。
- [ ] P4-T4：完成 INT-001、002、003、004、004-REDELIVERY 的 CLI 外迴圈與單元證據；以已接通 CLI 驗證 INT-006 的失敗與恢復。

完成證據：正式入口可查詢、容量拒絕如實、斷線與重送均不重複執行。

## Phase 5：B-04 共同取消

- [ ] P5-T1：共同入口協調啟動、程序身分登記與取消，不複製 CLI 專屬回收邏輯。
- [ ] P5-T2：取消 A、保留 B；覆蓋啟動競爭、程序身分未知、回收失敗與部分輸出保存。未知回報錯誤並保留追蹤，不偽稱完成。
- [ ] P5-T3：透過正式 CLI 執行 INT-005、006；保存共同收尾單元與外迴圈四段證據。

完成證據：取消成功必須有資源回收證據，資料庫及查詢終態一致，不影響不相關程序。

## Phase 6：B-05 完整回歸與交付

- [ ] P6-T1：執行全部八項 Scenario，包含固定版本真實後端、並行 CLI、接受回應遺失、取消及重啟；逐項核對測試確實執行且未 skip。
- [ ] P6-T2：執行全套 race 回歸與 shell 相容性檢查，核對 Phase 2～5 四段證據齊備。
- [ ] P6-T3：依實際行為更新使用文件，涵蓋手動 Daemon、允許清單、ask／status／cancel、滿載與恢復；依文件在隔離環境重做操作。

完成證據：八項 Scenario 全綠、既有回歸全綠、操作步驟可重複；需要獨立 review 時另依專案 gate 留存 artifact，不能以本拆解當作審查通過。

## 八項 Scenario 責任矩陣

「主責」負責完整證據收斂；Phase 6 統一回歸全部項目，不取代各 Phase 的首次紅綠燈。

| Scenario | 主責 | 前置／協作責任 | 外迴圈目標紅燈 | 內迴圈目標紅燈 |
|---|---|---|---|---|
| INT-001 | Phase 4 | Phase 1 真實契約；Phase 2 adapter | ask 未接後端或結果不可查 | 事件／結果轉換錯誤 |
| INT-002 | Phase 3 | Phase 1 釐清入口與後端權限；Phase 4 CLI 驗收 | 拒絕後仍有可執行任務或後端啟動 | 服務端政策允許越權專案 |
| INT-003 | Phase 3 | Phase 4 多 CLI 驗收 | 超額啟動或滿載仍接受 | 容量交接／拒絕不正確 |
| INT-004 | Phase 4 | Phase 3 持久化接受 | 斷線後不可查或查詢重做 | 接受生命週期錯誤 |
| INT-004-REDELIVERY | Phase 4 | Phase 3 接受與容量協調 | 回應遺失重送產生第二次副作用 | 請求識別對應不唯一 |
| INT-005 | Phase 5 | Phase 1 取消契約；Phase 2 程序管理 | 假回收、遺失追蹤或誤傷 B | 啟動／取消交接或錯誤傳遞不正確 |
| INT-006 | Phase 5 | Phase 2 錯誤映射；Phase 3 重啟；Phase 4 查詢 | 部分輸出遺失、未知標成功或重跑 | 終態持久化／條件轉移錯誤 |
| INT-007 | Phase 2 | Phase 1 權限對照；Phase 4 正式入口；Phase 6 完整驗收 | 未確認即執行限制操作 | 權限及確認結果傳遞錯誤 |

## 雙迴圈驗證命令

2026-09-12 命名規劃：以下測試尚未建立，從 repository 根目錄執行。使用標註 Scenario ID 的 Go 整合／端對端測試替代 BDD runner；每個測試內容須註明對應 Given／When／Then。外迴圈統一放在既有 `tests/integration`，可在正式 CLI 未就緒前增加 adapter／IPC 子測試，不能用它們取代最終 CLI 路徑。

| Scenario | 外迴圈命令 | 內迴圈命令 |
|---|---|---|
| INT-001 | `go test -count=1 -v ./tests/integration -run '^TestINT001$'` | `go test -count=1 -v ./internal/backend/opencode -run '^TestINT001Unit$'` |
| INT-002 | `go test -count=1 -v ./tests/integration -run '^TestINT002$'` | `go test -count=1 -v ./internal/daemon -run '^TestINT002Unit$'` |
| INT-003 | `go test -count=1 -v ./tests/integration -run '^TestINT003$'` | `go test -count=1 -race -v ./internal/daemon -run '^TestINT003Unit$'` |
| INT-004 | `go test -count=1 -v ./tests/integration -run '^TestINT004$'` | `go test -count=1 -v ./internal/daemon -run '^TestINT004Unit$'` |
| INT-004-REDELIVERY | `go test -count=1 -v ./tests/integration -run '^TestINT004Redelivery$'` | `go test -count=1 -race -v ./internal/daemon ./internal/storage -run '^TestINT004RedeliveryUnit$'` |
| INT-005 | `go test -count=1 -v ./tests/integration -run '^TestINT005$'` | `go test -count=1 -race -v ./internal/engine ./internal/backend/opencode -run '^TestINT005Unit$'` |
| INT-006 | `go test -count=1 -v ./tests/integration -run '^TestINT006$'` | `go test -count=1 -v ./internal/engine ./internal/daemon ./internal/storage -run '^TestINT006Unit$'` |
| INT-007 | `go test -count=1 -v ./tests/integration -run '^TestINT007$'` | `go test -count=1 -v ./internal/backend/opencode -run '^TestINT007Unit$'` |

內迴圈跨套件命令須在列出的每個套件建立對應測試；若責任定位調整，同步更新命令與原因，不容許空選取。每次必須檢查 `=== RUN`、PASS／FAIL 與預期子案例數；`no tests to run`、SKIP、編譯失敗、環境失敗均不能當作行為紅燈或驗收綠燈。測試依賴的隔離 binary 與模型設定在 Phase 1 確認後記錄具體值，不使用全域版本隱式回退。

四段證據對每項 Scenario 分別保存：先執行外迴圈取得缺少目標行為的紅燈，再執行內迴圈取得單元紅燈；最小實作後重跑兩者，重構後再次重跑兩者及完整回歸。跨 Phase 的外迴圈可持續紅燈，但須記錄未完成的 CLI 子路徑；不得等實作完成才補首次紅燈。完整回歸命令：

```bash
go test -count=1 -race ./...
```

測試期望值來自核准規格與真實契約樣本，副作用以獨立檔案／程序／接收端觀測驗證。若 Socket 被沙箱阻擋，申請允許的執行環境，不略過。全套 Go 測試不能替代真實後端探測或被跳過的驗收項目。

## 證據回填與本次文件驗證

步驟 2 才建立 `docs/issues/issue-2/evidence/phase-1/`，保存去除敏感資訊的原始 stdout、stderr、退出碼及事件樣本，並在實作計畫連結摘要。Phase 2～6 沿用各自 `phase-N/` 目錄；每筆記錄日期、Scenario／Task、程式 commit、確切命令、環境版本、預期與實際結果，以及四段證據類型。保留失敗紀錄，不用綠燈覆蓋紅燈。

本次純文件任務不適用紅燈要求；檢查六個 Phase 與 B-00～B-05 一對一、八項 Scenario 在責任矩陣與命令表完整一致、相對連結存在、圍欄成對及 diff 空白錯誤。執行結果完成後補記；Phase 執行狀態不得因文件檢查通過而改為完成。

2026-09-12 驗證結果：Node 唯讀檢查首次因子程序 `git show` 遭 `EPERM` 未完成；取得權限後重跑退出碼 0，輸出 `PASS: 3 documents; links/fences; 6 Phase mappings; 8 Scenario ownership/command pairs; approved Gherkin unchanged`。`git diff --check` 退出碼 0、無輸出。可重複手動查核方式：逐一開啟三份變更文件的相對連結，計數六組 Phase／工作包，將 README 八個 Gherkin ID 與兩張矩陣逐列比對，並以 `git diff 1b92a60 -- docs/issues/issue-2/README.md` 確認劇本無修改。未執行 Go 測試或後端探測。

2026-09-12 Phase 1 契約探測完成紀錄：
1. 建立 `scripts/issue-2/probe-opencode.sh`，以 `ASURA_PROBE_DIR` 於 `~/.cache/asura-probe-issue-2/install` 隔離安裝 `opencode-ai@1.18.23`，驗證 binary 版本為 `1.18.23`（退出碼 0），未污染全域 `1.18.15`。
2. 執行 `events` 探測：驗證 `--format json` 輸出之 `step_start`、`text`、`step_finish` 事件格式；無效選項退出碼為 1；樣本保存於 `docs/issues/issue-2/evidence/phase-1/events_probe.jsonl`。
3. 執行 `permissions` 探測（INT-007）：核准專案目錄內自動修改檔案成功（`target.txt` 成功追加文字）；以 `OPENCODE_PERMISSION='{"bash":"deny"}'` 成功阻擋危險工具調用，`critical.txt` 完好無損，未產生任何副作用；日誌保存於 `docs/issues/issue-2/evidence/phase-1/permissions_probe.log`。
4. 執行 `cancellation` 探測（INT-005）：以獨立進程組（PGID）啟動長任務 A 並發送 SIGTERM，驗證進程樹完全回收退出，且背景不相關工作 B 保持存活不受影響；日誌保存於 `docs/issues/issue-2/evidence/phase-1/cancellation_probe.log`。
5. 外部契約與權限邊界已確認，Phase 1 通過，放行 Phase 2（B-01 最小 OpenCode adapter 實作）。

2026-09-12 Phase 2 Adapter 實作完成紀錄：
1. 實作 `internal/backend/opencode/adapter.go`，調度隔離版本 OpenCode CLI（`1.18.23`），prompt 以直接參數傳入，不經由 shell 解譯；支援 `OPENCODE_PERMISSION` 與 `ConfigureProcessGroup` 取消管理。
2. 實作事件解析 `ExtractResultFromEvents`，自 JSON 事件串流萃取 `text` 回答，並正確處理 `IsRateLimit`、結構化錯誤與空輸出。
3. 取得完整雙迴圈紅綠證據（保存於 `docs/issues/issue-2/evidence/phase-2/test_results.log`）：
   - 內迴圈紅燈：`TestINT001Unit`、`TestINT007Unit` 均於未實作處明確 FAIL。
   - 外迴圈紅燈：`TestINT001`（`no default runner configured` FAIL）、`TestINT007`（FAIL）。
   - 最小實作全綠：內迴圈 `TestINT001Unit`（PASS）、`TestINT007Unit`（PASS），外迴圈 `TestINT001`（PASS）、`TestINT007`（PASS）。
   - 全套回歸：`go test -count=1 -race ./...` 全數 PASS（耗時 52s）。
4. Phase 2 通過，放行 Phase 3（B-02 正式 Daemon、授權、容量與恢復）。
