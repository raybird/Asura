# 需求與現況分析

2026-09-23。來源：[GitHub #21](https://github.com/raybird/Asura/issues/21)。本檔記錄規劃時已讀到的現況；待核准的外部行為以 [README](README.md#gherkin-驗收劇本) 為準。

## 需求描述

讓已登入單人 Web Console 使用者對授權 workspace 的 manual memories 列出、查看、新增及確認刪除，提交任務時選 explicit IDs 或明確 opt-in `bounded`，並查看已接受 Task 的記憶選取與 stage 稽核。#21 排除新檢索引擎、自主記憶、跨 workspace 分享、Web 任意 backend／mode 控制及獨立 Runner。

## 現況分析

- `internal/web/server.go` 先檢查 Host、變更方法的 Origin、session cookie，再處理 `/api/...`。`POST /api/tasks` 解碼成 `engine.SubmitRequest`，但建構 IPCRequest 時僅轉送 `ID/Workspace/Prompt`。現有 `decode` 限制 HTTP body 64 KiB 並拒絕未知欄位；此解碼形狀目前會接受 `resolved_context` 欄位但不轉送。
- `internal/web/page.go` 提供登入、workspace 選單、prompt 提交、Task 查詢／取消。`currentRequestID` 在成功後重設，失敗後保留，但沒有把首次送出的完整 payload 固定；表單若變更後重試，可用同 ID 送不同內容。頁面用 `textContent` 顯示文字，專案選單清空使用 `innerHTML = ''`，沒有插入使用者文字。
- `internal/daemon/daemon.go` 的 memory add/list/get/remove 每次先呼叫 `AuthorizeCanonicalWorkspace`；Task submit 亦先 canonical 授權、正規化 IDs、計算 fingerprint，重送時回傳原 Task，之後才解析 explicit IDs、bounded recall、密封與執行。無效 ID 在執行前拒絕。`memory_list` 使用 `ListMemories` 全量讀取，無法直接暴露為 Web 清單。
- `internal/storage/sqlite_memory.go` 的 `ListMemories` 依 `(created_at,id)` 排序，但載入完整 content 及全部列；Get/Delete 依 workspace 與 ID 精確查詢。`internal/storage/sqlite.go` 目前有 `(workspace,id)` 索引供 recall，沒有對清單排序的 `(workspace,created_at,id)` 複合索引。
- `internal/engine/engine.go` 的 `SubmitRequest` 已有 `memory_ids`、`memory_recall`、`resolved_context`。Task 保存 `memory_refs`、`resolved_context`、`pipeline_trace`；Phase 3F 的 Decision 包含 explicit／auto IDs、政策、選取分數、略過計數、估算額度及首次 stage admission。舊 Task 可缺 recall 決策。
- `internal/auth/policy.go` 允許根目錄及其子路徑，但每筆 memory 的 scope 仍是 canonical workspace 字串精確相等；父子目錄間不共享記憶。越界 symlink 與撤權必須在每次 Daemon 操作拒絕。

## 問題點總結

Web 缺少記憶管理與召回入口；既有全量 IPC 清單會將內容無界載入及傳輸；前端重試沒有保存完整 request payload；歷史 Task 雖保存稽核資料，Web 尚未展示。這些分別影響可用性、資源／隱私邊界、冪等性及可追溯性。

## 目標與邊界

以既有 Web session 與 Daemon workspace 授權作雙層邊界，新增服務端有界瀏覽與明確 detail，允許經核准欄位轉送至共同 submit 流程，從已保存 Task 資料呈現稽核。具體 API、大小與 UI 交互決策見 [technical-analysis.md](technical-analysis.md)，未核准前不修改產品行為。
