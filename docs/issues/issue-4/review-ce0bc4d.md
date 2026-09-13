# 程式碼審查報告

## 變更摘要

本次審查涵蓋 Issue #4 Phase 2A 單一 Web 遠端入口之完整實作與驗收（分支 `issue-4-web-entry`，當前 HEAD `ce0bc4d2613ac59676f94655a70aca1da8d4bb17`，相對於 `origin/main` 共 8 個 commit）。主要異動模組與檔案如下：

1. **CLI 命令與路由接線**：
   - `cmd/asura/web.go`：新增 `asura web` 子命令，支援 `-listen`（預設 loopback `127.0.0.1:8080`）、`-origin`、`-key`（或環境變數 `ASURA_WEB_KEY`）與 `-socket`。
   - `cmd/asura/run.go`、`cmd/asura/run_test.go`：CLI 路由分派與參數缺漏驗證。
2. **Web 服務端與單頁應用 (SPA)**：
   - `internal/web/server.go`：實作單人 HTTP 控制台，涵蓋 SHA-256 constant-time 密鑰登入、12 小時 HttpOnly/SameSite=Strict 工作階段、Host 與 Origin 防護、CSP 安全標頭、專案清單查詢、非同步任務受理、狀態查詢、共同取消以及基於 Unicode Rune 的長輸出分頁讀取。
   - `internal/web/page.go`：內嵌單頁應用，提供密鑰登入、授權專案下拉選單、自然語言提交、Task ID 查詢與取消；以 `textContent` 純文字渲染防止腳本執行。
3. **授權政策與安全邊界擴充**：
   - `internal/auth/policy.go`：新增 `AllowedRoots()` 提供授權目錄清單副本；實作 `resolveWorkspace` 向上逐層遞迴解析現存祖先目錄之符號連結，徹底阻擋指向清單外路徑與深層未建立子目錄之符號連結逃逸。
   - `internal/auth/web_test.go`：驗證符號連結越界拒絕之單元測試。
4. **守護進程中樞 IPC 擴充**：
   - `internal/daemon/daemon.go`：新增 `projects` IPC 操作；在 `status`、`cancel`、`submit`（含重送）中加入 `authorizeTask` 授權校驗，確保清單外歷史任務與非法路徑均無法讀取、取消或重送。
5. **雙迴圈測試與驗收證據**：
   - `internal/web/server_test.go`、`internal/web/session_test.go`：Web 模組單元測試（內迴圈）。
   - `tests/integration/web_test.go`、`tests/integration/web_policy_test.go`：真實 Daemon IPC 串接與端到端整合測試（外迴圈），完整覆蓋 WEB-001～WEB-010。
   - `docs/issues/issue-4/evidence/`：完整留存 Phase 1 至 Phase 5 所有外紅、內紅、最小實作綠燈與全套回歸日誌。
   - `docs/issues/issue-4/evidence/phase-5/browser-verification.md`：記錄真實瀏覽器手動操作步驟與驗收結果。
6. **專案文件與審查報告保存**：
   - `README.md`、`docs/issues/issue-4/README.md`、`docs/issues/issue-4/implementation-plan-decomposition.md`。
   - `docs/issues/issue-4/review-ff6a3c8.md`：前次審查紀錄。

---

## 審查版本

- **Reviewed HEAD SHA**：`ce0bc4d2613ac59676f94655a70aca1da8d4bb17`
- **獨立 reviewer**：Independent Code Reviewer (subagent)
- **Review artifact**：`docs/issues/issue-4/review-ce0bc4d.md`
- **Reviewed patch-id**：`8568c424f4a3b115c378e304e2d9696703a21bdd`

---

## 整體評估

本次變更完整落實 Issue #4 Phase 2A 之單一 Web 遠端入口需求。

在架構設計方面，嚴格維持 `CLI -> Web -> Daemon -> Engine -> Policy / Storage` 的清晰責任分離邊界。Web 服務端完全不直接連線 SQLite 資料庫或發起作業系統進程，所有提交、查詢、取消與專案清單操作均經由 Unix Domain Socket IPC 委託 Daemon 統一處理。每個 HTTP 操作均使用獨立的 IPC 連線與 context timeout 保護，徹底避免並發交錯與資源洩漏。

在安全性方面，落實了嚴密的縱深防禦：採用 constant-time 密鑰雜湊比對防時序攻擊、HttpOnly + SameSite=Strict Cookie 標籤、強制比對 Host 與 Origin 標頭以防止 CSRF 與 DNS Rebinding、嚴格的 CSP 標頭設定、前端純文字 `textContent` 渲染杜絕 DOM XSS，以及針對未建立子目錄的多層祖先符號連結遞迴解析，防止路徑越界。

在測試證據方面，WEB-001 至 WEB-010 十項已核准劇本均具備外迴圈（整合）與內迴圈（單元）測試，真實保存了各階段的紅燈與綠燈紀錄。Issue #4 的所有測試（`TestWEB*` 與 Web 單元測試）全數秒級通過（耗時約 1.3 秒），斷言真實且無同義反覆假綠燈。

針對既有環境觀察，本次審查特別發現專案在 Issue #2 遺留的 OpenCode 契約測試會直接發起真實外部 CLI 呼叫，在缺乏模型 API 金鑰的隔離環境中會逾時失敗；而 Issue #4 的 Web 整合測試全面採用 `engine.WithRunner` 依賴注入，成功實現與外部環境的完全解耦，此架構模式值得肯定並推廣。

綜合評估，本次提交無任何阻斷性（MUST FIX）問題，架構與安全性兼備，建議判定為 **`PASS`**。

---

## 需求符合度

- **需求描述**：`Issue #4 單一 Web 遠端入口 Phase 2A`（依據 `docs/issues/issue-4/README.md`，承接 Phase 1.5）。
- **是否符合需求**：是（完全符合）。
- **缺漏或過度實作說明**：
  - 完整落實 10 項已核准 Gherkin Scenario（WEB-001～WEB-010）。
  - 未超出需求範圍：未實作多帳號/多角色系統、未引入額外的第三方資料庫或自建任務執行池、未自動遮蔽敏感日誌、未引入第三方 Cloudflare Tunnel SDK（維持由使用者於外部自行管理 tunnel 的架構承諾）。
  - 遵循「簡單優先」原則，單頁應用內嵌於二進位檔中交付，無額外的前端打包構建相依性。

---

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX** — 邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX** — 潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE** — 可讀性、最佳實踐，可列入後續 backlog

### 🟡 SHOULD FIX

- **問題 1：Web Session 達到 128 上限時的拒絕策略未實作 LRU 驅逐**
  - **位置**：`internal/web/server.go:223-227`
  - **說明**：目前在 `login` 處理中，當清理過期 session 後若活躍數量仍達到 128，會直接回傳 HTTP 429（`session capacity exceeded`）。雖然本系統為單人使用，但若使用者在多個裝置、無痕分頁或透過腳本頻繁登入而未顯式登出，累積達到 128 個未過期的 session 時，會導致合法使用者在接下來的 12 小時內無法登入，直到 session 自然過期或 Web 服務重啟。
  - **建議**：在單人系統架構下，當達到上限時，可驅逐最舊（最早建立）或最接近過期的 session，以騰出空間給新登入請求，而非直接返回 429 拒絕。

- **問題 2：既有後端測試對真實外部環境與模型連線的脆弱性依賴**
  - **位置**：`internal/backend/opencode/adapter_test.go:29-33`、`tests/integration/int_test.go:50-60`
  - **說明**：在執行全套回歸測試時，Issue #2 遺留的 `TestINT001Unit`、`TestINT001` 與 `TestINT007` 直接調用了真實的 `opencode run` 二進位檔。在缺乏外部 LLM API 金鑰或網路無法連線至模型提供商的隔離環境下，這些測試會嘗試連線並重試，最終耗時 80 多秒後拋出 `exit status 1` 導致失敗。
  - **對比與建議**：相較之下，Issue #4 的所有 Web 測試均使用 `engine.WithRunner` 依賴注入，完全不依賴外部連線且秒級完成。建議後續將此模式推廣至後端測試，為 `opencode.Adapter` 引入抽象執行介面或使用 Fake Binary，以確保在無外部網路與無金鑰環境下仍可穩定執行回歸。

### 🟢 NICE TO HAVE

- **問題 3：自訂監聽位址時預設 Origin 與 Host 檢查的提示**
  - **位置**：`cmd/asura/web.go:42-45`
  - **說明**：若使用者執行 `asura web -listen 0.0.0.0:8080`，程式預設將 allowed origin 設為 `http://0.0.0.0:8080`。然而瀏覽器通常會以 `http://127.0.0.1:8080` 或 `http://<LAN_IP>:8080` 存取，導致請求在 `server.go:57` 的 `r.Host != s.host` 檢查中直接被回絕 403 Forbidden。
  - **建議**：可在 CLI 啟動輸出或文件補充說明：若綁定非 loopback 位址，強烈建議明確透過 `-origin` 指定瀏覽器存取的完整位址。

- **問題 4：長輸出分頁由 Web 端一次性拉取全文的記憶體負載**
  - **位置**：`internal/web/server.go:315-327`
  - **說明**：目前分頁端點 `handleResultPagination` 是先透過 IPC 向 Daemon 索取整個 Task 實體（包含完整 Result 字串），再由 Web 服務端轉為 `[]rune` 進行切片。對於一般任務輸出（數 KB 至數 MB）十分敏捷，但若未來單一任務輸出達到數十 MB 以上，高頻率的分頁輪詢可能產生短暫的記憶體分配壓力。
  - **建議**：未來若進入 Phase 2B/3 有海量日誌需求時，可考慮在 Storage/Daemon 層提供按 byte/rune range 串流讀取結果的 IPC 介面。現階段維持簡單實作符合當前規模。

---

## 驗收標準與測試證據

### 1. 驗收標準覆蓋與紅綠燈證據

核對 `docs/issues/issue-4/README.md` 核准表與 `docs/issues/issue-4/implementation-plan-decomposition.md`，所有十項 Scenario 均有外迴圈整合測試與內迴圈單元測試覆蓋：

| Scenario ID | 驗收場景名稱 | 外迴圈測試 (Integration) | 內迴圈測試 (Unit) | 雙迴圈紅綠證據 |
|---|---|---|---|---|
| **WEB-001** | 密鑰登入與未登入拒絕 | `tests/integration/web_test.go:TestWEB001` | `internal/web/server_test.go:TestWEB001Unit` | 具備外紅、內紅、最小綠燈與全套回歸日誌 |
| **WEB-002** | 專案授權涵蓋提交查詢與取消 | `tests/integration/web_test.go:TestWEB002`, `web_policy_test.go:TestWEB002Daemon` | `internal/auth/web_test.go:TestWEB002Unit` | 具備外紅、內紅、祖先符號連結修正紅燈與綠燈 |
| **WEB-003** | 接受任務與共同容量 | `tests/integration/web_test.go:TestWEB003` | `internal/web/server_test.go:TestWEB003Unit` | 具備滿載 503、離線 503 與接受 202 驗證 |
| **WEB-004** | 回應遺失重送不重複執行 | `tests/integration/web_test.go:TestWEB004` | `internal/web/server_test.go:TestWEB004Unit` | 驗證同識別 5 條並行連線重送，執行計數嚴格為 1 |
| **WEB-005** | 重送衝突與取消後重新執行 | `tests/integration/web_test.go:TestWEB005` | `internal/web/server_test.go:TestWEB005Unit` | 驗證同識別異內容拒絕 400、已取消重送拒絕 400 |
| **WEB-006** | 關閉頁面後恢復狀態與結果 | `tests/integration/web_test.go:TestWEB006` | `internal/web/page.go:pollTask` (前端 SPA 整合) | 驗證登出後新 session 透過 Task/Request ID 查回結果，連續輪詢無重複執行 |
| **WEB-007** | 長輸出以純文字分頁交付 | `tests/integration/web_test.go:TestWEB007` | `internal/web/server_test.go:TestWEB007Unit` | 驗證 50 組長輸出還原 100% 吻合、非法參數拒絕 400、空結果處理 |
| **WEB-008** | 共同取消如實回報 | `tests/integration/web_test.go:TestWEB008` | `internal/web/server_test.go:TestWEB008Unit` | 驗證 A 取消回收確認、B 存活完成、前端中斷不殺背景工作 |
| **WEB-009** | 失敗與服務重啟保留真實結果 | `tests/integration/web_test.go:TestWEB009` | `internal/web/server_test.go` 終態映射 | 驗證服務重啟後失敗狀態、部分輸出與錯誤保留，查詢不重跑 |
| **WEB-010** | 工作階段與跨站請求維持登入邊界 | `tests/integration/web_test.go:TestWEB010` | `internal/web/server_test.go:TestWEB010Unit`, `session_test.go:TestWEB010ExpiredSession` | 驗證跨站 POST 拒絕 403、Host 偽造拒絕 403、登出即刻失效 |

### 2. 誠實回報與防作弊檢查

- **同義反覆檢查**：
  - `TestWEB007` 長文字分頁驗收中，期望值採用人工固定的 50 組多語 Unicode 與 HTML 字串拼接而成，並非在測試中調用被測切片函式重算，斷言嚴格檢驗 `reconstructed.String() == fullText`。
  - `TestWEB004` 重送去重中，斷言驗證受控後端的 `f.starts.Load() == 1`，來自實際執行攔截器，而非以軟體假定狀態判定。
- **假綠燈防範**：
  - 檢查所有測試檔案，無任何 `t.Skip`、無被註解掉的斷言、無吞掉錯誤碼的情況。
  - Web 整合測試指令 `go test -count=1 -v ./tests/integration -run '^TestWEB'` 12 個測試全數 PASS（1.288s）。
- **規格修訂查核**：
  - 比對核准 commit：`git diff c91f72e..HEAD -- docs/issues/issue-4/README.md`。
  - 結果確認：Gherkin 劇本原文（WEB-001～WEB-010）無任何未經核准之增刪或文字修改；僅回填核准 commit 代碼、文件驗證紀錄與 Timeline 進度，符合規範。
- **Gate 豁免查核**：無任何豁免項目。

---

## 破壞性邊界條件

### Edge Case 1：並行同識別重送與衝突並發競態（Concurrent Conflicting Submissions）
- **輸入／狀態**：多個 HTTP 客戶端（例如網路重試或惡意腳本）在毫秒級時間差內，以完全相同的 `id`（Request ID）同時送出不同的 `prompt` 內容或不同的 `workspace`。
- **預期結果**：Daemon 與 Storage 應保證只有一個請求能成功取得原任務關聯或建立任務，其餘並行請求必須被偵測為 `request ID reused with different payload` 並回傳 HTTP 400 Bad Request，且受控後端至多僅啟動一次。
- **現有覆蓋**：`tests/integration/web_test.go:TestWEB004` 驗證了 5 條並行連線重送相同請求的去重安全；`TestWEB005` 驗證了衝突檢測。在 Daemon 端，SQLite 交易排他鎖與任務唯一鍵可確保狀態一致性。
- **判定**：通過（行為正確且已覆蓋）。

### Edge Case 2：超長／溢位分頁參數與負數偏移攻擊（Pagination Parameter Overflow & Negative Offset）
- **輸入／狀態**：請求 `GET /api/tasks/{id}/result?offset=9223372036854775808&limit=100` 或 `offset=-1`、`limit=0`、`limit=-50` 或非數字字串。
- **預期結果**：服務端必須在參數解析層直接防禦，返回 HTTP 400 Bad Request，不得引發 panic 或非預期的大記憶體切片。
- **現有覆蓋**：`internal/web/server.go:281-286` 在 `strconv.Atoi` 後嚴格檢查 `err1 != nil || err2 != nil || offset < 0 || limit <= 0`，並在 `internal/web/server_test.go:408-418` 與 `tests/integration/web_test.go:1005-1016` 中逐一驗證上述非法字串。若 limit 超過 10000 亦會自動裁切至 10000。
- **判定**：通過（邊界防護嚴密）。

### Edge Case 3：未建立子目錄之多層符號連結逃逸（Symlink Traversal on Non-existent Paths）
- **輸入／狀態**：在授權目錄下建立一個指向清單外路徑的符號連結（如 `allowed_root/link -> /etc`），使用者提交任務時請求的工作目錄為該符號連結下尚未建立的深層子路徑（如 `allowed_root/link/new_dir/sub_dir`）。
- **預期結果**：系統不得因子目錄尚未存在而退化為單純的文字路徑判定，必須解析出其現有祖先實際位於清單外，拒絕授權並回傳 HTTP 403 Forbidden。
- **現有覆蓋**：`internal/auth/policy.go:59-95` 之 `resolveWorkspace` 實作了向上遞迴解析現存祖先目錄之 `filepath.EvalSymlinks`；`internal/auth/web_test.go:TestWEB002Unit` 與 `tests/integration/web_test.go:TestWEB002` 測試了 `link`、`link/..`、`link/../new-project`，均精確回傳 403 拒絕，且資料庫無殘留任務。
- **判定**：通過（漏洞已徹底修復並驗證）。

### Edge Case 4：惡意 HTML / Script / XSS 輸出注入攻擊（Malicious Output Rendering）
- **輸入／狀態**：任務輸出結果中包含惡意攻擊字串，例如 `<script>alert(document.cookie)</script>`、`<img src=x onerror=...>` 等。
- **預期結果**：瀏覽器展示任務輸出時，必須將內容作為純文字渲染，腳本絕不被執行；API 回應必須包含 `nosniff` 與嚴格 CSP 標頭。
- **現有覆蓋**：`internal/web/page.go:278,310` 嚴格使用 `textContent` 賦值；`internal/web/server.go:56` 設定了 `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`（API 端）與 `default-src 'self'`（HTML 端）。`tests/integration/web_test.go:TestWEB007` 及真實瀏覽器驗收手冊（`docs/issues/issue-4/evidence/phase-5/browser-verification.md`）均驗證腳本未執行且字元完全還原。
- **判定**：通過（XSS 防護健全）。

---

## 架構符合度

- **分層與依賴方向查核結果**：
  ```text
  cmd/asura/ (CLI / Web 啟動)
      │
      ▼
  internal/web/ (HTTP Server, 登入守門, SPA)
      │
      ▼ (僅透過 Unix Domain Socket IPC 通訊)
  internal/daemon/ (常駐守護進程, 連線管理)
      │
      ▼
  internal/engine/ (狀態排程, Worker Pool)
      │
      ├──► internal/auth/ (工作目錄授權 Policy)
      └──► internal/storage/ (SQLite 狀態儲存)
  ```
- **依據與邊界保證**：
  1. **禁止越層直接存取**：Web 模組內部完全沒有引用 `internal/storage` 的實例，亦無直接存取資料庫或發起 OS 系統行程的邏輯。所有操作統一經由 `daemon.IPCRequest` 代理。
  2. **並行連線獨立性**：Web 伺服器的 `exchange` 函式（`internal/web/server.go:247`）每次 HTTP 請求均發起獨立的 Unix Socket 連線，並設置 10 秒 timeout，避免多個並行 Web 請求共用未同步的 socket 連線。
  3. **服務端授權清單唯一真相**：Web 端專案清單來自 Daemon 端之 `AllowedRoots()`，客戶端無法在請求中自行夾帶或擴大授權路徑。

---

## 安全性檢查

1. **存取控制與授權檢查**：
   - 所有業務端點（`/api/projects`、`/api/tasks` 等）強制經過 `authenticated` Cookie 檢查，未通過者回傳 401 Unauthorized。
   - 歷史任務與跨專案操作：即便知道 Task ID，若該任務所屬 Workspace 不在當前 Daemon 授權清單內，查詢與取消均回傳 403 Forbidden。
2. **輸入驗證與 Injection 防護**：
   - JSON 請求主體採用 `http.MaxBytesReader` 限制 64KB，並設置 `DisallowUnknownFields()`，防止參數注入或記憶體耗盡。
   - Task ID 路由處理強制檢查 `id == ""` 或 `strings.Contains(id, "/")`，攔截 URL 路徑穿越。
   - 輸出渲染採用純文字 `textContent`，杜絕 DOM XSS。
3. **敏感資料與環境變數保護**：
   - 存取密鑰不寫入 URL、一般日誌或 HTTP 回應。
   - 密鑰驗證採用 `crypto/subtle.ConstantTimeCompare`，避免時序側信道洩漏。
   - 記憶體中儲存 Session 採用 SHA-256 雜湊值（`map[[32]byte]time.Time`），不直接儲存原始 Token。
   - HTTP 標頭強制加入 `Cache-Control: no-store` 與 `X-Content-Type-Options: nosniff`。
4. **CSRF 與跨站請求防護**：
   - Cookie 設置 `HttpOnly: true`、`SameSite: Strict`，在 HTTPS 下自動啟用 `Secure: true`。
   - `server.go` 強制檢查非 GET/HEAD 請求之 `Origin` 標頭必須與伺服器設定完全一致，且 `r.Host` 必須與允許之 host 完全一致，阻擋 DNS rebinding 與跨站請求偽造。

---

## 程式品質與效能

- **代碼風格與命名**：Go 慣用語命名清晰，型別定義明確（`web.Config`, `web.Server`），註解詳述實作考量與對應之 Scenario。
- **資源釋放與生命週期**：所有 net 連線、HTTP 回應主體與 Context 取消函式均正確使用 `defer` 關閉與釋放；伺服器支援 SIGINT/SIGTERM Graceful Shutdown（5 秒超時退場）。
- **效能觀察**：長輸出分頁限制單頁上限 10000 runes，避免無界記憶體消耗；前端定時輪詢間隔為 1 秒，終態或斷線時自動終止計時器。

---

## 改進建議

1. **實作 Session LRU 驅逐機制**（對應 SHOULD FIX 問題 1）：
   在 `internal/web/server.go` 的 `login` 函式中，當 session 數達到 128 上限時，自動逐出最早建立或最接近到期的 session 鍵值，保障單人合法使用體驗不被意外鎖死。
2. **重構後端測試以解耦外部環境依賴**（對應 SHOULD FIX 問題 2）：
   將 Issue #4 中 `engine.WithRunner` 的依賴注入模式引入 `internal/backend/opencode`，在單元與契約測試中以 mock runner 或 fake binary 取代真實外部模型呼叫，避免無網路/無金鑰時測試逾時報錯。
3. **CLI 自訂位址指引優化**（對應 NICE TO HAVE 問題 3）：
   在 `cmd/asura/web.go` 中，若檢測到 `-listen` 包含 `0.0.0.0` 但 `-origin` 為空，可於啟動輸出顯示 Warning 提示，提醒使用者可能需要手動指定 `-origin`。
4. **儲存層長輸出串流支援**（對應 NICE TO HAVE 問題 4）：
   後續若任務結果常態性超過數十 MB，可於 `internal/storage` 擴充結果切片查詢 API，直接在 SQL 查詢層進行字元截取，避免 Web 服務端分配過大記憶體。

### 2026-09-13 審查修復追蹤
- **問題 1（Session LRU 驅逐，SHOULD FIX）**：已於 2026-09-13 修復完成。活躍 session 達到 128 上限時自動逐出最早過期之 session，騰出空間給新登入請求，由 `internal/web/session_test.go:TestSession_EvictOldestWhenCapacityReached` 驗證通過。
- **問題 2（後端測試解耦，SHOULD FIX）**：依據《手術式修改》與《Monorepo 規則》，不於本次 Issue #4 Web PR 跨模組重構 Issue #2 既有測試，保留於後續獨立任務處理。
- **問題 3（0.0.0.0 提示，NICE TO HAVE）**：已於 2026-09-13 修復完成。當 `-listen` 包含 `0.0.0.0` 且未提供 `-origin` 時，CLI 啟動輸出警告提醒，由 `cmd/asura/run_test.go` 驗證通過。
- **問題 4（長輸出分頁串流，NICE TO HAVE）**：現階段資料量規模維持簡單實作，待後續海量日誌需求再行擴充。

---

## 流程判定

**`PASS`**

獨立審查員已完成分支 `issue-4-web-entry`（HEAD SHA: `ce0bc4d2613ac59676f94655a70aca1da8d4bb17`，Reviewed patch-id: `8568c424f4a3b115c378e304e2d9696703a21bdd`）的全面審查。所有 10 項已核准驗收標準（WEB-001～WEB-010）與關鍵安全邊界均具備完整的紅綠重構測試證據與真實瀏覽器操作驗收，Web 整合測試與單元測試全數零錯誤通過，且無任何 MUST FIX 阻斷性問題。報告已持久化存入 `docs/issues/issue-4/review-ce0bc4d.md`。
