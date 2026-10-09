# Asura (阿修羅)

> **Personal autonomous AI agent orchestrator and control plane**

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/badge/release-v1.1.0-success)](CHANGELOG.md)

Asura 是一個以 Go 撰寫的單一執行檔，在一台 Linux 主機上統一管理 AI coding agent（OpenCode、Codex、OMP、Antigravity）的**執行**：誰可以提交、在哪個 workspace、用哪個 runtime、能力是否相符、何時取消，以及事後如何稽核。skills、MCP、workflow 與 domain logic 由各 runtime 自行處理。

- **一個 daemon 管全部**：CLI、Web Console、Cron 共用同一套任務佇列、容量、授權與取消；SQLite 單一狀態引擎。
- **可靠回收**：每個任務一個進程組（PGID），取消時整棵進程樹回收，防 PID 重用誤殺。
- **兩種管線**：`direct`（單一 Striker）或 `three-face`（Oracle 規劃 → Striker 執行 → Critic 審查）。
- **Fail-closed**：能力不符在模型呼叫前拒絕；失敗不自動 retry、不自動切換 runtime。
- **專案記憶與稽核**：專案級記憶與有界召回；接受任務時封存執行契約，可事後追溯。

名稱隱喻「三面六臂」：三面是 Oracle／Striker／Critic 三個角色，六臂是向外的執行通道（CLI、daemon、記憶、agent 執行池等）。

## 安裝

只支援 Linux（進程組回收、`/proc` 身分驗證與 `bwrap` 隔離皆依賴 Linux）。v1.1.0 起可用 installer 安裝 amd64／arm64 正式版，下載後先核對 SHA-256 與版本才替換，不需 root：

```bash
curl -fsSL https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh
asura doctor    # 唯讀檢查主機與 runtime
asura setup     # 建立 config，可選擇啟用 systemd user service
```

v1.0.0 沒有版本資訊，請從 [Releases](https://github.com/raybird/Asura/releases) 手動下載 tar.gz 並以 checksum 檔核對。安裝、設定、service 與移除細節見 [安裝與設定](docs/guides/setup.md)。

或自行編譯（Go 1.26+）：

```bash
git clone https://github.com/raybird/Asura.git && cd Asura
go build -o bin/asura ./cmd/asura
```

另需至少一個已登入的 runtime CLI。Asura 只接受固定版本，版本不符會在模型呼叫前拒絕：

| Runtime | 固定版本 | 備註 |
|---|---|---|
| `opencode`（預設） | OpenCode 1.18.23 | 可擔任所有 stage |
| `codex` | Codex CLI 0.155.0 | 可擔任所有 stage |
| `omp` | OMP CLI 18.4.4、Bun 1.4.2、`openai-codex/gpt-6-luna` | 需 `bwrap`；只能擔任 Striker |
| `antigravity` | Antigravity CLI 1.2.13、`gemini-3.8-flash-low` | 需 `bwrap`；只能擔任 Striker |

細節見 [Runtime 與執行控制](docs/guides/runtimes.md)。

## 五分鐘上手

```bash
# 1. 啟動 daemon，只允許指定專案目錄
./bin/asura daemon --socket /tmp/asura.sock --allowed-roots /path/to/project

# 2. 提交任務（立即回傳 Task ID；加 --wait 等待結果）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project "修復單元測試"

# 規劃 → 執行 → 審查三段式；--backend auto 依能力自動挑 runtime
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project \
  --mode three-face --backend auto "重構認證模組並補齊測試"

# 3. 查狀態、稽核、取消
./bin/asura status <task-id>
./bin/asura audit --id <task-id> --socket /tmp/asura.sock
./bin/asura cancel <task-id>
```

想用瀏覽器操作，啟動 Web Console（預設只聽 loopback）：

```bash
ASURA_WEB_KEY="your-secret-key" ./bin/asura web --listen 127.0.0.1:8080 --socket /tmp/asura.sock
```

## 指令一覽

| 指令 | 用途 | 說明文件 |
|---|---|---|
| `daemon` | 常駐執行中樞，集中授權、排程與容量 | [CLI 參考](docs/guides/cli-reference.md#daemon) |
| `ask` | 經 daemon 提交 AI 任務 | [CLI 參考](docs/guides/cli-reference.md#ask) |
| `run` | 本機直接執行並追蹤任務 | [CLI 參考](docs/guides/cli-reference.md#run) |
| `status` / `audit` | 查任務狀態、管線歷程與執行來源 | [CLI 參考](docs/guides/cli-reference.md#status)、[執行稽核](docs/guides/runtimes.md#執行稽核) |
| `cancel` | 取消任務並回收進程樹 | [CLI 參考](docs/guides/cli-reference.md#cancel) |
| `runtime inventory` | 查詢目前可用的 runtime | [Runtime 與執行控制](docs/guides/runtimes.md#可用性清冊) |
| `cron` | 定時排程 | [CLI 參考](docs/guides/cli-reference.md#cron) |
| `memory` | 專案記憶、Git 候選、效用與版本取代 | [專案記憶](docs/guides/memory.md) |
| `web` | 單人 Web Console | [Web Console](docs/guides/web-console.md) |
| `version` | 顯示版本、commit 與平台（`--json` 供腳本使用） | [安裝與設定](docs/guides/setup.md#安裝) |
| `doctor` | 唯讀檢查主機、config 與 runtime 是否就緒 | [安裝與設定](docs/guides/setup.md#診斷asura-doctor) |
| `setup` | 建立 config 與資料目錄，可選擇啟用 user service | [安裝與設定](docs/guides/setup.md#初始設定asura-setup) |
| `service` | 管理 systemd user service | [安裝與設定](docs/guides/setup.md#user-serviceasura-service) |

## 版本範圍

2026-10-06：`v1.0.0` 將 main（Issue #1～#41）定為第一版基準。2026-10-10：`v1.1.0` 新增安裝與初始設定（Issue #45）。

| 範圍 | 內容 |
|---|---|
| 執行中樞 | CLI 與 Daemon 共用任務、容量與取消契約；PGID 進程樹回收；SQLite 單一狀態引擎 |
| 入口 | 本機 CLI、單人 Web Console、Cron 排程 |
| Runtime | OpenCode、Codex、OMP、Antigravity 四個 adapter；能力感知 admission、`auto` 選擇、stage 級綁定、可用性清冊 |
| 管線 | `direct`（單一 Striker）與 `three-face`（Oracle → Striker → Critic） |
| 記憶 | 專案級記憶、有界詞彙召回、Git 候選審核、效用回饋、版本取代 |
| 稽核 | 接受時封存契約，`status`／`audit`／Web 唯讀查詢執行來源 |
| 安裝與設定（v1.1） | checksum 與版本核對的 installer、`version`、唯讀 `doctor`、`setup`、`config.toml` operator defaults、systemd user service |

**目前不包含**：Telegram 等其他遠端入口、自動 failover 或 retry、模型內部 Context 編輯／壓縮、語意（向量）召回、多使用者或跨主機部署，以及系統層級的效能與長期穩定性量測門檻（架構決策 D-09 未定義）。`--allowed-roots` 是入口授權政策，不是作業系統沙箱。完整異動見 [CHANGELOG](CHANGELOG.md)。

## 文件

- 使用指南：[安裝與設定](docs/guides/setup.md)、[CLI 參考](docs/guides/cli-reference.md)、[Runtime 與執行控制](docs/guides/runtimes.md)、[專案記憶](docs/guides/memory.md)、[Web Console](docs/guides/web-console.md)
- 開發者：[開發指南](docs/development.md)（目錄結構、開發流程、issue 索引）、[架構設計](docs/superpowers/specs/2026-09-11-asura-architecture-design.md)
- AI 開發助理規範：[AGENTS.md](AGENTS.md)、[docs/AGENTS.md](docs/AGENTS.md)

## 授權

[MIT](LICENSE)

*最新更新日期：2026-10-10（v1.1.0 發佈）*
