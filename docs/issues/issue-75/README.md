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

寫入角色改在 Asura 的 bwrap 內執行，比照 OMP／Antigravity 使用 PID／IPC namespace 與 `--die-with-parent`。真正寫回主機的只有下列路徑：

- workspace
- OpenCode 自己的 data 與 state 目錄（預設 `~/.local/share/opencode`、`~/.local/state/opencode`）：OAuth 登入更新、session、log 都寫在這裡

HOME 的其他部分掛成暫時 overlay（Q5）：沙箱內可以寫，寫入放在 tmpfs，執行結束就丟，主機上什麼都不留。這樣 MCP server 與工具快取（`uvx`、`npx`、`go test` 等）照常可用。`/tmp` 是每次執行私有的 tmpfs。HOME 以外的路徑（`/etc`、`/opt` 等）唯讀。`/run` 換成 tmpfs（Q6），只唯讀掛回名稱解析需要的路徑，沙箱內連不到主機 `/run` 底下的 socket，例如 docker 與使用者的 session bus。環境變數照舊沿用使用者原本的環境，不改登入方式。

2026-11-03 依 PR #86 審查修訂：

- HOME overlay 需要 bwrap 0.10 以上；更舊的版本與找不到 bwrap 一樣判為不可用（Q7）。
- workspace 等於或包含 HOME、`/tmp`、`/var/tmp`、`/run` 時，可寫掛載會蓋掉 overlay 與遮蔽，寫入角色拒絕執行（Q8）。
- `/var/tmp` 也換成 tmpfs。名稱解析只唯讀掛回 resolv.conf 檔案本身，不帶同目錄的 varlink socket。執行檔只掛檔案本身，不掛它所在的目錄。另加 `--new-session`，沙箱內的命令沒有控制終端。

2026-10-29 探測（OpenCode 1.18.31）：

| 掛載 | 命令 | 結果 |
|------|------|------|
| 根目錄唯讀，只有 workspace 可寫 | `opencode session list` | 失敗：`FileSystem.open (~/.local/share/opencode/log/opencode.log)` |
| 另加 data、state 可寫，cache 唯讀 | `opencode session list`、`opencode models openai`、`opencode debug config` | 都成功 |
| 同上，真實 `opencode run --auto`（步驟 1） | 模型以 bash 寫 workspace、`/tmp`、workspace 外的同層目錄 | workspace 寫入成功；同層目錄得到 `Read-only file system`；主機 `/tmp` 沒有檔案。但 serena（`uvx`）、mcp-memory-libsql（`npx`）兩個 MCP server 起不來，沙箱外是 connected；`~/.cache/opencode/models.json` 也更新失敗（EROFS） |
| 改為 HOME 暫時 overlay，data、state 照舊 bind | `opencode mcp list`；寫 HOME 與同層目錄 | 三個 MCP server 都是 connected；兩個寫入在沙箱內成功，主機上都不存在 |
| 同上（`/run` 未遮蔽） | `curl --unix-socket /run/docker.sock http://localhost/_ping` | 回 `OK`：帳號在 docker 群組，模型可以經 docker 寫到主機任何地方 |
| 另加 `--tmpfs /run`，唯讀掛回 `/run/systemd/resolve` | 同上，另測 `/var/run/docker.sock`、DNS、HTTPS、`opencode mcp list`、`opencode models` | docker 兩條路徑都被擋；DNS、HTTPS、三個 MCP server、models 都正常 |

## 關鍵差異

| 項目 | 變更前 | 變更後 |
|------|--------|--------|
| 寫入角色的檔案系統 | 整個主機都可寫 | 只有 workspace、OpenCode data／state 寫回主機；HOME 其他部分的寫入在執行結束後丟棄；HOME 以外唯讀 |
| 寫入 HOME 下的工具快取與 MCP 資料（`~/.cache/go-build`、`~/.npm`、mcp-memory-libsql 的資料庫等） | 寫回主機 | 沙箱內可寫，執行結束後丟棄，不保存 |
| 取消前就已脫離行程樹的命令 | 被 PID 1 接手，取消後繼續執行 | 留在 bwrap 的 PID namespace 裡，取消時一併回收 |
| 主機 `/run` 底下的 socket（docker、session bus） | 連得到 | 連不到 |
| 主機沒有 `bwrap`，或 bwrap 低於 0.10 | OpenCode 可用 | OpenCode 整體判為 unavailable（`dependency_not_found`），auto 改選其他 runtime |
| workspace 是 HOME、HOME 的上層，或 `/tmp`、`/run` 本身 | 照常執行 | 寫入角色以 unsupported 拒絕，不呼叫模型 |
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
    When 模型在同一個 shell 命令裡，先寫 workspace，再寫 workspace 外的同層目錄、HOME 下的檔案、~/.config/opencode、~/.cache/opencode 與 HOME 以外的路徑
    Then workspace 內的檔案寫入成功
    And 任務結束後，主機上 workspace 外的每個對應檔案都不存在或內容不變

  @SCN-002
  Scenario: OpenCode 自己的資料照常保存，/tmp 是私有的
    When 寫入角色的任務執行完成
    Then OpenCode 的 data 與 state 目錄可以寫入（session、log、登入更新），任務結束後在沙箱外看得到這次的 session
    And 模型寫進 /tmp 與 HOME（data、state 以外）的檔案，任務結束後不會出現在主機上
    And 使用者設定的 MCP server 在沙箱內照常啟動

  @SCN-007
  Scenario: 模型連不到主機 /run 底下的 socket
    Given 主機的 /run 底下有 docker socket 與使用者的 session bus
    When 模型在沙箱內連線 /run/docker.sock、/var/run/docker.sock 與 /run/user/<uid>/bus
    Then 每個連線都失敗
    And 名稱解析與對模型 API 的 HTTPS 連線照常

  @SCN-003
  Scenario: 取消時回收取消前就已脫離行程樹的命令
    Given 模型啟動的命令先在背景留下一個另開 session 的子行程，然後啟動它的 shell 結束
    When 任務被取消（行程內取消或 asura cancel）
    Then 5 秒內該子行程已不存在
    And 原本的 process group 也已不存在

  @SCN-004
  Scenario: 主機沒有可用的 bwrap 時 fail-closed
    Given 主機的 PATH 找不到 bwrap，或 bwrap 低於 0.10（不支援 overlay）
    When 執行 runtime inventory 或 doctor
    Then OpenCode 判為 unavailable，reason 為 dependency_not_found
    And auto 不選 OpenCode；明確指定 OpenCode 時帶 reason 拒絕，不改選其他 runtime
    And bwrap 存在但無法建立沙箱時，寫入角色在啟動模型前以 unavailable 失敗，不在沙箱外執行，失敗原因帶有 bwrap 的錯誤訊息

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

  @SCN-008
  Scenario: workspace 蓋住 HOME 或遮蔽目錄時拒絕
    Given workspace 等於或包含 HOME、/tmp、/var/tmp 或 /run（例如 workspace 就是 HOME 或 /）
    When 寫入角色執行
    Then 在啟動 opencode 前以 unsupported 失敗，不呼叫模型
    And workspace 是 HOME 底下的子目錄時照常執行
```

## Gherkin 核准紀錄
- **核准 commit**: e0ffc67（SCN-001～006 初次核准於 d753164；Q5 修訂 SCN-001、SCN-002 於 3dcc7e4；Q6 新增 SCN-007 於 2a52140；Q7 修訂 SCN-004、Q8 新增 SCN-008 於 e0ffc67）
- **核准來源**：
  - GitHub #75 的問題描述（2026-10-21 建立）：寫入角色必須寫不到 workspace 外；方向是比照 OMP／Antigravity，在 Asura 的 bwrap 內執行 → SCN-001、SCN-006。
  - #74 的 TBD-1（2026-10-28 Q1）：取消前就已脫離行程樹的 OpenCode 命令，在 #75 以 PID namespace 補上 → SCN-003。
  - #73 的 known limitation：OpenCode 的真實端到端（含 #73 SCN-008 的 OpenCode live）留到 #74、#75 驗收時補做 → SCN-005。
  - 使用者 2026-10-29 本 issue Q1：「data/state 可寫，其餘唯讀 (Recommended)」→ SCN-001、SCN-002。
  - 使用者 2026-10-29 本 issue Q2：「接受並寫進文件 (Recommended)」→ HOME 下的工具快取不可寫，只寫進文件，不另開放路徑。已被 Q5 取代：改為 overlay 後工具快取在沙箱內可寫，這項限制不存在。
  - 使用者 2026-10-29 本 issue Q3 更正：「整個 OpenCode 判不可用 (Recommended)」→ SCN-004。第一次提問的選項說明寫了「唯讀角色不受影響」，與清冊以整個 runtime 為單位的實際機制不符；更正後重新詢問，以此答案為準。
  - 使用者 2026-10-29 本 issue Q4：「不放，維持現況 (Recommended)」→ 唯讀角色不在範圍內。
  - 使用者 2026-10-29 本 issue Q5（步驟 1 實測後）：「HOME 改用暫時 overlay (Recommended)」→ SCN-001、SCN-002 修訂：HOME 內的寫入在沙箱內成功、主機上不留；MCP server 照常啟動。
  - 使用者 2026-11-03 本 issue Q6（步驟 1 發現沙箱內連得到 docker socket）：「本 issue 遮蔽 /run，其餘另開 (Recommended)」→ SCN-007。clisandbox 的同一缺口另開 [#84](https://github.com/raybird/Asura/issues/84)，需要網路隔離的缺口另開 [#85](https://github.com/raybird/Asura/issues/85)。
  - 使用者 2026-11-03 本 issue Q7（PR #86 審查 M1：overlay 需要 bwrap 0.10，CI 與 Ubuntu 24.04 只有 0.9.0）：「要求 bwrap ≥ 0.10 (Recommended)」→ SCN-004 修訂。
  - 使用者 2026-11-03 本 issue Q8（審查 M2：workspace 是 HOME 時可寫到 `~/.config/opencode`）：「比照 clisandbox 拒絕 (Recommended)」→ SCN-008 的 HOME 部分。
  - 使用者 2026-11-03 本 issue Q11（第二輪審查 S-A：`/tmp`、`/var/tmp`、`/run` 是實作時推導出來的，Q8 沒有問到）：「保留擴大的範圍 (Recommended)」→ SCN-008 的 `/tmp`、`/var/tmp`、`/run` 部分。理由：workspace 等於或包含這些目錄時，可寫掛載會蓋掉 tmpfs 遮蔽，例如 workspace 是 `/run` 時又連得到 docker socket，SCN-007 就能被繞過；子目錄照常可用。
  - 使用者 2026-11-03 本 issue Q9（審查 S1：`/run` 以外仍有 0666 的 root daemon socket）：「揭露並併入 #85 (Recommended)」→ TBD-5。
  - 使用者 2026-11-03 本 issue Q10（審查 S3：data 目錄可寫的範圍）：「改寫 TBD-1 揭露，維持可寫 (Recommended)」→ TBD-1，另開 [#87](https://github.com/raybird/Asura/issues/87)。

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| SCN-001 | 2026-10-29（Q5 修訂） | 已核准 |
| SCN-002 | 2026-10-29（Q5 修訂） | 已核准 |
| SCN-003 | 2026-10-29 | 已核准 |
| SCN-004 | 2026-11-03（Q7 修訂） | 已核准 |
| SCN-005 | 2026-10-29 | 已核准 |
| SCN-006 | 2026-10-29 | 已核准 |
| SCN-007 | 2026-11-03 | 已核准 |
| SCN-008 | 2026-11-03（Q8、Q11） | 已核准 |

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
| TBD-1 | 依 Q1，data 目錄整個可寫並寫回主機。模型的命令能改其中所有東西：`auth.json`、所有專案共用的 `opencode.db`、`snapshot/` 底下的 git 目錄與 `repos/`。之後在主機上直接執行的 OpenCode（不經 Asura）會讀這些資料；能否藉 snapshot 的 git 設定執行命令沒有驗證（PR #86 審查 S3）。OpenCode 的驗證不設 protected files：OAuth 更新本來就會改寫 `auth.json`，設了會誤判失敗 | 不影響本次交付（2026-11-03 Q10）：追蹤：[#87](https://github.com/raybird/Asura/issues/87) | 接受的殘餘風險，在 PR 揭露。SCN-001 不涵蓋 data／state 目錄 |
| TBD-3 | 步驟 1 沙箱內 10 次真實執行有 2 次在 240 秒逾時（唯讀 HOME 與 overlay 各一次），都停在 instance 初始化之後、建立 session 之前，也都是閒置數小時後的第一次執行。主機上 4 次對照都正常，但沒有一次是冷啟動，原因未確認（[evidence/step1.md](./evidence/step1.md)「逾時的對照」） | 待確認 | 不阻擋實作：Asura 有逾時保護，逾時以失敗回報。步驟 5 的 14 次模型呼叫都沒有逾時（最長 12.9 秒），但沒有記錄當時的快取狀態，不能當冷啟動對照（[evidence/e2e.md](./evidence/e2e.md)「逾時」）。在 PR 揭露 |
| TBD-5 | 沙箱沒有網路隔離：localhost 上的服務與 abstract unix socket 仍然連得到。帳號讀得到憑證的服務（例如啟動後的 microk8s API）可以被用來繞過沙箱。唯讀掛載也擋不住 `connect`：`/run` 以外權限 0666 的 root daemon socket 仍連得到，例如 `/var/snap/multipass/common/multipass_socket` 與 livepatchd，能否利用沒有驗證（PR #86 審查 S1，Q9 併入 #85） | 不影響本次交付（2026-11-03 Q6）：追蹤：[#85](https://github.com/raybird/Asura/issues/85) | 本 issue 只遮蔽 `/run` 底下的 socket（SCN-007）。2026-10-29 檢查時，localhost 上只有 ollama（11434），microk8s API 沒有在 listen |
| TBD-6 | OMP／Antigravity 的 clisandbox 只把 `/run/user` 換成 tmpfs，沙箱內同樣連得到 docker socket | 不影響本次交付（2026-11-03 Q6）：追蹤：[#84](https://github.com/raybird/Asura/issues/84) | 既有缺口，不在本 issue 修改 |
| TBD-4 | overlay 的上層放在 tmpfs（記憶體），模型或工具在 HOME 大量寫入（例如 Go build cache）時會占用記憶體，執行結束才釋放 | 待確認 | 在 PR 揭露，不設上限 |
| TBD-2 | 本機沒有內建版本 1.18.23 的隔離安裝（`~/.cache/asura-probe-issue-2/install` 不存在），SCN-006 要先安裝才能執行 | 已解決（2026-11-03）：#73 留下的 `~/.cache/asura-verify/opencode-1.18.23` 就是 1.18.23 的隔離安裝，不需要另外安裝 | SCN-006 以它執行通過（[evidence/e2e.md](./evidence/e2e.md)） |

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-29 | 建立 issue 文件。不呼叫模型的探測：OpenCode 1.18.31 需要可寫的 data 目錄，cache 唯讀可正常執行。Q1、Q2、Q4 照建議；Q3 的選項說明有誤，更正後重新詢問，答「整個 OpenCode 判不可用」。SCN-001～006 核准 | Raybird |
| 2026-10-29 | 步驟 1 實測：唯讀 HOME 讓使用者的 MCP server 起不來。Q5 改為 HOME 暫時 overlay，SCN-001、SCN-002 修訂並重新核准；Q2 的限制隨之取消 | Raybird |
| 2026-11-03 | 步驟 1 另外發現，沙箱內連得到主機的 docker socket（clisandbox 也一樣）。Q6 決定本 issue 遮蔽 `/run`，新增 SCN-007 並核准；clisandbox 的缺口開 #84，網路隔離開 #85 | Raybird |
| 2026-11-03 | 步驟 2～6 完成：寫入角色在 bwrap 內執行；OpenCode 1.18.31 第一次通過 `runtime verify`，端到端四條路徑與內建 1.18.23 通過 | Raybird |
| 2026-11-03 | PR #86 獨立審查 RETURN TO execute-task（M1 bwrap 版本、M2 workspace 蓋住 HOME）。Q7～Q10 照建議：SCN-004 修訂、新增 SCN-008；TBD-1、TBD-5 改寫，另開 #87 | Raybird |
| 2026-11-03 | 第二輪審查 PASS，指出 SCN-008 的 `/tmp`、`/var/tmp`、`/run` 沒有核准來源（S-A）；Q11 保留擴大的範圍 | Raybird |
| 2026-11-03 | 第三輪差異審查 PASS（`01e7cd1..3bbd0b1`，[報告](https://github.com/raybird/Asura/pull/86#issuecomment-6596897999)），沒有 MUST FIX 與 SHOULD FIX；PR CI [run 41588358727](https://github.com/raybird/Asura/actions/runs/41588358727) 為 success | Raybird |
| 2026-11-03 | PR #86 squash 合併至 main（merge commit `6dde8a9`，2026-11-03T06:27:40Z），tree 與審查的 HEAD `3bbd0b1` 相同。GitHub #75 由 `Closes #75` 自動關閉。合併後 main push CI [run 41590020074](https://github.com/raybird/Asura/actions/runs/41590020074) 為 success。TBD-3（冷啟動逾時）沒有追蹤 issue，維持待確認；TBD-1、TBD-5、TBD-6 分別由 #87、#85、#84 追蹤 | Raybird |

---
**建立日期**: 2026-10-29  
**分級**: Medium — OpenCode adapter 的寫入角色啟動方式、inventory 探測、live 測試與文件，範圍有界  
**風險**: High — 安全邊界：可寫範圍錯了，不是模型能寫到 workspace 外，就是寫入角色無法使用；真實行為只能用 live 驗證\
**狀態**: 已完成 — PR #86 於 2026-11-03 合併（merge commit `6dde8a9`）
