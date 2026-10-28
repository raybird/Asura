# 修訂紀錄 (Changelog)

本文件用於紀錄 Asura 專案的發布版本歷史、異動詳情與基礎設施變更。格式遵循 [Keep a Changelog](https://keepachangelog.com/)。

## [Unreleased]

### 🚀 新增功能 (Features)
- **[runtime]**：新增 `asura runtime verify <runtime>`。對已安裝的 runtime 版本實際執行 probe（輸出、workspace 邊界、唯讀或憑證邊界、取消；Codex 另以 `codex sandbox` 不經模型驗證唯讀沙箱），全部通過才把身分（runtime、版本、SHA-256）記到 `$XDG_DATA_HOME/asura/runtime-verification.json`，之後該版本即可使用；`--list` 列出內建身分與紀錄，`--remove` 移除紀錄，內建身分不能移除 (#73)

### 🔄 相容性
- **[runtime]**：四個 runtime 只接受內建已驗證的身分（OpenCode 1.18.23、Codex 0.155.0、OMP 18.4.4、Antigravity 1.2.13），或驗證紀錄中身分相符的安裝。其他版本判為新的 reason `version_unverified`，在模型呼叫前拒絕；`doctor` 與拒絕訊息提示 `asura runtime verify <runtime>`。daemon、`runtime inventory`、`doctor`、本機 `run` 使用同一規則：OpenCode 原本在本機 `run`、`doctor` 與 `runtime inventory` 接受任何 1.18.x（daemon 只接受 1.18.23），現在未驗證的 1.18.x 一律拒絕。清冊 policy 維持 `inventory-v1`，升級前封存的排隊任務與 cron 不受影響 (#73)

### 🐛 錯誤修正 (Bug Fixes)
- **[runtime]**：取消任務後，Codex 與 OpenCode 由模型啟動的命令會繼續執行（Codex 的命令跑在另開 process group 的 PID namespace 裡，OpenCode 的 bash 工具另開 session），內建版本也一樣。現在所有取消路徑（行程內取消、`asura cancel`、engine 取消）都會凍結並回收 runtime process group 成員的整棵子孫樹。`runtime verify` 的 cancellation probe 改為同時留下另開 session 的背景子行程，取消後必須一併消失 (#74)

### 🔒 安全性 (Security)
- **[installer]**：未指定 `--version` 而 `releases/latest` 解析到沒有發布者驗證的 v1.1.0 時，不論有沒有 `gh` 都在下載前拒絕安裝，避免把 latest 指回 v1.1.0 來繞過驗證；`--version v1.1.0` 仍照常安裝並警告 (#58)

### 📌 已知限制
- Codex 的唯讀沙箱檢查驗的是 `codex sandbox -P :read-only`。正式任務的 `codex exec -s read-only` 交給 OS 沙箱的權限設定，在 0.163.0 上已確認與它相同；這是 Codex 的內部行為，新版本需要重新確認 (#73, #74)
- OpenCode 的新版本（例如 1.18.31）目前過不了 `runtime verify`，因為寫入角色沒有 OS 沙箱（#75）；修好之前只能使用內建版本。Codex 0.163.0 已可通過驗證 (#73, #74)
- OpenCode 的命令如果在取消前就已脫離行程樹（例如 `cmd &` 之後 shell 先結束），取消時不會被回收；預定在 #75 以 PID namespace 補上 (#74)

## [v1.2.0] - 2026-10-16

**Publisher Verification**：installer 除了 SHA-256 checksum，也驗證 Release 檔案確實由本 repo 的 Release workflow 從該 tag 建置（GitHub artifact attestation）。v1.2.0 是第一個帶驗證檔的正式版。另修正取消任務時的狀態競態。執行核心、admission、stage 綁定、稽核與 config 的行為不變。

### 🚀 新增功能 (Features)
- **[installer]**：驗證 Release 發布者身分：有 `gh` 2.68.0 以上時以 GitHub artifact attestation 驗證 archive（鎖定 `release.yml` 與該 tag），失敗或缺驗證檔即拒絕安裝；沒有 `gh` 時警告後只核對 checksum；新增 `--require-attestation`。v1.1.0 沒有驗證檔，安裝時警告 (#47)

### 🐛 錯誤修正 (Bug Fixes)
- **[runtime]**：local auto 執行路徑中，取消請求剛好落在 runtime 登記 PGID 與檢查取消意圖之間時，任務會被誤記為 failed、`cancel` 回報「task is not running」；改為先標記 cancelling，再由取消協調器收斂為 cancelled (#48)

### ⚙️ 建置與發佈
- **[ci]**：Release workflow 為 archive 產生 build provenance attestation 並上傳驗證檔；觸發 ref 不是該 tag 時在建置前失敗；含 `-` 的 tag 發佈為 prerelease (#47)
- **[test]**：修正 CI 負載下的偶發失敗：queue guard 測試等 workspace 實際釋放後再佔用 (#54)；execution audit 的並行 writer 測試改為每次 observation 有限次數的寫入 (#60)

### 📌 已知限制
- 發布者驗證需要 `gh` 2.68.0 以上，且要能連到 Sigstore（`tuf-repo-cdn.sigstore.dev`）；連不到視為驗證失敗、拒絕安裝。沒有 `gh` 時只核對 checksum 並警告。
- v1.1.0 沒有驗證檔，安裝 v1.1.0 時只核對 checksum 並警告；取消 v1.1.0 免驗由 #58 追蹤。
- `install.sh` 本身由 `main` 分支提供，不在發布者驗證的防護範圍內。

## [v1.1.0] - 2026-10-10

**Distribution & Bootstrap**：在 Linux 上從安裝到可用（Install → Verify → Doctor → Config → Setup → User Service → Ready），全程不需 root。執行核心、admission、stage 綁定與稽核的權威不變；沒有 config 時所有指令維持 v1.0 行為。

### 🚀 新增功能 (Features)
- **[installer]**：`install.sh` 安裝正式 Release：核對唯一 checksum 項目、archive 內容與暫存 binary 回報的版本後才替換；任何失敗都保留原 binary；支援 `--version`／`--install-dir`，不需 root (#45)
- **[cli]**：`asura version`（`--json`）、唯讀 `asura doctor`、`asura setup`、`asura service install|start|stop|restart|status|uninstall` (#45)
- **[config]**：`config.toml`（`schema_version = 1`）提供 operator defaults，逐設定採 CLI > ENV > config > 內建預設；嚴格驗證、不安全權限拒絕載入；有 config 時改用 XDG 路徑 (#45)

### ⚙️ 建置與發佈
- **[ci]**：新增 Release workflow，推送 `v*` tag 或手動指定 tag 時，編譯 Linux amd64／arm64 靜態執行檔並附上 checksum 上傳到 GitHub Release
- **[ci]**：Release 建置注入 tag／commit 供 `asura version` 回報；已發佈 asset 不再覆寫 (#45)

### 🔄 相容性
- 沒有 config 檔時，所有指令維持 v1.0 的 DB／socket 預設與本機 fallback；建立 config 後不自動搬移既有 DB (#45)

### 📌 已知限制
- installer 的 SHA-256 checksum 與 archive 來自同一個 GitHub Release，可偵測下載損壞與檔案不一致，但不證明發布者身分；簽章或 artifact attestation 另行追蹤 (#47)。
- installer 只支援含版本資訊的 Release（v1.1.0 起），v1.0.0 請手動下載。
- `asura doctor` 只檢查 runtime 是否存在與版本，不驗證登入、額度或模型回應；daemon socket 可連線不代表 daemon 身分與健康已驗證。
- user service 只支援 systemd user manager；不設定 linger，登出後是否持續執行依主機設定。

## [v1.0.0] - 2026-10-06

第一版基準：**Asura v1.0 Local Agent Runtime Control Plane baseline**。涵蓋 main 上 Issue #1～#41 的全部交付（程式碼截至 `db7967c`，其後僅有發佈文件整理）。v0.1.0 只有文件基礎設施，本版是第一個含程式碼的發佈，沒有相對於前版的不相容變更。

### 🚀 新增功能 (Features)

**執行核心與中樞**
- **[core]**：本機最小執行核心：任務狀態引擎、SQLite 持久化、PGID 進程樹回收、`StartTime` 身分驗證防 PID 重用、重啟後未完成任務明確標為中斷失敗 (#1)
- **[daemon]**：Daemon 非同步調度與 Unix socket IPC；CLI `run`／`ask`／`status`／`cancel` 經共同中樞提交、查詢、取消與重送；`--allowed-roots` 服務端授權 (#2)
- **[web]**：單人 Web 遠端入口：存取密鑰換 session、Host／Origin 驗證、結果分頁、斷線恢復查詢 (#4)
- **[cron]**：互動／背景容量政策、同專案寫入互斥、5 欄位 Cron 排程管理（重疊略過、漏跑不補、未授權自動暫停） (#6)

**記憶**
- **[memory]**：專案級記憶 CRUD 與精確 workspace 隔離、撤權保留 (#9)
- **[context]**：任務接受時封存 sealed execution context，重送沿用原快照 (#11)
- **[recall]**：`ask --memory-recall bounded` 有界詞彙召回（`lexical-v1`），結果與決策可稽核 (#19)
- **[web]**：Web Console 記憶管理與召回操作 (#21)
- **[gitmem]**：Git 變更感知的候選記憶擷取與人工審核（`memory capture`／`candidates`／`approve`／`discard`） (#23)；Web 版擷取與審核 (#27)
- **[recall]**：記憶效用回饋（UFL）與 `ASURA_MEMORY_UTILITY_POLICY=apply` 有界降權 (#29)
- **[memory]**：記憶生命週期、`--supersedes` 原子版本取代與 durable 標記 (#31)

**管線、後端與 Context**
- **[pipeline]**：`--mode three-face` 固定管線（Oracle → Striker → Critic），fail-closed 並保存各 stage trace (#13)
- **[backend]**：多後端基礎（OpenCode、Codex） (#15)；新增 OMP 與 Antigravity adapter 與 conformance 測試 (#25)
- **[contextwin]**：固定版本能力探測、stage-aware 上下文預估與 `ASURA_CONTEXT_LIMIT`／`ASURA_CONTEXT_POLICY` 准入控制 (#17)

**Runtime 控制平面**
- **[runtime]**：執行需求與共同 capability matcher，接受前預檢並封存 runtime 能力快照 (#33)
- **[runtime]**：`--backend auto` 能力感知選擇與 `ASURA_RUNTIME_PRIORITY` 可調優先序 (#35)
- **[runtime]**：Three-Face 逐 stage runtime 綁定與執行前防護 (#37)
- **[runtime]**：Runtime 可用性清冊（`asura runtime inventory`、Web 清冊頁），admission 使用 fresh inventory (#39)
- **[audit]**：執行來源稽核（`execution-audit-v1`）與唯讀查詢 `asura audit`、`GET /api/tasks/{id}/audit` (#41)

### ⚙️ 環境需求
- Go 1.26+、Linux。
- OMP 與 Antigravity adapter 需要 `bwrap`（PID／IPC namespace）；四個 runtime 皆以固定版本驗證，版本不符會在模型呼叫前拒絕。版本清單見 README。
- 可用環境變數：`ASURA_WEB_KEY`、`ASURA_CONTEXT_LIMIT`、`ASURA_CONTEXT_POLICY`、`ASURA_RUNTIME_PRIORITY`、`ASURA_MEMORY_UTILITY_POLICY`。

### 📌 已知限制
- 未提供：Telegram 等其他遠端入口、自動 failover／retry、模型內部 Context 編輯或壓縮、語意召回、多使用者部署。
- 系統層級的效能與長期穩定性門檻尚未定義與量測（架構決策 D-09）；目前只有記憶 lifecycle 的大資料量探測。
- `--allowed-roots` 是入口授權政策，不是作業系統沙箱。
- 執行稽核的 hash chain 只偵測不一致，不提供不可否認性。

## [v0.1.0] - 2026-09-11

### 📝 初始版本
- 專案初始化與 Git 版本庫建立
- 導入 `dev-rules-kit` 規範與文件基礎設施：
  - 建立專案層級與跨工具 AI 核心規範（`AGENTS.md`、`CLAUDE.md`）
  - 部署文件動態分級與開發閉環規範（`docs/AGENTS.md`、`docs/agents/`）
  - 部署標準架構、領域與修訂紀錄模板（`docs/_templates/`）
  - 設定 `.gitignore` 排除本地狀態與暫存檔
