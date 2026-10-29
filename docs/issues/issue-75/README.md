# Issue 75 - OpenCode 寫入角色沒有 OS 沙箱，模型可寫到 workspace 外

## 文件清單
- [implementation-plan.md](./implementation-plan.md)

## 概述

GitHub [#75](https://github.com/raybird/Asura/issues/75)。OpenCode 的寫入角色（Striker）直接在主機上執行，模型透過 bash 工具能寫到 workspace 外。內建版本 1.18.23 與目前安裝的 1.18.31 都一樣。`"external_directory":"deny"` 只管 OpenCode 的檔案工具，不管 bash 命令的內容，擋不住。

這是 OpenCode 新版本過不了 `asura runtime verify` 的最後一個原因（workspace-boundary probe）。另外，#74 留下的 TBD-1 也在本 issue 處理：OpenCode 的命令如果在取消前就已脫離行程樹（例如 `cmd &` 之後 shell 先結束），會被 PID 1 接手，現在取消時回收不到。

## 快速導覽

### 當前流程

寫入角色以使用者原本的環境直接執行 `opencode run --auto`。權限政策 `DefaultApprovedPermissionPolicy` 的 bash 是 `ask`，`--auto` 會自動放行。整個檔案系統都可以寫，唯一的界線是 OpenCode 自己的權限設定。

唯讀角色（Oracle／Critic）使用隔離的設定目錄與唯讀 agent：不提供 bash，edit 一律 deny。#73 的 read-only probe 已通過，本 issue 不更動。

### 修正後流程

寫入角色改在 Asura 的 bwrap 內執行，比照 OMP／Antigravity 使用 PID／IPC namespace 與 `--die-with-parent`。根目錄唯讀掛載，可寫的只有下列路徑：

- workspace
- OpenCode 自己的 data 與 state 目錄（預設 `~/.local/share/opencode`、`~/.local/state/opencode`）：OAuth 登入更新、session、log 都寫在這裡
- 每次執行私有的 `/tmp`（tmpfs，結束即丟）

`~/.cache/opencode`、`~/.config/opencode` 與其他路徑都是唯讀。環境變數照舊沿用使用者原本的環境，不改登入方式。

2026-10-29 探測（不呼叫模型，OpenCode 1.18.31）：

| 掛載 | 命令 | 結果 |
|------|------|------|
| 根目錄唯讀，只有 workspace 可寫 | `opencode session list` | 失敗：`FileSystem.open (~/.local/share/opencode/log/opencode.log)` |
| 另加 data、state 可寫，cache 唯讀 | `opencode session list`、`opencode models openai`、`opencode debug config` | 都成功 |

## 關鍵差異

| 項目 | 變更前 | 變更後 |
|------|--------|--------|
| 寫入角色的檔案系統 | 整個主機都可寫 | 只有 workspace、OpenCode data／state、私有 `/tmp` 可寫 |
| 寫入 HOME 下的工具快取（`~/.cache/go-build`、`~/.npm` 等） | 可以 | 不行，這類命令會失敗；與 Codex workspace-write、OMP 相同 |
| 取消前就已脫離行程樹的命令 | 被 PID 1 接手，取消後繼續執行 | 留在 bwrap 的 PID namespace 裡，取消時一併回收 |
| 主機沒有 `bwrap` | OpenCode 可用 | OpenCode 整體判為 unavailable（`dependency_not_found`），auto 改選其他 runtime |
| OpenCode 新版本的 verify | workspace-boundary 失敗 | 全部 probe 可以通過 |

## 涉及檔案

### OpenCode adapter
- `internal/backend/opencode/adapter.go`：寫入角色改以 bwrap 啟動
- `internal/backend/opencode/inventory.go`：探測 bwrap 依賴

### 驗證
- `internal/backend/opencode/verify_live_test.go`：live 測試

### 文件
- `docs/guides/runtimes.md`：OpenCode 的隔離方式、已知限制
- `README.md`：「安裝」的 runtime 需求
- `CHANGELOG.md`

## Gherkin 驗收劇本

```gherkin
Feature: OpenCode 寫入角色在 OS 沙箱內執行

  Background:
    Given runtime 是 OpenCode，角色是寫入角色（Striker）
    And 主機有可建立 user namespace 的 bwrap

  @SCN-001
  Scenario: 模型的命令寫不到 workspace 外
    When 模型在同一個 shell 命令裡，先寫 workspace，再寫 workspace 外的同層目錄、HOME 下的檔案、~/.config/opencode 與 ~/.cache/opencode
    Then workspace 內的檔案寫入成功
    And workspace 外的每個寫入都失敗，主機上對應的檔案不存在或內容不變

  @SCN-002
  Scenario: OpenCode 自己的資料照常保存，/tmp 是私有的
    When 寫入角色的任務執行完成
    Then OpenCode 的 data 與 state 目錄可以寫入（session、log、登入更新），任務結束後在沙箱外看得到這次的 session
    And 模型寫進 /tmp 的檔案，任務結束後不會出現在主機的 /tmp

  @SCN-003
  Scenario: 取消時回收取消前就已脫離行程樹的命令
    Given 模型啟動的命令先在背景留下一個另開 session 的子行程，然後啟動它的 shell 結束
    When 任務被取消（行程內取消或 asura cancel）
    Then 5 秒內該子行程已不存在
    And 原本的 process group 也已不存在

  @SCN-004
  Scenario: 主機沒有 bwrap 時 fail-closed
    Given 主機的 PATH 找不到 bwrap
    When 執行 runtime inventory 或 doctor
    Then OpenCode 判為 unavailable，reason 為 dependency_not_found
    And auto 不選 OpenCode；明確指定 OpenCode 時帶 reason 拒絕，不改選其他 runtime
    And bwrap 存在但無法建立 namespace 時，寫入角色在啟動模型前以 unavailable 失敗，不在沙箱外執行

  @SCN-005
  Scenario: OpenCode 新版本通過驗證後各路徑可用
    Given 已安裝的 OpenCode 版本不是內建身分
    When 使用者執行 asura runtime verify opencode
    Then 所有 probe 通過並寫入驗證紀錄
    And daemon、runtime inventory、doctor、本機 run 都接受這個版本
    And asura runtime verify opencode --remove 之後，各路徑回到 version_unverified

  @SCN-006
  Scenario: 內建版本的寫入角色在沙箱內照常運作
    Given OpenCode 是內建版本 1.18.23
    When 寫入角色執行一個需要修改 workspace 檔案的任務
    Then 任務成功，workspace 內的檔案被修改
```

## Gherkin 核准紀錄
- **核准 commit**: 待提交
- **核准來源**：
  - GitHub #75 的問題描述（2026-10-21 建立）：寫入角色必須寫不到 workspace 外；方向是比照 OMP／Antigravity，在 Asura 的 bwrap 內執行 → SCN-001、SCN-006。
  - #74 的 TBD-1（2026-10-28 Q1）：取消前就已脫離行程樹的 OpenCode 命令，在 #75 以 PID namespace 補上 → SCN-003。
  - #73 的 known limitation：OpenCode 的真實端到端（含 #73 SCN-008 的 OpenCode live）留到 #74、#75 驗收時補做 → SCN-005。
  - 使用者 2026-10-29 本 issue Q1：「data/state 可寫，其餘唯讀 (Recommended)」→ SCN-001、SCN-002。
  - 使用者 2026-10-29 本 issue Q2：「接受並寫進文件 (Recommended)」→ HOME 下的工具快取不可寫，只寫進文件，不另開放路徑。
  - 使用者 2026-10-29 本 issue Q3 更正：「整個 OpenCode 判不可用 (Recommended)」→ SCN-004。第一次提問的選項說明寫了「唯讀角色不受影響」，與清冊以整個 runtime 為單位的實際機制不符；更正後重新詢問，以此答案為準。
  - 使用者 2026-10-29 本 issue Q4：「不放，維持現況 (Recommended)」→ 唯讀角色不在範圍內。

全部 Scenario 於 2026-10-29 核准。

## 實作步驟

見 [implementation-plan.md](./implementation-plan.md)「實作步驟」。

## 風險與首要驗證

- **最大風險**：OpenCode 在唯讀根目錄的 bwrap 裡能否完整跑完一次真實的 `opencode run`。不呼叫模型的探測只證明它能啟動、能讀 DB。真實執行還會用到模型 API、OAuth 更新、bash 工具、snapshot 等，可能需要 data／state／`/tmp` 以外的可寫路徑。若真的需要，Q1 核准的可寫範圍就不夠，必須回頭找使用者決定。
- **風險等級與理由**：High。這是安全邊界：漏掉一個可寫路徑，模型就能寫到 workspace 外；限制過頭，寫入角色就不能用。真實行為只能用 live 觀察，內建版本與新版本都受影響。
- **首要驗證**：主要實作之前，先手動用計畫中的 bwrap 參數執行一次 OpenCode 1.18.31 的 `opencode run --auto`（1～2 次模型呼叫）。模型的命令要同時做三件事：寫 workspace、寫 workspace 外、寫 `/tmp`。接著檢查任務是否完成、session 是否寫入，以及 stderr 與 log 裡有沒有唯讀檔案系統的錯誤。
- **選擇理由**：這是唯一還沒觀察過的外部行為，屬於契約驗證。單元測試只能用假的 opencode，答不了這個問題；直接做主要實作的話，發現路徑不夠時會連測試一起返工。
- **完成證據**：
  - 首要驗證記錄命令、結果，以及實際需要的可寫路徑。
  - SCN-001～004 的確定性測試先紅後綠。
  - live `asura runtime verify opencode` 在 1.18.31 全部 probe 通過；拿掉 bwrap 的失效對照組在 workspace-boundary 失敗。
  - SCN-005 的四條路徑都通過端到端；SCN-006 以內建版本實際執行通過。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | 依 Q1，data 目錄可寫，模型的命令仍能改 OpenCode 自己的資料，包含 `auth.json`。OpenCode 的驗證不設 protected files：OAuth 更新本來就會改寫 `auth.json`，設了會誤判失敗 | 待確認 | 接受的殘餘風險，在 PR 揭露。SCN-001 不涵蓋 data／state 目錄 |
| TBD-2 | 本機沒有內建版本 1.18.23 的隔離安裝（`~/.cache/asura-probe-issue-2/install` 不存在），SCN-006 要先安裝才能執行 | 待確認 | 只影響 SCN-006 的證據。安裝前會先向使用者確認 |

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-29 | 建立 issue 文件。不呼叫模型的探測：OpenCode 1.18.31 需要可寫的 data 目錄，cache 唯讀可正常執行。Q1、Q2、Q4 照建議；Q3 的選項說明有誤，更正後重新詢問，答「整個 OpenCode 判不可用」。SCN-001～006 核准 | Raybird |

---
**建立日期**: 2026-10-29  
**分級**: Medium — OpenCode adapter 的寫入角色啟動方式、inventory 探測、live 測試與文件，範圍有界  
**風險**: High — 安全邊界：可寫範圍錯了，不是模型能寫到 workspace 外，就是寫入角色無法使用；真實行為只能用 live 驗證\
**狀態**: 實作中（2026-10-29）
