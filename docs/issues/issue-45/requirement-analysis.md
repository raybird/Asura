# Issue 45 — 需求與現況分析

盤點日期：2026-10-07。原始碼基準：`611577b`。本文件因路徑、CLI fallback 與 inventory 行為必須調查才成立而建立。

## 需求與目標

[GitHub #45](https://github.com/raybird/Asura/issues/45) R1～R14 要求使用者無需 clone／Go，即可安裝、診斷、設定與常駐 Linux Asura。保留 Phase 4 執行權威，不引入 remote runtime 或新的 supervisor。驗收與最小修改邊界集中在 README。

## 現況分析

| 真相來源 | 2026-10-07 確認行為 | 對設計的限制 |
|---|---|---|
| `cmd/asura/config.go` | `ASURA_SOCKET` 非空優先，其次 `~/.asura/asura.sock`，無 HOME 才使用 temp 路徑；解析預設會 MkdirAll | Doctor 不能直接沿用有使用者目錄副作用的 helper |
| `cmd/asura/daemon.go` | DB `asura.db` 相對 cwd；workers=2、queue=10；無 allowed roots 使用 cwd | Service 不能依自己的 cwd 默默放寬授權或換 DB |
| `cmd/asura/run.go` | DB `asura.db`；無 allowed roots 使用 workspace；backend 預設 opencode | 無 config 時需維持 local execution 與授權預設 |
| `cmd/asura/ask.go`、Cron／memory／Web | 使用 DefaultSocketPath；ask backend 預設 opencode | 共用設定不得改變 request／sealed contract authority |
| `status.go`、`cancel.go`、`execution_audit.go` | socket 預設只有 ASURA_SOCKET；未連上 daemon 可 fallback 至本地 DB | 不能以統一 socket helper 掩蓋原本的本機模式 |
| `cmd/asura/inventory.go` | 有 socket 先問 daemon，失敗可本機 CaptureInventory；本機建立暫存隔離 OpenCode config | Doctor 重用 registry／probe，必要暫存資產須清理，不能改寫使用者設定 |
| `internal/backend/inventory.go` | 單 probe 3 秒、整體 5 秒；availability 與 reason 有界；失敗不回傳原始 stderr | 不另建 runtime availability 判定；目前 entry 不含 executable path |
| 各 adapter `inventory.go` | 驗固定版本、CLI layout／hash、Bun 或 bwrap 相依；不讀 token 或呼叫模型 | Availability 不等於已登入／有模型額度；不能宣稱 doctor 驗證登入 |
| OpenCode `inventory.go` | 有固定本機 probe cache binary fallback，之後才查 PATH | 單獨 exec.LookPath 並不能證明顯示的是實際 probe executable |
| `internal/contextwin/config.go` | `ASURA_CONTEXT_LIMIT`／`POLICY`／`RESERVE_RATIO`；嚴格驗證正數、observe/reject、0<ratio<1 | Config 必須重用驗證並維持原先 ENV 語意 |
| daemon／run | `ASURA_RUNTIME_PRIORITY`；daemon 有 `ASURA_MEMORY_UTILITY_POLICY` observe/apply | runtime priority 仍是 preference，不能覆寫 sealed execution contract |
| `cmd/asura/web.go` | `--key` 或 `ASURA_WEB_KEY` | 不把 secret 加入 config、unit 或診斷輸出 |
| `internal/daemon/daemon.go` Start | socket sidecar flock、檢查存活 daemon、移除 stale socket、啟動後恢復 interrupted tasks | Path parent 必須存在；setup／doctor 不可直接 Start 或開 store 以診斷 |
| `.github/workflows/release.yml` | tag checkout、CGO=0、linux amd64/arm64、沒有 version ldflags | Version surface 須補正式 build metadata；v1.0.0 無該指令 |

## Release 真實契約

2026-10-07 執行 `gh release view v1.0.0 --json tagName,isPrerelease,assets,url`：tag=`v1.0.0`、isPrerelease=false；實際 assets：

- `asura_v1.0.0_linux_amd64.tar.gz`
- `asura_v1.0.0_linux_arm64.tar.gz`
- `asura_v1.0.0_checksums.txt`

Archive root 為 `asura_<tag>_linux_<arch>/`，內含 `asura`、LICENSE、README、CHANGELOG。README 泛稱 `checksums.txt`，不是實際完整檔名。Installer 應依 release workflow 契約，不依 README 寫死版本。

## 問題與待調查邊界

1. 直接切換 XDG 會使無 flag 的既有資料查詢改讀另一個 DB；第 1 題先確認相容模式與不自動遷移政策。
2. Config merge 要辨識 flag 是否明確出現，不能用「值與 default 相同」判斷 override。
3. Doctor 若呼叫現有 DefaultSocketPath 或 SQLite constructor 會產生副作用，需以純 path resolution／metadata assessment 代替。
4. Inventory 的 executable 來源與 bwrap 需求不能另由 doctor 猜測；如需擴充來源資訊，僅補 adapter 接線，維持 authority。
5. 真實 systemd user lifecycle、arm64 執行及 Release 安裝證據與 stub fixture 分開取得，環境缺失不可記成通過。

## 盤點證據與限制

2026-10-07：`gh issue view 45 --json title,body,state,comments,url` 顯示 OPEN、comments=[]；工作區盤點前 clean，branch=`main`；沒有 `docs/issues/issue-45/`。PR 搜尋只有文字命中其他 issue 的已合併 PR，不能當作 #45 已交付依據。

本次為文件與程式碼盤點，未執行 installer、setup、runtime model call、systemd mutation 或程式測試。不把既有 release 存在視為新 installer 的驗收證據。
