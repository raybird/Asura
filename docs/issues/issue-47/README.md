# Issue 47 - Installer：Release 發布者身分驗證（artifact attestation）

## 概述

來源：[GitHub #47](https://github.com/raybird/Asura/issues/47)。#45 的 installer 只核對 SHA-256，但 checksum manifest 與 archive 來自同一個 GitHub Release，能改寫 Release asset 的人可以兩者一起換掉。本 issue 讓 Release workflow 產生 GitHub artifact attestation，installer 在可用時以 `gh attestation verify` 驗證發布者身分，不再只依賴同一來源的 checksum。

防護範圍：Release asset 被替換或竄改。`install.sh` 本身來自 `main`（2026-10-15 確認 `main` 未設分支保護），能改寫 repo 的人可直接改 installer，不在本 issue 的防護範圍，於文件揭露。

## 文件清單
- [implementation-plan.md](./implementation-plan.md)

## 關鍵差異

| 項目 | 變更前 | 變更後 |
|------|--------|--------|
| Release workflow | 建置 archive 與 checksum 後上傳 | 另產生 build provenance attestation，驗證檔（Sigstore bundle）一併上傳；觸發 ref 不是該 tag 時在建置前失敗；含 `-` 的 tag 發佈為 prerelease |
| installer（有支援的 `gh`） | 只核對 checksum | 比 v1.1.0 新的版本必須通過 attestation 驗證（鎖定 repo、`release.yml` 與 `refs/tags/<tag>`），失敗或缺驗證檔即拒絕安裝 |
| installer（沒有支援的 `gh`） | 只核對 checksum | 照常安裝，stderr 明確警告只核對了 checksum；`--require-attestation` 時拒絕 |
| 安裝 v1.1.0 | 只核對 checksum | 照常安裝並提示此版本沒有發布者驗證；`--require-attestation` 時拒絕 |

## 涉及檔案

```text
.github/workflows/release.yml     ← 修改：權限、tag ref 檢查、attestation、prerelease、上傳驗證檔
install.sh                        ← 修改：gh 偵測、--require-attestation、驗證流程
scripts/installer/                ← 修改：harness 加 gh stub 與驗證檔路由；新增驗證情境測試
docs/guides/setup.md              ← 修改：信任邊界、旗標、限制
CHANGELOG.md                      ← 修改：Unreleased
scripts/release/build-archives.sh ← 不動（archive 與 checksum 契約不變）
```

## Gherkin 驗收劇本

```gherkin
Feature: installer 驗證 Release 發布者身分

  Background:
    Given 「支援的 gh」指 PATH 上的 gh 提供 attestation verify，且支援 --bundle、--signer-workflow、--source-ref
    And 「驗證檔」指 Release workflow 為該 tag 產生、隨 Release 上傳的 attestation bundle

  @SCN-001
  Scenario: 有支援的 gh 時，新版本通過發布者驗證後才安裝
    Given 一個比 v1.1.0 新的 Release，含 archive、checksum manifest 與驗證檔
    And 機器上有支援的 gh，且未登入 GitHub
    When 使用者以 installer 安裝該版本
    Then installer 在 checksum 核對後，以 gh 驗證 archive，鎖定 repo raybird/Asura、workflow .github/workflows/release.yml、來源 ref refs/tags/<tag>，並拒絕 self-hosted runner
    And 驗證通過後才替換 binary，輸出註明已驗證發布者

  @SCN-002
  Scenario Outline: 有支援的 gh 時，發布者驗證不過就拒絕安裝
    Given 一個比 v1.1.0 新的 Release，且 <狀況>
    And 機器上有支援的 gh
    And 安裝目錄已有舊的 asura binary
    When 使用者以 installer 安裝該版本
    Then installer 以非零狀態結束並說明發布者驗證失敗，不退回只核對 checksum
    And 舊 binary 保持原樣，暫存檔已清除

    Examples:
      | 狀況 |
      | archive 與 checksum manifest 被一起換掉（兩者一致），驗證檔仍是原版 |
      | 驗證檔不存在 |
      | 驗證檔的簽章來源是其他 tag 或其他 workflow |
      | gh 無法完成驗證（例如連不到 Sigstore） |

  @SCN-003
  Scenario: 沒有支援的 gh 時，照常安裝並警告
    Given 一個比 v1.1.0 新的 Release
    And 機器上沒有 gh，或 gh 不支援所需的 attestation 驗證
    When 使用者以 installer 安裝該版本，未加 --require-attestation
    Then installer 完成 checksum 與既有檢查後安裝
    And stderr 警告只核對了 checksum、未驗證發布者，並說明原因（找不到 gh 或 gh 不支援）

  @SCN-004
  Scenario: 要求驗證但沒有支援的 gh 時，在變更前失敗
    Given 機器上沒有 gh，或 gh 不支援所需的 attestation 驗證
    When 使用者以 installer 安裝並加上 --require-attestation
    Then installer 在下載 archive 與任何檔案變更前以非零狀態結束，說明需要支援 attestation 的 gh

  @SCN-005
  Scenario Outline: v1.1.0 沒有驗證檔，依旗標決定安裝或拒絕
    Given 要安裝的版本是 v1.1.0（指定或由 latest 解析）
    And 機器上 <gh 狀態>
    When 使用者以 installer 安裝，<旗標>
    Then <結果>

    Examples:
      | gh 狀態       | 旗標                     | 結果 |
      | 有支援的 gh   | 未加 --require-attestation | 不下載驗證檔，照常安裝並提示此版本沒有發布者驗證 |
      | 沒有 gh       | 未加 --require-attestation | 不下載驗證檔，照常安裝並提示此版本沒有發布者驗證 |
      | 有支援的 gh   | 加上 --require-attestation | 下載 archive 與變更檔案前以非零狀態結束，說明 v1.1.0 沒有發布者驗證 |

  @SCN-006
  Scenario: Release workflow 為 tag 產生可驗證的驗證檔
    Given 維護者推送 v* tag
    When Release workflow 執行
    Then 兩個平台的 archive 都有 build provenance attestation，驗證檔以固定名稱上傳到該 Release
    And 以支援的 gh、未登入、套用 SCN-001 的身分鎖定，可驗證兩個 archive
    And tag 含 "-" 時，Release 標為 prerelease，releases/latest 不變

  @SCN-007
  Scenario: 觸發 ref 不是該 tag 時，Release workflow 在建置前失敗
    Given 維護者以 workflow_dispatch 從非該 tag 的 ref 觸發，輸入某個 tag
    When Release workflow 執行
    Then workflow 在建置與上傳前失敗，說明需以該 tag 作為觸發 ref
```

## Gherkin 核准紀錄
- **核准 commit**: 待提交
- **核准來源**: 2026-10-15 使用者以 `/dev-cycle issue:47` 要求推進，逐題確認答案：
  - Q1「GitHub attestation (Recommended)」：release.yml 產生 attestation、驗證檔上傳到 Release，installer 以 `gh attestation verify --bundle` 驗證並鎖定 `release.yml` 與對應 tag（SCN-001、SCN-006）
  - Q2「有 gh 就必驗，沒有則警告 (Recommended)」：有 gh 時驗證失敗或缺驗證檔即拒絕、不退回；沒 gh 時警告後安裝；`--require-attestation` 強制（SCN-001～SCN-004）
  - Q3「固定切點：v1.1.0 及更早免驗 (Recommended)」：依版本號判定，不看驗證檔是否存在（SCN-005）
  - Q4「合併前發測試 prerelease (Recommended)」：含 `-` 的 tag 標為 prerelease，從 PR 分支推送 `v1.1.1-rc.1` 之類的 tag 跑真實 workflow 並以新 installer 實裝與竄改對照（SCN-006、步驟 1、3）
  - Q5「視同沒有 gh (Recommended)」：gh 不支援 attestation 驗證時依 SCN-003／SCN-004 處理
  - 總結確認「照此核准 (Recommended)」：D1 免驗只限剛好 v1.1.0，v1.1.0-rc.* 也要求驗證（SCN-005）；D2 簽章身分鎖定 `refs/tags/<tag>`，觸發 ref 不是該 tag 時 workflow 在建置前失敗（SCN-001、SCN-007）；D3 連不到 Sigstore 視為驗證失敗，不加略過旗標、只在文件揭露（SCN-002）

全部 Scenario 於 2026-10-15 核准。

## 風險與首要驗證

- **最大風險**：真實 Release workflow 產生的驗證檔，能否在未登入的使用者機器上以 `gh attestation verify --bundle` 加上身分鎖定驗過，且竄改或身分不符時確實失敗。不成立時，有 gh 的使用者會裝不起任何新版本，或驗證形同虛設
- **風險等級與理由**：High — 屬安全邊界；核心外部行為（GitHub attestation 與 gh 驗證契約）尚未在本專案的 workflow 上確認；失敗會擋住所有有 gh 的使用者安裝
- **首要驗證**：先改 release.yml，從 issue 分支推送測試 prerelease tag `v1.1.1-rc.1` 跑真實 workflow；下載驗證檔與 archive，在未登入的環境以預定的身分鎖定參數驗證，並以竄改 archive、錯誤 source ref、錯誤 signer workflow 三個對照組確認會失敗
- **選擇理由**：這是唯一能取得「本專案 workflow 簽出的真實驗證檔」的途徑。2026-10-15 已用 cli/cli 的公開 attestation 確認 gh 未登入時以 `--bundle` 可驗證、竄改與錯誤 ref 會失敗，但那不是本專案的 workflow 與身分
- **完成證據**：真實 workflow 成功；Release 為 prerelease 且 latest 仍是 v1.1.0；兩個 archive 以身分鎖定驗證 exit 0；三個對照組全部非零並顯示對應的失敗原因

## Timeline

| 日期 | 異動 | 負責人 |
|------|------|--------|
| 2026-10-15 | 建立；Q1～Q5 與總結確認回答後核准 SCN-001～SCN-007 | Raybird |

---
**建立日期**: 2026-10-15  
**分級**: Medium — 修改 installer、release workflow、installer 測試與安裝文件，範圍有界、不涉及 runtime 程式  
**風險**: High — 安全邊界，且依賴尚未在本專案驗證的外部簽章契約\
**狀態**: 待實作
