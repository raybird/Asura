# 實作計畫與任務清單

建立日期：2026-09-14。規格及風險見 [README](README.md)。本檔是唯一任務來源；2026-09-14 七項驗收已核准，以下 Phase／Task 依相依順序執行；功能實作與測試尚未開始。

## 設計提案與使用方式

建議命令 `asura memory add/list/get/remove --workspace <path>`，add 收純文字、get/remove 收 ID。CLI → Daemon 授權與固定 scope → SQLite；不經 WorkerPool。既有命令與 prompt 維持原樣。

擬增 store 操作 `CreateMemory`、`ListMemories`、`GetMemory`、`DeleteMemory`，每個操作都要求 workspace；IPC 使用專屬 action 與具型別參數。實際簽名依已核准查詢契約與既有 IPC 限制決定，不增加泛用 repository 抽象。

## Phase / Task

| Task | 狀態 | 相依 | 產出與完成判準 |
|---|---|---|---|
| P1-T1 | ⏳ 規格核准完成，待介面檢核 | 無 | 2026-09-14 TBD-1～4 與全部 Scenario 已核准（來源及提交見 README）；實作前核對既有 IPC 輸入界限與錯誤映射，若需新增使用者限制則記錄具體影響，不把未決技術參數當作功能已完成 |
| P1-T2 | 📝 待實作 | P1-T1 | MEM-002～004、007 的 Daemon 外迴圈及 scope／授權單元紅燈；A 正向有效，B 固定資料無洩漏／變更的反向斷言確實可失敗 |
| P2-T1 | 📝 待實作 | P1-T2 | MEM-001、005 儲存紅燈後新增 schema 與 scope CRUD；舊 Task／Cron 資料升級前後一致，重啟及指定刪除通過 |
| P2-T2 | 📝 待實作 | P2-T1 | 接 Daemon 記憶處理器，全部操作使用同一經授權 scope；MEM-002～004、007 外／內迴圈由紅轉綠 |
| P3-T1 | 📝 待實作 | P2-T2 | CLI 參數及正式 IPC 接線，MEM-001、005、006 外紅與 CLI 單元紅後綠燈；失聯／儲存錯誤非零退出，無替代儲存寫入，管理零後端啟動 |
| P3-T2 | 📝 待實作 | P3-T1 | 完整記憶及既有 race 回歸，固定 prompt 對照證明無自動注入；更新 README 使用方式，提交可追溯審查資料 |

## 測試策略

| Scenario | 外迴圈 | 內迴圈 |
|---|---|---|
| MEM-001 | CLI 寫入 → 重啟 → 查詢固定文字及來源 | SQLite CRUD 與舊庫升級 |
| MEM-002 | 同 root A/B、錯配 scope/ID、資料不變對照 | SQL scope 條件及 Daemon 解析 |
| MEM-003 | 撤權／越界的讀寫刪除拒絕 | Policy 與 handler 守門 |
| MEM-004 | 別名可取、子目錄不可繼承 | 固定 canonical scope 與符號連結變更 |
| MEM-005 | 刪除 → 重啟 → 指定 ID 不存在、另一筆保留 | scope 限定刪除及錯誤 |
| MEM-007 | 撤權拒絕且資料不變 → 重新授權 → 原內容可讀並可寫入及刪除 | 授權檢查與保留資料，不因撤權清除記憶 |
| MEM-006 | 失聯／儲存失敗非零退出且無替代儲存寫入；受控 runner 零啟動及原 prompt 對照 | CLI 錯誤映射與 handler 失敗 |

不同層級分別有傳輸／授權／SQL 失敗面，保留雙迴圈。重構若 no-op，記理由並沿用同版最小實作綠燈。證據於原 Task 連結，保存日期、版本、命令、退出碼與固定期望／實際值；不把環境或編譯失敗當行為紅燈。

預定驗證命令（測試建立後才可執行；須核對實際測試數與零 SKIP）：

```bash
go test -count=1 -v ./tests/integration -run '^TestMEM'
go test -count=1 -race ./internal/storage ./internal/auth ./internal/daemon ./cmd/asura
go test -count=1 -race ./...
```

## 文件與交付檢查

- 核准集合與 Scenario 完全相等；待核准項不得放行實作。
- 新增 schema 的相容證據與拒絕路徑均有真實紅綠紀錄。
- 功能完成才更新根 README；現有 project.md 常青對照仍為範本，本期交付前登記使用者功能 → 根 README 的實際對照，不擴充其他文件。
- 2026-09-14 此次只有分析文件與 GitHub 立項，功能測試不適用；以相對連結、Scenario 對應、Markdown 圍欄及 git diff --check 檢查文件。
