# 需求分析

查核日期：2026-09-30；基準 main `231d846`。

## 需求描述與目標

沿用 #23 的 capture → candidate → approve/discard → git-approved memory，新增 #27 J1～J4 Web 入口；既有人工 memory 與 sealed Task 保持原契約。需求與不包含項目以 issue README 與 GitHub 原文為準。

## 現況分析

- `internal/daemon/gitmem_handlers.go`：各 handler 在 store 操作前呼叫 `AuthorizeCanonicalWorkspace`；approve 使用 `ApproveCandidateTx`。Capture 回傳 candidate 及完整 GitFacts，不能直接當成 Web bounded DTO。
- `internal/storage/sqlite_gitmem.go`：list 的 SQL LIMIT 最多 50，排序 created_at DESC/id ASC，draft SQL 前綴 513 bytes；Go 摘要可再附加 `...`，實際不是絕對 512 bytes。沒有 cursor。detail 讀完整 draft/facts，沒有 byte 限制。
- `internal/web/memory.go`：既有 manual memory 使用 strict DTO、64 KiB body、32 KiB content、512 KiB HTTP JSON 上限；可沿用入口驗證模式。
- `internal/web/server.go` exchange：10 秒 timeout、HTTP context 取消後關閉獨立 Unix socket；decoder 尚未加 response reader 上限。
- `internal/daemon/daemon.go`：connection context 配合 watchIPCDisconnect，Git handler 使用 connCtx。半關閉寫入與完整斷線不能混淆。
- `internal/gitmem/capture.go`、checkpoint.go、git.go：capture 有 10 秒硬期限，CommandContext 取消時 kill process group。
- `internal/web/page.go`：memory/session generation 已提供遲到 response 隔離模式，可延續。

## 問題點

缺 Web API/UI、cursor 與 bounded detail；HTTP 編碼上限不能取代 SQL/IPC 讀取上限。現有 cancellation 機制可沿用，但尚無新 capture HTTP 端到端證據。

## 已執行查核

2026-09-30 執行：

```bash
go test -count=1 -v ./internal/daemon ./internal/gitmem ./internal/storage -run 'TestDaemon_GitMemory_BoundedCandidatesListAndDetail|TestBoundedMemoryQueryCanceledOnIPCDisconnect|TestCaptureService_PartialCandidate_CannotBeApprovedUntilCompleteUpgrade|TestApproveCandidateTx_PartialCandidateRejected'
```

exit 0；daemon 1.061s、gitmem 0.061s、storage 0.015s；四個頂層測試通過。Daemon list/detail fixture 有 50 筆、每筆 10 KiB draft。Disconnect 測試只覆蓋 memory page/detail，不能宣稱 capture HTTP disconnect 已驗收。超大 provenance、完整 capture disconnect 與大量游標遍歷 probe 尚待執行；計畫保留前置 gate，不偽造結果。
