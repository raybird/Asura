# Issue 29 需求分析

分析日期：2026-10-01。基準：`7785a204dc0de486938ee223342ae2af17eb6c2a`。

## 需求與現況

`internal/daemon/daemon.go` 先授權 canonical workspace、計算 fingerprint，再檢查 RequestID；既有 Task 直接回傳，cancelled 不接受 redelivery。新請求先解析 explicit、再 bounded recall、取得容量、序列化 ExecutionContext，最後 CreateTask。這個順序不能因 ledger 而在重送時新增事件。

`internal/daemon/recall.go` 在 250ms context 內讀候選並以實際 ComposePrompt 增量計量。`internal/recall/recall.go` 固定 256 candidates、4096 query bytes／128 terms、4 auto memories、8192 formatted bytes／2048 estimated tokens；排序 lexical score 降序、ID 升序，explicit 去重且不受 auto budget 裁切。

`internal/storage/sqlite_memory.go` 的 RecallCandidates 用 SQL LIMIT 257 與欄位 sentinel，先 exact workspace 過濾；排除 legacy 不完整 Git 記憶。候選順序與 bounded SQL 必須保留，utility 不能擴成全表讀取。

`internal/pipeline/types.go` CriticReview 僅 Verdict／Findings／Summary／Metadata；`stage_critic.go` 以既有 backend 呼叫解析 review 並驗證 verdict，尚無 memory usage 訊號。`engine.go` 既有 trace persistence failure 與執行錯誤關聯，UFL 次要失敗必須另設界線，不能改壞既有 trace 契約。

`cmd/asura/status.go` 可輸出 sealed Recall JSON；目前沒有 recall_id、utility ledger、policy、per-memory aggregate 或 utility query。

## 問題與目標

只記錄選取不足以判斷使用效用。模型估計不能當 exact，也不能把 missing 當 unused。目標是 bounded immutable ledger、可重建且冪等的 aggregate、預設不改排序、顯式有界 down-weight，以及授權後才能查詢的 CLI audit。

本文件因需要調查既有執行與資料流而建立；沒有 production 變更或 probe 成功的主張。
