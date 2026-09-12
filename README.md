# Asura (阿修羅)

> **Personal autonomous AI agent orchestrator and control plane**

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Asura（阿修羅）** 是一套以 Go 語言打造的個人自主 AI 控制平面與輕量編排中樞（Personal Autonomous AI Control Plane & Orchestrator）。

其核心神話隱喻為「三面六臂、非天非魔、極致戰力」：
- **三面（Three Faces）**：由 **智者之面 (Oracle)**、**執行之面 (Striker)** 與 **審查之面 (Critic)** 組成的動態協同管線，告別單一 Prompt 的混亂負擔。
- **六臂（Six Execution Arms）**：向外伸展的通訊與執行通道（本機 CLI、常駐守護進程、記憶中樞、Agent 執行池等）。
- **單一中樞守護（Unified Daemon）**：以 Go 語言編譯為單一靜態二進位檔案，徹底終結多進程對 SQLite 的檔案鎖與 Migration 競爭。

---

## 核心特色 (Key Features)

- 🚀 **極致輕量守護**：以 Go 靜態編譯單一二進位檔案（`asura`），具備常駐低資源消耗與高啟動速度。
- 🛡️ **POSIX 進程樹隔離與精確回收**：
  - 獨立進程組（PGID）管理，取消任務時直達整組進程樹強殺（SIGKILL）。
  - 不可變核心時間戳記（`StartTime`）身分驗證，徹底防禦 Linux PID 重用誤殺。
  - 三態進程組身分合約：組內孤兒進程生命週期追蹤，杜絕假成功與殭屍進程殘留。
- 🔒 **跨行程平坦排他鎖與狀態引擎**：
  - 啟動區間平坦排他鎖（`.task-<hash>.lock`），徹底消除啟動與取消間的競態。
  - 單一 SQLite 狀態引擎，統一管理任務佇列、執行狀態、日誌與追蹤資料。
- 🧩 **後端契約與異常防禦**：
  - 抽象化 Runner 契約，無縫對接 OpenCode、Claude 等執行環境。
  - 內建 429 速率限制 1.1 秒快速失敗、410 模型下架精確攔截、空輸出防禦與部分失敗保護。

---

## 快速開始 (Quick Start)

### 1. 編譯建置

需求環境：Go 1.22+，Linux 環境。

```bash
# 複製專案
git clone https://github.com/raybird/Asura.git
cd Asura

# 建置單一二進位檔案
go build -o bin/asura ./cmd/asura
```

### 2. 常用 CLI 指令

#### 提交並執行任務 (run)
```bash
# 本機執行指令並追蹤任務
./bin/asura run \
  --workspace /path/to/project \
  --allowed-roots /path/to/project \
  "echo 'Hello Asura'"
```

#### 查詢任務狀態 (status)
```bash
./bin/asura status <task-id>
```

#### 安全取消任務 (cancel)
```bash
# 立即終止任務進程組並確保孤兒進程完全回收
./bin/asura cancel <task-id>
```

---

## 架構設計與議題文件

- **系統架構設計規範**：請參閱 [架構設計文件](docs/superpowers/specs/2026-09-11-asura-architecture-design.md)，涵蓋信任邊界、排程拓撲、進程模型與 D-01 至 D-09 技術決策。
- **議題追蹤與驗收劇本**：請參閱 [Issue 1 本機執行核心](docs/issues/issue-1/README.md) 與 [實作計畫](docs/issues/issue-1/implementation-plan.md)。

---

## 基礎設施與 AI 開發規範

本專案之 AI 開發規範、文件體系與工作流程遵循 [`dev-rules-kit`](../dev-rules-kit) 標準架構：

- **核心行為準則**：請參見根目錄 [AGENTS.md](AGENTS.md)，涵蓋「編寫前先思考」、「簡單優先」、「手術式修改」、「目標驅動執行與驗證」及「BDD + TDD 雙迴圈」等原則。
- **Claude Code 導覽**：請參見根目錄 [CLAUDE.md](CLAUDE.md)。
- **文件規範與開發閉環**：請參見 [docs/AGENTS.md](docs/AGENTS.md)，詳細規範 issue 的動態分級（Small / Medium / Large）、驗收標準形式（輕量驗收條件 vs Gherkin 劇本）與紅綠重構證據要求。

### 目錄結構

```
Asura/
├── .gitignore             # Git 忽略設定（排除本地資料庫與暫存檔）
├── README.md              # 專案說明文件
├── CHANGELOG.md           # 變更修訂紀錄
├── AGENTS.md              # 跨平台 AI 核心開發規則
├── CLAUDE.md              # Claude Code 進入點與導覽
├── cmd/
│   └── asura/             # CLI 程式進入點 (run, cancel, status, version)
├── internal/
│   ├── auth/              # 工作目錄與授權政策 (Policy)
│   ├── backend/           # 執行後端整合 (opencode process, parser)
│   ├── daemon/            # 守護進程服務中樞
│   ├── engine/            # 任務狀態引擎 (Engine, Worker Pool)
│   └── storage/           # SQLite 持久化儲存層
├── tests/
│   └── integration/       # 端到端整合測試 (SCN 驗收與安全覆核)
└── docs/                  # 專案技術文件與議題紀錄
    ├── AGENTS.md          # docs/ 目錄規範與開發閉環指引
    ├── superpowers/       # 架構設計規格書 (Architecture Spec)
    └── issues/            # 議題閉環追蹤 (Issue 1 ~ N)
```

### 開發工作流程

日常開發遵循以 Issue 為中心的開發閉環：

```
new-issue ──► decompose (僅 Large) ──► execute-task (BDD + TDD) ──► create-commit ──► create-pr ──► review
```

可直接搭配全域工作流程 `/dev-cycle {ID}` 或各階段專屬技能推進。

---

*最新更新日期：2026-09-12*

