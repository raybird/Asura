# 專案客製規範

本檔由專案維護，初始化只在不存在時建立，更新上游時保留。規劃與交付前讀取；未填內容從 repo 的設定、現有文件與命令取得，不為填表猜測。

## 專案邊界與驗證入口

單一 Go module（`go.mod`），CLI 入口 `cmd/asura`，各能力是 `internal/` 下的獨立 package，邊界見 `docs/development.md`「目錄結構」。

完整驗證以 `.github/workflows/ci.yml` 為準；本機最小回歸（2026-10-06 確認）：

```bash
go vet ./...
go test -count=1 -race -v ./...
go build ./cmd/asura
```

CI 另跑記憶 lifecycle 效能探測（`-tags lifecycleperf`）與 Web UI／瀏覽器探測（Node、Playwright 1.50.1）；改到 `internal/storage` 記憶 lifecycle 或 `internal/web` 時，依 ci.yml 一併執行對應步驟。

## 提交與交付

優先採專案提交規範；未定義時使用 `type(scope): 中文摘要`，issue 編號放 footer `Refs: #ID`。PR base 由現有 PR 或專案設定取得；無法確定才詢問。分支以 issue-ID 加描述為預設，可依既有慣例調整。

## 常青文件更新責任

用專案實際路徑建立「變更 → 文件」對照：部署拓撲／路由與依賴 → 架構文件；領域規則／實體關係 → 領域文件；部署／告警 → 操作文件；使用者功能 → 使用手冊。只列已有或此次確實需要建立的文件。

- CLI 命令參數與輸出（daemon, run, ask, status, cancel, audit, runtime, cron）→ `docs/guides/cli-reference.md`；新增或移除子命令時同步根目錄 README「指令一覽」
- Runtime adapter 版本、admission、auto 選擇、stage 綁定、清冊、稽核與上下文窗口 → `docs/guides/runtimes.md`；固定版本變動時同步 README「安裝」的 runtime 表
- Web Console 功能與 API → `docs/guides/web-console.md`
- 發佈版本 → `CHANGELOG.md` 新增版本節，README「v1.0 範圍」表隨之更新
- 專案記憶契約（基礎管理、Git 候選、有界召回、效用回饋、版本取代）與 `memory` 子命令 → `docs/guides/memory.md`
- 系統架構設計、排程拓撲、進程樹模型與技術決策 → `docs/superpowers/specs/2026-09-11-asura-architecture-design.md`

## 本地 gate 與案例

- **綠燈不等於有跑到**：runtime contract／live 測試在前置條件缺少時以 `t.Skip` 跳過，`go test` 仍回報 ok。前置條件為固定版本 runtime binary（OpenCode `1.18.23`；其他版本見 README）、模型登入憑證，以及可建立 user namespace 的 `bwrap`（Ubuntu 需 AppArmor 放行，作法見 ci.yml）。以 `-v` 執行並檢查 `SKIP` 行；改動觸及 `internal/backend` 時，把被跳過的測試名稱與原因寫進驗證證據，不計為通過。
