# Issue 31 驗證證據

執行日期：2026-10-01。核准基準 `56871b8de89bb8dc774185780fd48e4667b10d88`，實作工作區 `issue-31-memory-lifecycle`；被測 Go 來源完整 SHA-256 見 [tested-source.sha256](tested-source.sha256)。提交後來源相同才沿用證據。沒有真實模型或 AI adapter 調用。

## P1-T1 — 既有刻畫
`go test ./internal/storage ./internal/daemon ./internal/engine ./internal/recall` exit 0：[baseline.log](baseline.log)。包含既有 scope isolation、Task sealing/redelivery、CRUD/delete 與 migration 測試。

## P2-T1 / P3-T1 / P4-T1 — lifecycle 紅綠
- 單元與 IPC 外迴圈紅燈：`go test ./internal/storage ./internal/daemon -run TestLifecycle -count=1` exit 1，舊 Memory 缺 active/durable metadata、相同 IPC 請求重試建立不同 successor：[lifecycle-red.log](lifecycle-red.log)。兩種不同可觀察層級保留各自紅燈。
- legacy snapshot 單元紅燈：`go test ./internal/engine -run TestLifecycle -count=1` exit 1，舊 sealed JSON 缺 active 預設：[legacy-snapshot-red.log](legacy-snapshot-red.log)；最小修復後相同命令 exit 0：[legacy-snapshot-green.log](legacy-snapshot-green.log)。不重寫原 persisted JSON。
- Storage 綠燈：`go test ./internal/storage ./internal/gitmem -count=1` exit 0：[storage-green.log](storage-green.log)。真實 SQLite SQL trigger 在 predecessor UPDATE／Git provenance INSERT 注入失敗，驗 rollback；8 並行 CAS 只一個成功；重試、payload conflict、self/cross-scope、chain、deleted tombstone、300 superseded candidate filtering、UFL predecessor保留與 successor neutral 均有字面值 assertion。
- Daemon／recall 綠燈：`go test ./internal/daemon ./internal/recall -run TestLifecycle -count=1` exit 0：[daemon-green.log](daemon-green.log)。explicit audit 顯示 superseded/by；Task 原 context 字串在 delete／重啟／status/redelivery 後完全相同；durable 固定 score=1、weight=1000000，不改 tie-breaker。
- CLI 真實 IPC：`go test ./cmd/asura -run TestLifecycleCLI -count=1` exit 0：[cli-green.log](cli-green.log)，add/durable/detail 與缺 operation ID 拒絕。
- migration rollback 補充：`go test -race ./internal/storage -run TestLifecycleMigrationRollback -count=1` exit 0：[migration-rollback.log](migration-rollback.log)。failed migration 不留 marker，移除失敗 trigger 後可重試。

## P4-T2 — Git 原子契約
暫時在相同工作區恢復 main 原 ApproveCandidateTx 行為（僅新增 options 型別欄位使測試可編譯，沒有新行為），執行 `go test ./internal/storage -run TestLifecycleGitApprovalAtomic -count=1` exit 1：既有 approval 靜默忽略不同 lifecycle payload：[git-baseline-red.log](git-baseline-red.log)。探測後 finally 還原正式實作；storage-green 與完整 regression 證明最終行為。單迴圈理由：此測試直接使用真實交易及 candidate/provenance，沒有 mock 或額外 UI 實作責任；Daemon 既有 Git approval IPC 另由完整 regression 覆蓋。

## P5-T1 — 性能與完整回歸
- `/usr/bin/time -v go test ./internal/storage -run TestLifecycleLargeDataProbe -count=1 -v` exit 0：[performance.log](performance.log)。10k/100k legacy migration、relations fixture，五次 recall/detail 答案均正確；migration <30 秒、各 query <2 秒。EXPLAIN 使用 idx_memory_active。time 記 Max RSS，包含 go tool/build 及測試子程序，不宣稱純 Daemon RSS；本機 Go 1.27.1 linux/amd64、Linux 6.8.0-142-generic，fixture 在臨時 SQLite，非生產效能承諾。
- `go vet ./...` exit 0：[vet.log](vet.log)。
- `go test -count=1 -race ./...` exit 0：[regression.log](regression.log)。全部套件，未刪除／跳過既有測試。
- `go build ./cmd/asura` exit 0；`git diff --check` exit 0。
- 最後 Go 變動為 migration rollback 新測試及 CLI help 字串，完整 race 使用稍早相同 production 邏輯；新增測試已獨立 race 綠燈，CLI 以最終來源重新通過。manifest 為最終來源；此差異明確揭露。

## code-simplify
2026-10-01 使用 code-simplify 檢查本次變更一次，no-op：shared transaction helper 已供 manual/Git 共用；IPC wrapper 保留不同回傳型別與既有公開 API。沒有可證明安全且實質更簡單的修改，不新增抽象或範圍外重構。最小實作綠燈與完整回歸同時作最終綠燈。

## 驗收映射
| 驗收 | 最終驗證 |
|---|---|
| MLIFE-001 | LegacyMetadata、LegacyMigrationAndBounds、LegacySealedSnapshot |
| MLIFE-002 | AtomicChainRetry、RollbackAndConcurrentCAS、IPCAtomicRecall |
| MLIFE-003 | CandidateLimitAndUtility、IPCAtomicRecall |
| MLIFE-004 | SealedTaskAndExplicitAudit |
| MLIFE-005 | AtomicChainRetry |
| MLIFE-006 | AtomicChainRetry（delete、重開與 deleted retry） |
| MLIFE-007 | DurableDoesNotBoost |
| MLIFE-008 | AtomicChainRetry（durable predecessor） |
| MLIFE-009 | AtomicChainRetry、IPCAtomicRecall、既有授權回歸 |
| MLIFE-010 | SealedTaskAndExplicitAudit |
| MLIFE-011 | CandidateLimitAndUtility、GitApprovalAtomic |
| MLIFE-012 | vet、race regression、build；10k/100k probe |

全部 Test 名稱以 TestLifecycle 為前綴。現有完整 regression 覆蓋 MEM/CTXMEM/RECALL/WEBMEM/GITMEM/WEBGIT/UFL/CTXWIN/TFACE/MBACK/BADAPT/Cron 與 queue/redelivery/cancel。
