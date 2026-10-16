# Issue 58 - Installer：正式版有 attestation 後，取消 latest 解析到 v1.1.0 的免驗 (Small)

## 概述

來源：[GitHub #58](https://github.com/raybird/Asura/issues/58)。#47（PR #57）讓 installer 驗證 Release 的 artifact attestation，但對 v1.1.0 免驗（#47 SCN-005）：不論是 `--version v1.1.0` 還是由 `releases/latest` 解析到 v1.1.0，都只核對 checksum 並警告。PR #57 審查的 SHOULD FIX 1 指出，能改寫 Release 檔案的人可以替換 v1.1.0 的 archive 與 checksum，再把 latest 指回 v1.1.0；之後不帶 `--require-attestation` 的預設安裝就會裝上被竄改的 binary，只留下一行警告。

前置條件已在 2026-10-16 達成：第一個帶 attestation 的正式版 v1.2.0 已發佈，`releases/latest` 指向 v1.2.0（[#58 留言](https://github.com/raybird/Asura/issues/58#issuecomment-6209404290)）。

### 現況（2026-10-16，main `29fa058`）

`install.sh:127-131` 只看 `tag` 是否等於 `v1.1.0`，不區分 tag 的來源：

- 由 latest 解析到 v1.1.0：免驗，照常安裝並警告（`TestAttestationV110` 的 `with-gh`／`without-gh`）
- `--version v1.1.0`：同上
- 加 `--require-attestation`：兩種來源都在下載 archive 前拒絕

本 issue 只改「由 latest 解析到 v1.1.0」這條路徑；`--version v1.1.0` 維持現狀。這取代 #47 SCN-005 中「由 latest 解析」的部分，#47 的文件保留歷史原貌。`install.sh` 由 `main` 分支提供，合併後即生效，不需要另發 Release。

## 涉及檔案

- `install.sh` — latest 解析到 v1.1.0 時拒絕
- `scripts/installer/install_attestation_test.go` — 修改 `TestAttestationV110` 的 latest 情境
- `scripts/installer/install_test.go`、`scripts/installer/install_safety_test.go` — 只把 latest fixture 的版本號從 v1.1.0 換成 v1.2.0（見步驟 2）；依 Q3 加強 hardlink 測試的失敗原因斷言
- `docs/guides/setup.md` — 「發布者驗證」表格與限制
- `CHANGELOG.md` — Unreleased

## 驗收條件

1. **AC-1**：未指定 `--version`、`releases/latest` 解析到 v1.1.0 時，不論有支援的 `gh`、沒有 `gh`、`gh` 太舊，或加上 `--require-attestation`，installer 都以非零狀態結束，stderr 說明 latest 解析到沒有發布者驗證的 v1.1.0，並提示確實要安裝時用 `--version v1.1.0`。除了查詢 latest 之外不發出任何下載請求，也不建立或變更安裝目錄 — 檢查方式：installer harness 測試，修正前先取得紅燈（目前會安裝並只警告）
   - 失敗路徑：任何一種情境仍會安裝、下載 checksum／archive，或動到安裝目錄，都判定未通過
2. **AC-2**：其他路徑行為不變：`--version v1.1.0` 有或沒有 `gh` 都照常安裝並警告、加 `--require-attestation` 則拒絕；latest 解析到帶驗證檔的版本時照常驗證並安裝；`--version` 指定其他版本與 prerelease 的行為不變。installer 與 release 的既有測試全部通過，本機最小回歸（`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`）通過 — 檢查方式：既有測試與回歸命令
   - 失敗路徑：為了通過而修改其他情境的既有斷言，即判定未通過
3. **AC-3**：真實環境：以本分支的 `install.sh` 對真實 GitHub（latest 為 v1.2.0）在有 `gh` 未登入時預設安裝，仍顯示 `Verified publisher attestation` 並安裝 v1.2.0；`--version v1.1.0` 仍安裝並警告。`docs/guides/setup.md` 的表格與限制改寫為新行為，`CHANGELOG.md` Unreleased 記錄此變更 — 檢查方式：實際安裝紀錄與文件人工核對
   - 失敗路徑：預設安裝失敗或沒有驗證；文件仍描述 latest 解析到 v1.1.0 會免驗

- **核准日期**: 2026-10-16
- **核准來源**: GitHub #58「目標」（使用者於 2026-10-15 依 #47 Q6 決定開立）；2026-10-16 使用者以 `/dev-cycle issue:58` 要求推進，並回答 #58「待決定」兩題：Q1「一律拒絕 (Recommended)」，接受建議：latest 解析到 v1.1.0 時不論有沒有 gh 都在下載前拒絕，提示改用 `--version v1.1.0`（AC-1）；Q2「維持免驗並警告 (Recommended)」，接受建議：明確指定 `--version v1.1.0` 的行為不變（AC-2）
- **核准 commit**: 30dcacb

## 風險與首要驗證

- **最大風險**：屬安全邊界。新判定若誤判，會擋住所有人的預設安裝；若沒擋住，降版繞過仍然存在
- **風險等級與理由**：High — 涉及安全邊界；修改範圍小、行為已由 Q1／Q2 決定，主要風險在判定是否確實只作用在 latest 路徑
- **首要驗證**：先在 harness 以「latest 解析到 v1.1.0」重現降版路徑（修正前會安裝並只警告）取得紅燈，再修正；另以本分支的 installer 對真實 GitHub 預設安裝，確認 latest 為 v1.2.0 時仍正常驗證並安裝
- **選擇理由**：harness 紅燈直接證明攻擊路徑被關閉；真實安裝證明不會擋住正常的預設安裝
- **完成證據**：修正前紅燈、修正後各情境的 harness 綠燈；真實預設安裝顯示 Verified 並裝上 v1.2.0

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|------|------|------|------|
| TBD-1 | latest 解析到 v1.1.0 時的行為（#58 待決定第二題） | 已解決 | 2026-10-16 Q1：一律拒絕，不論有沒有 gh |
| TBD-2 | 明確指定 `--version v1.1.0` 是否也取消免驗（#58 待決定第一題） | 已解決 | 2026-10-16 Q2：維持免驗並警告，加 `--require-attestation` 才拒絕 |
| TBD-3 | PR #64 審查 NICE TO HAVE 1（`需決策`）：hardlink 測試不檢查失敗原因，拿掉 installer 的 hardlink 防線或 fixture 沒換版號時都會假綠；補斷言是否違反 AC-2「不得修改其他情境的既有斷言」 | 已解決 | 2026-10-16 Q3 回答「在本 PR 補上斷言 (Recommended)」：加強既有斷言（不放寬）不屬 AC-2 失敗路徑禁止的修改，在本 PR 要求 stderr 含 `unsafe archive entry type in <asset>` |

## 實作與驗證步驟

1. ✅ **AC-1：latest 解析到 v1.1.0 時拒絕**— 驗證方式：先改 `TestAttestationV110` 的 latest 情境為預期拒絕並取得紅燈，再修改 `install.sh`
   - 產出：測試與 installer 的修改
   - 相依：無
   - 完成判準：修正前紅燈（會安裝並只警告），修正後四種情境都拒絕，且只請求 latest API、安裝目錄未建立
   - 完成證據（2026-10-16，go1.27.1 linux/amd64）：
     - 測試：`TestAttestationV110` 原本的 latest 情境（有 gh／沒有 gh 都安裝並警告）拆成兩組。`explicit-with-gh`／`explicit-without-gh` 改用 `--version v1.1.0`，斷言與原本相同（安裝並警告、不下載驗證檔、不呼叫 gh）。`latest-with-gh`／`latest-without-gh`／`latest-gh-too-old`／`latest-require` 預期拒絕：stderr 為 `asura-install: error: latest release resolved to v1.1.0, which has no publisher attestation; to install it anyway, use --version v1.1.0`，HTTP 請求只有 latest API，沒有呼叫 gh，也沒有建立安裝目錄
     - 修正前紅燈：`go test -count=1 -run 'TestAttestationV110' -v ./scripts/installer` FAIL。`latest-with-gh`／`latest-without-gh`／`latest-gh-too-old` 都是 `code=0`，只留下 `publisher not verified (v1.1.0 has no publisher attestation)` 警告，也就是降版路徑會安裝成功；`latest-require` 為 `--require-attestation: v1.1.0 has no publisher attestation`，訊息與新判定不同。其他 4 個子測試 PASS
     - 修正：`install.sh` 在解析 latest 並通過 `check_version` 後，tag 為 v1.1.0 就以上述訊息結束；位置在下載 checksum 與 archive 之前。原本 v1.1.0 免驗的判定不變，現在只會在 `--version v1.1.0` 時走到
     - 修正後：同一命令 8 個子測試全部 PASS
2. ✅ **AC-2：其他路徑不變與回歸**— 驗證方式：installer／release 既有測試、本機最小回歸
   - 產出：測試與回歸結果（寫回本節）
   - 相依：步驟 1
   - 完成判準：既有斷言未改且全數通過
   - 完成證據（2026-10-16）：
     - fixture 調整：#45 的 installer 測試以 `stableTag`（v1.1.0）當「目前的正式版 latest」fixture，用來測 checksum、archive 安全檢查、架構選擇與工具 fallback 等與版本無關的行為。依 Q1，latest 解析到 v1.1.0 會被拒絕，所以 `TestInstallFailuresPreserveExistingBinary`（14 個子測試）、`TestInstallFreshAmd64`、`TestInstallExplicitDirWithSpace`、`TestInstallToolFallbacks`、`TestInstallArm64Asset` 在修改後都會失敗。處理方式是把 fixture 換成目前真正的 latest：`stableTag` 改為 v1.2.0，並把 `install_test.go` 3 行請求 URL、`install_safety_test.go` 的 `root` 與 hardlink entry 名稱中的版本號一起改成 v1.2.0。各測試的檢查項目與預期結果（錯誤類型、訊息、安裝結果、請求的有無）都沒有改變。`TestAttestationV110` 原本借用 `stableTag` 代表 v1.1.0，改成直接寫 `"v1.1.0"`
     - 假綠檢查：`TestInstallRejectsHardlinkAndMultilineVersion` 只斷言「安裝失敗、既有安裝未受損」，不檢查 stderr，fixture 若沒有跟著改，就可能因為 latest 被拒絕而假綠。以 `go test -overlay` 暫時印出 stderr（只用於確認，未進入交付），GNU tar 與 busybox 兩種情況都是 `unsafe archive entry type in asura_v1.2.0_linux_amd64.tar.gz`，確實是因為 hardlink 而拒絕
     - 未調整的 v1.1.0：`harness_selfcheck_test.go` 測的是 harness 自己的 `fetch.sh`，不經過 installer 判定；`install_safety_test.go` 的明確降版（`--version v1.1.0`）與兩個版本格式錯誤測試不經過 latest，維持原樣
     - `go test -count=1 -v ./scripts/installer` ok
     - 本機最小回歸（交付版本）：`go vet ./...` 通過；`go test -count=1 -race -v ./...` 23 個 package 全部 `ok`；`go build ./cmd/asura` 成功。被 SKIP 的 14 項不計為通過，都是需要固定版本 runtime 或登入憑證的 live／contract 測試，與本次修改無關：TestLiveAdapter（2 處）、TestCodexProbe_BinaryVersion、TestCodexProbe_NonInteractive_JSONL、TestCodexProbe_ReadOnlySandbox、TestCodexProbe_WorkspaceWriteSandbox、TestCodexProbe_ContextCancellation、TestAdapter_Run_Live、TestCodexProbe_Live、TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly、TestUtilityLiveProbe、TestUtilityCapacityProbe、TestINT001、TestINT007
     - 重構 no-op：installer 只新增一個判定，測試沿用既有寫法，沒有可精煉的內容
     - 審查後加強（Q3，2026-10-16）：`TestInstallRejectsHardlinkAndMultilineVersion` 的 hardlink 子測試加上斷言，要求 stderr 含 `asura-install: error: unsafe archive entry type in asura_v1.2.0_linux_amd64.tar.gz`。對照：暫時把 `install.sh` 的 hardlink 判定改成 `if false`（之後以 `git checkout` 還原），`hardlink-gnu`／`hardlink-busybox` 都 FAIL，stderr 為 `staged binary reports , expected v1.2.0 linux/amd64`（加強前兩者都 PASS）；以 `go test -overlay` 把 fixture 改回 v1.1.0，兩者也 FAIL，stderr 為 latest 拒絕訊息。還原後同一測試 PASS，`go vet ./scripts/installer` 與 `go test -count=1 -v ./scripts/installer` ok；其餘套件未修改，沿用上方的全 repo 回歸
3. ✅ **AC-3：真實安裝與文件**— 驗證方式：本分支 `install.sh` 對真實 GitHub 安裝；文件人工核對
   - 產出：安裝紀錄、`setup.md` 與 `CHANGELOG.md` 修改
   - 相依：步驟 1
   - 完成判準：預設安裝顯示 Verified 並裝上 v1.2.0；`--version v1.1.0` 照常警告；文件描述與新行為一致
   - 完成證據（2026-10-16，gh 2.74.2，以空的 `GH_CONFIG_DIR` 並移除 token，模擬未登入；安裝到 scratch 目錄）：
     - `releases/latest` 為 v1.2.0。以本分支的 `sh install.sh --install-dir <dir>` 預設安裝：exit 0，印出 `Verified publisher attestation for asura_v1.2.0_linux_amd64.tar.gz` 與 `Installed asura v1.2.0`，`asura version` 為 `v1.2.0 (commit 29fa058…)`
     - `--version v1.1.0`：exit 0，安裝 v1.1.0（commit `cc68482…`），stderr 警告 `publisher not verified (v1.1.0 has no publisher attestation)`
     - `--version v1.1.0 --require-attestation`：exit 1，`--require-attestation: v1.1.0 has no publisher attestation`，沒有建立安裝目錄
     - latest 實際解析到 v1.1.0 的情況無法在真實 GitHub 上製造（必須真的移動 latest），這部分由步驟 1 的 harness 測試涵蓋
     - 文件：`docs/guides/setup.md`「發布者驗證」表格把「安裝 v1.1.0」拆成「latest 解析到 v1.1.0」（拒絕）與「以 `--version v1.1.0` 安裝」（行為不變）兩列；「限制」改寫降版風險，只剩明確指定 v1.1.0 的情況；最後驗證日期更新。`CHANGELOG.md` Unreleased 新增安全性條目

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-16 | 建立；Q1、Q2 回答後核准 AC-1～AC-3 | Raybird |
| 2026-10-16 | PR #64 獨立審查 PASS（審查 `29fa058..4d4e767`，[報告](https://github.com/raybird/Asura/pull/64#issuecomment-6209995425)），無 MUST FIX；修正 SHOULD FIX 兩項（setup.md 驗證日期的證據來源、涉及檔案清單）；NICE TO HAVE 1 依 Q3 在本 PR 補上 hardlink 失敗原因斷言 | Raybird |
| 2026-10-16 | PR #64 第二次獨立審查 PASS（審查 `29fa058..55c7c33`，[報告](https://github.com/raybird/Asura/pull/64#issuecomment-6210196706)），無 MUST FIX、SHOULD FIX；NICE TO HAVE 兩項（測試字面版號改用 `stableTag`、步驟 2 判準補註 Q3）未處理 | Raybird |
| 2026-10-16 | PR #64 squash 合併至 main（merge commit `b4d647f`，合併內容即審查的 HEAD `55c7c33`）；GitHub #58 由 `Closes #58` 自動關閉。合併後 main push CI [run 38866299475](https://github.com/raybird/Asura/actions/runs/38866299475) 為 success；`raw.githubusercontent.com/raybird/Asura/main/install.sh` 與合併版本相同，新行為已對外生效；issue 結案 | Raybird |

---
**建立日期**: 2026-10-16  
**分級**: Small — installer 單一判定加測試與文件  
**風險**: High — 安全邊界，誤判會擋住預設安裝或留下降版繞過\
**狀態**: 已完成 — PR #64 於 2026-10-16 合併（merge commit `b4d647f`）
