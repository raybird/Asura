# Issue 73 - Runtime 版本改為「通過驗證即可使用」

## 概述

來源：[GitHub #73](https://github.com/raybird/Asura/issues/73)。四個 runtime 目前都只接受寫死的單一版本或 hash，升級 runtime 後就不能用；OpenCode 的規則在各路徑還不一致（daemon 精確比對 `1.18.23`，本機 `run`、`doctor`、`runtime inventory` 用前綴 `1.18`）。2026-10-21 在維護者主機上，OpenCode 1.18.31 於 `runtime inventory` 顯示 available，但 daemon 路徑會拒絕；Codex 0.163.0 一律 `version_failed`。

本 issue 新增 `asura runtime verify <runtime>`：對已安裝的 runtime 實際執行涵蓋安全邊界的 contract probe（會呼叫模型），全部通過就在本機記下該安裝的身分（版本加上 SHA-256），之後只要身分相符就允許使用。沒有驗證過的版本，維持在模型呼叫前拒絕。開發時實測過的版本保留為內建已驗證，現有使用者升級 Asura 後行為不變。

## 文件清單
- [requirement-analysis.md](./requirement-analysis.md)
- [technical-analysis.md](./technical-analysis.md)
- [implementation-plan.md](./implementation-plan.md)

## 關鍵差異

| 項目 | 變更前 | 變更後 |
|------|--------|--------|
| 可用的版本 | 每個 runtime 只接受一個寫死的版本或 hash | 內建已驗證的身分，加上本機驗證紀錄中身分相符的安裝 |
| OpenCode 比對規則 | daemon 精確比對 `1.18.23`；其他路徑用前綴 `1.18` | 所有路徑一致：只放行內建或已驗證的身分 |
| 版本不在名單上 | `version_failed`，沒有任何解法提示 | `version_unverified`；doctor 與錯誤訊息提示執行 `asura runtime verify <runtime>` |
| 新版本怎麼用 | 只能等 Asura 改程式、發新版 | 使用者自行執行驗證，通過就能用 |
| 驗證紀錄 | 無 | 固定在 `$XDG_DATA_HOME/asura/`（預設 `~/.local/share/asura/`），不跟 `--db` 走；可列出、移除 |

## 涉及檔案

```text
internal/backend/inventory.go             ← 修改：新增 reason version_unverified；可用判定改讀「內建 + 本機紀錄」
internal/backend/verification/            ← 新增：驗證紀錄讀寫、身分（版本 + SHA-256）計算、probe 編排
internal/backend/opencode/                ← 修改：版本規則改為身分比對；live probe 改為可對任意已安裝版本執行
internal/backend/codex/                   ← 修改：同上
internal/backend/omp/、antigravity/       ← 修改：固定 hash 改為「內建 hash + 已驗證 hash」
internal/backend/clisandbox/              ← 修改：pinned file probe 接受已驗證的 hash 集合
internal/daemon/daemon.go                 ← 修改：移除寫死的 OpenCode 1.18.23／Codex 0.155.0，改用同一套判定
cmd/asura/inventory.go、doctor.go、run.go ← 修改：新增 runtime verify（含 --list、--remove）；doctor 顯示提示；移除 1.18 前綴
internal/config/config.go                 ← 修改：提供驗證紀錄的固定路徑
internal/pipeline/、internal/storage/     ← 不動：封存契約與 stage guard 的語意不變
docs/guides/runtimes.md、cli-reference.md、README.md、CHANGELOG.md ← 修改：版本政策與新命令
```

## Gherkin 驗收劇本

```gherkin
Feature: runtime 版本通過驗證才可使用

  Background:
    Given 「身分」指 runtime 回報的版本字串，加上 Asura 實際會執行的執行檔（OMP／Antigravity 為既有的整組安裝檔）的 SHA-256
    And 「內建已驗證」指開發時實測過的身分：OpenCode 1.18.23、Codex 0.155.0，以及 OMP 18.4.4、Antigravity 1.2.13 既有的 hash
    And 「驗證紀錄」位於 $XDG_DATA_HOME/asura/（預設 ~/.local/share/asura/），只有 asura runtime verify 會寫入
    And 「判定路徑」指 runtime inventory、doctor、本機 run（含 --backend auto）、daemon 的 admission 與 stage 執行前檢查

  @SCN-001
  Scenario: 內建已驗證的身分不需驗證即可使用
    Given 已安裝的 runtime 身分等於內建已驗證
    And 本機沒有任何驗證紀錄
    When 任一判定路徑檢查該 runtime
    Then 該 runtime 為 available，行為與變更前相同

  @SCN-002
  Scenario: 未驗證的身分在模型呼叫前被拒絕
    Given 已安裝的 runtime 身分不是內建已驗證，本機也沒有相符的驗證紀錄
    When 任一判定路徑檢查該 runtime
    Then 該 runtime 為 unavailable，reason 為 version_unverified
    And doctor 與錯誤訊息提示執行 asura runtime verify <runtime>
    And 指定該 runtime 的任務在模型呼叫前被拒絕、不建立 Task；auto 選擇時排除它

  @SCN-003
  Scenario: 驗證全部通過後，所有判定路徑都放行該身分
    Given 已安裝的 runtime 身分未驗證，且 runtime 已登入
    When 使用者執行 asura runtime verify <runtime>，且該 runtime 的每個 probe 都通過
    Then 驗證紀錄新增一筆，含 runtime、版本、SHA-256、驗證時間與執行驗證的 Asura 版本
    And 之後不論使用哪個 --db、有沒有 config，任一判定路徑都把該 runtime 判為 available

  @SCN-004
  Scenario: 任一 probe 失敗或無法執行時，整體失敗且不放行
    Given 已安裝的 runtime 身分未驗證
    When 使用者執行 asura runtime verify <runtime>，且有 probe 失敗或無法執行（未登入、逾時、bwrap 不可用）
    Then 命令逐項印出每個 probe 的通過或失敗與原因，結束碼非 0
    And 驗證紀錄沒有新增，既有的其他紀錄不變
    And 該 runtime 仍為 version_unverified

  @SCN-005
  Scenario: 唯讀角色若能寫入 workspace，驗證必須失敗
    Given 一個 runtime 在 Asura 要求唯讀的角色下，實際上能寫入 workspace
    When 使用者執行 asura runtime verify <runtime>
    Then 唯讀 probe 失敗，依 SCN-004 不放行

  @SCN-006
  Scenario: 執行檔被替換後，即使版本字串相同也要重新驗證
    Given 某個身分已有驗證紀錄
    When 執行檔或安裝檔內容改變，但回報的版本字串相同
    Then 任一判定路徑都把該 runtime 判為 version_unverified

  @SCN-007
  Scenario: 列出與移除驗證紀錄
    Given 本機有驗證紀錄
    When 使用者執行 asura runtime verify --list
    Then 列出每筆紀錄的 runtime、版本、SHA-256 與驗證時間，並標出內建已驗證的身分
    When 使用者執行 asura runtime verify --remove 指定其中一筆
    Then 該筆被移除，之後該身分回到 version_unverified
    When 使用者試圖移除內建已驗證的身分
    Then 命令失敗並說明內建身分不能移除，紀錄不變

  @SCN-008
  Scenario: OpenCode 在所有判定路徑使用同一規則
    Given 已安裝 OpenCode 1.18.31，沒有相符的驗證紀錄
    When 依序以 runtime inventory、doctor、本機 run、daemon admission 檢查 OpenCode
    Then 四者都判為 version_unverified；驗證通過後四者都判為 available

  @SCN-009
  Scenario: 驗證紀錄在任務執行前被移除時，任務 fail-closed
    Given 一個以已驗證身分接受的任務，尚未開始執行該 stage
    When 該身分的驗證紀錄在 stage 執行前被移除
    Then stage 執行前的檢查拒絕執行，不呼叫模型、不改選其他 runtime

  @SCN-010
  Scenario: 驗證不會自動觸發
    Given 已安裝的 runtime 身分未驗證
    When 啟動 daemon、執行 doctor、runtime inventory、run 或 ask
    Then 不執行任何會呼叫模型的 probe，只回報 version_unverified 與提示
```

## Gherkin 核准紀錄
- **核准 commit**: 64d7b0e
- **核准來源**: 2026-10-21 使用者要求「依照你的建議，有個驗證的方式有通過就算可以使用」（方向：新版本通過驗證才可使用，未驗證者 fail-closed）；同日逐題確認：
  - Q1「四個 runtime 一起做」：OpenCode、Codex、OMP、Antigravity 都納入（Background、SCN-001～SCN-006）
  - Q2「寫入模型的 live probe (Recommended)」：OpenCode／Codex 驗唯讀沙箱寫不進去、workspace-write 只能寫 workspace、輸出可解析、取消回收行程樹；OMP／Antigravity 驗在 Asura 的 bwrap 內可執行且輸出可解析、取消、登入憑證邊界；全部通過才記下身分（SCN-003、SCN-005）
  - Q3「本機紀錄，綁版本與 SHA-256 (Recommended)」：只有驗證命令能寫入，執行檔或安裝樹換掉即失效（SCN-003、SCN-006）
  - Q4「保留為內建已驗證 (Recommended)」：現有寫死的版本與 hash 免驗證可用（SCN-001）
  - Q5「統一精確比對 (Recommended)」：OpenCode 各路徑一致，未驗證的 1.18.x 也拒絕（SCN-008）
  - Q6「只有明確執行新命令 (Recommended)」：新增 `asura runtime verify <runtime>`，其他命令不自動驗證（SCN-002、SCN-010）
  - Q7「固定在 ~/.local/share/asura/ (Recommended)」：依 XDG_DATA_HOME，不跟 `--db` 走（Background、SCN-003）
  - Q8「可以 (Recommended)」：任一 probe 失敗或無法執行即整體失敗、不寫紀錄、逐項印出、結束碼非 0、既有紀錄不動（SCN-004）
  - Q9「提供 list 與 remove (Recommended)」：內建身分不能移除（SCN-007）
  - Q10「獨立成 version_unverified (Recommended)」：與 `version_failed` 分開，doctor 只在未驗證時提示 verify 命令（SCN-002、SCN-008，TBD-1）
  - Q11「stage 執行前拒絕 (Recommended)」：紀錄被移除時，已接受但尚未執行的任務沿用 stage 執行前重新判定、不改選的語意（SCN-009，TBD-2）
  - Q12「可以，不綁模型 (Recommended)」：OpenCode／Codex 的 probe 用 runtime 的預設模型，紀錄不綁模型（SCN-003，TBD-4）
  - Q13「不調升，只擴充 reason (Recommended)」：清冊 policy 版本維持 `inventory-v1`，只在 reason 白名單加入 `version_unverified`，升級前封存的排隊任務與 cron 不受影響（SCN-009，TBD-3）
- **Phase 1 決策**（2026-10-21，live probe 發現內建版本也有的既有問題後逐題確認，見 [evidence/phase1-probes.md](./evidence/phase1-probes.md)）：
  - Q14「另開 issue 修，probe 維持必要 (Recommended)」：取消後 Codex／OpenCode 由模型啟動的命令不會被回收，另開 [#74](https://github.com/raybird/Asura/issues/74)；取消 probe 仍是通過驗證的必要條件（SCN-004、SCN-005 不變）
  - Q15「另開 issue 加沙箱，probe 維持必要 (Recommended)」：OpenCode 寫入角色沒有 OS 沙箱，另開 [#75](https://github.com/raybird/Asura/issues/75)；workspace 邊界 probe 仍是必要條件。Q2 的「workspace-write 只能寫 workspace」對 OpenCode 現況不成立，由 #75 補齊
- **Phase 3 決策**（2026-10-27）：
  - Q16「改用 OMP、Antigravity (Recommended)」：Task 3.5 的端到端改用驗證得過的 OMP 18.12.6 與 Antigravity 1.4.4，不再等 #74、#75；Codex 與 OpenCode 的端到端在 #74、#75 驗收時補做。SCN 內容不變
- **審查決策**（2026-10-27，PR #77 獨立審查 [留言](https://github.com/raybird/Asura/pull/77#issuecomment-6438498249) 的 `需決策` 項目）：
  - Q17「補不經模型的沙箱檢查 (Recommended)」：Codex 唯讀角色沙箱正常時不會嘗試寫入，nonce 正向對照分不出「宣告 read-only 但 OS 沙箱沒生效」。唯讀 probe 另外以 `codex sandbox` 不經模型直接驗證：唯讀 policy 寫 workspace 必須失敗，workspace-write 必須成功（SCN-005）
  - Q18「先比雜湊，不執行 (Recommended)」：Antigravity 非內建的雜湊先查驗證紀錄，沒有相符紀錄就直接 `version_unverified`，不在 host 執行 agy；有紀錄則用紀錄裡的版本。只有 `asura runtime verify` 會執行 `agy --version`（SCN-002、SCN-006）
  - Q19：一併處理改善建議：Codex 改用 `clisandbox.TreeHash`；verify 前先檢查紀錄檔可讀寫；過時註解、開頭提示文字、OMP 換樹行為的文件、Proof 表 SCN-001 引用
- **第二輪審查決策**（2026-10-28，PR #77 第二輪獨立審查 [留言](https://github.com/raybird/Asura/pull/77#issuecomment-6439324006)，判定 PASS）：
  - Q20「接受並記錄，#74 時再補驗 (Recommended)」：`read-only-sandbox` 驗的是 `codex sandbox -P :read-only`，正式任務跑的是 `codex exec -s read-only`，兩者的對應是 Codex 內部行為，目前無法直接驗證。接受這項殘餘風險，見 TBD-7
  - Q21：一併處理 SHOULD FIX 2（文件措辭）、NICE 3、4（唯讀沙箱 probe 加固）、NICE 5、6（`AdmitDigest` 收尾）；NICE 7～9 不處理

| Scenario | 核准日期 | 狀態 |
|----------|---------|------|
| SCN-001 | 2026-10-21 | 已核准 |
| SCN-002 | 2026-10-21 | 已核准 |
| SCN-003 | 2026-10-21 | 已核准 |
| SCN-004 | 2026-10-21 | 已核准 |
| SCN-005 | 2026-10-21 | 已核准 |
| SCN-006 | 2026-10-21 | 已核准 |
| SCN-007 | 2026-10-21 | 已核准 |
| SCN-008 | 2026-10-21 | 已核准 |
| SCN-009 | 2026-10-21 | 已核准 |
| SCN-010 | 2026-10-21 | 已核准 |

## 風險與首要驗證

- **最大風險**：驗證本身抓不到安全邊界失效。如果唯讀 probe 在新版 runtime 上跑不起來、或在沙箱失效時仍判定通過，`runtime verify` 就會把不安全的版本放行，比現在寫死版本更糟
- **風險等級與理由**：High — 屬安全邊界（唯讀角色的寫入隔離）；probe 對新版本（Codex 0.163.0、OpenCode 1.18.31）的外部行為尚未確認；錯誤放行時任務照常成功，不會有任何錯誤訊號
- **首要驗證**：在寫任何產品程式前，先把現有 live probe 改成可對任意已安裝版本執行，拿本機的 Codex 0.163.0 與 OpenCode 1.18.31 實跑，再以對照組確認 probe 抓得到失效：故意拿掉唯讀旗標或改用可寫設定，唯讀 probe 必須失敗
- **選擇理由**：只有實際執行新版本才能知道 probe 在新版本上能不能跑、結果是否可信；hermetic fixture 驗證不了外部 runtime 的沙箱語意
- **完成證據**：每個 runtime 的 probe 在本機已安裝版本上有明確的通過或失敗結果，並記錄版本與命令；每個唯讀 probe 都有一組「沙箱失效」對照組實際失敗；probe 呼叫模型的次數與所需登入條件記入 implementation-plan。2026-10-22 已達成，見 [evidence/phase1-probes.md](./evidence/phase1-probes.md) 與 Task 1.4

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | 新的 unavailable reason 命名為 `version_unverified`，與既有的 `version_failed`（版本取不到或格式錯）分開 | 已解決 | 2026-10-21 Q10 回答「獨立成 version_unverified」：使用者要能分辨「可以自己驗證」與「版本壞掉」，doctor 只在前者提示 verify 命令（SCN-002、SCN-008） |
| TBD-2 | 驗證紀錄被移除時，已接受但尚未執行的任務在 stage 執行前被拒絕（SCN-009），會讓排隊中的任務失敗 | 已解決 | 2026-10-21 Q11 回答「stage 執行前拒絕」：移除紀錄通常是發現該版有問題，照跑等於收不回來；沿用既有 fail-closed 語意（SCN-009） |
| TBD-3 | 新增 reason 是否要調升 inventory policy 版本：`InventorySnapshot.Validate` 要求 policy 版本完全相等 | 已解決 | 2026-10-21 Q13 回答「不調升，只擴充 reason」：舊的封存內容不會含新 reason，照樣通過驗證，升級前的 cron 與排隊任務不受影響；policy 維持 `inventory-v1`（SCN-009） |
| TBD-4 | OpenCode／Codex 的 probe 使用 runtime 的預設模型，驗證紀錄不綁模型 | 已解決 | 2026-10-21 Q12 回答「可以，不綁模型」：沙箱與輸出格式屬於 runtime CLI 本身，一個版本驗證一次即可（SCN-003） |
| TBD-5 | OMP 與 Antigravity 的登入在 2026-10-21 已過期，Task 1.3 的 probe 無法執行 | 已解決 | 2026-10-22 使用者重新登入後重跑，Task 1.3、1.4 完成。Antigravity 那次很可能並未過期：snap 版 go 讓測試行程帶著 `SNAP_NAME`，libsecret 查不到 keyring，adapter 才改讀舊的 token 檔（[evidence/phase1-probes.md](./evidence/phase1-probes.md)「2026-10-22 重跑」） |
| TBD-6 | Codex／OpenCode 新版本要等 #74、#75 合併後才可能通過驗證 | 已解決 | 2026-10-27 Q16：Task 3.5 改用 OMP 與 Antigravity，不再相依 #74、#75。#73 合併後，已安裝的 Codex 0.163.0 與 OpenCode 1.18.31 在 #74、#75 修好前維持 `version_unverified`，只有內建版本可用 |
| TBD-7 | Codex 的 `read-only-sandbox` 驗 `codex sandbox -P :read-only`，正式任務跑 `codex exec -s read-only`；若未來版本讓 exec 的唯讀對應到較寬政策，同時仍告訴模型它是唯讀，`read-only` 與 `read-only-sandbox` 都可能通過 | 已接受（2026-10-28 Q20） | 目前 Codex 新版本受 #74 影響過不了 cancellation，沒有實際放行路徑。#74 驗收時（Codex 新版本第一次可能通過驗證）補驗 exec 唯讀的對應，例如觀察 exec 實際啟動的 codex-linux-sandbox 參數 |

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-21 | 建立；Q1～Q13 回答後核准 SCN-001～SCN-010 | Raybird |
| 2026-10-21 | Phase 1 live probe：唯讀 probe 與失效對照組可信；發現取消回收（#74）與 OpenCode 沙箱（#75）兩個內建版本也有的問題，Q14、Q15 決定另開 issue、probe 維持必要；OMP／Antigravity 登入過期，Task 1.3 待重新登入 | Raybird |
| 2026-10-21 | Task 2.1 相依收窄為 Task 1.1、1.2 並完成：驗證紀錄讀寫與身分計算，路徑固定為 `$XDG_DATA_HOME/asura/runtime-verification.json` | Raybird |
| 2026-10-21 | Task 2.2 完成：OpenCode／Codex 改為身分比對，新增 `version_unverified`；OpenCode 內建版本改為精確的 1.18.23；Codex 身分涵蓋 npm 平台套件（原生檔與附帶的 bwrap） | Raybird |
| 2026-10-21 | Task 2.3 完成：OMP／Antigravity 改為內建或已驗證的身分；OMP 的 node_modules 樹雜湊記在驗證紀錄（`tree_sha256`），執行時固定 | Raybird |
| 2026-10-21 | Task 2.4 完成：daemon、runtime inventory、doctor、本機 run 都讀同一份驗證紀錄；拒絕訊息與 doctor 提示 `asura runtime verify <runtime>` | Raybird |
| 2026-10-21 | Task 2.5 完成：紀錄在 stage 執行前移除時任務 fail-closed 且不改選；升級前封存的 `inventory-v1` 快照仍通過驗證。未改產品程式 | Raybird |
| 2026-10-22 | Task 1.3、1.4 完成：OMP 18.12.6、Antigravity 1.4.4 probe 全過，對照組皆失敗；新增憑證邊界 probe、取消 probe 改以啟動標記判定、新增 `clisandbox.TreeHash` 修正 OMP 樹雜湊不一致；Phase 1 判定 probe 可信 | Raybird |
| 2026-10-22 | Task 3.1 完成：probe 產品化（`verification.Spec`／`Prepare`／`Verify`，四個 runtime 的 `VerificationSpec`），live 測試改走同一路徑；唯讀 probe 改以 nonce 為正向對照，並修訂 Task 3.1 完成判準（理由見 evidence/task-3.1.md） | Raybird |
| 2026-10-22 | Task 3.2 完成：新增 `asura runtime verify <runtime>`，全部 probe 通過才寫入紀錄，任一失敗或無法執行時結束碼非 0、紀錄不變；內建身分不執行 probe | Raybird |
| 2026-10-22 | Task 3.3 完成：`asura runtime verify --list` 列出內建身分與紀錄；`--remove <runtime> <version> [<sha256>]` 移除紀錄，內建身分不能移除 | Raybird |
| 2026-10-22 | Task 3.4 完成：確認 daemon 啟動、inventory、doctor、run、ask 都不自動執行 probe，以反向檢查證明測試有效 | Raybird |
| 2026-10-27 | Q16：Task 3.5 改用 OMP 與 Antigravity，不再相依 #74、#75。Task 3.5 完成：OMP 18.12.6、Antigravity 1.4.4 以 `asura runtime verify` 驗證後，四條判定路徑都放行，本機 run 與 daemon ask 成功；移除紀錄後回到 `version_unverified` | Raybird |
| 2026-10-27 | Task 4.1 完成：runtimes.md 新增「版本驗證」，cli-reference 新增 `runtime verify`，README 的 runtime 表與指令一覽、CHANGELOG `[Unreleased]` 更新；所有 Task 完成 | Raybird |
| 2026-10-27 | PR #77 獨立審查 RETURN TO execute-task：SCN-008 的 OpenCode 跨路徑覆蓋缺口（MUST FIX）；Q17～Q19 決定補 Codex 唯讀沙箱的確定性檢查、Antigravity 先比雜湊不執行、一併處理改善建議；新增 Phase 5 | Raybird |
| 2026-10-27 | Phase 5 完成：OpenCode 跨路徑測試；Codex 新增不經模型的 `read-only-sandbox` probe（真實 0.163.0 通過、失效對照失敗）；Antigravity 判定不執行未驗證的 agy；Codex 改用 `clisandbox.TreeHash`；verify 先檢查紀錄檔 | Raybird |
| 2026-10-28 | PR #77 第二輪獨立審查 PASS；Q20 接受 Codex exec／sandbox 唯讀對應的殘餘風險（TBD-7，#74 時補驗）；Q21 處理文件措辭與唯讀沙箱 probe 加固，新增 Task 5.5 | Raybird |
| 2026-10-28 | PR #77 第三輪獨立審查 PASS（增量 `92a84f9..af6ee49`，[報告](https://github.com/raybird/Asura/pull/77#issuecomment-6458102527)），無 MUST FIX、SHOULD FIX；NICE TO HAVE 三項未處理：process group 測試只量耗時、runtimes.md 多一個半形空格、責任對照沒有加上 Task 5.5 | Raybird |
| 2026-10-28 | PR #77 squash 合併至 main（merge commit `ca3461a`，合併內容即審查的 HEAD `af6ee49`）；GitHub #73 由 `Closes #73` 自動關閉，issue 結案。合併後 main push CI [run 40615145464](https://github.com/raybird/Asura/actions/runs/40615145464) 為 success。TBD-7 與 Codex／OpenCode 的真實端到端留到 #74、#75 驗收 | Raybird |

---
**建立日期**: 2026-10-21  
**分級**: Large — 跨四個 runtime adapter、清冊、admission、daemon 與 CLI，新增驗證紀錄與子命令  
**風險**: High — 唯讀隔離是安全邊界，新版本上 probe 的可信度尚未確認\
**狀態**: 已完成 — PR #77 於 2026-10-28 合併（merge commit `ca3461a`）
