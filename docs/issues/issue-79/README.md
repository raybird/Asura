# Issue 79 - Runtime 未驗證時，--backend auto 建立任務失敗（audit_invalid） (Small)

## 概述

GitHub [#79](https://github.com/raybird/Asura/issues/79)。#73 新增了 inventory reason `version_unverified`，也更新了 `backend.validUnavailableReason`，但漏了稽核驗證 `audit.probeReason()` 的白名單。auto 的 admission 快照包含所有 runtime，所以只要其中一個是 `version_unverified`，稽核 header（`internal/audit/types.go:295`）就驗證不過：

- 本機 `run --backend auto`：`create task in storage: audit_invalid`
- daemon `ask --backend auto`：`failed to persist initial task: audit_invalid`

admission 拒絕的稽核紀錄（`internal/audit/rejection.go:57`）也使用同一個白名單。

2026-10-28 在 #74 的 Codex 端到端發現。`origin/main`（`a5e2e58`）的 binary 也重現得到，證據見分支 `issue-74-cancel-process-tree` 的 `80afa75:docs/issues/issue-74/evidence/e2e.md`「發現：auto 路徑的稽核驗證」（#74 合併前，main 上沒有這份檔案）。

## 涉及檔案
- `internal/audit/types.go`：`probeReason()` 白名單加入 `backend.VersionUnverified`
- `internal/storage/sqlite_execution_audit_test.go`：稽核 header 的驗證測試（經 `CreateTask` 與 `Header.Validate`）
- `cmd/asura/runtime_selection_test.go`：本機 auto 路徑的整合測試（建立任務、admission 拒絕投影）
- `CHANGELOG.md`：不修改。缺陷只存在於尚未發布的 #73 變更（`[Unreleased]`），發布時不會出現在任何版本裡

## 驗收條件

1. **AC-1**：主機上有一個 runtime 可用、另一個是 `version_unverified` 時，本機 `run --backend auto` 能建立任務，並由可用的 runtime 完成 — 檢查方式：`cmd/asura` 整合測試（假 runtime：codex 內建 0.155.0、opencode 1.18.31）；另以真實 binary 執行本機 run
   - 失敗路徑：任務建立失敗（`audit_invalid`），或結束碼不是 0
2. **AC-2**：同樣條件下，daemon `ask --backend auto --wait` 能建立並完成任務 — 檢查方式：以真實 binary 啟動獨立的 daemon 執行 ask，與 AC-1 共用同一個稽核驗證（`Header.Validate`）
   - 失敗路徑：`failed to persist initial task: audit_invalid`
3. **AC-3**：稽核驗證對其他 reason 的嚴格度不變：快照裡 `Unavailable` 的 runtime，reason 不在已知清單裡時仍然拒絕；`Available` 的 runtime 帶 `version_unverified` 時也拒絕 — 檢查方式：`internal/storage/sqlite_execution_audit_test.go` 的 `TestExecutionAuditAcceptsUnverifiedRuntime`
   - 失敗路徑：未知 reason 或狀態不一致的快照被接受

- **核准日期**: 2026-10-28
- **核准來源**：
  - 使用者 2026-10-28 在 #74 的 Q3 回答「另開 issue 先修 (Recommended)」。
  - GitHub #79 的問題描述：`--backend auto` 在有未驗證的 runtime 時必須可用 → AC-1、AC-2。
  - AC-3 是既有行為，維持不變。

**風險**: Low — 只在列舉的白名單加入一個已存在於 `backend.validUnavailableReason` 的值，行為明確、容易回復；以既有稽核測試與 AC-3 守住其他 reason 的嚴格度。

## 實作與驗證步驟
1. ✅ **AC-1、AC-3：稽核接受 `version_unverified`** — 驗證方式：先寫整合測試與單元測試，確認紅燈；再修改白名單，確認轉綠
   - 完成證據（2026-10-28）：
     - `internal/storage` 的 `TestExecutionAuditAcceptsUnverifiedRuntime`：auto 快照為 codex 可用、opencode `version_unverified`。修改前 `CreateTask` 回 `audit_invalid`（紅），修改後通過，稽核記下 `reason=version_unverified`。同一個測試涵蓋 AC-3：未知的 reason（`not_a_reason`），以及 `Available` 卻帶 `version_unverified`，都仍以 `ErrInvalid` 拒絕。
     - `cmd/asura` 的 `TestRuntimeSelectionLocalAutoWithUnverifiedRuntime`：假 codex 0.155.0（內建）、假 opencode 1.18.31（未驗證）。修改前 `code=1: error: create task in storage: audit_invalid`（紅），修改後任務完成，`status` 顯示結果與 `version_unverified`。
     - 兩個測試保障的是不同層級（稽核驗證本身、CLI 的建立任務路徑），各自保留紅燈。
     - 附帶效果（2026-10-28 PR #80 審查 SHOULD FIX 1）：admission 拒絕投影（`rejection.go:57`）也使用同一個白名單。修正前，只要快照裡有 `version_unverified`，`Rejection.Validate()` 就失敗，CLI 不印 `Admission rejection:`；Web 的 400 回應與 daemon 的 `RejectionError` 也一樣拿不到投影。修正後恢復成 #73 之前的契約：拒絕時一定附上投影。`TestRuntimeSelectionLocalAutoRejectionWithUnverifiedRuntime`（只有未驗證的 codex 0.163.0）：把 `types.go` 換回 BASE 版本時失敗（`rejection projection missing`），HEAD 通過。
2. ✅ **AC-1、AC-2：真實 binary** — 驗證方式：以獨立 DB 與 socket 啟動 daemon，執行本機 run 與 daemon ask 的 `--backend auto`。驗證紀錄放在 scratchpad 的 `XDG_DATA_HOME`，只含 Codex 0.163.0，不碰真實紀錄檔。
   - 完成證據（2026-10-28T03:11Z）：
     - inventory 為 codex `available`，其餘三個 `version_unverified`。
     - 本機 `run --backend auto`（`ASURA_RUNTIME_PRIORITY=codex`）結束碼 0，任務 `task-1793157086-76b3cac11b58` 的結果為 `E2E-LOCAL-auto`，`Backend version (striker): codex-cli 0.163.0`。
     - daemon `ask --backend auto --wait` 結束碼 0，輸出 `E2E-DAEMON-auto`。
     - daemon 以確認過的 PID 停止，socket 已移除；`~/.local/share/asura/` 維持不存在。
     - 修改前的對照：`origin/main` 的 binary 在同樣條件下兩條路徑都是 `audit_invalid`（`80afa75:docs/issues/issue-74/evidence/e2e.md`「發現」，分支 `issue-74-cancel-process-tree`）。

專案 gate（2026-10-28，審查修正後重跑）：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...` exit 0，24 個套件 ok，沒有 FAIL；skip 26 個，與 main 相同（live opt-in 或版本不是內建身分），不算通過。

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-28 | 建立。#74 的 Codex 端到端發現此缺陷，使用者 Q3 選擇另開 issue 先修 | Raybird |
| 2026-10-28 | PR #80 獨立審查 PASS（[報告](https://github.com/raybird/Asura/pull/80#issuecomment-6459894405)）。使用者選擇處理 SHOULD FIX 1（補拒絕投影測試）與 NICE 2～4（測試位置、gofmt、證據連結） | Raybird |

---
**建立日期**: 2026-10-28  
**分級**: Small  
**風險**: Low\
**狀態**: 進行中
