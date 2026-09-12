# 程式碼審查報告

## 變更摘要
本次 Commit `6701a37`（相對於 `6c8895b`）針對前次審查報告 `docs/issues/issue-1/review-6c8895b.md` 所提出的重大缺陷 **M-01 [P1]**（孤兒進程組身分無法確認時誤判已回收、清空 PGID 並宣稱成功）進行修復：
1. **核心身分校驗改進 (`internal/backend/opencode/process.go`)**：在 `VerifyTaskProcessGroup` 函式中，當 Leader 已退出、監護 Runner 已失效且存活孤兒進程未持有 `ASURA_TASK_ID` 標記時，從原先返回 `(false, nil)`（被呼叫端誤判為已結束／已回收）修正為回傳 `(false, fmt.Errorf(...))`，嚴格保留未知狀態並揭露錯誤。
2. **呼叫端安全攔截 (`cmd/asura/cancel.go`)**：呼叫端 `safeKillTaskProcessGroup` 攔截上述身分無法確認之錯誤，拒絕清零 PGID，拒絕宣稱成功，中斷取消流程並向 stderr 回報非 0 退出碼。
3. **單元與端到端測試補強 (`internal/backend/opencode/process_test.go`, `tests/integration/cli_test.go`)**：新增 `TestVerifyTaskProcessGroup_OrphanGroup` 第 4 步斷言與端到端整合測試 `TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely`，完整模擬 Leader 率先 exit 0、Runner 崩潰中斷且子進程以 `exec env -i` 清除環境變數的情境，斷言 cancel 失敗報錯且 SQLite 中的 PGID 完好保留。
4. **文件 Timeline 更新 (`docs/issues/issue-1/README.md`)**：記錄 M-01 修復歷程。

---

## 審查版本
- **Reviewed HEAD SHA**：`6701a37e35cb2ec1f955da7d733b98938ca8169b`
- **Base SHA**：`6c8895b0cd9c5fb7b4de02963a46274d3c6f98ef`
- **獨立 reviewer**：獨立程式碼審查員（Code Reviewer Subagent）
- **Review artifact**：`docs/issues/issue-1/review-6701a37.md`
- **Reviewed patch-id**：`9fdd770ae5b95a1c5fc1d643d7dd770a142b2d2d`

---

## 整體評估
本次變更精準且嚴格地落實了 M-01 的修復要求，沒有任何過度實作或推測性程式碼。核心邏輯將三態判定（吻合、明確不符／已結束、無法確認）的語意切實收斂，杜絕了假成功與盲目強殺，完美保障了 SCN-004 的進程資源追蹤要求。測試案例具備真實失敗場景模擬，無假綠燈。審查結果建議直接給予 **PASS**。

---

## 需求符合度
- **需求描述**：`fix(cli): 孤兒進程組身分無法確認時回報錯誤並保留 PGID (M-01)`
- **是否符合需求**：是（完全符合）。
- **缺漏或過度實作說明**：
  - 無缺漏：在 Leader exit 0、Runner 崩潰且子進程清除環境變數時，系統不再誤判為「已回收」，而是準確回報「身分無法確認」，保留資料庫中 PGID 追蹤資訊，不向未知程序盲目發信號，且 CLI 退出碼為 1。
  - 無過度實作：手術式修改了 `process.go` 的回傳值，沿用現有錯誤傳遞機制，未加入非必要的外部相依或抽象層。

---

## 問題與風險

- 🔴 **MUST FIX**：無。
- 🟡 **SHOULD FIX**：無。
- 🟢 **NICE TO HAVE**：無。

---

## 驗收標準與測試證據

- **驗收標準覆蓋**：
  - 核心驗收標準 **SCN-004（取消任務不影響其他工作）**：在任務執行期間或 Leader 退出後，取消流程確保資源被安全回收且不誤傷其他工作；對於身分不明的孤兒進程組，杜絕誤判已回收與假成功。
  - 規格修訂查核：比對 `docs/issues/issue-1/README.md`，核准 commit 為 `b48c614`。本次異動僅在 Timeline 區塊記錄 M-01 修復歷程，未修改任何 Scenario 定義或驗收條件，符合規定。
- **紅綠燈與誠實回報檢查**：
  - `TestVerifyTaskProcessGroup_OrphanGroup`：以單元測試驅動，明確驗證當元資料與 TaskID 皆不符時，函式回傳 error（若未修復此測試會因回傳 nil 而失敗）。
  - `TestCLI_LatestReview_M01_OrphanUnknownIdentityFailsSafely`：端到端整合測試完整驗證 M-01 重現路徑，斷言 cancel 必須返回非 0 退出碼、stderr 包含 `unable to verify process identity`，且 `finalTask.PGID == targetPGID`。斷言具備獨立真實性，絕非同義反覆。
- **Gate 豁免查核**：無。

---

## 破壞性邊界條件

- **Edge Case 1：Leader 已退出、Runner 崩潰，但孤兒進程組在取消發起前已自然終止退出**
  - *輸入／狀態*：命令啟動短暫背景工作（例如 `sleep 0.1`），Leader 與 Runner 均退出，在執行 `asura cancel` 時孤兒進程組已經自然結束。
  - *預期結果*：系統應判定進程組已無存活成員，清空 PGID 並正常返回成功（exit code 0），不應誤報身分無法確認錯誤。
  - *現有覆蓋*：`VerifyTaskProcessGroup` 第一步即透過 `!IsProcessGroupAlive(pgid)` 探測，若整組已退出則直接返回 `(false, nil)`，呼叫端安全清空 PGID 並成功返回。`TestVerifyTaskProcessGroup_OrphanGroup` 第 6 步有完整斷言。
  - *判定*：**PASS**。
- **Edge Case 2：同一進程組內部分孤兒子進程清除環境變數，另一部分仍保留 `ASURA_TASK_ID`**
  - *輸入／狀態*：任務衍生多個子進程，其中一個執行 `exec env -i`，另一個為普通子進程持有 `ASURA_TASK_ID`。Leader 退出、Runner 崩潰。
  - *預期結果*：只要組內任一成員持有身分證據，即可證明整組所屬任務，應確認身分成功並將整組（包含清除環境變數者）全數 SIGKILL 回收。
  - *現有覆蓋*：`hasProcessGroupMemberWithTaskID` 遍歷 `/proc` 屬於該 `pgid` 的所有成員，找到任一吻合即回傳 `(true, nil)`，進而驅動整組強殺與 PGID 清理。
  - *判定*：**PASS**。
- **Edge Case 3：進程組 Leader 退出後，PGID 被系統重新分配給特權或無權限讀取 environ 的外部進程**
  - *輸入／狀態*：原任務已退出，PGID 被重用分配給其他使用者或 root 之特權進程，取消端無權讀取 `/proc/<PID>/environ`。
  - *預期結果*：讀取 environ 遭遇 `EACCES` 權限不足，系統不得盲殺該進程，亦不得當成吻合或已回收。
  - *現有覆蓋*：`hasProcessGroupMemberWithTaskID` 遇到非 `os.ErrNotExist` 之系統錯誤時會記錄 `lastErr` 並於遍歷結束後回傳；`VerifyTaskProcessGroup` 接收錯誤後回傳 `(false, sErr)`；呼叫端中斷並報錯，不發送信號。
  - *判定*：**PASS**。

---

## 架構符合度
- **分層邊界**：
  - `internal/backend/opencode/process.go` 專注於 POSIX 進程組操作與 Linux `/proc` 身分探測，封裝核心階層細節。
  - `cmd/asura/cancel.go` 負責使用者介面互動、跨行程排他鎖握手、調度 SQLite 儲存層狀態與回報錯誤。
  - 分層清晰，無反向相依或循環引用。
- **依據與限制**：依據 Linux `/proc` 檔案系統規範與 POSIX 進程組語意，在非 Linux 系統上行為由對應 build tags 隔離。

---

## 安全性檢查
- **存取控制與授權檢查**：取消端在執行操作前，均透過資料庫驗證任務存在性與擁有權。
- **PID 重用與誤殺防禦**：
  - 核心不可變 `StartTime`（`/proc/<pid>/stat` 欄位 22）優先核驗；
  - 孤兒場景由 Runner 憑證（PID + StartTime）與組內 `ASURA_TASK_ID` 雙重防線防護；
  - 當所有證據均無法佐證時，回退至失敗報錯，徹底杜絕盲目對重用 PGID 發送 SIGKILL 的高危險行為。
- **假成功防禦**：身分未確認時嚴禁清空 PGID、嚴禁回傳 exit code 0，杜絕「看似成功、實則孤兒進程失控殘留」的假綠燈。

---

## 程式品質與效能
- **可讀性**：程式碼命名明確（如 `hasProcessGroupMemberWithTaskID`、`VerifyTaskProcessGroup`），三態返回值的語意在註解中有清晰完整的規格說明。
- **效能考量**：遍歷 `/proc` 僅在 Leader 已退出且 Runner 失效的邊界罕見孤兒狀態下觸發；遍歷過程中快速過濾非數字目錄與非目標 `pgrp`，開銷極低。
- **健壯性**：在遍歷 `/proc` 時明確忽略短暫退出的進程（`os.ErrNotExist`），防止進程生滅競態引發虛假報錯。

---

## 流程判定
- **判定結果**：`PASS`
- **判定理由**：
  1. 獨立審查員已完成當前 HEAD (`6701a37`) 的完整嚴格審查；
  2. 缺陷 M-01 得到精準修復，無任何殘留或新增之 MUST FIX 問題；
  3. SCN-004 驗收標準與破壞性邊界條件皆有確實、真實的測試證據；
  4. 審查報告已完備產出並保存為 `docs/issues/issue-1/review-6701a37.md`。
