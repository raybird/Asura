# Issue 45 — v1.1 Distribution & Bootstrap

## 概述

來源：[GitHub #45](https://github.com/raybird/Asura/issues/45)。2026-10-07 盤點基準為 `611577b`；issue 沒有留言或既有核准紀錄。

目標是提供 Install → Verify → Doctor → Config → Setup → User Service → Ready，支援 Linux amd64／arm64，安裝正式 Release，不要求 root。設定僅提供 operator defaults，不改變 runtime selection、StageBindingPlan、ExecutionContract、ExecutionAudit 的權威。

2026-10-09 全部 Scenario 與 DIST-TBD 決策已核准，來源見「Gherkin 核准紀錄」及「待確認事項」。issue 的 Requirements 與候選劇本是需求來源；設計決策保留 issue 的 DIST-TBD 編號。

不包含 macOS／Windows、套件倉庫、容器部署、runtime 自動安裝／登入、secret manager、remote execution、federation、自動 retry／failover、背景更新或 telemetry。

## 文件清單

- [需求與現況分析](requirement-analysis.md)
- [技術方案與待決設計](technical-analysis.md)
- [實作計畫](implementation-plan.md) — 唯一任務來源；核准後由 decompose 細化 Phase／Task。

## 關鍵差異

| 項目 | 盤點現況（2026-10-07） | 目標（2026-10-09 核准） |
|---|---|---|
| 安裝 | 手動解壓或 Go build | 正式 Release、checksum、atomic replace |
| DB／socket | cwd `asura.db`／`~/.asura/asura.sock`；指令間有差異 | 無 config 維持舊行為；setup 啟用 XDG；不搬移舊 DB |
| 診斷 | runtime inventory | 重用 inventory 的 read-only doctor |
| 設定／常駐 | CLI、ENV、手動 daemon | config defaults、冪等 setup、選用 systemd user service |

## 涉及檔案

下列是預計必要邊界；實際新增檔名由已核准方案決定。

```text
install.sh                      新增：正式 Release 安裝入口
cmd/asura/                      修改：共用設定、version／doctor／setup／service 接線與測試
internal/config/                新增：operator config／XDG／precedence 與測試
internal/backend/               限 inventory 接線或 executable 來源資訊；保留 admission／seal 政策
internal/contextwin/config.go   限設定驗證重用；保留 context admission 語意
.github/workflows/release.yml   修改：version 注入、asset 契約與 install fixture
scripts/                        新增：installer／clean Linux fixture（沿用實際測試入口）
go.mod、go.sum                   僅 TOML parser 確有需要時更新
README.md、docs/guides/          更新：安裝、設定、診斷、service 與保留資料指南
docs/development.md             更新：issue 索引
docs/issues/issue-45/            規格、權威計畫與後續證據
internal/storage/               不改：SQLite schema／Task／Memory 契約
internal/engine、pipeline、audit/ 不改：Phase 4 執行與稽核權威
```

## 風險與首要驗證

- **最大風險**：下載／驗證失敗仍取代既有 binary，或 config／路徑預設使既有資料不可見、擴張 allowed roots。
- **風險等級與理由**：High — 涉及供應鏈、安全邊界與既有 Task／Memory 資料保留；Large 是因跨 CLI、設定、發佈與 service 邊界，兩軸分開判定。
- **首要驗證**：先刻畫現有 CLI／ENV／DB／socket 行為，再以 sentinel binary／config／DB fixture 驗證 checksum mismatch、缺 asset、下載中斷及替換失敗。
- **選擇理由**：契約盤點與故障注入直接驗證是否錯讀資料或破壞 binary，比先跑一般 onboarding 更能降低最大風險。
- **完成證據**：失敗情境有非零 exit、舊 binary hash 不變、資料內容不變；precedence 使用不同字面值證明來源。紅綠命令、結果、被測版本與環境保存於原 Task 或 evidence。2026-10-07 尚未取得實作測試證據。

## Gherkin 驗收劇本

2026-10-09 核准；具體 enum、路徑及介面以 technical-analysis 已確認的決策為準。

```gherkin
Feature: Linux Distribution & Bootstrap

  @DIST-001
  Scenario: Fresh amd64 install
    Given 無 Asura 的 Linux amd64 fixture 與含官方 checksum 的 stable Release
    When 執行預設 installer
    Then 使用者 bin 目錄取得通過 SHA-256 的 amd64 binary
    And version 機器輸出對應該 Release 且未要求 sudo 或 Go

  @DIST-002
  Scenario: Fresh arm64 install
    Given Linux arm64 fixture 與同一 Release 的兩種架構 asset
    When 執行 installer
    Then 下載 arm64 asset 且 checksum 正確
    And 原生 arm64 或明確標示的模擬環境能執行 version

  @DIST-003
  Scenario: Checksum failure
    Given 已安裝 sentinel binary 與被竄改的下載 archive
    When 執行 installer
    Then 非零退出且既有 binary 的內容與可執行權限不變
    And 不留下被當成成功安裝的 partial binary

  @DIST-004
  Scenario Outline: 安全重新安裝與升降版
    Given 既有 binary 與獨立的 config 和 Task／Memory 資料
    When 進行 <操作>
    Then 未指定版本使用 latest stable，同版重跑重新驗證，顯式 --version 可安裝較舊正式版
    And 設定與資料內容不變且不自動重啟 daemon
    And 任何安裝前失敗不破壞既有 binary
    Examples:
      | 操作 |
      | 同版本重新安裝 |
      | 升級 stable release |
      | 顯式指定較舊版本 |
      | 下載中斷 |
      | 缺少 asset 或 checksum 項目 |
      | binary replace 失敗 |
      | 既有目標為 symlink 或非一般檔案 |
      | 顯式指定無 version metadata 的 v1.0.0 |

  @DIST-005
  Scenario: Unsupported OS or arch
    Given 非 Linux 或非 amd64／arm64 的主機
    When 執行 installer
    Then 明確非零退出且尚未安裝 binary

  @DIST-006
  Scenario: Doctor ready 與缺少必要條件
    Given Linux system／config 條件正常且至少一個符合固定版本及能力的 runtime
    When 執行 doctor
    Then 報告 READY 或 DEGRADED、exit 0 與可用能力範圍
    And 沒有 model call 或 Task submission，也不宣稱已驗證模型登入
    And 當所有 runtime 不可用或必要 system／config 條件失敗時報告 NOT_READY、exit 1

  @DIST-007
  Scenario: Optional runtime failure
    Given 一個可用 runtime 與缺失或版本不符的 optional runtime
    When 執行 doctor
    Then 顯示有界 reason 與能力摘要並報告 DEGRADED、exit 0
    And 不因 optional runtime 缺失判成 NOT READY

  @DIST-008
  Scenario: Doctor secret safety 與唯讀邊界
    Given ENV／credential 與 probe stderr 含已知 secret sentinel，且有既存 config／DB
    When 執行 doctor 並讓其中一個 probe 失敗或逾時
    Then stdout／stderr 不含任何 sentinel 或任意原始 probe 輸出
    And 不改寫 config／DB／runtime credential 或建立 user service
    And 所有探測在核准的 deadline 內結束

  @DIST-009
  Scenario: Config precedence
    Given CLI、ENV、config 與內建預設分別使用不同值
    When 執行支援該設定的 CLI 並逐層移除 override
    Then 觀察到 CLI 大於 ENV 大於 config 大於內建預設
    And CLI 顯式空值或等於預設的值也不被低順位來源蓋過

  @DIST-010
  Scenario Outline: Invalid config fail-closed
    Given schema_version 為 1 的 config.toml 包含 <錯誤>
    When 啟動需要載入設定的指令
    Then 明確非零退出且不靜默使用內建預設或啟動 daemon
    And 錯誤不回顯 secret 值
    Examples:
      | 錯誤 |
      | TOML 語法錯誤 |
      | 未知欄位 |
      | 未支援的 schema／enum |
      | 無效的 path／數值 |
      | 已存在但無法讀取的設定檔 |
      | 其他使用者可寫入或非本人擁有的設定檔 |

  @DIST-011
  Scenario: Fresh setup
    Given 全新 Linux 使用者目錄與使用者指定的 allowed root
    When 在終端機互動執行 setup，或以 --allowed-roots 在腳本執行 setup
    Then 建立 XDG config／data 並只允許指定 root
    And 設定檔名為 config.toml 且 schema_version 為 1
    And config 使用 XDG_CONFIG_HOME、DB 使用 XDG_DATA_HOME；socket 優先 XDG_RUNTIME_DIR，否則使用 XDG_STATE_HOME
    And 顯示 doctor runtime 結果與下一步
    And 只有互動明確同意或顯式 --service 才安裝、啟用並啟動 user service
    And 非互動模式缺少建立設定所需的 allowed roots 時明確失敗，不等待輸入
    And 未經選擇不啟用 service、安裝或登入 runtime、修改 shell rc 或啟動 Web

  @DIST-012
  Scenario: Setup idempotency
    Given 已有設定、Task／Memory／DB、runtime credential 及 user unit
    When 使用相同選項重跑 setup
    Then 不覆寫已有自訂設定或重置資料與登入
    And 不自動搬移或刪除既有 DB／Task／Memory
    And 新參數與既有設定衝突時明確失敗並提示手動修改設定
    And 不產生重複 unit，失敗時不宣稱完成

  @DIST-013
  Scenario: User service lifecycle
    Given 可用 systemd user bus 與有效 config
    When 使用 asura service install／start／stop／restart／status
    Then systemd 管理 daemon 並使用正式 config
    And restart policy 有界且 unit 不含 secret，也不設定 linger
    And stop／restart 先等待任務排空最多 60 秒，逾時回收 unit 所有剩餘進程
    And 異常退出隔 5 秒重啟，60 秒內最多啟動 3 次；手動 stop 不自動重啟
    And 中斷任務下次啟動標記 failed，不自動重跑

  @DIST-014
  Scenario: No-systemd host
    Given 沒有 systemd 或沒有 user bus 的 Linux 主機
    When 查詢 service 並以 standalone 啟動 daemon
    Then service 不可用原因明確且 standalone 仍能依 config 使用
    And 必要條件與至少一個 runtime 可用時 doctor 報告 DEGRADED、exit 0
    And 不自行啟動另一個 supervisor

  @DIST-015
  Scenario: Uninstall data retention
    Given binary、user unit、config 與已知 Task／Memory 資料
    When 依文件移除 binary 或執行 asura service uninstall
    Then config 與 SQLite／Task／Memory 保持可讀且內容不變
    And 不執行隱藏資料清除

  @DIST-016
  Scenario: Release contract 與 clean Linux onboarding
    Given 正式 Release 的 tag、version metadata、兩架構 archive 與 checksum manifest
    When 在無 Go／repo 的 clean Linux fixture install、doctor、setup 並啟動 daemon
    Then asset 名稱、archive 路徑、tag／version／commit／arch 與 checksum 一致
    And 測試證據區分真實 Release、HTTP fixture、stub runtime 與真實 runtime
    And 同 tag 重跑 release workflow 不覆寫已發佈的 archive 或 checksum manifest

  @DIST-017
  Scenario: Backward compatibility 與完整回歸
    Given v1.0 CLI／ENV 行為基線與 Phase 4 測試
    When 驗證無 config、有 config、CLI override 的 daemon／ask／run／status／cancel／audit／inventory／Cron／memory／Web
    Then 明列的相容契約與 Phase 4 authority 不變
    And 無設定檔時保留各指令原本的 DB／socket 預設與本機 fallback
    And go vet、全套 race test 與 build 通過
    And runtime live／contract 跳過項目明列而不計為通過
```

## Gherkin 核准紀錄

- **核准 commit**: 384a0d1
- **核准來源**: 2026-10-07 使用者對第 1 題回答「1」，接受「沒有設定檔時沿用原本 DB／socket 路徑；執行 setup 後使用 XDG。舊資料不自動搬移」。已確認 DIST-TBD-8 與 DIST-009／011／012／014／015／017 的路徑相容部分；其他行為尚待確認，未將整個 Scenario 記為已核准。
- **第 2 題來源**: 2026-10-07 使用者回答「1」，接受 TOML（config.toml）與 schema_version = 1，拒絕未知欄位／無效設定，缺檔沿用舊行為、壞檔明確失敗。確認 DIST-TBD-6；影響 DIST-009／010／011／012／017 的設定格式與解析部分，其餘必要決策未完成前不把完整 Scenario 記為已核准。
- **第 3 題來源**: 2026-10-07 使用者回答「1」，接受新增 asura service install／start／stop／restart／status／uninstall，由 systemd 管理，移除 service 保留設定與資料。確認 DIST-TBD-11；影響 DIST-013／014／015 的管理介面，不推定核准 setup 自動啟用或 unit restart／shutdown 政策。
- **第 4 題來源**: 2026-10-07 使用者回答「1」，接受 setup 互動＋腳本模式：TTY 引導選 allowed roots／service，腳本用 --allowed-roots／--service 明確指定；service 預設不啟用，明確選擇才安裝並啟動；重跑保留既有設定與資料，衝突拒絕覆寫。確認 DIST-TBD-9；影響 DIST-011／012／013，不推定核准 unit restart／shutdown 政策。
- **第 5 題來源**: 2026-10-07 使用者回答「1」，接受 doctor「可用就成功」：READY=0、DEGRADED=0、NOT_READY=1；至少一個 runtime 與必要條件可用時，optional runtime 或 user service 缺項只警告，不中止部署腳本；不呼叫模型、不保證登入或模型請求成功。確認 DIST-TBD-4；影響 DIST-006／007／014 的 readiness 與 exit code。
- **第 6 題來源**: 2026-10-07 使用者回答「1」，接受 installer 預設 latest stable、同版重跑重新驗證、--version 可顯式指定較舊正式版；失敗保留舊 binary，不修改設定或資料、不自動重啟 daemon；不保證資料格式向下相容。確認 DIST-TBD-12；影響 DIST-004 的版本選擇與降版行為。
- **第 7 題來源**: 2026-10-07 使用者回答「1」，接受 service stop／restart 最多等待 60 秒，逾時由 systemd 強制結束，長任務可能中斷；下次啟動標記 failed、不重跑。接受異常退出隔 5 秒重啟、60 秒內最多啟動 3 次，手動 stop 不自動重啟。確認 DIST-TBD-10 的停止期限與重啟政策；影響 DIST-012／013／017。
- **第 8 題來源**: 2026-10-09 使用者回答「1」，接受 doctor 只以目錄存在性、存取權限與 filesystem 唯讀狀態評估 config／data／socket 路徑可寫性，不建立測試檔、不開 SQLite；輸出標明「權限評估，未實際寫入」，無法判定時顯示限制；runtime 探測暫存只放私有 temp 並清理。確認 DIST-TBD-15 的可寫性檢查部分；影響 DIST-006／008／010，其餘 secret、deadline 與 path 失敗政策仍待確認。
- **第 9 題來源**: 2026-10-09 使用者回答「1」，接受新增 `asura version`（人類輸出）與 `--json`（version／commit／os／arch），Release 由 CI 以 ldflags 注入 tag／commit，source build 明示 dev／unknown；installer 只支援含 version metadata 的 Release（v1.1.0 起），`--version v1.0.0` 明確拒絕並提示手動下載。確認 DIST-TBD-3；影響 DIST-001／002／004／016。
- **第 10 題來源**: 2026-10-09 使用者回答「1」，接受 installer 入口為 `https://raw.githubusercontent.com/raybird/Asura/main/install.sh`，以 `curl -fsSL <入口> | sh -s -- [--version vX.Y.Z] [--install-dir PATH]` 執行；預設 `~/.local/bin`，不改 shell rc，不在 PATH 時只提示；未指定版本經 GitHub `releases/latest` 取 latest stable，`--version` 可顯式指定 prerelease；沿用 `asura_<tag>_linux_<arch>.tar.gz`／`asura_<tag>_checksums.txt` 與 archive root，x86_64→amd64、aarch64／arm64→arm64。確認 DIST-TBD-1／14；影響 DIST-001／002／005／016。
- **第 11 題來源**: 2026-10-09 使用者回答「1」，接受 installer 在 `<install-dir>/asura` 為 symlink、目錄或其他非一般檔案時拒絕並非零退出，保持原樣並提示移除或改用 `--install-dir`；共同替換流程為私有 temp 下載、唯一精確 manifest 項目、archive 路徑／型別檢查、同目錄 stage（0755）並執行 `version --json` 核對 tag／arch 後 rename，失敗清理暫存且保留舊 binary，不自動 rollback 或重啟 daemon。確認 DIST-TBD-2；DIST-004 新增「既有目標為 symlink 或非一般檔案」Example；影響 DIST-003／004。
- **第 12 題來源**: 2026-10-09 使用者回答「1」，接受 config 涵蓋 daemon 營運預設：db、socket、allowed_roots、workers、queue、runtime_priority、context 三項（沿用 contextwin 驗證）、memory_utility_policy；不納入 `--permission`、Web listen／origin／key 與每次任務 flag（backend／mode／workspace）。逐設定採 CLI > ENV > config > 內建預設，只比較該設定實際存在的來源；不新增 CLI flag 或 ENV；ENV 空值等同未設；CLI 顯式值（含空值／等於預設）一律勝出。確認 DIST-TBD-7；影響 DIST-009／010／011／013／017。
- **第 13 題來源**: 2026-10-09 使用者回答「1」，接受 doctor 一律以本機 registry 與 CaptureInventory 探測（同 `runtime inventory` 本機分支），availability／version／reason 沿用既有判定；adapter 另提供僅 doctor 讀取的 probe 實際使用 executable 路徑，無法提供時顯示 unknown，不以 PATH 猜測；RuntimeInventoryEntry、ExecutionContract 封存與 `runtime-inventory-v1` 不變；daemon 連線為獨立有界檢查。確認 DIST-TBD-5（唯讀強度已由第 8 題確認）；影響 DIST-006／007／008／017。
- **第 14 題來源**: 2026-10-09 使用者回答「1」，接受 binary 移除只提供文件步驟（先 `asura service uninstall`，再刪除 install-dir 中的 binary），文件列出 config／DB 位置並說明保留，不新增 `asura uninstall` 或資料清除指令；service uninstall 只移除 Asura 管理的 unit。確認 DIST-TBD-13；影響 DIST-015。
- **第 15 題來源**: 2026-10-09 使用者回答「1」，接受 setup 以 0700 建立 config／data／socket 目錄、0600 建立 config；載入既有 config 時，檔案非目前使用者擁有，或檔案／所在目錄可被 group／others 寫入，即拒絕載入並非零退出，錯誤指出路徑、問題與修正指令且不回顯內容；doctor 判 NOT_READY。確認 DIST-TBD-15 的 config 權限部分；DIST-010 新增對應 Example；影響 DIST-006／010／011。
- **第 16 題來源**: 2026-10-09 使用者回答「1」，接受移除 release.yml 的 `--clobber`，同名 asset 已存在時 workflow 失敗，重新發佈須維護者先手動刪除舊 asset；確保同一版本號恆對應同一 binary 與 checksum。確認 DIST-TBD-15 的 release asset 覆寫部分；DIST-016 新增對應 Then；第 9 題的 v1.0.0 拒絕同步增列為 DIST-004 Example。影響 DIST-004／016。
- **總結確認來源**: 2026-10-09 使用者回答「確認」，接受 DIST-TBD-15 其餘假設——相對路徑 XDG 變數依規範視為未設；socket 路徑超過 sun_path 上限（108 bytes）明確失敗並提示改用 `--socket`／config，doctor 判 NOT_READY；doctor 沿用 inventory 單 probe 3 秒／整體 5 秒、daemon 連線 2 秒、整體 10 秒且輸出有界，只輸出固定 reason，ENV／config 只顯示是否設定與路徑；config schema 無 secret 欄位，setup／service 不把 `ASURA_WEB_KEY` 或 API key 寫入 config／unit；unit `ExecStart` 用 binary 絕對路徑並依 systemd 規則跳脫、不經 shell，僅 setup 當下 `XDG_CONFIG_HOME` 非預設時加入 `Environment=XDG_CONFIG_HOME=...`；installer 需 `curl` 或 `wget`、`tar`、`sha256sum` 或 `shasum -a 256`、`uname`，缺任一項在變更 filesystem 前非零退出。同時核准 DIST-001～017 全部 Scenario。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| DIST-001 | 2026-10-09 | 已核准 |
| DIST-002 | 2026-10-09 | 已核准 |
| DIST-003 | 2026-10-09 | 已核准 |
| DIST-004 | 2026-10-09 | 已核准 |
| DIST-005 | 2026-10-09 | 已核准 |
| DIST-006 | 2026-10-09 | 已核准 |
| DIST-007 | 2026-10-09 | 已核准 |
| DIST-008 | 2026-10-09 | 已核准 |
| DIST-009 | 2026-10-09 | 已核准 |
| DIST-010 | 2026-10-09 | 已核准 |
| DIST-011 | 2026-10-09 | 已核准 |
| DIST-012 | 2026-10-09 | 已核准 |
| DIST-013 | 2026-10-09 | 已核准 |
| DIST-014 | 2026-10-09 | 已核准 |
| DIST-015 | 2026-10-09 | 已核准 |
| DIST-016 | 2026-10-09 | 已核准 |
| DIST-017 | 2026-10-09 | 已核准 |

## 待確認事項

方案集中於 technical-analysis；2026-10-09 全部項目已解決，Scenario 核准依上表。

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| DIST-TBD-1 | Installer URL、version pin、install-dir 介面 | 已解決 | 2026-10-09 使用者第 10 題回答「1」：入口為 main 分支 raw `install.sh`，固定 URL 便於文件與 CI fixture，installer 修正即時生效；安全性由 checksum 與 atomic replace 保障；`--version`／`--install-dir`，預設 `~/.local/bin`、latest stable。影響 DIST-001／002／005／016 |
| DIST-TBD-2 | Atomic replace 與 rollback 邊界 | 已解決 | 2026-10-09 使用者第 11 題回答「1」：驗證 checksum／archive 與 staged binary 版本後同目錄 rename；非一般檔案目標一律拒絕，避免默默換掉開發者 symlink 或寫出安裝目錄；成功後不自動 rollback、不重啟 daemon。影響 DIST-003／004 |
| DIST-TBD-3 | Version 與 build metadata contract | 已解決 | 2026-10-09 使用者第 9 題回答「1」：新增 `asura version`／`--json`（version／commit／os／arch），CI 注入 tag／commit，source build 顯示 dev／unknown；installer 最低支援 v1.1.0，v1.0.0 無 version metadata 故拒絕，確保每次安裝都能比對版本。影響 DIST-001／002／004／016 |
| DIST-TBD-4 | Doctor enum、exit code 與 readiness 能力範圍 | 已解決 | 2026-10-07 使用者第 5 題回答「1」：READY／DEGRADED exit 0、NOT_READY exit 1；必要條件與至少一個 runtime 可用時 optional／service 缺項只警告；不驗模型登入。影響 DIST-006／007／014 |
| DIST-TBD-5 | Inventory 共用與 executable 來源；唯讀檢查強度 | 已解決 | 2026-10-09 使用者第 13 題回答「1」：doctor 本機重用 registry／CaptureInventory；executable 經 adapter 旁路回報 probe 實際路徑，不足顯示 unknown；不改封存的 inventory／ExecutionContract，避免絕對路徑進入 audit 與 Phase 4 authority 變更；唯讀強度依第 8 題。影響 DIST-006／007／008／017 |
| DIST-TBD-6 | Config 格式、schema、unknown keys 與錯誤政策 | 已解決 | 2026-10-07 使用者第 2 題回答「1」：config.toml、schema_version = 1、嚴格解析；缺檔保留相容模式，壞檔明確失敗，使用成熟 parser。影響 DIST-009／010／011／012／017 |
| DIST-TBD-7 | Precedence、既有 ENV mapping 與新增 override | 已解決 | 2026-10-09 使用者第 12 題回答「1」：config 涵蓋 daemon 營運預設（含 priority／context／utility policy），因 systemd user service 不繼承 shell ENV；排除 permission、Web 與每次任務 flag；逐設定 CLI > ENV > config > built-in，不新增 flag／ENV。由既有程式推得：status／cancel／audit／inventory 有 config 時以 config socket 嘗試 daemon，連線失敗沿用既有本機 fallback 並讀 config db；無 config 維持舊行為。影響 DIST-009／010／011／013／017 |
| DIST-TBD-8 | XDG 路徑、無 config 相容模式與資料遷移 | 已解決 | 2026-10-07 使用者第 1 題回答「1」：無 config 保留各指令舊預設，setup 後使用 XDG config／data／runtime-or-state socket，不自動搬移或刪除舊資料；避免升級後讀不到既有 Task。影響 DIST-009／011／012／014／015／017 |
| DIST-TBD-9 | Setup 互動／非互動、既有 config 更新政策 | 已解決 | 2026-10-07 使用者第 4 題回答「1」：互動＋腳本模式；service 明確 opt-in；缺必要輸入或設定衝突明確失敗；重跑保留設定與資料，兼顧個人上手與腳本部署。影響 DIST-011／012／013 |
| DIST-TBD-10 | Unit 生成、更新、restart／shutdown policy | 已解決 | 2026-10-07 使用者第 7 題回答「1」：停止最多等待 60 秒，逾時 systemd 回收；異常退出隔 5 秒重啟，60 秒內最多啟動 3 次；不 retry Task，避免停止卡住。Unit 接線依已確認的 config／service 介面，保守保留非受管理 unit。影響 DIST-012／013／017 |
| DIST-TBD-11 | Service CLI 或 setup-managed surface | 已解決 | 2026-10-07 使用者第 3 題回答「1」：新增 asura service 六個管理子命令，由 systemd 管理，uninstall 保留設定與資料；操作集中且可測試。影響 DIST-013／014／015 |
| DIST-TBD-12 | Reinstall、升級、顯式降版政策 | 已解決 | 2026-10-07 使用者第 6 題回答「1」：latest stable 預設、同版重新驗證、--version 允許顯式降版；保留設定／資料，失敗保留舊 binary，不自動重啟 daemon；可自行回退 binary，但不保證資料向下相容。影響 DIST-004 |
| DIST-TBD-13 | Uninstall 與資料保留 | 已解決 | 2026-10-09 使用者第 14 題回答「1」：binary 由使用者依文件手動刪除，service uninstall 只處理受管理 unit，不提供 data purge；需求只要求保留資料，不新增會刪除自身 binary 的指令與其邊界測試。影響 DIST-015 |
| DIST-TBD-14 | Release asset naming 與舊 release metadata | 已解決 | 2026-10-09 第 10 題一併確認：沿用現有 asset 命名與 archive root，不改 release 命名；舊 release metadata 依第 9 題不支援 v1.0.0。影響 DIST-001／002／016 |
| DIST-TBD-15 | Secret／唯讀／filesystem／service 安全邊界 | 已解決 | 2026-10-09 第 8 題：doctor 可寫性只做權限評估、不實際寫入；第 15 題：不安全 config 權限拒絕載入；第 16 題：release asset 不可覆寫；總結確認：相對 XDG 視為未設、socket 長度上限失敗、doctor deadline／輸出上限與不回顯值、config／unit 無 secret、unit 路徑跳脫與 XDG_CONFIG_HOME 固定、installer 工具前置檢查。影響 DIST-003／004／006／008／010／011／012／013／015／016 |

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-07 | 依 #45 建立現況分析、待核准驗收與計畫草案；提出 DIST-TBD-8 第 1 題 | Codex |
| 2026-10-07 | 使用者回答「1」，確認 DIST-TBD-8 相容模式與不自動搬移資料；其餘設計與完整劇本仍待核准 | 使用者 |
| 2026-10-07 | 使用者第 2 題回答「1」，確認 DIST-TBD-6 TOML 與嚴格解析政策 | 使用者 |
| 2026-10-07 | 使用者第 3 題回答「1」，確認 DIST-TBD-11 asura service 管理介面與移除時保留資料 | 使用者 |
| 2026-10-07 | 使用者第 4 題回答「1」，確認 DIST-TBD-9 setup 互動＋腳本模式與 service opt-in 政策 | 使用者 |
| 2026-10-07 | 使用者第 5 題回答「1」，確認 DIST-TBD-4 doctor 可用就成功的狀態與 exit code | 使用者 |
| 2026-10-07 | 使用者第 6 題回答「1」，確認 DIST-TBD-12 同版重新驗證與顯式降版政策 | 使用者 |
| 2026-10-07 | 使用者第 7 題回答「1」，確認 DIST-TBD-10 限時停止與有界 crash 重啟政策 | 使用者 |
| 2026-10-09 | 使用者第 8 題回答「1」，確認 DIST-TBD-15 doctor 可寫性只做權限評估、不實際寫入 | 使用者 |
| 2026-10-09 | 使用者第 9 題回答「1」，確認 DIST-TBD-3 version 介面與 installer 最低支援 v1.1.0 | 使用者 |
| 2026-10-09 | 使用者第 10 題回答「1」，確認 DIST-TBD-1 installer 入口與 DIST-TBD-14 asset 命名 | 使用者 |
| 2026-10-09 | 使用者第 11 題回答「1」，確認 DIST-TBD-2 替換流程與非一般檔案目標拒絕；DIST-004 增列對應 Example | 使用者 |
| 2026-10-09 | 使用者第 12 題回答「1」，確認 DIST-TBD-7 config 涵蓋範圍與逐設定 precedence | 使用者 |
| 2026-10-09 | 使用者第 13 題回答「1」，確認 DIST-TBD-5 doctor 重用本機 inventory 與 executable 旁路 | 使用者 |
| 2026-10-09 | 使用者第 14 題回答「1」，確認 DIST-TBD-13 binary 依文件手動移除、不提供資料清除 | 使用者 |
| 2026-10-09 | 使用者第 15 題回答「1」，確認 DIST-TBD-15 不安全 config 權限拒絕載入；DIST-010 增列對應 Example | 使用者 |
| 2026-10-09 | 使用者第 16 題回答「1」，確認 release asset 不可覆寫；DIST-016 增列對應 Then，DIST-004 增列 v1.0.0 拒絕 Example | 使用者 |
| 2026-10-09 | 使用者回答「確認」，接受 DIST-TBD-15 其餘假設並核准 DIST-001～017 | 使用者 |

## 建檔檢查證據

2026-10-07 建檔時從 repo 根目錄執行靜態檢查，exit 0。2026-10-09 核准後改為檢查核准表與 15 個已解決事項，從 repo 根目錄執行下列腳本 exit 0：4 份文件、17 個唯一 Scenario 與已核准表完全一致、15 個待決事項皆已解決、4 個本機文件連結存在。另執行 `git diff --check`，exit 0；新文件尚未追蹤，該命令不涵蓋新文件，以下腳本另檢查其尾端空白。純規格文件不適用程式紅綠測試；尚未執行產品測試。

```bash
python3 - <<'PY'
from pathlib import Path
import re
root = Path('docs/issues/issue-45')
files = list(root.glob('*.md'))
assert len(files) == 4
body = (root / 'README.md').read_text().split('## 建檔檢查證據')[0]
ids = re.findall(r'^  @(DIST-\d{3})$', body, re.M)
table = re.findall(r'^\| (DIST-\d{3}) \| 2026-10-09 \| 已核准 \|$', body, re.M)
assert len(ids) == len(set(ids)) == len(table) == 17
assert set(ids) == set(table)
assert len(re.findall(r'^\| DIST-TBD-\d+ \|', body, re.M)) == len(re.findall(r'^\| DIST-TBD-\d+ \| [^|]+ \| 已解決 \|', body, re.M)) == 15
links = 0
for file in files:
    text = file.read_text()
    assert all(line == line.rstrip() or (file.name == 'README.md' and line.endswith('  ') and not line.endswith('   ')) for line in text.splitlines())
    for target in re.findall(r'\]\(([^)]+)\)', text):
        if '://' in target:
            continue
        path = target.split('#')[0]
        assert not path or (file.parent / path).exists()
        links += 1
assert links == 4
print('PASS: documents, scenarios, pending decisions, local links, whitespace')
PY
```

---

**建立日期**: 2026-10-07  
**分級**: Large — 跨 CLI、operator config、installer、release 與 user service  
**風險**: High — binary／資料保留與供應鏈安全  
**狀態**: 進行中；2026-10-09 Task 0.1／0.2／1.1／1.2／2.1／2.3／3.1／3.2／4.1／4.2／4.3 完成；2.2 待 arm64 執行環境；下一步 Task 5.1
