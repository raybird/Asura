# 修訂紀錄 (Changelog)

本文件用於紀錄 Asura 專案的發布版本歷史、異動詳情與基礎設施變更。格式遵循 [Keep a Changelog](https://keepachangelog.com/)。

## [Unreleased]

### 🚀 新增功能 (Features)
- **[installer]**：`install.sh` 安裝正式 Release：核對唯一 checksum 項目、archive 內容與暫存 binary 回報的版本後才替換；任何失敗都保留原 binary；支援 `--version`／`--install-dir`，不需 root (#45)
- **[cli]**：`asura version`（`--json`）、唯讀 `asura doctor`、`asura setup`、`asura service install|start|stop|restart|status|uninstall` (#45)
- **[config]**：`config.toml`（`schema_version = 1`）提供 operator defaults，逐設定採 CLI > ENV > config > 內建預設；嚴格驗證、不安全權限拒絕載入；有 config 時改用 XDG 路徑 (#45)

### ⚙️ 建置與發佈
- **[ci]**：新增 Release workflow，推送 `v*` tag 或手動指定 tag 時，編譯 Linux amd64／arm64 靜態執行檔並附上 checksum 上傳到 GitHub Release
- **[ci]**：Release 建置注入 tag／commit 供 `asura version` 回報；已發佈 asset 不再覆寫 (#45)

### 🔄 相容性
- 沒有 config 檔時，所有指令維持 v1.0 的 DB／socket 預設與本機 fallback；建立 config 後不自動搬移既有 DB (#45)

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
