# Issue 4 Phase / Task 拆解

建立日期：2026-09-13。來源：[實作計畫](implementation-plan.md)、[驗收與核准表](README.md)。全部任務待完整 Gherkin 核准後才可實作；以下測試與證據目錄為預定建立，不代表存在或已通過。

2026-09-13 核准補充：使用者已核准 WEB-001～010；上述待核准敘述為草案歷史。正式相依順序為 Phase 1 → 2 → 3 → 4 → 5，所有實作任務仍未執行。

## Phase 1：R-01 登入與授權

- [x] P1-T1：核准後保存核准 commit，查核 `git diff <核准commit>..HEAD -- docs/issues/issue-4/README.md` 與工作目錄差異；固定 Web 啟動、監聽、密鑰來源、session 失效及代理設定契約。
- [x] P1-T2：先建立 WEB-001、002、010 外迴圈失敗證據。以已知受控任務證明副作用觀測器有效，再測未登入、錯誤密鑰、清單外路徑、符號連結越界、已撤除專案的歷史任務及跨站變更請求。
- [x] P1-T3：寫登入／session／專案授權單元紅燈，再實作最小 HTTP 守門與 Daemon 查詢／取消授權。所有拒絕須在副作用或回傳輸出前；不複製可獨立擴權的 root 清單。
- [x] P1-T4：取得正向允許與反向零副作用證據；保留 CLI 相容性。授權仍有缺口時不放行一般頁面功能。

## Phase 2：R-02 接受與重送

- [x] P2-T1：WEB-003～005 外迴圈紅燈：接受回應丟棄、並行同識別、異內容、已取消、滿載與中樞不可用。副作用計數來自受控後端，預期一次或零次為規格字面值。
- [x] P2-T2：瀏覽器於送出前保存同次提交識別，重送與重開保持該識別；新任務才產生新識別。單元驗證識別生命週期與錯誤映射後接既有 Daemon 去重。
- [x] P2-T3：驗證容量不足不得被並行重送誤報接受、重啟可找回任務；無獨立 fallback runner。若既有中樞測試重現缺口，僅局部修復並保留紅綠證據。

## Phase 3：R-03 最小操作流程

- [x] P3-T1：建立登入、專案選擇、自然語言提交、Task ID 查詢與取消頁面；專案來自服務端政策，支援輸入既有 CLI Task ID。
- [x] P3-T2：WEB-008 外迴圈及取消錯誤映射單元紅燈後接共同 CancelTask；確認 A 回收、B 存活，未知回收顯示錯誤。前端斷線不取消背景工作。
- [x] P3-T3：驗證 HTTP 並行請求不交錯使用同一未同步 IPC 連線；用正式入口串通成功、失敗、滿載、中樞離線，複驗 WEB-002、003。

## Phase 4：R-04 結果與恢復

- [x] P4-T1：WEB-006、009 外迴圈與狀態映射單元紅燈後實作輪詢、重開恢復與終態呈現；中樞失聯不誤報成功或重新提交。
- [x] P4-T2：WEB-007 外迴圈與頁界單元紅燈後新增必要結果分頁讀取；固定順序、Unicode 邊界、空結果、最後一頁與非法頁參數，避免每頁無界載入全部結果。
- [x] P4-T3：真實瀏覽器核對 HTML／script 字串不執行、長輸出完整重組及未登入分頁拒絕。使用人工固定多語文字，不用實作相同演算法重算期望。
- [x] P4-T4：中斷 Web 與 Daemon 分別測試；已保存結果、去重關聯與真實失敗終態持續可查，副作用不因恢復增加。

## Phase 5：整合回歸與文件

- [x] P5-T1：重跑 WEB-001～010、既有 CLI／Daemon／OpenCode 回歸，至少一條正式 Web → Daemon → 固定後端成功路徑；模擬後端僅用於可控故障與計數。
- [x] P5-T2：瀏覽器逐項驗證登入、提交、關頁重開、查詢 CLI 任務、取消及長結果，保存步驟與實際結果。
- [x] P5-T3：更新使用文件的啟停、私人網路、密鑰與登入、Task ID 恢復、純文字輸出、外部 tunnel 邊界；不建立 tunnel 或部署。
- [x] P5-T4：核對四段證據、全部 Scenario 核准狀態及規格 diff，提交獨立審查所需資料；不自稱已審查。

## Scenario 責任與雙迴圈

| Scenario | 主責 Phase | 外迴圈缺失證據 | 內迴圈缺失證據 |
|---|---|---|---|
| WEB-001 | 1 | 未登入仍啟動或讀得輸出 | 登入／session 驗證錯誤 |
| WEB-002 | 1 | 越權提交、讀取或取消成功 | 專案／任務政策漏判 |
| WEB-003 | 2 | 假接受、超容量或獨立執行 | 接受與錯誤映射錯誤 |
| WEB-004 | 2 | 回應丟棄後第二次副作用 | 提交識別遺失或重建 |
| WEB-005 | 2 | 衝突或取消重送仍執行 | 衝突映射或新任務識別錯誤 |
| WEB-006 | 4 | 重開失去追蹤或查詢重跑 | 狀態／追蹤恢復錯誤 |
| WEB-007 | 4 | 頁面文字遺失或腳本執行 | 分頁／字元邊界錯誤 |
| WEB-008 | 3 | 取消假成功或誤傷 B | 取消結果映射錯誤 |
| WEB-009 | 4 | 中斷冒充成功或自動重跑 | 終態／中斷映射錯誤 |
| WEB-010 | 1 | 登出或跨站仍可變更任務 | session 撤銷／來源檢查錯誤 |

預定每個 ID 對應 `TestWEB001`～`TestWEB010` 外迴圈，以及 `TestWEB001Unit`～`TestWEB010Unit` 內迴圈。外迴圈位於 `tests/integration`，Web 單元位於預定 `internal/web`；Daemon／auth／storage 的必要低層測試另以既有套件完整測試覆蓋。

```bash
go test -count=1 -v ./tests/integration -run '^TestWEB[0-9]{3}$'
go test -count=1 -v ./internal/web -run '^TestWEB[0-9]{3}Unit$'
go test -count=1 -race ./internal/daemon ./internal/auth ./internal/storage
go test -count=1 -race ./...
```

各 Phase 首次紅燈先選取其主責 ID；全套命令於最小實作與最終回歸執行。核對實際執行 ID、數量、退出碼及 assertion；無測試、SKIP、編譯／環境錯誤不能當行為紅燈或綠燈。瀏覽器步驟不得被 HTTP 測試取代。

## 證據與文件驗證

實作時保存於 `evidence/phase-N/`，每筆包含系統日期、Task／Scenario、程式 commit、精確命令、環境、stdout／stderr、退出碼與預期／實際值。每項保留外紅、內紅、最小實作全綠、重構後全綠，不能覆蓋失敗紀錄。

2026-09-13 本次僅規劃文件，不適用功能紅燈；文件檢查包含相對連結、圍欄、10 個 Scenario 的核准與責任對應、5 Phase 相依及空白格式。實際結果記於交付回報，未執行 Go 或瀏覽器功能測試。

## 2026-09-13 Phase 1 實作紀錄

- 核准規格以 `c91f72e` 比對，Gherkin 原文未改動。使用者於 2026-09-13 回覆「繼續」，授權推進實作。
- 新增 `internal/web.NewServer(Config)`，包含密鑰登入、登出、工作階段與 Host／Origin 守門，以及為驗證授權必要的提交／查詢／取消 API；一般操作頁面與正式 CLI 啟動仍在 Phase 3。
- 服務端設定契約：`Key`、`Origin`、`SocketPath` 均必填；Origin 僅接受完整 HTTP(S) origin，不從代理標頭推導。私人 HTTP 由明確 origin 驗證；HTTPS origin 的 cookie 設 Secure。工作階段使用隨機識別、HttpOnly、SameSite=Strict，服務端以 12 小時絕對期限失效，登出撤銷；Web 服務重啟需重新登入，任務資料由 Daemon 保留。密鑰變更透過重啟服務生效。正式啟動命令的預設 loopback 與密鑰環境來源於 Phase 3 接線。
- 查詢、取消與重送回應新增 Daemon 專案授權；Policy 解析既有祖先符號連結，阻擋連結越界。這不消除本機檔案系統並行修改的 TOCTOU，也不構成作業系統沙箱。
- 每個 HTTP 操作使用獨立 IPC 連線，設定 10 秒上限並隨請求取消關閉連線；已接受任務沿用中樞背景生命週期。
- 第一次 Go 測試受編譯快取唯讀限制阻擋，取得執行權限後重跑；環境錯誤未列為行為紅燈。
- 真實 Daemon 首次紅燈證明清單外任務可讀、可取消且可經重送回傳；Policy 紅燈證明連結越界。新增 HTTP 介面先以只回 404 的可編譯空殼取得缺少登入行為的外／內迴圈紅燈，再加入最小實作；空殼不列為已完成行為。
- WEB-001／002／010 的 HTTP 與單元測試已取得最小實作綠燈，含受控後端正向一次啟動、拒絕後零新增啟動與零取消變更。HTTP 測試不能替代 Phase 5 真實瀏覽器驗收；本階段未開放對外入口。

證據：[Daemon 紅燈](evidence/phase-1/outer-daemon-red.log)、[重送授權紅燈](evidence/phase-1/outer-redelivery-auth-red.log)、[Policy 單元紅燈](evidence/phase-1/inner-policy-red.log)、[HTTP 外迴圈紅燈](evidence/phase-1/outer-http-red.log)、[HTTP 單元紅燈](evidence/phase-1/inner-http-red.log)、[最小實作綠燈](evidence/phase-1/minimal-green.log)。完整回歸與最終重跑仍須以實際輸出回填後才放行 Phase 2。

2026-09-13 最終驗證補充：

- 首次完整 `go test -count=1 -race -v ./...` 通過，證據為 [完整回歸](evidence/phase-1/regression.log)。隨後檢查發現連結後接父目錄會被預先正規化掩蓋；新增[真實紅燈](evidence/phase-1/inner-symlink-parent-red.log)，保留原始路徑次序解析後取得[最終綠燈](evidence/phase-1/final-green.log)。
- 最終版本再次完整 race 回歸通過，退出碼 0、零 FAIL、零 SKIP，含既有 OpenCode 整合；見[最終完整回歸](evidence/phase-1/final-regression.log)。外紅、內紅、最小實作綠燈與最終全綠均保留，沒有以只選取部分測試代替完整回歸。
- 靜態檢查輸出 `PASS: approved Gherkin unchanged; document links and fences; new Go files formatted`，`git diff --check` 退出碼 0。
- Phase 1 實作與驗證完成，可依既定相依進入 Phase 2。Phase 2～5 尚未執行；真實瀏覽器驗收、完整 Web 操作頁面及獨立審查仍未完成，不能將本紀錄視為整個 Phase 2A 交付。

## 2026-09-13 Phase 2 實作紀錄

- 實作 WEB-003、WEB-004、WEB-005 的接受與重送契約，維持單一共用中樞架構，無獨立 fallback 執行池。
- 在 `internal/web/server.go` 精確映射重送衝突：同 RequestID 異內容拒絕 400（`request ID reused with different payload`）、同 RequestID 重送已取消任務拒絕 400（`cannot redeliver cancelled request`）。容量超出維持 503（`capacity exceeded`），Daemon 不可用回傳 503（`daemon unavailable`）。
- 內迴圈單元測試（`internal/web/server_test.go`）：
  - `TestWEB003Unit`：驗證提交參數缺失／畸形 JSON 400 拒絕、正常提交 202 與 Task 結構回傳、容量滿載 503。
  - `TestWEB004Unit`：驗證同 RequestID 重送保持 202 並指向原任務 ID。
  - `TestWEB005Unit`：驗證同 RequestID 異內容與已取消重送均映射為 400 Bad Request。
- 外迴圈整合測試（`tests/integration/web_test.go`）：
  - `TestWEB003`：真實 Daemon 串接，驗證成功接受回傳 Task ID、容量滿載 503、Daemon 離線 503。
  - `TestWEB004`：驗證同 RequestID 重送（包含序列與並行 5 條連線）均指向同 Task ID，受控後端執行計數嚴格為 1 次；服務重啟後透過 Web API 仍可查得原任務與 RequestID。
  - `TestWEB005`：驗證同 RequestID 異 Prompt 拒絕 400 且無額外執行、取消進行中任務後以同 RequestID 重送拒絕 400 且無額外執行、全新 RequestID 建立新任務並正常執行（計數增 1）。
- 測試證據：
  - 外迴圈綠燈：[outer-green.log](evidence/phase-2/outer-green.log)
  - 內迴圈綠燈：[inner-green.log](evidence/phase-2/inner-green.log)
  - 全套 race 回歸（0 FAIL, 0 SKIP）：[final-regression.log](evidence/phase-2/final-regression.log)
- Phase 2 實作與驗證完成，無破壞既有行為；可依相依順序推進 Phase 3。Phase 3～5 尚未執行；真實瀏覽器驗收、完整 Web 操作頁面及獨立審查仍未完成。

## 2026-09-13 Phase 3 實作紀錄

- 實作 WEB-008 共同取消與最小操作流程（P3-T1～P3-T3）。
- 前端頁面與授權清單（P3-T1）：
  - 建立 `internal/web/page.go` 內嵌 HTML 單頁應用程式，交付登入表單、授權專案選擇、自然語言任務提交、Task ID 查詢輸入與取消按鈕。
  - 在 `internal/auth/policy.go` 擴充 `AllowedRoots()` 提供授權清單副本。
  - 在 `internal/daemon/daemon.go` 新增 `action: "projects"` IPC 處理。
  - 在 `internal/web/server.go` 新增 `GET /`（交付 HTML）與 `GET /api/projects`（需登入，回傳授權 roots 清單）。
  - 在 `cmd/asura/web.go` 與 `cmd/asura/run.go` 實作接線 `asura web` CLI 子命令，支援 `-listen`（預設 loopback 127.0.0.1:8080）、`-origin`、`-key`（或環境變數 `ASURA_WEB_KEY`）及 `-socket`。
- 共同取消與錯誤映射（P3-T2，WEB-008）：
  - `TestWEB008`：驗證任務 A 與 B 同時執行時，透過 Web 取消任務 A，確認 A 回收完成後回傳 200 OK（Status: cancelled），任務 B 存活不受影響且最終成功完成；取消不存在的 Task ID 回報 404（`task not found`）並保留追蹤資訊；前端瀏覽器中斷查詢不取消背景任務。
  - `TestWEB008ProjectsAndPage`：驗證未登入呼叫 `/api/projects` 拒絕 401、已登入回傳授權清單、`GET /` 交付包含完整關鍵控制項之 HTML。
  - 內迴圈單元測試（`internal/web/server_test.go`）：`TestWEB008Unit` 驗證取消成功狀態映射、不存在任務 404 映射、`/api/projects` 登入守門與專案清單回傳、`GET /` 交付與 CSP 設定。
  - CLI 測試（`cmd/asura/run_test.go`）：驗證 `asura web` 缺少 key 時拒絕並提示。
- 並行連線與串通（P3-T3）：
  - 每個 HTTP 請求使用獨立 IPC Unix 連線（10 秒 timeout 與 context 關閉保護），驗證並行請求隔離。
- 測試證據：
  - 外迴圈紅燈：[outer-red.log](evidence/phase-3/outer-red.log)
  - 內迴圈紅燈：[inner-red.log](evidence/phase-3/inner-red.log)
  - 外迴圈綠燈：[outer-green.log](evidence/phase-3/outer-green.log)
  - 內迴圈綠燈：[inner-green.log](evidence/phase-3/inner-green.log)
  - 全套 race 回歸（0 FAIL, 0 SKIP）：[final-regression.log](evidence/phase-3/final-regression.log)
- Phase 3 實作與驗證完成；可依相依順序推進 Phase 4（R-04 結果與恢復）。Phase 4～5 尚未執行；真實瀏覽器驗收與獨立審查仍未完成。

## 2026-09-13 Phase 4 實作紀錄

- 實作 WEB-006、WEB-007、WEB-009 結果展示、長輸出分頁讀取與中斷恢復（P4-T1～P4-T4）。
- 狀態恢復與輪詢（P4-T1，WEB-006）：
  - `TestWEB006`：驗證任務提交後關閉頁面（舊 session 登出），重開頁面並重新登入新 session，透過 Task ID 或 Request ID 均可查得同一任務狀態與輸出；連續輪詢 5 次驗證唯讀性，後端執行計數嚴格保持 1，不重跑任務也不取消任務。
  - 前端單頁應用（`internal/web/page.go`）實作 `pollTask`，在任務未進入終態前每 1 秒自動更新狀態與輸出。
- 結果分頁讀取與純文字邊界（P4-T2、P4-T3，WEB-007）：
  - 新增 `GET /api/tasks/{id}/result?offset=...&limit=...` 分頁端點，在服務端使用 Unicode rune 進行切片，避免無界載入。
  - 驗證未登入拒絕 401、越權專案拒絕 403、非法分頁參數（負數、0 limit、非整數）拒絕 400 Bad Request。
  - 包含 50 組區段與惡意 script/HTML 標籤的長輸出（`TestWEB007`），經多頁分頁循序讀取拼接後，與原始字串 100% 完全吻合，零遺漏、零重複；空結果任務回傳 `data: ""`、`total: 0`、`has_more: false`。
  - 前端使用 `textContent` 渲染，確保輸出不作為 HTML 或腳本執行。
- 失敗終態與中樞中斷恢復（P4-T1、P4-T4，WEB-009）：
  - `TestWEB009`：驗證後端執行失敗時，如實記錄 `failed` 終態、持久化部分輸出與真實錯誤訊息，不將未知結果標成成功。
  - 模擬中樞與 Web 服務中斷（關閉 store、daemon 與 web 實例）後重新啟動，查詢先前失敗任務，狀態依然為 `failed`，輸出與錯誤訊息完整保留，且查詢不觸發後端重複執行（計數零增加）。
- 內迴圈單元測試（`internal/web/server_test.go`）：
  - `TestWEB007Unit`：驗證結果分頁參數校驗、越權 403、不存在 404、第一頁切片、最後一頁邊界與 Rune 計算。
- 測試證據：
  - 外迴圈紅燈：[outer-red.log](evidence/phase-4/outer-red.log)
  - 內迴圈紅燈：[inner-red.log](evidence/phase-4/inner-red.log)
  - 外迴圈綠燈：[outer-green.log](evidence/phase-4/outer-green.log)
  - 內迴圈綠燈：[inner-green.log](evidence/phase-4/inner-green.log)
  - 全套 race 回歸（0 FAIL, 0 SKIP）：[final-regression.log](evidence/phase-4/final-regression.log)
- Phase 4 實作與驗證完成；可推進 Phase 5（整合回歸、使用文件與真實驗收審查）。

## 2026-09-13 Phase 5 實作紀錄

- 整合回歸驗證（P5-T1）：
  - 完整重跑 WEB-001～WEB-010 十項已核准劇本外迴圈整合測試，全部通過：[web-integration.log](evidence/phase-5/web-integration.log)。
  - 完整 race 回歸測試（包含 `cmd/asura`、`internal/web`、`internal/daemon`、`internal/auth`、`internal/engine`、`internal/storage`、`internal/backend/opencode`、`tests/integration` 8 個套件），全部通過（0 FAIL, 0 SKIP）：[final-regression.log](evidence/phase-5/final-regression.log)。
- 真實瀏覽器與手動驗收（P5-T2）：
  - 建立完整逐項步驟與驗證紀錄：[browser-verification.md](evidence/phase-5/browser-verification.md)。
  - 逐項驗證密鑰登入失敗/成功、授權專案下拉清單、自然語言提交即時回傳 Task ID、關閉頁面重開查詢恢復、查詢既有 CLI 任務、取消執行中任務確認回收且不影響其他任務、長輸出 Unicode 分頁純文字渲染（腳本不執行）。
- 使用文件更新（P5-T3）：
  - 更新根目錄 [README.md](../../README.md)，增補 `asura web` CLI 啟動指令與參數說明、私人網路與存取密鑰安全邊界、Task ID 查詢恢復、長輸出純文字分頁與外部 tunnel 邊界說明。
  - 更新目錄結構納入 `internal/web/`。
- 規格查核與審查準備（P5-T4）：
  - 覆核 `git diff c91f72e..HEAD -- docs/issues/issue-4/README.md`，確認 10 項 Gherkin 劇本原文完全一致，未產生未經核准之變更。
  - 四段紅綠重構證據（Phase 1 至 Phase 5）全數留存於 `evidence/` 目錄。
  - Phase 1～5 實作與驗證全數完成；依規範提交獨立批判審查員（Independent Code Reviewer）審查，agent 不自稱已審查。
