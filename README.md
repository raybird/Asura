# Asura

Asura 專案。

## 基礎設施與規範

本專案之 AI 開發規範、文件體系與工作流程遵循 [`dev-rules-kit`](../dev-rules-kit) 標準架構：

- **核心行為準則**：請參見根目錄 [AGENTS.md](AGENTS.md)，涵蓋「編寫前先思考」、「簡單優先」、「手術式修改」、「目標驅動執行與驗證」及「BDD + TDD 雙迴圈」等原則。
- **Claude Code 導覽**：請參見根目錄 [CLAUDE.md](CLAUDE.md)。
- **文件規範與開發閉環**：請參見 [docs/AGENTS.md](docs/AGENTS.md)，詳細規範 issue 的動態分級（Small / Medium / Large）、驗收標準形式（輕量驗收條件 vs Gherkin 劇本）與紅綠重構證據要求。

## 目錄結構

```
Asura/
├── .gitignore             # Git 忽略設定（排除本地資料庫與暫存檔）
├── README.md              # 專案說明文件
├── CHANGELOG.md           # 變更修訂紀錄
├── AGENTS.md              # 跨平台 AI 核心開發規則
├── CLAUDE.md              # Claude Code 進入點與導覽
└── docs/                  # 專案技術文件與議題紀錄
    ├── AGENTS.md          # docs/ 目錄規範與開發閉環指引
    ├── agents/            # 動態分級與範本參考
    │   ├── document-types.md
    │   ├── issue-checklist.md
    │   └── readme-templates.md
    └── _templates/        # 規格與架構文件範本
        ├── architecture-template.md
        ├── changelog-template.md
        └── domain-template.md
```

## 開發工作流程

日常開發遵循以 Issue 為中心的開發閉環：

```
new-issue ──► decompose (僅 Large) ──► execute-task (BDD + TDD) ──► create-commit ──► create-pr ──► review
```

可直接搭配全域工作流程 `/dev-cycle {ID}` 或各階段專屬技能推進。

---

*建立日期：2026-09-11*
