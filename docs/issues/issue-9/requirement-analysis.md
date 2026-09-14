# 現況與需求分析

分析日期：2026-09-14。程式基底：main `202f1b3`。

## 需求與目標

人工保存可再取用的專案知識，能追溯來源並明確刪除。先確保跨專案不外洩；不推測自動整理、模型記憶或語意檢索需求。待確認政策以 [README](README.md#待確認事項) 為權威。

## 現況

- `SQLiteStore` 初始化 tasks、cron_schedules、cron_runs；使用 WAL、foreign_keys 與單一連線。現有 Task 保存 prompt、workspace、result，不是人工維護的記憶集合。
- Daemon IPC 由 action 分派，已有 submit、status、cancel 與 cron 管理；可在同一中樞加入獨立記憶動作，不需啟動 Worker 或後端。
- `Policy.AuthorizeWorkspace` 允許授權 root 及子目錄；`CanonicalWorkspace` 解析路徑但在解析失敗時退回清理字串。記憶動作不能將此回退視為授權成功，必須先驗證授權。
- WorkspaceQueue 已以 workspace 排隊，但這是執行互斥，不是資料隔離。記憶 SQL 必須自身帶 scope 條件。
- Engine 將 req.Workspace 與 req.Prompt 交給 runner；自動注入會改變既有模型輸入，並涉及接受／重送後記憶變動時的一致性。本期建議維持原任務契約。

## 問題與邊界

授權 root 可能涵蓋多個專案，不能直接用 root 作共享記憶鍵。父子目錄是否共享是產品決策，現有授權程式不能代替決策。刪除記憶也不能誤刪 Task 或 Cron。

需求分析文件有建立必要：精確 scope 與任務輸入行為必須閱讀程式才能確認，保存與任務使用方式仍需決策。必要檔案只維護於 README。
