# Issue 45 — 實作計畫

建立日期：2026-10-07。風險與首要驗證引用 [README](README.md#風險與首要驗證)。本檔是唯一任務來源。2026-10-09 全部 Scenario 核准後由 decompose 細化為下列 Phase／Task。

## 設計方案

Installer 下載 → manifest 校驗 → archive 安全檢查 → 同目錄 stage → atomic rename。CLI 在原有 handler 之前解析 operator defaults，再以明確出現的 flag 覆寫；不修改全域 ENV。Doctor 共用 registry／inventory 的探測契約，setup／service 共用設定路徑與 user unit 邏輯。介面依 technical-analysis 已確認的決策；具體函式簽名由 execute-task 決定，不預建抽象。

## Scenario 責任對照

2026-10-09 依核准規格（核准 commit `384a0d1`）細化。每個 Scenario、風險證據與交付成果只有一個責任 Task；其他 Task 可支援但不重複責任。

| 驗收／交付 | 責任 Task | 支援 Task |
|---|---|---|
| 風險證據：無 config 的 DB／socket／fallback 基線 | 0.1 | 3.2、6.3 |
| 風險證據：installer 故障判準自檢 | 0.2 | 2.1、2.3 |
| DIST-001 | 2.1 | 1.1、6.2 |
| DIST-002 | 2.2 | 1.2 |
| DIST-003 | 2.3 | 0.2 |
| DIST-004 | 2.3 | 0.2、1.1 |
| DIST-005 | 2.1 | — |
| DIST-006 | 4.2 | 3.1、4.1 |
| DIST-007 | 4.2 | 4.1 |
| DIST-008 | 4.3 | 4.2 |
| DIST-009 | 3.2 | 0.1、3.1 |
| DIST-010 | 3.1 | 3.2 |
| DIST-011 | 5.2 | 3.1、4.2、5.1 |
| DIST-012 | 5.2 | 5.1 |
| DIST-013 | 5.3 | 5.1 |
| DIST-014 | 5.1 | 4.2 |
| DIST-015 | 5.1 | 6.1 |
| DIST-016 | 6.2 | 1.2、2.1 |
| DIST-017 | 6.3 | 0.1、3.2 |
| 交付：常青文件與使用指南 | 6.1 | — |

相依順序：0.1／0.2／1.1／3.1／4.1 可先行；1.1 → 1.2；0.2＋1.1 → 2.1 → 2.2／2.3；0.1＋3.1 → 3.2；3.1＋4.1 → 4.2 → 4.3；3.2 → 5.1 → 5.2；5.1 → 5.3；1.2＋2.3＋5.2 → 6.2；全部功能 Task → 6.1、6.3。無循環相依；High 風險的首要驗證 0.1／0.2 先於依賴它的 installer 與 CLI 接線。

## Phase 0 — 首要驗證：既有行為基線與故障判準

### Task 0.1 — 刻畫 v1.0 無 config 的路徑與 fallback 行為
- 產出與範圍：`cmd/asura` characterization 測試，固定無 config 時 daemon／run／status／cancel／audit／inventory／ask／cron／memory／web 的 DB 預設（cwd `asura.db`）、socket 預設（`ASURA_SOCKET` → `~/.asura/asura.sock`）、status／cancel／audit／inventory 未設 socket 走本機 DB、socket 連線失敗回落本機 DB、allowed roots 預設（daemon 為啟動目錄、run 為 workspace）、workers／queue 預設，以及 `ASURA_RUNTIME_PRIORITY`／`ASURA_CONTEXT_*`／`ASURA_MEMORY_UTILITY_POLICY` 的空值與錯誤值語意。不改 production code。
- 相依：無
- 驗收編號：風險證據（支援 DIST-009／017）
- 完成判準：基線測試在未修改的 `main`（`1be939b`）全綠；每項以字面值期望（非重算實作）斷言；涵蓋清單逐項對應上列行為。
- 驗證：不改變可觀察行為；以 characterization test 綠燈為等價證據，並以刻意改錯一項預設的本地實驗確認測試會轉紅（實驗不提交）。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 測試：`cmd/asura/no_config_baseline_test.go`（`TestNoConfigDefaultSocketPath`、`TestNoConfigFlagDefaults`、`TestNoConfigLocalFallbackUsesCwdDB`、`TestNoConfigRunDefaults`、`TestNoConfigEnvValidation`、`TestNoConfigDaemonDefaults`）。daemon 以 `go build` 產出的真實 binary 在暫存 HOME／cwd 啟動，斷言啟動行字面值 `Daemon started on socket <HOME>/.asura/asura.sock (workers: 2, queue: 10)`、`./asura.db` 建立、cwd 外 workspace 回 `not in allowed roots`、cwd 內 workspace 通過 root 檢查（fixture 無 runtime，停在 `runtime admission rejected`），並以 SIGTERM 正常結束；ENV 以空值傳入驗證空值等同未設。
  - 被測版本：production code 與基線 `1be939b` 相同（`git diff 1be939b --stat -- . ':!docs'` 無輸出），僅新增測試檔；go1.27.1 linux/amd64。
  - 綠燈：`go test -count=1 -run 'TestNoConfig' -v ./cmd/asura` → 6 個 PASS，exit 0；`go vet ./...` exit 0；`go test -count=1 -race -v ./cmd/asura` → ok，無 FAIL／SKIP；`gofmt -l cmd/asura` 無輸出。
  - 判準反向自檢（本地變異實驗，每次以 `git checkout` 還原，未提交；以 `go test -count=1 -race -run 'TestNoConfig' ./cmd/asura` 執行）：
    - daemon `--db` 預設改 `other.db` → `TestNoConfigFlagDefaults`、`TestNoConfigDaemonDefaults` FAIL
    - `DefaultSocketPath` 目錄改 `.asura2` → `TestNoConfigDefaultSocketPath`、`TestNoConfigFlagDefaults`、`TestNoConfigDaemonDefaults` FAIL
    - status `--socket` 預設改為 `$HOME/.asura/asura.sock` → `TestNoConfigFlagDefaults` FAIL
    - daemon root 預設加入 `filepath.Dir(cwd)` → `TestNoConfigDaemonDefaults` FAIL（`outside workspace accepted`）
    - daemon `--workers` 預設改 3 → `TestNoConfigFlagDefaults`、`TestNoConfigDaemonDefaults` FAIL
    - run root 預設改為不存在的路徑 → `TestNoConfigRunDefaults` FAIL
    - cancel 在 socket 已設但連線失敗時直接 return 1（移除本機 fallback）→ `TestNoConfigLocalFallbackUsesCwdDB` FAIL
    - 首次以 `{"/"}` 取代 daemon root 時測試仍綠：`auth.Policy` 以 `root+"/"` 前綴比對，`/` 作為 root 不會涵蓋子目錄，屬既有行為，非本 Task 範圍，未修改；改用父目錄變異後確認測試轉紅。
  - 精煉：code-simplify 檢查本次測試檔一次，no-op——每個測試對應一類基線行為，字面期望不可再合併而不失可讀性；沿用上列綠燈。
  - 不改變可觀察行為：diff 僅新增 `_test.go`，依 verification.md 以 characterization 綠燈與變異紅燈為等價證據。

### Task 0.2 — Installer 故障 fixture 與 sentinel 判準自檢
- 產出與範圍：Go test 驅動真實 `sh` 的 installer harness；以 PATH 前置的 `curl`／`wget`／`uname` stub 將 GitHub URL 映射到本地 fixture，不在 installer 加測試專用參數。Fixture 含正常 archive、竄改 archive、缺 asset、manifest 缺項／重複項、中斷下載、含 `..`／絕對路徑／symlink 的 archive、sentinel binary（固定內容與 0755）。判準函式比對 sentinel 內容 hash、權限與 install-dir 殘留檔。
- 相依：無
- 驗收編號：風險證據（支援 DIST-003／004）
- 完成判準：對照組驗證判準本身——「會覆寫 sentinel 的控制腳本」使判準紅、「不動檔案的控制腳本」使判準綠、「留下暫存檔的控制腳本」被殘留檢查抓到；控制腳本只存在測試中。
- 驗證：判準自檢即本 Task 證據（尚無 installer，不記 installer 紅燈）。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`scripts/installer/harness_test.go`（fixture Release、curl／wget／uname stub、`sentinel` 判準與 `violations`）、`scripts/installer/harness_selfcheck_test.go`。stub 以 `routes.tsv` 精確比對 URL，未登錄者回 404（exit 22），`partial` 回前 64 bytes 並 exit 18；fixture archive root 與內容對齊 `release.yml`。
  - 判準自檢 `TestSentinelOracleDetectsDamage`：noop 控制腳本無違反；覆寫、chmod、刪除、換成 symlink、install-dir 暫存殘留、TMPDIR 殘留各自回報對應違反（7 個子測試 PASS）。
  - Fixture 可區分性 `TestFixtureFaultsAreDistinguishable`：控制腳本以 stub 取 latest／asset／manifest 並自行核對，正常 amd64／arm64 exit 0 並列出對應 root；竄改 exit 4（checksum mismatch）、缺 asset exit 22、manifest 缺項／重複項 exit 3（entries 0／2）、中斷 exit 18；`../`、絕對路徑與 symlink archive 可被 `tar -t` 列出，供 2.3 驗證 installer 拒絕（10 個子測試 PASS）。
  - 變異實驗（本地、未提交，事後確認原碼還原）：stub 對未登錄 URL 改回成功 → `missing-asset` FAIL；判準略過權限比對 → `chmod` FAIL。
  - 命令：`go vet ./...` exit 0；`go test -count=1 -race -v ./...` 全部 package ok（2026-10-09，branch `issue-45-distribution-bootstrap`，go1.27.1 linux/amd64）；既有 SKIP 為 runtime live／contract 前置條件缺少（`TestLiveAdapter`、`TestCodexProbe_*`、`TestAdapter_Run_Live`、`TestINT001`、`TestINT007`、`TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly`、`TestUtilityLiveProbe`、`TestUtilityCapacityProbe`），本 Task 未觸及 `internal/backend`，不計為通過；`gofmt -l scripts` 無輸出。
  - 精煉：code-simplify 移除與 `isLatest` 重複的 `noLatest` 欄位，重跑 `go test -count=1 -race ./scripts/installer` ok。
  - 尚無 installer，本 Task 不記 installer 紅燈；紅燈由 2.1／2.3 取得。

## Phase 1 — Version 與 release 契約

### Task 1.1 — `asura version` 與 `--json`
- 產出與範圍：`cmd/asura` 新增 `version` 子命令；人類輸出與 `--json`（`version`／`commit`／`os`／`arch`）；以 ldflags 可注入的 package 變數，未注入時為 `dev`／`unknown`。
- 相依：無
- 驗收編號：支援 DIST-001／002／004／016
- 完成判準：未注入 build 輸出 `dev`／`unknown`；以 `-ldflags -X` 注入字面 tag／commit 的 build 輸出相同字面值；`--json` 可被 `encoding/json` 解析且欄位固定；os／arch 與 `GOOS`／`GOARCH` 一致。
- 驗證：CLI 測試以 `go build -ldflags` 產出 binary 執行（外迴圈）＋ handler 單元測試；兩層失敗面不同（注入接線 vs 輸出格式），保留雙迴圈。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`cmd/asura/version.go`（`version`／`commit` package 變數，預設 `dev`／`unknown`；`--json` 固定四欄；多餘參數 exit 2）、`run.go` dispatch 與 usage 一行、`cmd/asura/version_test.go`。
  - 外迴圈紅燈：`go test -count=1 -run 'TestVersion' -v ./cmd/asura` → `TestVersionStampedBuild` FAIL（以 `-ldflags "-X main.version=v9.8.7 -X main.commit=0123abcd"` 建出的 binary 執行 `version --json` exit status 1），原因為命令不存在。
  - 單元紅燈：同一命令 `TestVersionUnstampedBuild` FAIL：`code=1 error: unknown command "version"`。
  - 最小實作後綠燈：`go test -count=1 -race -run 'TestVersion|TestNoConfig' -v ./cmd/asura` → 8 PASS（含 0.1 基線）；期望值為字面 `asura dev (commit unknown, linux/amd64)`、`v9.8.7`／`0123abcd`，os／arch 對照 `runtime.GOOS`／`GOARCH`。
  - 精煉後綠燈：code-simplify 移除測試中只建置不斷言的 arm64 交叉編譯段並統一變數名；重跑 `go vet ./...` exit 0、`go test -count=1 -race ./cmd/asura ./scripts/...` ok、`go build ./cmd/asura` exit 0（2026-10-09，go1.27.1 linux/amd64）。
  - 雙迴圈理由：外迴圈驗 ldflags 注入接線（只有真實 build 才會失敗的面），單元層驗輸出格式與用法錯誤。
  - 常青文件：CLI 參考與 README 指令一覽依計畫由 6.1 統一更新。

### Task 1.2 — Release workflow 注入 metadata、移除 `--clobber`
- 產出與範圍：`.github/workflows/release.yml` build 時注入 tag／commit；建置抽為 `scripts/release/build-archives.sh` 供 workflow 與測試共用；測試對兩架構 archive 解出 binary 核對（原生架構執行 `version --json`，非原生架構核對 ELF machine 與注入字面值）；移除 `--clobber`；asset 命名與 archive root 不變。
- 相依：1.1
- 驗收編號：支援 DIST-002／016
- 完成判準：本機以 workflow 相同命令建出的 archive 名稱、root、checksum 與注入值一致；`gh release upload` 不含 `--clobber`；`actionlint`（若可用）或 YAML 解析通過。
- 驗證：本機重放 build 段 shell 的腳本測試；真實 tag 發佈屬 6.2 的發佈後驗證。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`scripts/release/build-archives.sh`（命名、archive root、內容與 manifest 格式沿用 v1.0.0，ldflags 加入 `-X main.version`／`-X main.commit`）、`.github/workflows/release.yml`（Build 步驟改呼叫腳本並以 `git rev-parse HEAD` 傳 commit；upload 移除 `--clobber`）、`scripts/release/build_test.go`。
  - 紅燈（外迴圈與單元同層合併：腳本即被測單元，無其他整合面）：`go test -count=1 -v ./scripts/release` → `TestBuildArchivesContract` FAIL（`build-archives: exit status 2`，腳本不存在）、`TestReleaseWorkflowUsesContract` FAIL（`release.yml does not build via scripts/release/build-archives.sh`）。
  - 綠燈：`go test -count=1 -race -v ./scripts/release` → 2 PASS：asset 恰為兩個 archive 與 `asura_v9.9.9-test_checksums.txt`、`sha256sum -c` 兩項 OK、archive 項目恰為 root 目錄與 `asura`／`LICENSE`／`README.md`／`CHANGELOG.md`、ELF machine 分別為 x86-64／AArch64、binary 含注入字面值、amd64 `version --json` 回 `v9.9.9-test`／`deadbeefcafe`／`linux`／`amd64`；workflow 使用腳本且無 `--clobber`。
  - 判準反向自檢：本地移除腳本中的 `-X` 注入（事後還原，未提交）→ `TestBuildArchivesContract` FAIL（`amd64 binary lacks injected version metadata`）。
  - 精煉：首版以 `go version -m` 讀 ldflags，實測 `-trimpath -s -w` 建置不含該資訊而改為字面值比對；manifest 命令還原為與 v1.0.0 相同的 `sha256sum *.tar.gz`。最終 `go vet ./...` exit 0、`go test -count=1 -race ./cmd/asura ./scripts/...` ok；`release.yml` 以 PyYAML 解析成功；本機無 actionlint，未執行（2026-10-09，go1.27.1 linux/amd64）。
  - 限制：真實 tag 觸發的 workflow 與 GitHub 上傳行為（含同名 asset 存在時失敗）未在本機執行，屬 6.2 發佈後驗證。

## Phase 2 — 安裝器

### Task 2.1 — `install.sh` 主流程、平台判定與前置工具檢查
- 產出與範圍：repo 根目錄 POSIX `install.sh`：`--version`／`--install-dir`，預設 `~/.local/bin`；Linux 限定、`x86_64`→amd64、`aarch64`／`arm64`→arm64；需 `curl` 或 `wget`、`tar`、`sha256sum` 或 `shasum -a 256`、`uname`，缺任一項在變更 filesystem 前失敗；`releases/latest` 取 stable；私有 temp 下載、唯一精確 manifest 項目、archive 路徑／型別檢查、同目錄 stage（0755）＋ `version --json` 核對後 rename；install-dir 不存在則建立；不在 PATH 只提示；不改 shell rc、不用 sudo。
- 相依：0.2、1.1
- 驗收編號：DIST-001、DIST-005
- 完成判準：0.2 harness 下 amd64 fresh install 取得 checksum 相符 binary 且 `version --json` 對應 fixture tag；非 Linux、不支援架構、缺工具各自非零且 install-dir 無 binary；含空白的 install-dir 可用；執行過程無 sudo 呼叫（stub 記錄）。
- 驗證：外迴圈 harness 驅動真實 sh；installer 無獨立單元層，單迴圈合併理由記於證據。
- 狀態：📝 待實作
- 證據：

### Task 2.2 — arm64 安裝證據
- 產出與範圍：harness 以 `uname -m` 為 `aarch64`／`arm64` 時下載 arm64 asset；arm64 binary 在原生 arm64 或明確標示的 qemu-user 環境執行 `version --json`。
- 相依：2.1
- 驗收編號：DIST-002
- 完成判準：下載 URL 為 arm64 asset 且 checksum 相符；arm64 執行證據標明原生或模擬環境；缺可執行環境時記未驗證，不以 amd64 結果代替。
- 驗證：沿用 2.1 harness 的外迴圈。
- 狀態：📝 待實作
- 證據：

### Task 2.3 — 安裝失敗保留、重裝與升降版
- 產出與範圍：installer 失敗路徑與版本行為：checksum 不符、缺 asset／manifest 項目、重複項目、下載中斷、危險 archive、rename 失敗、既有目標為 symlink／目錄／非一般檔案、`--version v1.0.0` 拒絕並提示手動下載；同版重跑重新下載驗證、升級、顯式降版；不動 config／DB、不重啟 daemon。
- 相依：2.1
- 驗收編號：DIST-003、DIST-004
- 完成判準：每個失敗 Example 非零退出、0.2 判準證明 sentinel 內容與權限不變且無殘留暫存檔；成功 Example 安裝的 binary 版本對應指定 tag；harness 前後 config／DB fixture hash 不變；stub 記錄無 `systemctl`／kill 呼叫。
- 驗證：外迴圈 harness；每個 Example 先取得紅燈（2.1 主流程缺對應防護時）再補實作。
- 狀態：📝 待實作
- 證據：

## Phase 3 — Config／XDG 與 CLI 接線

### Task 3.1 — `internal/config` 嚴格載入與路徑解析
- 產出與範圍：新增 `internal/config`：XDG 路徑解析（相對 XDG 視為未設、socket 優先 `XDG_RUNTIME_DIR` 否則 state、sun_path 108 bytes 上限）；`config.toml` 嚴格解析（`schema_version = 1`、拒絕未知欄位／未支援 schema／錯誤 enum／無效 path／數值）；context 欄位沿用 `contextwin` 驗證；權限檢查（非本人擁有或檔案／目錄 group／others 可寫即拒絕）；錯誤不回顯值。必要時新增成熟 TOML parser 至 go.mod／go.sum。
- 相依：無
- 驗收編號：DIST-010
- 完成判準：DIST-010 六個 Example 各自回傳可判別錯誤且訊息不含 fixture 中的 sentinel 值；缺檔回傳「無 config」而非錯誤；XDG 解析以字面路徑期望驗證各 fallback。
- 驗證：單元測試為主；DIST-010「不啟動 daemon」的外部行為由 3.2 的 CLI 外迴圈承擔。
- 狀態：📝 待實作
- 證據：

### Task 3.2 — CLI 接線與逐設定 precedence
- 產出與範圍：各 handler 在 parse 前載入 config 取得預設，`flag.Visit` 辨識顯式 flag；逐設定 CLI > ENV > config > 內建；不修改 process ENV；有 config 時 status／cancel／audit／inventory 以 config socket 嘗試 daemon、失敗回落 config db；bad config 時需載入設定的指令非零退出且不啟動 daemon；無 config 行為與 0.1 基線一致。
- 相依：0.1、3.1
- 驗收編號：DIST-009
- 完成判準：以四層不同字面值逐層移除 override 觀察生效值；CLI 顯式空值／等於預設值不被低順位覆蓋；bad config 下 daemon 非零且 socket 未建立；0.1 基線全綠。
- 驗證：外迴圈 CLI 測試觀察 daemon／client 實際使用的路徑；單元層由 3.1 承擔，兩層失敗面不同。
- 狀態：📝 待實作
- 證據：

## Phase 4 — Doctor

### Task 4.1 — Adapter executable 旁路
- 產出與範圍：`internal/backend` 各 adapter 提供僅供 doctor 讀取的 probe 實際 executable 路徑（OpenCode 含固定 cache fallback）；`RuntimeInventoryEntry`、ExecutionContract 與 `runtime-inventory-v1` 不變。
- 相依：無
- 驗收編號：支援 DIST-006／007
- 完成判準：各 adapter 回報的路徑等於其 probe 實際執行的 binary（以 PATH 與 cache 同時存在的 fixture 區分）；無法提供時回 unknown；既有 inventory／contract 序列化 golden 不變。
- 驗證：adapter 單元測試；backend live／contract SKIP 名稱與原因記錄。
- 狀態：📝 待實作
- 證據：

### Task 4.2 — `asura doctor` readiness
- 產出與範圍：`asura doctor`：system（Linux、arch、`/proc`、bwrap 需求、path 權限評估標明未實際寫入）、config（解析、權限、socket 長度、allowed roots）、runtime（本機 registry＋CaptureInventory＋4.1 executable）、user service 狀態、daemon 連線（2 秒）；`READY`／`DEGRADED` exit 0、`NOT_READY` exit 1、用法錯誤 exit 2；optional runtime／service 缺項只警告。
- 相依：3.1、4.1
- 驗收編號：DIST-006、DIST-007
- 完成判準：stub runtime 組合分別得到 READY、DEGRADED（optional 缺失或版本不符、無 user service）、NOT_READY（無 runtime、必要 system／config 失敗）與對應 exit；輸出含有界 reason 與能力摘要；不提交 Task、不呼叫模型（stub 記錄）。
- 驗證：CLI 外迴圈 + 判定邏輯單元測試，雙迴圈。
- 狀態：📝 待實作
- 證據：

### Task 4.3 — Doctor secret 與唯讀邊界
- 產出與範圍：整體 10 秒 deadline、輸出上限、只輸出固定 reason、ENV／config 只顯示是否設定與路徑；不開 SQLite、不寫 config／DB／credential、不建立 unit；runtime 探測暫存只在私有 temp 並清理。
- 相依：4.2
- 驗收編號：DIST-008
- 完成判準：ENV、credential 檔與 probe stderr 植入 sentinel，probe 失敗與逾時（含掛起的 daemon peer）下 stdout／stderr 皆無 sentinel；doctor 在 deadline 內結束；前後 config／DB／credential hash 與 user unit 目錄不變；私有 temp 無殘留。
- 驗證：CLI 外迴圈；先以掛起 peer／洩漏 stub 取得紅燈。
- 狀態：📝 待實作
- 證據：

## Phase 5 — User service 與 setup

### Task 5.1 — `asura service` 子命令與 unit
- 產出與範圍：`asura service install|start|stop|restart|status|uninstall`；unit 含 binary 絕對路徑（systemd 規則跳脫、不經 shell）、`KillMode=mixed`、`TimeoutStopSec=60s`、`Restart=on-failure`、`RestartSec=5s`、`StartLimitIntervalSec=60s`、`StartLimitBurst=3`；僅 setup 時非預設 `XDG_CONFIG_HOME` 寫入 `Environment=`；無 secret、不設 linger；非 Asura 管理的同名 unit 拒絕處理；無 systemd／user bus 時明確原因；uninstall 只移除受管理 unit。
- 相依：3.2
- 驗收編號：DIST-014、DIST-015
- 完成判準：systemctl stub 記錄的 argv 與 unit 內容符合上列；無 systemd 時 service 回明確原因且 standalone daemon 依 config 可啟動、doctor 為 DEGRADED exit 0；uninstall 前後 config 與 SQLite／Task／Memory 可讀且 hash 不變；不啟動其他 supervisor。
- 驗證：CLI 外迴圈 + unit 生成單元測試；systemctl stub 不等於 lifecycle 證據（見 5.3）。
- 狀態：📝 待實作
- 證據：

### Task 5.2 — `asura setup` 互動／腳本模式與冪等
- 產出與範圍：`asura setup`：TTY 互動選 allowed roots 與 service；非 TTY 需 `--allowed-roots`，僅 `--service` 安裝並啟動 service；建立 XDG config（0600）／data（0700）；顯示 doctor 結果與下一步；重跑保留設定與資料，參數衝突明確失敗並提示手動修改；不安裝／登入 runtime、不改 shell rc、不啟動 Web、不搬移舊 DB，偵測舊 DB 時顯示指定舊路徑的指引；半途失敗不宣稱完成。
- 相依：5.1
- 驗收編號：DIST-011、DIST-012
- 完成判準：fresh 非互動建立的 config 為 `schema_version = 1` 且只含指定 root；互動以 pty 驅動；非 TTY 缺 root 立即失敗不等待 stdin；重跑前後 config／DB／credential／unit hash 不變且無重複 unit；衝突參數非零；注入寫入失敗後重跑可完成。
- 驗證：CLI 外迴圈（含 pty）+ 合併／衝突判定單元測試。
- 狀態：📝 待實作
- 證據：

### Task 5.3 — 真實 systemd user bus lifecycle
- 產出與範圍：在可用 user bus 的主機以隔離 unit 名與暫存 XDG 執行 install／start／stop／restart／status／uninstall；驗證 60 秒停止期限、異常退出 5 秒重啟與 60 秒內 3 次上限、手動 stop 不重啟、中斷任務下次啟動標記 failed。
- 相依：5.1
- 驗收編號：DIST-013
- 完成判準：`systemctl --user show` 與 journal 記錄的狀態轉換、時間與重啟次數符合核准值；長任務 fixture 逾時被回收且下次啟動為 failed、未重跑；測試後無殘留 unit。缺 user bus 時記未驗證，不以 stub 代替。
- 驗證：手動／腳本化真實環境驗證，記錄主機、systemd 版本與命令輸出；不屬 `go test` 預設套件。
- 狀態：📝 待實作
- 證據：

## Phase 6 — 文件、clean onboarding 與完整回歸

### Task 6.1 — 常青文件與使用指南
- 產出與範圍：README（安裝、指令一覽）、`docs/guides/cli-reference.md`（version／doctor／setup／service 與 config precedence）、安裝／設定／service／移除與資料保留說明、`docs/guides/runtimes.md`（doctor 與 inventory 關係）、`docs/development.md` issue 索引、`CHANGELOG.md` v1.1.0 節。
- 相依：2.3、4.3、5.2
- 驗收編號：交付成果（支援 DIST-015）
- 完成判準：文件中的命令、flag、路徑與實作一致；相對連結存在；移除步驟與資料位置明列。
- 驗證：不改變可觀察行為；以連結與命令存在性的靜態檢查腳本為等價證據。
- 狀態：📝 待實作
- 證據：

### Task 6.2 — Clean Linux onboarding 與 release 契約
- 產出與範圍：無 Go／repo 的 clean Linux 容器，以 1.2 相同命令建出的 HTTP fixture release 執行 install → doctor → setup → standalone daemon；核對 asset 名稱、archive 路徑、tag／version／commit／arch 與 checksum。正式 v1.1.0 Release 發佈後以真實 URL 重跑 install 與 version 核對。
- 相依：1.2、2.3、5.2
- 驗收編號：DIST-016
- 完成判準：fixture 流程全通且證據標明 HTTP fixture／stub runtime；發佈後真實 Release 驗證另記，未發佈前狀態標為等待外部驗收窗（窗口為 v1.1.0 tag 發佈日），不以 fixture 代替。
- 驗證：容器端到端腳本；記錄映像、命令與輸出。
- 狀態：📝 待實作
- 證據：

### Task 6.3 — 完整回歸與相容矩陣
- 產出與範圍：無 config、有 config、CLI override 三種狀態下 daemon／ask／run／status／cancel／audit／inventory／Cron／memory／Web 的相容矩陣；Phase 4 authority（StageBindingPlan、ExecutionContract、ExecutionAudit）不變；`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`；SKIP 逐項記名稱與原因。
- 相依：0.1、2.3、3.2、4.3、5.2、5.3、6.2
- 驗收編號：DIST-017
- 完成判準：三條命令 exit 0；0.1 基線全綠；矩陣每格有結果；SKIP 清單不計為通過；觸及 `internal/web` 或 memory lifecycle 時依 ci.yml 執行對應探測。
- 驗證：回歸證據彙整，固定被測提交。
- 狀態：📝 待實作
- 證據：

## 使用方式對照

現況（2026-10-07）：手動 download／extract → 指定 daemon flags。目標（2026-10-09 核准）：`curl -fsSL https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh` → `asura doctor` → `asura setup` → user service 或 standalone → `asura ask ...`。功能實作前不更新常青指南為已提供的功能。

## 測試策略

- Installer 外迴圈用真實 sh／HTTP fixture／archive／checksum／filesystem；底層重用同層 failure fixture 時可合併紅燈，記錄無另一層整合責任的理由。stub 網路驗故障，不宣稱正式 Release E2E。
- Config unit 覆蓋 parse／validation／merge／path；CLI 外迴圈驗 daemon/client 使用真正生效值與 v1.0 fallback，不只測 loader。
- Doctor runtime probe 用命令記錄及 secret sentinel fixture，禁止執行 prompt；必要的真實 runtime contract 證據與 stub inventory 分開。
- Setup/service 保存 config、DB／Task／Memory、credential 的前後值；冪等判準不是只看 exit 0。systemctl mock 驗 argv／錯誤，不等於 systemd lifecycle 已跑通。
- 純文件以相對連結、Scenario 集合／核准表一致與命令存在性靜態檢查取代紅燈，不記作程式測試。
- 原 Task 記錄外迴圈紅、unit 紅、最小實作綠、精煉後綠的命令／exit／原因／環境與提交；精煉 no-op 明列理由，不重複跑相同證據。

完整回歸入口（2026-10-07 依 project.md）：`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`。backend 相關 runtime contract／live SKIP 必須記錄名稱／原因，不算通過；如影響 Web 或 memory lifecycle，另跑 ci.yml 對應檢查。

## 交付前檢查清單

- [x] DIST-TBD 必要決策有來源；全部現存 Scenario 已核准或逐項豁免（2026-10-09，核准 commit `384a0d1`）。
- [x] 核准規格保存有效 commit；decompose 在本檔補齊每個 Scenario 的責任 Task（2026-10-09）。
- [ ] 每個 Task 完成且 evidence 對應固定交付版本；沒有把 stub／skip 當真實證據。
- [ ] 常青文件符合實際介面；不改 Phase 4 authority 或 SQLite schema。
- [ ] 依 create-commit／create-pr 固定交付 BASE／HEAD；獨立 reviewer 報告持久化。
- [ ] PASS 後等待使用者合併；本流程不自行 merge。

2026-10-09：完成拆解；尚未開始實作，沒有程式驗收通過的宣稱。
