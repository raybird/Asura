# Issue 74 - 取消任務時，Codex 與 OpenCode 由模型啟動的命令不會被回收

## 文件清單
- [implementation-plan.md](./implementation-plan.md)

## 概述

GitHub [#74](https://github.com/raybird/Asura/issues/74)。任務取消後，Codex 與 OpenCode 由模型啟動的 shell 命令會繼續執行，內建版本（Codex 0.155.0、OpenCode 1.18.23）與新版本（Codex 0.163.0、OpenCode 1.18.31）都一樣。兩個 adapter 都宣稱 `CapProcessCancellation`。

這也是 #73 之後 Codex、OpenCode 新版本過不了 `asura runtime verify` 的原因之一。

## 快速導覽

### 當前流程

所有取消路徑最後都呼叫 `opencode.KillProcessGroup(pgid)`，只送 `kill(-pgid, SIGKILL)` 給 Asura 自己啟動的 process group。這些路徑包括：行程內 context 取消、外部 `asura cancel`、engine 取消、啟動紀錄失敗時的回收。Codex adapter 的行程內取消另外直接呼叫 `syscall.Kill(-pgid)`。

2026-10-28 實測 Codex 0.163.0（`codex sandbox -P :workspace -- sh -c 'sleep A & setsid sleep B & wait'`）的行程樹：

```
node (pgid=P, Asura 的 group)
└─ codex ─ codex-linux-sandbox (pgid=P)
   └─ bwrap (另開 pgid)
      └─ codex-linux-sandbox (另開 session；PID namespace 的 init)
         └─ sh ─ sleep A
                └─ sleep B (setsid)
```

`kill(-P)` 之後，PID namespace 的 init 被 PID 1 接手並繼續執行，`sleep A`、`sleep B` 都還在。直接 kill 這個 init，兩者立刻被核心一併回收。

依 #73 的紀錄，OpenCode 的 bash 工具也另開 session，取消後同樣被 `systemd --user` 接手。

### 修正後流程

取消時先凍結並回收 group 成員的整棵子孫樹（以 `/proc` 的 PPID 關係找出，不論 session 或 process group），再回收 group 本身。Codex 與 OpenCode 的所有取消路徑（行程內、`asura cancel`、engine）共用同一個實作；OMP 與 Antigravity 不變，照舊由 clisandbox 的 bwrap（`--unshare-pid --die-with-parent`）讓整個 PID namespace 一起被回收。

## 關鍵差異

| 項目 | 變更前 | 變更後 |
|------|--------|--------|
| 取消時回收的範圍 | 只有 Asura 啟動的 process group | group 成員的整棵子孫樹，包含另開 session／process group 的子孫，以及 Codex 的 PID namespace |
| Codex 行程內取消 | adapter 直接 `syscall.Kill(-pgid)` | 與其他取消路徑共用同一個實作 |
| cancellation probe | 模型執行單一 `sleep N` | 模型執行的命令同時留下前景子行程與另開 session 的背景子行程，取消後兩者都必須消失 |
| Codex、OpenCode 新版本的 verify | cancellation 失敗 | Codex 可以通過；OpenCode 仍受 #75 影響，過不了 workspace-boundary |

## 涉及檔案

### 取消
- `internal/backend/opencode/process.go`：`KillProcessGroup`，所有取消路徑的共用入口
- `internal/backend/codex/adapter.go`：行程內取消改用共用實作
- `cmd/asura/cancel.go`、`cmd/asura/runtime_process.go`、`cmd/asura/run.go`、`internal/engine/cancel.go`：呼叫端，預期不需要修改，只需要驗證

### 驗證
- `internal/backend/verification/probe.go`：cancellation probe 的命令與判定
- `internal/backend/codex/verify_live_test.go`、`internal/backend/opencode/verify_live_test.go`：live 測試

### 文件
- `docs/guides/runtimes.md`：版本驗證的 probe 表、已知限制
- `CHANGELOG.md`

## Gherkin 驗收劇本

```gherkin
Feature: 取消任務時回收模型啟動的命令

  Background:
    Given runtime 是 Codex 或 OpenCode
    And 模型已經啟動一個 shell 命令，這個命令留下一個前景子行程，和一個另開 session 的背景子行程

  @SCN-001
  Scenario: 行程內取消會回收另開 session 與 PID namespace 裡的命令
    When 執行中的任務 context 被取消
    Then Run 在 20 秒內返回
    And 5 秒內，兩個子行程與原本的 process group 都已不存在

  @SCN-002
  Scenario: 外部取消也回收同一棵行程樹
    Given 任務由另一個 Asura 行程執行，並已記錄 process group 與身分
    When 使用者執行 asura cancel <task-id>
    Then 行程身分檢查通過後，兩個子行程與原本的 process group 都已不存在
    And 任務狀態為已取消

  @SCN-003
  Scenario: 回收不會波及任務行程樹以外的行程
    Given 同一個使用者另有一個與任務無關的行程，它不是任務 process group 任何成員的子孫
    When 任務被取消
    Then 該無關行程仍在執行
    And 身分檢查判定 process group 不屬於該任務時，不回收任何行程（維持既有行為）

  @SCN-004
  Scenario: cancellation probe 要求另開 session 的背景子行程也被回收
    Given 某個 runtime 取消後，另開 session 的背景子行程仍在執行
    When 執行 asura runtime verify
    Then cancellation probe 失敗，訊息指出殘留的行程
    And 驗證紀錄不變

  @SCN-005
  Scenario: Codex 新版本通過驗證後各路徑可用
    Given 已安裝的 Codex 版本不是內建身分
    When 使用者執行 asura runtime verify codex
    Then 所有 probe 通過並寫入驗證紀錄
    And daemon、runtime inventory、doctor、本機 run 都接受這個版本
    And asura runtime verify codex --remove 之後，各路徑回到 version_unverified

  @SCN-006
  Scenario: Codex exec 的唯讀角色與 read-only-sandbox 驗的是同一個權限設定
    When Codex 以 exec -s read-only 執行模型啟動的命令
    Then codex-linux-sandbox 收到的檔案系統權限，與 codex sandbox -P :read-only 相同（路徑以外的欄位一致，workspace 都不可寫）
```

## Gherkin 核准紀錄
- **核准 commit**: 7f3787e
- **核准來源**：
  - GitHub #74 的問題描述（2026-10-21 建立）：取消後由模型啟動的命令必須被回收。
  - #73 的決策（2026-10-21 Q14）：取消 probe 維持 verify 的必要條件，Codex／OpenCode 的缺陷在本 issue 修正。
  - #73 的 Q20（2026-10-28）：「接受並記錄，#74 時再補驗 (Recommended)」→ SCN-006。
  - #73 README：Codex 的真實端到端留到 #74 驗收 → SCN-005。
  - 使用者 2026-10-28 本 issue Q1：「取消當下仍在跑的全部 (Recommended)」→ SCN-001～003。取消前就已脫離行程樹的 OpenCode 命令不在範圍內，見 TBD-1。
  - 使用者 2026-10-28 本 issue Q2：「加強 (Recommended)」→ SCN-004 與 Background。

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| SCN-001 | 2026-10-28 | 已核准 |
| SCN-002 | 2026-10-28 | 已核准 |
| SCN-003 | 2026-10-28 | 已核准 |
| SCN-004 | 2026-10-28 | 已核准 |
| SCN-005 | 2026-10-28 | 已核准 |
| SCN-006 | 2026-10-28 | 已核准 |

## 實作步驟

見 [implementation-plan.md](./implementation-plan.md)「實作步驟」。

## 風險與首要驗證

- **最大風險**：有兩個。第一，回收範圍判斷錯誤：漏掉子孫，取消就仍然留下命令；範圍過大，則會殺到使用者其他無關的行程。第二，「取消當下所有命令仍是 group 成員的子孫」這個假設，目前只在 Codex 上實測過，OpenCode 的實際行程樹還沒看過。
- **風險等級與理由**：High。這是會送出 SIGKILL 的程式，而且 Asura 宣稱自己有取消能力（`CapProcessCancellation`）。Codex、OpenCode 的真實行為只能用 live 測試觀察。
- **首要驗證**：先寫一個確定性的重現測試：假 runtime 留下另開 session 的子孫，另開 process group 的子孫底下再有子孫，用現有的 `KillProcessGroup` 取消，證明會有殘留（紅燈）。同時用一次 live OpenCode 執行，記錄模型命令的實際行程樹，確認上面的假設。
- **選擇理由**：重現測試直接證明缺陷存在，之後也是修正的回歸網，而且不需要模型。OpenCode 的行程樹是唯一還沒觀察過的前提，必須在主要實作之前確認。
- **完成證據**：
  - 確定性測試先紅後綠。
  - 無關行程的對照測試是綠燈。
  - live Codex 0.163.0 與 OpenCode 1.18.31 的 cancellation probe（加強後的命令）通過，而且拿掉修正時會失敗。
  - Codex 端到端通過 verify。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | OpenCode 的命令如果在取消前就已脫離行程樹（例如 `cmd &` 之後 shell 先結束），會被 PID 1 接手，子孫樹找不到它 | 已接受（2026-10-28 Q1） | 不在本 issue 範圍。#75 會替 OpenCode 加上 OS 沙箱，屆時以 PID namespace 補上。Codex 不受影響：這類命令仍在它的 PID namespace 裡，會隨 init 一起被回收 |
| TBD-2 | SCN-005 的「本機 run」：本機與 daemon 的 `--backend auto`，在主機上有任何 runtime 是 `version_unverified` 時都會因 `audit_invalid` 失敗。這是 #73 帶進來的缺陷，main 也重現得到 | 已解決（2026-10-28）：#79 由 PR #80 修正（merge commit `01ce169`），本分支併入 main 後補跑本機 run 通過，見 evidence/e2e.md「補跑」 | SCN-005 其他三條路徑（daemon、inventory、doctor）已通過。#79 合併後，本分支以 merge 併入 main，再補跑本機 run 的驗證前、驗證後、移除後三個階段 |
| TBD-3 | Antigravity 1.4.4 沒有用最終版 cancellation 命令（`&&` 串接）跑到 live：2026-10-28T05:02Z 那次的 CLI 登入已過期，所有需要模型的 probe 都是 `authentication_failed` | 待確認：重新登入 Antigravity 後補跑 `TestVerifyLive_Antigravity` | 兩版命令只差 `;` 換成 `&&`；`;` 版在 04:4x 全部通過（cancellation 6.6s）。最終命令在 Codex、OpenCode、OMP 上都已通過 live |

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-28 | 建立 issue 文件。實測 Codex 0.163.0 的行程樹，確認殘留的是 PID namespace 的 init；Asura 的 bwrap 裡無法再開 Codex 的 bwrap（AppArmor）。Q1、Q2 照建議，SCN-001～006 核准 | Raybird |
| 2026-10-28 | 步驟 1～5 完成，live 證據見 evidence/。步驟 6 的 Codex 端到端發現 #73 帶進來的 auto 路徑稽核缺陷，Q3「另開 issue 先修」，開 #79；SCN-005 的本機 run 等 #79（TBD-2） | Raybird |
| 2026-10-28 | #79 合併（PR #80，`01ce169`），本分支以 merge 併入 main（保留核准 commit `7f3787e` 與 #79 證據引用的 `80afa75`）。補跑 SCN-005 本機 run 通過，TBD-2 解決；步驟 6 完成 | Raybird |
| 2026-10-28 | 併入 main 後的完整 gate 抓到兩個不穩定的問題：fork 測試沒節流，拖慢同時執行的套件；cancellation probe 先寫標記再 `setsid`，取消落在兩者之間時會出現假綠燈。改為節流，並改由另開 session 的子行程寫標記。命令裡的 `;` 改成 `&&`，因為 `TestRuntimeVerifyCommand` 的假 codex 會用 `;` 切開命令。最終命令的 live 結果：Codex、OMP 全部通過，OpenCode 照預期只有 #75 那一項失敗，Codex 與 OpenCode 的失效對照照樣失敗。Antigravity 登入過期，最終命令沒有執行到；`;` 版命令全部通過（evidence/step4.md） | Raybird |
| 2026-10-28 | PR #82 獨立審查 PASS（[報告](https://github.com/raybird/Asura/pull/82#issuecomment-6461457482)），沒有 MUST FIX。使用者選擇全部處理：SHOULD 1（測試清理會誤殺機器上無關的 sleep）、SHOULD 2～3（計畫與 e2e 證據的一致性）、NICE 4～7（processTree 改回傳 BFS 順序、註解措辭、凍結迴圈設上限、CHANGELOG 補上 OMP／Antigravity 的回收方式） | Raybird |

---
**建立日期**: 2026-10-28  
**分級**: Medium — 共用取消實作、Codex adapter、cancellation probe 與文件，範圍有界  
**風險**: High — 會送出 SIGKILL，回收範圍錯誤會留下命令或殺到無關行程；真實行為只能用 live 驗證\
**狀態**: 審查中（2026-10-28）
