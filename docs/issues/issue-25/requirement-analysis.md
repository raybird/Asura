# Issue 25 - 需求分析

## 需求與目標

2026-09-29 依 [issue #25](https://github.com/raybird/Asura/issues/25) 調查，規格與範圍以 [README](README.md) 為準。加入兩個可替換執行 adapter，以共同 contract 承載新 CLI；不將 Asura 語意移交外部 agent。

## 現況分析

基線 `19cb3d40e3826e6e1754fbd1848cca9e3725cc60`：

- `backend.Backend` 已提供 ID、Version、Capabilities、HasCapability、Run；ExecutionResult 帶 BackendID、BackendVersion、PartialOutput、FailureClass、Events 及 Usage。無須預設重設介面。
- `backend.Registry` 為通用映射，Daemon 組裝 OpenCode role-routing backend，偵測到 Codex binary 才註冊 Codex。新增具體 adapter 的接線位於 composition root。
- Codex 固定版本為 0.155.0，Run 前驗版本，使用受控 argv、角色 sandbox 參數與 PGID callback。這只說明可沿用的程式形狀，不替新 CLI 證明相同能力。
- ask/run 目前以 opencode/codex 白名單驗證且預設 opencode；CLI 擴充需要同步 daemon registry，不能只改 help。
- Pipeline Stage 0 已依共同 capability 檢查 workspace write，Three-Face 另要求 read-only。StageTrace 有 BackendID／BackendVersion；status 尚未印出兩者。
- Daemon fingerprint 納入 backend；ExecutionContext 與持久化 Task 保存 Backend，queue wake 讀原 Task.Backend，空值相容既有 opencode。需用新 backend 參數化重啟、redelivery 與 queue 測試，不能以欄位存在代替驗收。
- Context catalog 對未知 model 回傳 unknown／0；新 backend 的模型設定與 admission 需要一致，不能推測容量。Memory selection、recall 與 Git-approved capture 沿用 Asura 路徑。

## 問題點

兩個新 CLI 的實際安全及事件契約尚缺證據；共同介面存在不代表第三、第四後端已符合。版本、模型與 provider 動態 default 可能使已封存的任務在執行時漂移。status 顯示也有明確缺口。

## 外部來源與調查限制

2026-09-29 檢視 [OMP 官方 repo](https://github.com/can1357/oh-my-pi) 與 [Antigravity CLI 官方 repo](https://github.com/google-antigravity/antigravity-cli)，僅用於確認來源入口，未以 README 宣告 capability。

本機 `command -v omp agy` 只找到 `/home/kevin/.local/bin/agy`；`timeout 10s agy --version` exit 0，輸出 `1.2.12`；`timeout 10s agy --help` exit 0，列出 `--print`、`--output-format`、`--mode`、`--sandbox`、conversation/continue 等參數。沒有執行模型或登入探測，也未安裝 OMP。plan 或 sandbox 參數名稱不足以證明唯讀與取消安全。

本文件因需求現況需讀程式才能確認而建立；所有待決項集中 README。
