# 開發指南

給 Asura 貢獻者與 AI 開發助理的入口。建置、測試與本地 gate 見 [docs/agents/project.md](agents/project.md)。

## 架構

- **系統架構設計規範**：[架構設計文件](superpowers/specs/2026-09-11-asura-architecture-design.md)，涵蓋信任邊界、排程拓撲、進程模型與 D-01 至 D-09 技術決策。
- **單一中樞守護（Unified Daemon）**：以 Go 編譯為單一靜態二進位檔案，終結多進程對 SQLite 的檔案鎖與 Migration 競爭。

## 目錄結構

```
Asura/
├── .gitignore             # Git 忽略設定（排除本地資料庫與暫存檔）
├── .github/workflows/     # CI（vet、test、build；固定版本 OpenCode 與 bubblewrap）與 Release（推送 v* tag 時發佈 Linux 執行檔）
├── README.md              # 專案說明文件
├── CHANGELOG.md           # 變更修訂紀錄
├── LICENSE                # MIT 授權
├── AGENTS.md              # 跨平台 AI 核心開發規則
├── CLAUDE.md              # Claude Code 進入點與導覽
├── cmd/
│   └── asura/             # CLI 程式進入點 (daemon, run, ask, status, cancel, audit, runtime, web, cron, memory)
├── internal/
│   ├── audit/             # 執行稽核契約與查詢 (execution-audit-v1)
│   ├── auth/              # 工作目錄與授權政策 (Policy)
│   ├── backend/           # Runtime adapter (opencode, codex, omp, antigravity)、能力需求、選擇、綁定與清冊
│   ├── contextwin/        # 上下文窗口管理、預算、估算器與准入引擎
│   ├── cron/              # 定時排程器與日曆計算
│   ├── daemon/            # 守護進程服務中樞
│   ├── engine/            # 任務狀態引擎 (Engine, Worker Pool)
│   ├── gitmem/            # Git 變更感知與候選記憶擷取
│   ├── pipeline/          # Three-Face 管線核心 (Oracle, Striker, Critic, Orchestrator)
│   ├── recall/            # 有界記憶召回與效用調權
│   ├── storage/           # SQLite 持久化儲存層
│   └── web/               # 單人 Web 遠端入口 (SPA 控制台與同源 API)
├── scripts/               # 各 issue 的後端契約探測腳本
├── tests/
│   └── integration/       # 端到端整合測試 (SCN, CRON, MEM, TFACE, CTXWIN 驗收)
└── docs/                  # 專案技術文件與議題紀錄
    ├── AGENTS.md          # docs/ 目錄規範與開發閉環指引
    ├── development.md     # 本文件
    ├── agents/            # 驗收、驗證、審查規範與專案客製規範
    ├── guides/            # 使用指南（CLI、Runtime、記憶、Web Console）
    ├── _templates/        # 架構、領域與修訂紀錄模板
    ├── plans/             # 階段規劃歷史
    ├── superpowers/       # 架構設計規格書 (Architecture Spec)
    └── issues/            # 議題閉環追蹤 (Issue 1 ~ N)
```

## AI 開發規範與工作流程

本專案之 AI 開發規範、文件體系與工作流程遵循 [`dev-rules-kit`](https://github.com/raybird/dev-rules-kit) 標準架構：

- **核心行為準則**：根目錄 [AGENTS.md](../AGENTS.md)，涵蓋「編寫前先思考」、「簡單優先」、「手術式修改」、「目標驅動執行與驗證」及「BDD + TDD 雙迴圈」等原則。
- **Claude Code 導覽**：根目錄 [CLAUDE.md](../CLAUDE.md)。
- **文件規範與開發閉環**：[docs/AGENTS.md](AGENTS.md)，規範 issue 的動態分級（Small / Medium / Large）、驗收標準形式（輕量驗收條件 vs Gherkin 劇本）與紅綠重構證據要求。

日常開發遵循以 Issue 為中心的開發閉環：

```
new-issue ──► decompose (僅 Large) ──► execute-task (BDD + TDD) ──► create-commit ──► create-pr ──► review
```

可直接搭配全域工作流程 `/dev-cycle {ID}` 或各階段專屬技能推進。

## 議題索引

v1.0 涵蓋的全部 issue（議題追蹤與驗收劇本）：

- [Issue 1 本機執行核心與後端契約探測 (Phase 1)](issues/issue-1/README.md) 與 [實作計畫](issues/issue-1/implementation-plan.md)
- [Issue 2 本機 AI 執行路徑整合 (Phase 1.5)](issues/issue-2/README.md) 與 [實作計畫拆解](issues/issue-2/implementation-plan-decomposition.md)
- [Issue 4 單一 Web 遠端入口 (Phase 2A)](issues/issue-4/README.md) 與 [實作計畫拆解](issues/issue-4/implementation-plan-decomposition.md)
- [Issue 6 定時排程與容量調度 (Phase 2B)](issues/issue-6/README.md) 與 [實作計畫拆解](issues/issue-6/implementation-plan-decomposition.md)
- [Issue 9 專案級基礎記憶 (Phase 3A)](issues/issue-9/README.md) 與 [實作計畫](issues/issue-9/implementation-plan.md)
- [Issue 11 專案記憶與 AI 任務 Context 整合 (Phase 3B)](issues/issue-11/README.md) 與 [實作計畫](issues/issue-11/implementation-plan.md)
- [Issue 13 Three-Face Fixed Pipeline (Phase 3C)](issues/issue-13/README.md) 與 [實作計畫](issues/issue-13/implementation-plan.md)
- [Issue 15 Multi-Backend Foundation (Phase 3D)](issues/issue-15/README.md) 與 [實作計畫](issues/issue-15/implementation-plan.md)
- [Issue 17 Context Window Management Foundation (Phase 3E)](issues/issue-17/README.md) 與 [實作計畫](issues/issue-17/implementation-plan.md)
- [Issue 19 Bounded Automatic Recall (Phase 3F)](issues/issue-19/README.md) 與 [實作計畫](issues/issue-19/implementation-plan.md)
- [Issue 21 Web Console 專案記憶管理與召回操作 (Phase 3G)](issues/issue-21/README.md) 與 [實作計畫](issues/issue-21/implementation-plan.md)
- [Issue 23 Git-Aware Project Memory Capture (Phase 3H)](issues/issue-23/README.md) 與 [實作計畫](issues/issue-23/implementation-plan.md)
- [Issue 25 Backend Adapter Expansion & Conformance (Phase 3I)](issues/issue-25/README.md) 與 [實作計畫](issues/issue-25/implementation-plan.md)
- [Issue 27 Web Git Memory Capture & Review (Phase 3J)](issues/issue-27/README.md) 與 [實作計畫](issues/issue-27/implementation-plan.md)
- [Issue 29 Memory Utility Feedback Loop Foundation (Phase 3K)](issues/issue-29/README.md) 與 [實作計畫](issues/issue-29/implementation-plan.md)
- [Issue 31 Memory Lifecycle & Supersession Foundation (Phase 3L)](issues/issue-31/README.md) 與 [實作計畫](issues/issue-31/implementation-plan.md)
- [Issue 33 Runtime Capability & Execution Requirements (Phase 4A)](issues/issue-33/README.md) 與 [實作計畫](issues/issue-33/implementation-plan.md)
- [Issue 35 Capability-aware Runtime Selection (Phase 4B)](issues/issue-35/README.md) 與 [實作計畫](issues/issue-35/implementation-plan.md)
- [Issue 37 Stage-specific Runtime Binding (Phase 4C)](issues/issue-37/README.md) 與 [實作計畫](issues/issue-37/implementation-plan.md)
- [Issue 39 Runtime Inventory / Availability (Phase 4D)](issues/issue-39/README.md) 與 [實作計畫](issues/issue-39/implementation-plan.md)
- [Issue 41 Control Plane Execution Audit (Phase 4E)](issues/issue-41/README.md) 與 [實作計畫](issues/issue-41/implementation-plan.md)

階段規劃歷史：[本機核心合併後的階段規劃](plans/post-core-roadmap.md)（已由 v1.0 完成）
