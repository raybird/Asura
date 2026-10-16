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
- **核准 commit**: 待提交

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

## 實作與驗證步驟

1. **AC-1：latest 解析到 v1.1.0 時拒絕**— 驗證方式：先改 `TestAttestationV110` 的 latest 情境為預期拒絕並取得紅燈，再修改 `install.sh`
   - 產出：測試與 installer 的修改
   - 相依：無
   - 完成判準：修正前紅燈（會安裝並只警告），修正後四種情境都拒絕，且只請求 latest API、安裝目錄未建立
2. **AC-2：其他路徑不變與回歸**— 驗證方式：installer／release 既有測試、本機最小回歸
   - 產出：測試與回歸結果（寫回本節）
   - 相依：步驟 1
   - 完成判準：既有斷言未改且全數通過
3. **AC-3：真實安裝與文件**— 驗證方式：本分支 `install.sh` 對真實 GitHub 安裝；文件人工核對
   - 產出：安裝紀錄、`setup.md` 與 `CHANGELOG.md` 修改
   - 相依：步驟 1
   - 完成判準：預設安裝顯示 Verified 並裝上 v1.2.0；`--version v1.1.0` 照常警告；文件描述與新行為一致

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-16 | 建立；Q1、Q2 回答後核准 AC-1～AC-3 | Raybird |

---
**建立日期**: 2026-10-16  
**分級**: Small — installer 單一判定加測試與文件  
**風險**: High — 安全邊界，誤判會擋住預設安裝或留下降版繞過\
**狀態**: 進行中
