# Issue 45 — 技術方案與待決設計

建立日期：2026-10-07。決策狀態與來源以 README 為準，未確認部分為建議草案。本文件因 XDG 相容模式與 service 介面有實質方案取捨而建立。

## 可行性與架構影響

沿用單一 Go binary、標準 flag parser、既有 registry／CaptureInventory 及 daemon。Installer 為 POSIX sh，僅使用正式 GitHub Release 的 archive／manifest。Config loader 不修改 process ENV，不替代 runtime admission 或重新解讀歷史 sealed Task。

## 方案選型與取捨

### DIST-TBD-8：XDG 與舊預設

方案 A（建議）：沒有 config 時保留每個指令的既有模式；setup 建 config 後才採 XDG。方案 B：所有未指定路徑直接換 XDG。A 多一個相容分支，但避免舊資料突然不可見；B 解析較單純，但升級會改變既有自動化的外部行為。

2026-10-07 決策：使用者對第 1 題回答「1」，採方案 A，不自動搬移或刪除舊資料；CLI／ENV 顯式路徑優先。

```diff
 無 CLI／ENV path override
- 一律切換至 XDG DB／socket（方案 B）
+ 無 config：沿用各命令 v1.0 的 DB／socket 與 local fallback（方案 A）
+ 有 config：config 值優先，缺省 path 使用 XDG
+ setup：新建設定，不搬移或刪除舊 DB
```

已確認的目錄策略：config 放 `$XDG_CONFIG_HOME/asura/config.toml`，fallback `~/.config/asura/config.toml`（2026-10-07 第 2 題固定檔案格式與名稱）；DB：`$XDG_DATA_HOME/asura/asura.db`，fallback `~/.local/share/asura/asura.db`；socket 優先 `$XDG_RUNTIME_DIR/asura/asura.sock`，無 runtime dir 時 `$XDG_STATE_HOME/asura/asura.sock`，fallback `~/.local/state/asura/asura.sock`。相對 XDG、不存在 runtime dir、權限／owner、socket path 長度的失敗政策由 DIST-TBD-15 固定，不由目錄策略答案推定核准。

### DIST-TBD-6：設定格式與嚴格解析

第 2 題方案 A（建議）：`config.toml`，包含 `schema_version = 1`；拒絕 unknown keys、不支援的 schema、錯誤 enum／path／數值；缺檔保留相容模式，存在但無法讀取或解析則明確失敗，不回顯原始設定值。TOML 適合人工編輯與註解，需使用成熟 parser。

方案 B：`config.json`，採相同嚴格解析政策；可用 Go 標準函式庫，省去 TOML 相依，但不支援註解。影響 DIST-009／010／011／012／017；兩者都不新增 secret 欄位。

2026-10-07 決策：使用者對第 2 題回答「1」，採方案 A；TOML／schema_version = 1／嚴格解析／缺檔與壞檔分流已確認。具體 parser 套件是實作選擇，不另當需求核准題。

### DIST-TBD-11：Service surface

方案 A（建議）：`asura service install/start/stop/restart/status/uninstall`，setup 可呼叫相同管理邏輯。方案 B：setup 只生成 unit，生命週期全由使用者執行 systemctl。A 讓 install／uninstall 範圍明確且便於測試，增加薄 CLI wrapper；B 較少產品介面，但診斷與卸載步驟分散。兩者 systemd 均是唯一 service authority。

2026-10-07 決策：使用者第 3 題回答「1」，採方案 A；影響 DIST-013／014／015。只管理 systemd user service，不建立自製 supervisor；移除 service 不刪 config／DB／Task／Memory。此題僅決定管理介面，不推定核准 unit restart／shutdown 或 setup 自動啟用策略。

### DIST-TBD-9：Setup 互動與非互動

2026-10-07 第 4 題方案 A（建議）：終端機執行 `asura setup` 可互動選擇 allowed roots 與是否啟用 user service；service 預設不啟用，明確同意才 install／enable／start。非互動環境以 `asura setup --allowed-roots /path/to/project` 建新設定，僅顯式 `--service` 才 install／enable／start；必要輸入不足直接失敗，不等待 stdin。利於個人上手且可用腳本部署。

方案 B：只提供非互動 flags，即使在終端機也必須指定參數，減少互動程式與測試分支；其他預設與安全政策相同。兩者都保留既有 config，重跑不覆寫自訂值或重置 DB／Task／Memory／credential，顯示 doctor 結果，不安裝／登入 runtime。影響 DIST-011／012／013；既有 config 與不同新參數衝突時，明確失敗並提示手動編輯，避免忽略使用者輸入或默默覆寫。

2026-10-07 決策：使用者第 4 題回答「1」，採方案 A；確認互動＋腳本模式、service 明確 opt-in 與既有設定／資料保留。

### DIST-TBD-4：Doctor 狀態與 exit code

2026-10-07 第 5 題方案 A（建議）：`READY` exit 0、`DEGRADED` exit 0、`NOT_READY` exit 1。必要 system／config 檢查通過且至少一個 runtime 可用才具備啟動條件；optional runtime 缺失／版本不符或 user service 不可用記為 DEGRADED，不將仍可 standalone 使用的主機判成不可用。CLI 使用錯誤 exit 2。

方案 B：相同 readiness 判定，但 DEGRADED exit 2，讓嚴格部署腳本把非致命缺項也視為非零結果；NOT_READY 維持 exit 1。兩者都顯示明確狀態／原因／可用能力，至少一個可用 Striker 只保證 direct 的本機條件，不保證 three-face、模型登入、額度或模型成功回應。影響 DIST-006／007／014；差異是部署腳本是否因 optional 缺項中止。

2026-10-07 決策：使用者第 5 題回答「1」，採方案 A，optional 缺項不使仍可使用的主機回傳非零。

### DIST-TBD-12：Installer 版本選擇與降版

2026-10-07 第 6 題方案 A（建議）：未指定版本取 latest stable；同版本重跑仍下載並重新驗證；`--version vX.Y.Z` 明確指定較舊的正式 Release 時允許降版。只有使用者顯式指定版本才可能執行降版；不改寫設定或資料、不自動重啟正在運作的 daemon。這讓使用者能自行回退 binary，但不承諾舊 binary 與未來資料格式的相容性。

方案 B：只允許同版重新安裝或升級，指定較舊版本直接拒絕；較少誤降版機會，但回退須手動操作。兩者下載／checksum／替換失敗均保留舊 binary，採驗證完成後 atomic replace，不提供背景更新。影響 DIST-004；無 version metadata 的舊 binary 不能靠解析脆弱人類輸出推測版本，方案 B 如需比對必須先固定可追溯版本來源。

2026-10-07 決策：使用者第 6 題回答「1」，採方案 A；預設 latest stable、同版重新驗證、顯式降版允許，不改資料／設定、不自動重啟 daemon。

### DIST-TBD-10：Service 停止期限與重啟

2026-10-07 查核 `Daemon.Close`／`WorkerPool.Close`：停止接收新連線後等待執行中與佇列中的任務排空，沒有時間上限。`RecoverInterruptedTasks` 將中斷的 running／pending／queued／cancelling Task 標記 failed，不會自動 retry。

第 7 題方案 A（建議）：user unit 設 `KillMode=mixed`、`TimeoutStopSec=60s`；先只向 daemon 發 SIGTERM 等待排空，逾時後由 systemd 強制結束 unit 剩餘進程。Stop／restart 不會無限卡住，但超過期限的任務可能被中斷；下一次啟動沿用現有 recovery 標記 failed，不重跑 Task。

方案 B：相同 KillMode，但 `TimeoutStopSec=infinity`，等待現有任務排空，不因固定停止期限中斷任務；stop／restart 可能長時間或無限等待。兩者皆建議 `Restart=on-failure`、`RestartSec=5s`、`StartLimitIntervalSec=60s`、`StartLimitBurst=3`，限制 crash 重啟；手動 stop 不自動重啟，不設定 linger、不改 daemon 的核心 drain／recovery 邏輯。影響 DIST-012／013／017。

2026-10-07 契約查證：[systemd 官方 KillMode 文件](https://raw.githubusercontent.com/systemd/systemd/main/man/systemd.kill.xml) 確認 mixed 對主進程先發 SIGTERM，期限到達或主進程結束後回收剩餘 control group；[systemd 官方 service 文件](https://raw.githubusercontent.com/systemd/systemd/main/man/systemd.service.xml) 為 TimeoutStopSec／Restart 契約來源。最終仍需真實 user bus 測試，不以文件查證當 lifecycle 測試通過。

2026-10-07 決策：使用者第 7 題回答「1」，採方案 A，接受 60 秒期限可能中斷長任務；固定有界 crash 重啟與既有 recovery、不自動 retry Task。

### DIST-TBD-15：Doctor 可寫性檢查的唯讀邊界

2026-10-07 第 8 題方案 A（建議）：doctor 只以目錄存在性、存取權限與 filesystem 唯讀狀態評估 config／data／socket 路徑，不在這些目錄建立測試檔、不開 SQLite。輸出明列「權限／metadata 評估，未實際寫入」，不把推估當成寫入驗證；不能判定時顯示 unknown／限制，不偽稱已證明可寫。Runtime 探測必需的隔離暫存資產仍可在私有 temp 建立並清理，禁止改寫使用者 config／DB／credential。

方案 B：允許在已存在的 config／data／socket 目錄建立隨機私有暫存檔、測試寫入後刪除，以較直接驗證當下可寫；不建立缺失的目標目錄、不修改既存檔案或 DB。可抓到部分權限推估漏掉的磁碟／quota 問題，但檢查有短暫 filesystem 副作用，與嚴格唯讀不同。兩者不保證診斷後 filesystem 永遠可寫；影響 DIST-006／008／010，選 B 需明確修訂 doctor 唯讀契約。

2026-10-09 決策：使用者第 8 題回答「1」，採方案 A；doctor 維持嚴格唯讀，可寫性只做權限／metadata 評估並標明限制。DIST-TBD-15 其餘 secret、deadline 與 path 失敗政策於 2026-10-09 第 15、16 題與總結確認定案，見下表。

## 其餘建議契約

| 決策 | 內容（2026-10-09 全部已確認） | 理由與決策來源 |
|---|---|---|
| DIST-TBD-1（已確認） | Repo 根目錄 `install.sh`，入口 `https://raw.githubusercontent.com/raybird/Asura/main/install.sh`；`sh -s -- --version vX.Y.Z --install-dir PATH`，未指定 version 取 latest stable；預設 `~/.local/bin` | 2026-10-09 第 10 題回答「1」：採 main raw 入口而非 release 附件，固定 URL 便於文件與 fixture，installer 修正即時生效；latest 經 GitHub `releases/latest`（排除 prerelease／draft），`--version` 可顯式指定 prerelease；不修改 shell rc，不在 PATH 只提示 |
| DIST-TBD-2（已確認） | 私有暫存下載，先核對唯一且精確匹配的 SHA-256 manifest 項目；驗 archive 路徑／檔案型別；將 binary stage 到目標目錄（0755）並執行 `version --json` 核對 tag／arch 後同 filesystem rename；install-dir 不存在則建立 | 下載／校驗／rename 失敗保留舊 binary；2026-10-09 第 11 題回答「1」：既有目標為 symlink／目錄／非一般檔案時拒絕並非零退出、保持原樣並提示移除或改用 `--install-dir`，不替換 symlink 本身、不跟隨寫入其目標；不提供執行後自動 rollback、不中斷／重啟正在運行的 daemon |
| DIST-TBD-12（已確認） | 同版重跑重新驗證；default 使用 latest stable；顯式 version pin 可降版；不自動重啟 daemon | 2026-10-07 第 6 題回答「1」；缺 asset／manifest、工具缺失、下載中斷均非零，不改資料／設定 |
| DIST-TBD-3（已確認） | `asura version` 與 `--json`；穩定 version／commit／os／arch 欄位；source build 明列 dev／unknown；Release 用 tag／commit ldflags；installer 最低支援 v1.1.0 | 2026-10-09 第 9 題回答「1」：v1.0.0 無 version 指令且 release.yml 未注入 metadata，`--version v1.0.0` 明確拒絕並提示手動下載，不採只驗 checksum 的例外路徑；CI 不解析人類輸出 |
| DIST-TBD-14（已確認） | 沿用 `asura_<tag>_linux_<arch>.tar.gz`、`asura_<tag>_checksums.txt` 與 archive root；uname x86_64→amd64、aarch64/arm64→arm64 | 2026-10-09 第 10 題一併確認；不改現有 asset naming；tag／binary version／平台／checksum 由獨立 fixture 核對。release.yml 上傳使用 `--clobber`，同 tag asset 可被覆寫，列入 DIST-TBD-15 安全邊界 |
| DIST-TBD-6（已確認） | TOML + `schema_version = 1`；拒絕 unknown keys、不支援 schema、錯誤 enum／path；缺檔與壞檔分開 | 2026-10-07 第 2 題回答「1」；使用成熟 TOML parser，schema 只管 operator defaults，不放 token |
| DIST-TBD-7（已確認） | 逐設定 CLI > ENV > config > built-in，只比較該設定實際存在的來源；config 欄位：db、socket、allowed_roots、workers、queue、runtime_priority、context limit／policy／reserve_ratio、memory_utility_policy；不新增 CLI flag／ENV；ENV 空值等同未設 | 2026-10-09 第 12 題回答「1」：systemd user service 不繼承 shell ENV，故納入 priority／context／utility policy；排除 `--permission`（安全政策覆寫）、Web listen／origin／key（含 secret 且非 daemon）與 backend／mode／workspace。flag.Visit 辨識顯式 flag；status／cancel／audit／inventory 有 config 時以 config socket 嘗試 daemon，連線失敗沿用既有本機 fallback 讀 config db |
| DIST-TBD-4（已確認） | enum `READY/DEGRADED/NOT_READY`；前兩者 exit 0、NOT_READY exit 1；單可用 Striker 可啟動 direct，不代表 three-face 可用 | 2026-10-07 第 5 題回答「1」；optional 缺項只警告，READY 不保證登入／網路／模型；CLI 使用錯誤 exit 2 為建議介面 |
| DIST-TBD-5（已確認） | 共用既有本機 registry 與 CaptureInventory；executable 由 adapter 另提供僅 doctor 讀取的 probe 實際路徑，不足顯示 unknown；能力重用 descriptor | 2026-10-09 第 13 題回答「1」：RuntimeInventoryEntry 封存於 ExecutionContract（requirements.go）並進入 audit／Web，加入絕對路徑需升 `runtime-inventory-v1` 並改 Phase 4 authority，故採旁路；不用 doctor 的 PATH lookup 建第二 truth；daemon 連線為獨立有界檢查 |
| DIST-TBD-9（已確認） | TTY 可逐步選 allowed root 與 service；非 TTY 建新設定須顯式 root，僅 --service 啟用 service；已有 config 不覆寫，參數衝突明確失敗 | 2026-10-07 第 4 題回答「1」；重跑不初始化 SQLite、登入、安裝 runtime 或暴露 Web；偵測舊 DB 時顯示指定舊 path 的指引 |
| DIST-TBD-10（已確認） | User unit 使用 binary 絕對路徑與正式 config；停止最多 60 秒，crash 隔 5 秒重啟、60 秒內最多啟動 3 次 | 2026-10-07 第 7 題回答「1」；systemd 控制生命週期，不設定 linger 或 retry Task；已有非 Asura 管理的 unit 不覆寫 |
| DIST-TBD-13（已確認） | Service uninstall 只停用／移除受管理 unit；binary 依文件手動移除；不提供 data purge | 2026-10-09 第 14 題回答「1」：不新增 `asura uninstall`，避免自刪 binary、daemon 使用中與 symlink 邊界；文件區分 binary、config、unit、DB／Task／Memory 位置；不新增未被要求的資料刪除入口 |
| DIST-TBD-15（已確認） | 不回顯 ENV／config 原始 secret 或任意 probe stderr；diagnostic 固定 reason、有界輸出與 deadline；doctor 不開 DB、不變更 user state | path writable 採唯讀 metadata/access 評估，報告限制；process／filesystem 探針只用隔離暫存並清理。若要求實際寫入 user dir 才判 writable，須先核准唯讀邊界。2026-10-09 第 15 題回答「1」：setup 以 0700 目錄／0600 config 建立；既有 config 非本人擁有或檔案／目錄 group／others 可寫即拒絕載入（類 OpenSSH），doctor 判 NOT_READY。2026-10-09 第 16 題回答「1」：release.yml 移除 `--clobber`，同名 asset 存在即失敗，重新發佈需手動刪除舊 asset。2026-10-09 總結確認：相對 XDG 視為未設；socket 超過 108 bytes 明確失敗；doctor daemon 連線 2 秒、整體 10 秒；config／unit 無 secret；unit ExecStart 依 systemd 規則跳脫，僅非預設 XDG_CONFIG_HOME 寫入 Environment=；installer 工具缺失在變更前失敗 |

## 尚需固定的實作細節

- Config schema 的確切欄位與新增 flags／ENV；不把既有不支援的 flag 當成已提供。
- Setup config／unit 寫入採不覆寫與 atomic publish，錯誤途中留下哪些目錄必須可診斷且可重跑。
- systemd ExecStart 路徑空白與特殊字元引用；不可經 shell 插入 config 值或寫入 token。
- Doctor 整體 deadline／輸出上限（inventory 已有 3 秒／5 秒），可選 daemon connectivity 的明確開關；掛起 peer 不可無限等候。
- Clean amd64 fixture 真實 release install、arm64 native／emulation、systemd user bus 的執行位置；缺環境記未驗證，不用 stub 替代最終真實契約證據。

2026-10-07 尚未進行實作、release metadata 注入、使用者資料遷移或 service mutation。
