# 需求與現況分析

分析日期：2026-09-22；基線 `8576dd0323750a1aa8f2c550c0f69e8edd30e55d`。

## 需求與目標

減少每次手選 memory IDs 的負擔，同時保持 default-off、精確 scope、接受時快照與 explicit 優先。只讀既有 manual 記憶；不產生新記憶、不查 repository、不增加模型呼叫。

## 現況

- `daemon.go` submit：AuthorizeCanonicalWorkspace → explicit IDs 去重排序 → mode/backend 正規化 → fingerprint → redelivery → explicit ResolveMemoriesForTask → capacity → snapshot／CreateTask → dispatch。CreateTask 的 duplicate request 競態分支也會重新比對指紋，須一併保護。
- 重送前仍檢查當前 workspace 與既有 Task 授權；cancelled request 拒絕。不得因自動召回放寬。
- `sqlite_memory.go` 以 workspace/id 精確解析 explicit；ListMemories 以 created_at/id 排序且無 LIMIT。sqlite.go 只有 workspace 索引，單連線；全量讀取不能直接沿用。
- `ExecutionContext` 保存原始 prompt、workspace、backend、完整 snapshots、refs。ComposePrompt 固定 `### Project Context`、每筆 ID 包裝與換行，再接原始 prompt。
- 無記憶的接受分支目前沒有序列化 resolved_context；bounded 零匹配必須另外保存決策，以區分 off。
- Engine 執行優先使用提供或持久化的 context；queued/restart 路徑應只讀快照。既有 queued/symlink 測試通過不等於新的 recall restart 已通過。
- Daemon 已持有 ContextConfig。Pipeline 先 Prepare 實際 prompt，再 EvaluateStage。Oracle 有固定角色指令，Striker/Critic 含動態前序產物；接受時無法知道後者大小。
- estimator 為 ASCII ceil(n/3.5) + 非 ASCII 每字 2 tokens；reserve 預設為 prompt 的 20%。這是 estimated，未設可信窗口時 bypass，不能保證模型容量。
- status 現在輸出 Task、pipeline 摘要與結果，但沒有 recall provenance。

## 問題與範圍

召回需要獨立有界候選讀取、可重現但有限的詞彙相關性，以及可選 JSON 決策。候選採 ID 前綴會漏掉排序位置後面的相關記憶，必須讓使用者核准這個品質／有界性取捨。explicit IDs 可補選被漏掉的內容。

資料模型沒有 embedding、importance、更新時間；不得假設存在。分析文件建立原因：現況授權、指紋、sealed context 與預算組裝須讀程式才能確認；技術分析另有文字排序與索引方案的實質取捨。

涉及檔案與風險唯一來源見 [README](README.md)。
