# 程式碼審查報告

## 變更摘要
本次審查範圍為分支 `issue-2-local-ai-integration` 相對於 `main` 分支（commit `1cd0301`）之全部變更（共 9 個 commit，HEAD SHA 為 `112d67ea6c27806b72a6b29cf12948bbcb947098`）。本次變更完成了 Issue #2 本機 AI 執行路徑整合（Phase 1.5），實現了：
1. **OpenCode 1.18.23 Adapter 整合**（`internal/backend/opencode/adapter.go`）：隔離版本調度、JSON 事件串流解析、自然語言直接傳遞（不經 shell 解譯）、進程組（PGID）生命週期管理與 `OPENCODE_PERMISSION` 邊界控制。
2. **Daemon 服務端授權與非同步接受協定**（`internal/daemon/daemon.go`, `internal/daemon/pool.go`）：專案白名單前置檢查（INT-002）、非同步接受協定（P4-T2）、請求去重（INT-004-REDELIVERY）與全域容量管理（INT-003）。
3. **CLI 指令擴充**（`cmd/asura/ask.go`, `cmd/asura/cancel.go`, `cmd/asura/status.go`, `cmd/asura/run.go`）：新增 `asura ask`，保留 `asura run` 既有 shell 語意；Daemon 優先之查詢與共同取消入口。
4. **全套外迴圈整合測試與回歸驗證**（`tests/integration/int_test.go`）：八項已核准 Scenario（INT-001～INT-007, INT-004-REDELIVERY）端到端驗收，全套 race 回歸通過。

## 審查版本
- Reviewed HEAD SHA：`112d67ea6c27806b72a6b29cf12948bbcb947098`
- 獨立 reviewer：Independent Code Reviewer Subagent
- Review artifact：`docs/issues/issue-2/review-112d67e.md`
- Reviewed patch-id：`764a9adde53d54e36ebe54db900c2fa332402983`

## 整體評估
本次提交嚴格遵守 `docs/AGENTS.md` 1.22 規範與 TDD/BDD 雙迴圈驗證流程。六個 Phase（Phase 1～6）與八項核准 Scenario 責任拆解清晰，四段證據紀錄詳實。核心邏輯在安全性（指令不經 shell、前置授權檢查）、併發安全（全套 `go test -race` 通過零競態）、非同步接受協定與跨程序資源回收（PGID 三態回收）上設計嚴謹。未發現阻礙合併之 🔴 MUST FIX 問題，判定為 **PASS**，並就 3 項邊界防禦與斷言強度列為後續改進建議（SHOULD FIX）。

## 需求符合度
- 需求描述：Issue #2 本機 AI 執行路徑整合 Phase 1.5
- 是否符合需求：是
- 缺漏或過度實作說明：無。保留了 `asura run` 的 shell 語意，新增 `asura ask`，符合 TBD-1；手動啟動 Daemon 且不可用時明確報錯，符合 TBD-2；服務端集中管理白名單，符合 TBD-3；容量與持久化確認後立即回傳 Task ID，斷線續跑且去重，符合 TBD-4 與 INT-004 / INT-004-REDELIVERY；共同取消入口與安全回收，符合 TBD-5 與 INT-005 / INT-006；操作邊界落實，符合 TBD-6 與 INT-007。未引入未核准的插件架構或遠端協定。

## 問題與風險

嚴重度標籤說明：
- 🔴 **MUST FIX** — 邏輯錯誤、安全漏洞，必須在 merge 前修正
- 🟡 **SHOULD FIX** — 潛在問題、邊界未處理，建議修正
- 🟢 **NICE TO HAVE** — 可讀性、最佳實踐，可列入後續 backlog

- 🟡 **問題 1（去重邏輯撞擊 SQLite 主鍵約束）**：`internal/daemon/daemon.go:237` 在請求去重檢查中排除了 Cancelled 任務（`existing.Status != storage.StatusCancelled`）。若使用者或自動排程器以相同 RequestID 重新送出先前已取消之任務，流程會直接向下進入 `CreateTask`，導致 SQLite 丟出 `UNIQUE constraint failed: tasks.id` 低階錯誤，而非語意明確的業務錯誤或回傳已取消任務。建議在檢測到任務已處於 Cancelled 終態時，明確拒絕重送或提供重新啟動之語意處理。
- 🟡 **問題 2（WorkerPool 滿載回滾未檢查 DeleteTask 錯誤）**：`internal/daemon/daemon.go:273` 當 `pool.SubmitAsync` 因滿載（`ErrCapacityExceeded`）失敗時，執行 `_ = d.store.DeleteTask(context.Background(), taskID)`，未檢查刪除錯誤。若 SQLite 遭遇瞬態 I/O 鎖定導致刪除失敗，該任務將永遠停滯在 `StatusPending`，且 `RecoverInterruptedTasks` 僅重置 `StatusRunning` 任務，會產生無法被清理的幽靈 pending 任務。建議記錄刪除錯誤日誌，並擴充啟動恢復機制涵蓋超時未執行的 pending 任務。
- 🟡 **問題 3（外迴圈測試 TestINT007 斷言強度不足）**：`tests/integration/int_test.go:80-93` 的 `TestINT007` 測試中，呼叫 `adapter.Run(ctx, tmpDir, "請在 test.txt 加上一行 appended")` 後僅檢查了輸出非空（`out == ""`），未讀取 `test.txt` 檔案驗證追加內容是否存在，且未在外迴圈中測試限制操作（如刪除檔案）被攔截之分支。雖然 Phase 1 探測與內迴圈測試已有驗證，但外迴圈測試若斷言過弱，日後可能因後端行為變更而產生假綠燈。建議補強檔案內容斷言。
- 🟢 **問題 4（IPC 缺少最大 Payload 大小限制）**：`internal/daemon/daemon.go:186` 直接使用 `json.NewDecoder(conn).Decode(&req)` 解碼，未對傳入之 Prompt 長度設定上限（如 10MB）。超大型 Prompt 可能造成 Daemon 記憶體突增，建議增加請求大小限制。

## 驗收標準與測試證據
- 規格修訂查核：覆核 `git diff 1b92a60..HEAD -- docs/issues/issue-2/README.md`，僅回填核准 commit `1b92a60`、補充實作計畫拆解連結與 Timeline 執行時序紀錄，核准表中 8 項 Scenario 均維持已核准，條件、動作與預期結果無任何修改。
- 驗收標準覆蓋與紅綠燈證據：
  - INT-001：具備 Phase 2 內外迴圈紅燈日誌（`adapter not implemented`, `no default runner configured`）與 Phase 6 外迴圈 PASS（6.67s）。
  - INT-002：具備 Phase 3 內外迴圈紅燈日誌（前置授權缺失）與 Phase 6 外迴圈 PASS（0.02s）。
  - INT-003：具備 Phase 3 內外迴圈紅綠燈日誌與 Phase 6 外迴圈 PASS（0.07s）。
  - INT-004：具備 Phase 4 內外迴圈紅綠燈日誌與 Phase 6 外迴圈 PASS（0.12s）。
  - INT-004-REDELIVERY：具備 Phase 4 內外迴圈紅綠燈日誌與 Phase 6 外迴圈 PASS（0.17s）。
  - INT-005：具備 Phase 5 共同取消與進程組三態安全回收紅綠燈日誌與 Phase 6 外迴圈 PASS（0.17s）。
  - INT-006：具備 Phase 5 中斷恢復紅綠燈日誌與 Phase 6 外迴圈 PASS（0.01s）。
  - INT-007：具備 Phase 1 探測、Phase 2 內迴圈紅綠燈日誌與 Phase 6 外迴圈 PASS（23.86s）。
- 回歸測試：全套回歸 `go test -count=1 -race ./...` 耗時 52.775s，全數套件通過，零 Data Race。
- 誠實回報與防作弊檢查：逐項確認測試無 `[no tests to run]` 或 `SKIP`；斷言期望值均來自獨立字面值或獨立原子計數器，無同義反覆（Tautology）。
- Gate 豁免查核：無（無任何 Gate 豁免）。

## 破壞性邊界條件
- **Edge Case 1（已取消任務重送之主鍵衝突）**：
  - 輸入／狀態：同一 RequestID 先行提交後被取消，隨後客戶端以同一 RequestID 再次送出請求。
  - 預期結果：系統應回傳原取消狀態，或明確回報業務錯誤（如 "task already cancelled"）。
  - 現有覆蓋：`internal/daemon/daemon.go:237` 排除了 cancelled 任務，導致執行 `CreateTask` 引發 `UNIQUE constraint failed: tasks.id`。
  - 判定：🟡 SHOULD FIX。
- **Edge Case 2（滿載回滾刪除失敗殘留孤兒 Pending 任務）**：
  - 輸入／狀態：WorkerPool 飽和時 `SubmitAsync` 失敗，回滾呼叫 `DeleteTask` 若發生資料庫錯誤被忽略。
  - 預期結果：任務不應成為無法執行的永久 Pending 狀態；重啟時應能被自動清理或標記。
  - 現有覆蓋：現有 `RecoverInterruptedTasks` 僅重置 `StatusRunning`，未涵蓋逾期 Pending。
  - 判定：🟡 SHOULD FIX。
- **Edge Case 3（外迴圈整合測試副作用斷言過弱）**：
  - 輸入／狀態：`TestINT007` 調用 OpenCode 修改檔案。
  - 預期結果：測試應驗證檔案實際內容被修改，並驗證未授權操作被攔截。
  - 現有覆蓋：目前僅斷言 `out != ""`，未檢查磁碟檔案。
  - 判定：🟡 SHOULD FIX。

## 架構符合度
- 分層與依賴方向查核結果：
  - CLI (`cmd/asura`) -> IPC Client -> Unix Domain Socket
  - Daemon (`internal/daemon`) -> WorkerPool (`pool.go`) -> Engine (`internal/engine`)
  - Engine (`internal/engine`) -> Adapter (`internal/backend/opencode`) & Storage (`internal/storage`) & Policy (`internal/auth`)
  - 依賴方向單向內聚，無下層對上層的反向引用，無循環依賴。
- 依據與限制：符合專案分層約定；所有自然語言輸入均經由 Adapter 封裝，不經 shell。

## 安全性檢查
- 存取控制與授權檢查：Daemon 在接收請求後、進入 WorkerPool 前即呼叫 `policy.AuthorizeWorkspace` 阻擋未授權目錄，保證未授權請求 0 後端啟動、0 佔用並發容量（INT-002）。
- 輸入驗證與 injection 風險檢查：`internal/backend/opencode/adapter.go:121-130` 將 prompt 作為獨立參數傳遞給 `exec.CommandContext`，未透過 `sh -c` 解譯，完全免疫指令注入。
- 敏感資料與環境變數檢查：無硬編碼金鑰，OpenCode 探測腳本與 Adapter 設定均正確隔離快取與目錄。
- 資源回收安全性：進程組取消（`KillProcessGroup`）前先做身分檢驗與進程組確認，避免誤傷系統重用 PID/PGID 之無關程序（INT-005）。

## 程式品質與效能
- 可讀性與命名：結構分明，註解詳細標註 SCN/INT 編號與時序修復追蹤。
- 架構合理性：非同步接受協定（Asynchronous Acceptance Protocol）解耦了 HTTP/IPC 請求連線與耗時的 AI 推理執行。
- 維護性與效能觀察：採用 Unix Domain Socket 與連接池並行限制，關閉時以 `pool.Close()` 與 `wg.Wait()` 進行完整排水（Drain），資源管理得宜。

## 改進建議
1. **去重邏輯完善**：在 `internal/daemon/daemon.go:237` 中，對已處於 `StatusCancelled` 的任務回傳明確的取消任務狀態或錯誤，避免撞擊資料庫主鍵約束。
2. **Pending 任務孤兒防禦**：在 `internal/storage/sqlite.go` 的 `RecoverInterruptedTasks` 中，將建立時間超過一定閾值（如 10 分鐘）且仍處於 `StatusPending` 的孤兒任務一併標記為 `StatusFailed`。
3. **外迴圈斷言加強**：在 `tests/integration/int_test.go` 的 `TestINT007` 中，加上 `data, err := os.ReadFile(testFile)` 並檢查 `strings.Contains(string(data), "appended")`。

## 流程判定
- **`PASS`**
- 判定理由：獨立 reviewer 已完成當前 HEAD（`112d67e`）審查，八項 Scenario 與全套 race 回歸測試全數通過，無任何 🔴 MUST FIX，規格修訂合規，紅綠重構四段證據齊備且無假綠燈。所列 3 項 SHOULD FIX 屬邊界防禦與測試加固，不影響核心行為正確性與交付標準。
