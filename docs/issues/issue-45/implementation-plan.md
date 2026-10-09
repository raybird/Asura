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
- 產出與範圍：repo 根目錄 POSIX `install.sh`：`--version`／`--install-dir`，預設 `~/.local/bin`；Linux 限定、`x86_64`→amd64、`aarch64`／`arm64`→arm64；需 `curl` 或 `wget`、`tar`、`sha256sum` 或 `shasum -a 256`、`uname`，缺任一項在變更 filesystem 前失敗；`releases/latest` 取 stable；私有 temp 下載、同目錄 stage（0755）後 rename；install-dir 不存在則建立；不在 PATH 只提示；不改 shell rc、不用 sudo。
- 相依：0.2、1.1
- 範圍調整（2026-10-09）：checksum 比對、唯一精確 manifest 項目、archive 路徑／型別檢查、staged `version --json` 核對與 shasum fallback 移至 2.3，讓每項防護各自取得紅燈；2.1 只交付主流程。
- 驗收編號：DIST-001、DIST-005
- 完成判準：0.2 harness 下 amd64 fresh install 取得 checksum 相符 binary 且 `version --json` 對應 fixture tag；非 Linux、不支援架構、缺工具各自非零且 install-dir 無 binary；含空白的 install-dir 可用；執行過程無 sudo 呼叫（stub 記錄）。
- 驗證：外迴圈 harness 驅動真實 sh；installer 無獨立單元層，單迴圈合併理由記於證據。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：repo 根目錄 `install.sh`（POSIX sh，dash 執行）、`scripts/installer/install_test.go`；harness PATH 改為 stub 加白名單系統工具（`systemTools`），並新增記錄呼叫的 `sudo`／`systemctl` stub 與 `drop`。
  - 外迴圈紅燈：`go test -count=1 -run 'TestInstall' ./scripts/installer` → 18 個（子）測試 FAIL，原因為 `install.sh` 不存在。首輪 `TestInstallMissingTools`、`TestInstallUsageErrors` 因暫存路徑含工具名而假綠，改為比對 `asura-install: error: missing required tool: …` 與 `Usage: install.sh …` 字面訊息後才全數轉紅。
  - 綠燈：`go test -count=1 -run 'TestInstall' -v ./scripts/installer` → 全部 PASS：amd64 fresh install 內容等於 fixture、0755、只請求 latest／amd64 asset／manifest、無 sudo／systemctl、install-dir 只有 `asura`、TMPDIR 無殘留、輸出 `… is not in PATH` 提示且未建立 `.profile`；含空白的 `--install-dir` 可用且不建立預設目錄；Darwin/x86_64、Linux/riscv64、Linux/i686 回 `unsupported platform`；缺 curl+wget、tar、sha256sum+shasum、uname 各自在下載與檔案變更前失敗；無 curl 時改用 wget；未知參數與缺值回 exit 2。
  - 精煉：移除尚未使用的 sha256 函式與只因函式未被呼叫而必然通過的 shasum fallback 測試（移至 2.3）。`go vet ./...` exit 0、`go test -count=1 -race ./scripts/...` ok；本機無 shellcheck，未執行（2026-10-09，dash／go1.27.1 linux/amd64）。
  - 單迴圈理由：installer 為獨立 shell 腳本，沒有可分離的單元層；harness 以真實 sh、tar、檔案系統執行，網路以 stub 取代（屬 HTTP fixture 證據，非正式 Release E2E）。

### Task 2.2 — arm64 安裝證據
- 產出與範圍：harness 以 `uname -m` 為 `aarch64`／`arm64` 時下載 arm64 asset；arm64 binary 在原生 arm64 或明確標示的 qemu-user 環境執行 `version --json`。
- 相依：2.1
- 驗收編號：DIST-002
- 完成判準：下載 URL 為 arm64 asset 且 checksum 相符；arm64 執行證據標明原生或模擬環境；缺可執行環境時記未驗證，不以 amd64 結果代替。
- 驗證：沿用 2.1 harness 的外迴圈。
- 狀態：✅ 已完成（2026-10-09；arm64 執行為 qemu 模擬環境證據）
- 證據：
  - asset 選擇：`TestInstallArm64Asset`（`scripts/installer/install_test.go`）以 `uname -m` 為 `aarch64`／`arm64` 安裝，內容等於 arm64 fixture、只請求 arm64 asset；`go test -count=1 -run TestInstallArm64Asset -v ./scripts/installer` → 2 PASS。對應程式已由 2.1 的架構對應實作，無法另取紅燈；改以變異實驗確認判準：把 arm64 對應改成 amd64（事後還原、未提交）→ 兩個子測試 FAIL。
  - 授權：2026-10-09 使用者選擇「註冊 binfmt 後用容器跑（建議）」。執行 `docker run --privileged --rm tonistiigi/binfmt:qemu-v9.2.2 --install arm64`，主機 emulators 新增 `qemu-aarch64`（重開機後失效）。
  - arm64 執行：`sh scripts/release/onboarding-e2e.sh alpine:3.20.3 linux/arm64` → 容器 `uname -m` 為 `aarch64`；installer 只請求 `asura_v1.1.0_linux_arm64.tar.gz`，暫存 binary 在 qemu 下回報 `{"arch":"arm64","commit":"929bda4…","os":"linux","version":"v1.1.0"}`，與 tag／arch 一致後才替換；doctor DEGRADED exit 0、setup 完成、standalone daemon 啟動並以 SIGTERM exit 0、`== ONBOARDING OK`。腳本新增 `arch mismatch` 檢查：預設平台未固定時曾因本機 alpine tag 指向 arm64 而在 amd64 預期下正確失敗，之後固定預設 `linux/amd64`，兩種平台各重跑一次皆通過。
  - 證據分類：qemu-user 模擬＋HTTP fixture＋stub runtime，非原生 arm64 主機；真實 arm64 archive 的 ELF 架構與版本注入另由 1.2 核對。

### Task 2.3 — 安裝失敗保留、重裝與升降版
- 產出與範圍：installer 防護與版本行為：SHA-256 比對（含 shasum fallback）、唯一精確 manifest 項目、archive 路徑／型別檢查、staged `version --json` 核對 tag／arch；checksum 不符、缺 asset／manifest 項目、重複項目、下載中斷、危險 archive、rename 失敗、既有目標為 symlink／目錄／非一般檔案、`--version v1.0.0` 拒絕並提示手動下載；同版重跑重新下載驗證、升級、顯式降版；不動 config／DB、不重啟 daemon。
- 相依：2.1
- 驗收編號：DIST-003、DIST-004
- 完成判準：每個失敗 Example 非零退出、0.2 判準證明 sentinel 內容與權限不變且無殘留暫存檔；成功 Example 安裝的 binary 版本對應指定 tag；harness 前後 config／DB fixture hash 不變；stub 記錄無 `systemctl`／kill 呼叫。
- 驗證：外迴圈 harness；每個 Example 先取得紅燈（2.1 主流程缺對應防護時）再補實作。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`install.sh` 新增 SHA-256 比對（sha256sum，否則 shasum -a 256）、manifest 唯一精確項目、archive 項目限 root 下一般檔案／目錄且拒絕 `..`、staged binary `version --json` 核對 tag／os／arch、非一般檔案目標拒絕（下載前）、版本格式驗證與 v1.1.0 以前拒絕（`--version` 時在下載前）；測試 `scripts/installer/install_safety_test.go`、`install_real_test.go`。
  - 外迴圈紅燈：`go test -count=1 -run 'TestInstall(FailuresPreserve|RefusesNonRegular|ReinstallUpgrade)' -v ./scripts/installer` → checksum 不符（含僅 shasum）、manifest 缺項／重複、`..`、root 外、絕對路徑、symlink binary、版本不符、架構不符皆「仍安裝成功」FAIL；唯讀 install-dir 只有 cp 原生錯誤；`--version v1.0.0` 與 `v1.1.0/../x` 進入下載才失敗；symlink 目標被替換、目錄目標被 `mv` 放進目錄內而回報成功。首輪 checksum 兩例因 fixture 改了 archive root 而以錯誤原因失敗，修正 fixture（同 root 加入竄改檔）後確認是「竄改仍安裝」的紅燈。
  - 已由 2.1 滿足、無紅燈的情境（如實記錄）：缺 asset、下載中斷、archive 缺 binary，以及 `TestInstallReinstallUpgradeDowngrade`（升級到 latest、同版重裝重新下載 asset 與 manifest、`--version` 降版、prerelease 安裝且不查 latest）。
  - 綠燈：`go test -count=1 -race -v ./scripts/...` 全部 PASS（14 個頂層測試）；每個失敗情境非零退出、stderr 含 `asura-install: error:` 與對應訊息，sentinel 內容／0755 不變、install-dir 與 TMPDIR 無殘留、config.toml／XDG DB／舊 cwd DB fixture 內容不變、無 sudo／systemctl 呼叫；非一般檔案目標保持原樣，symlink 指向的檔案未被寫入。
  - 真實 archive：`TestInstallRealReleaseArchive` 以 `build-archives.sh` 建出的 amd64 archive 經 installer 安裝，安裝後執行輸出 `asura v1.1.0-real (commit 0123456789ab, linux/amd64)`，確認 installer 能解析真實 binary 的 `version --json`（欄位順序與 fixture 不同）。
  - 精煉：版本核對由兩段 case 改為逐欄迴圈；重跑上列測試全綠，`go vet ./...` exit 0（2026-10-09，dash／go1.27.1 linux/amd64）。
  - 限制：「binary replace 失敗」以唯讀 install-dir 使 stage 寫入失敗呈現；同目錄 `mv` 本身失敗未另行注入。網路為 stub fixture，非正式 Release。

## Phase 3 — Config／XDG 與 CLI 接線

### Task 3.1 — `internal/config` 嚴格載入與路徑解析
- 產出與範圍：新增 `internal/config`：XDG 路徑解析（相對 XDG 視為未設、socket 優先 `XDG_RUNTIME_DIR` 否則 state、sun_path 108 bytes 上限）；`config.toml` 嚴格解析（`schema_version = 1`、拒絕未知欄位／未支援 schema／錯誤 enum／無效 path／數值）；context 欄位沿用 `contextwin` 驗證；權限檢查（非本人擁有或檔案／目錄 group／others 可寫即拒絕）；錯誤不回顯值。必要時新增成熟 TOML parser 至 go.mod／go.sum。
- 相依：無
- 驗收編號：DIST-010
- 完成判準：DIST-010 六個 Example 各自回傳可判別錯誤且訊息不含 fixture 中的 sentinel 值；缺檔回傳「無 config」而非錯誤；XDG 解析以字面路徑期望驗證各 fallback。
- 驗證：單元測試為主；DIST-010「不啟動 daemon」的外部行為由 3.2 的 CLI 外迴圈承擔。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`internal/config/config.go`（`DefaultPaths`、`CheckSocketPath`、`Load`；nil 欄位代表未設定）、`internal/config/config_test.go`；`go.mod`／`go.sum` 新增 `github.com/BurntSushi/toml v1.5.0`（其餘 go.mod 排列維持原狀，`go mod verify` all modules verified）。
  - 紅燈：先以只有型別、函式回傳零值的骨架取得行為紅燈：`go test -count=1 -v ./internal/config` → `TestDefaultPaths`、`TestCheckSocketPath`、`TestLoadValid`、`TestLoadRejectsInvalid`、`TestLoadRejectsUnsafeFile` FAIL（29 個子測試）；`TestLoadMissing` 在骨架下已通過（骨架正好回 nil, nil），如實記錄。首輪 `TestLoadValid` 因 nil 解參考 panic 中斷後續測試，補 nil 防護後才取得完整紅燈。
  - 綠燈：`go test -count=1 -race -v ./internal/config` → 6 PASS。DIST-010 Examples：語法錯誤（`syntax error at line 2`）、未知欄位（含 `[context]` 巢狀，回報鍵名）、未支援／缺少 schema_version、enum／context 規則（沿用 `contextwin.LoadConfigWithLookup`）、runtime_priority（沿用 `backend.ParseRuntimePriority`，拒絕含逗號元素）、相對 db／socket／allowed_roots、超過 107 bytes 的 socket、workers／queue < 1、型別不符、無法讀取、group／others 可寫的檔案或目錄、非本人擁有（`/etc/passwd`）皆拒絕，且錯誤不含植入的 `SECRET-SENTINEL-7f3a`；XDG 解析以字面路徑驗證 runtime→state→HOME fallback 與相對 XDG 忽略。
  - 判準反向自檢（本地變異，事後還原）：拿掉 `Undecoded` 檢查 → unknown-field 兩例 FAIL；XDG 改為接受相對路徑 → `relative-xdg-ignored` FAIL；語法錯誤改為轉述 parser 原文 → `syntax` FAIL（訊息含 sentinel）；目錄權限檢查失效 → `other-writable-dir` FAIL（首次變異造成未使用變數編譯失敗，修正變異後確認）。
  - 精煉：toml 型別不符錯誤為一般 error，改以 regexp 只取行號與鍵名，不轉述原文；`go vet ./...` exit 0；`go test -count=1 -race ./internal/config ./internal/contextwin ./internal/backend ./cmd/asura` ok（2026-10-09，go1.27.1 linux/amd64）。
  - 範圍說明：DIST-010「不啟動 daemon」的 CLI 外部行為由 3.2 承擔。

### Task 3.2 — CLI 接線與逐設定 precedence
- 產出與範圍：RunCLI 對需要 socket／DB 的指令載入 config，各 handler 以「ENV > config > 內建」解析結果作為 flag 預設值，使任何顯式 flag（含空值與等於預設的值）自然勝出，不需 `flag.Visit`；逐設定 CLI > ENV > config > 內建；不修改 process ENV；有 config 時 status／cancel／audit／inventory 以 config socket 嘗試 daemon、失敗回落 config db；bad config 時需載入設定的指令非零退出且不啟動 daemon；無 config 行為與 0.1 基線一致。
- 相依：0.1、3.1
- 驗收編號：DIST-009
- 完成判準：以四層不同字面值逐層移除 override 觀察生效值；CLI 顯式空值／等於預設值不被低順位覆蓋；bad config 下 daemon 非零且 socket 未建立；0.1 基線全綠。
- 驗證：外迴圈 CLI 測試觀察 daemon／client 實際使用的路徑；單元層由 3.1 承擔，兩層失敗面不同。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`cmd/asura/config.go`（`cliConfig` 與 nil-safe 解析方法、`loadCLIConfig`、`localSocketDefault`、config 模式下 `DefaultSocketPath` 不建立 `~/.asura`、`prepareDirs` 以 0700 建 DB／socket 目錄）；`run.go` 在 dispatch 前載入 config，壞檔 exit 1，結束時清除 `activeConfig`；daemon／run／status／cancel／audit／inventory 改用解析後預設；daemon 在 config 模式檢查 socket 長度。測試 `cmd/asura/config_precedence_test.go` 與 `cmd/asura/main_test.go`（TestMain 指向空的 XDG_CONFIG_HOME，避免讀到開發者 config）。
  - 外迴圈紅燈：以只含型別的骨架執行 `go test -count=1 -run 'TestConfig' -v ./cmd/asura` → 9 個測試 FAIL：daemon 啟動行仍為 `~/.asura/asura.sock (workers: 2, queue: 10)`、status 未使用 config db、config priority 被忽略（得到 opencode）、utility policy 空值、bad config 下 daemon／web 等持續執行至 20 秒逾時。
  - 單元層：`TestConfigMemoryUtilityPolicy` 直接驗解析方法（ENV > config > observe），其餘解析規則由 3.1 單元測試承擔；外迴圈與單元失敗面不同，保留雙迴圈。
  - 綠燈：`go test -count=1 -race -v ./cmd/asura` ok。daemon 子行程字面驗證：config 值（`cfg.sock`、workers 5、queue 7、cfg.db、只允許 config root）勝過內建且未觸及 cwd `asura.db` 與 `~/.asura`；`ASURA_SOCKET` 勝過 config；`--socket`／`--db`／`--workers 2`（等於內建預設）／`--allowed-roots ""`（顯式空值回到 cwd root）勝過 ENV 與 config；config 未列路徑時使用 `$XDG_RUNTIME_DIR/asura/asura.sock` 與 `$XDG_DATA_HOME/asura/asura.db`。status／cancel／audit 在 socket 連不上時回落 config db、`--db` 優先；`run --backend auto` 依 config priority 選 codex，ENV 改為 opencode 優先後選 opencode；config `[context]` reject 生效，`ASURA_CONTEXT_POLICY=observe` 覆寫。bad config（未知欄位含 sentinel、schema 7、0666 權限、超長 XDG socket）下 daemon／run／ask／status／cancel／audit／inventory／web／cron／memory 皆 exit 1、stderr 含 config 且不含 sentinel、未建立 socket，`version` 不受影響。
  - 回歸：0.1 `TestNoConfig*` 全綠（首輪因 `activeConfig` 跨測試殘留使 `TestNoConfigDefaultSocketPath` FAIL，改為每次 RunCLI 結束清除後通過）。`go vet ./...` exit 0、`go build ./cmd/asura` exit 0、`go test -count=1 -race ./...` 23 個 package ok；SKIP 與 0.2 記錄相同（runtime live／contract 前置條件缺少），未觸及 `internal/backend`（2026-10-09，go1.27.1 linux/amd64）。
  - 精煉：合併測試 helper（`newXDGEnv` 只建目錄，`writeConfig` 寫檔），重跑 `go test -count=1 -race -run 'TestConfig' ./cmd/asura` ok。

## Phase 4 — Doctor

### Task 4.1 — Adapter executable 旁路
- 產出與範圍：`internal/backend` 各 adapter 提供僅供 doctor 讀取的 probe 實際 executable 路徑（OpenCode 含固定 cache fallback）；`RuntimeInventoryEntry`、ExecutionContract 與 `runtime-inventory-v1` 不變。
- 相依：無
- 驗收編號：支援 DIST-006／007
- 完成判準：各 adapter 回報的路徑等於其 probe 實際執行的 binary（以 PATH 與 cache 同時存在的 fixture 區分）；無法提供時回 unknown；既有 inventory／contract 序列化 golden 不變。
- 驗證：adapter 單元測試；backend live／contract SKIP 名稱與原因記錄。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`internal/backend/inventory.go` 新增 `ExecutableReporter` 介面；OpenCode 在 Probe 中記錄實際執行的 binary（cache fallback、PATH 或明確 BinaryPath；`atomic.Pointer` 避免與逾時中的 probe goroutine 競態），`RoleRoutingBackend` 回報 striker；codex 的 Probe 改用同一個 `ProbeExecutable` 解析，確保顯示與執行一致；OMP／Antigravity 回報建構時解析的絕對路徑。`RuntimeInventoryEntry`、ExecutionContract 結構與 `runtime-inventory-v1` 未修改（`git diff` 無結構變更）。
  - 紅燈：以回傳空字串的方法骨架執行 `go test -count=1 -run 'TestProbeExecutable' ./internal/backend/...` → opencode（cache fallback 應回報 cache 路徑卻得到空字串）、codex（PATH 上的 codex 回報空字串）、omp、antigravity 4 個 FAIL。
  - 綠燈：同命令加 `-race` 全數 PASS：OpenCode 同時存在 cache 與 PATH binary 時回報 cache、移除 cache 後回報 PATH、兩者皆無時為空、版本不符的明確 binary 仍回報、RoleRouting 回報 striker；codex 回報 PATH／明確路徑；OMP／Antigravity 回報明確路徑。
  - 過程記錄：首輪 OMP／Antigravity 在找不到 binary 時回報 `"."`——既有 `NewAdapter` 對空字串執行 `EvalSymlinks` 得到 `.`；未改動該既有行為，`ProbeExecutable` 改為只回報絕對路徑。
  - 回歸：`go vet ./...` exit 0；`go test -count=1 -race ./cmd/asura ./internal/...` 全部 ok；`go test -count=1 -race -v ./internal/backend/...` 全部 ok（2026-10-09，go1.27.1 linux/amd64）。
  - backend SKIP（不計為通過）：`TestLiveAdapter`（omp、antigravity：live OAuth model call opt-in）；`TestCodexProbe_BinaryVersion`、`TestCodexProbe_NonInteractive_JSONL`、`TestCodexProbe_ReadOnlySandbox`、`TestCodexProbe_WorkspaceWriteSandbox`、`TestCodexProbe_ContextCancellation`、`TestAdapter_Run_Live`、`TestCodexProbe_Live`：本機 codex 為 `codex-cli 0.160.1`，與固定版本 0.155.0 不符。
  - 精煉：no-op——每個 adapter 只新增一個方法，codex 已讓 Probe 與回報共用同一解析；沿用上列綠燈。

### Task 4.2 — `asura doctor` readiness
- 產出與範圍：`asura doctor`：system（Linux、arch、`/proc`、bwrap 需求、path 權限評估標明未實際寫入）、config（解析、權限、socket 長度、allowed roots）、runtime（本機 registry＋CaptureInventory＋4.1 executable）、user service 狀態、daemon 連線（2 秒）；`READY`／`DEGRADED` exit 0、`NOT_READY` exit 1、用法錯誤 exit 2；optional runtime／service 缺項只警告。
- 相依：3.1、4.1
- 驗收編號：DIST-006、DIST-007
- 完成判準：stub runtime 組合分別得到 READY、DEGRADED（optional 缺失或版本不符、無 user service）、NOT_READY（無 runtime、必要 system／config 失敗）與對應 exit；輸出含有界 reason 與能力摘要；不提交 Task、不呼叫模型（stub 記錄）。
- 驗證：CLI 外迴圈 + 判定邏輯單元測試，雙迴圈。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`cmd/asura/doctor.go`（System／Config／Runtimes／Service 四段檢查、`doctorStatus`、以 `backend.RequirementsForMode` 比對能力的 `availableModes`、只用 metadata 與 access(2) 的 `assessDir`、有界 `systemctl --user is-active asura.service` 與 2 秒 daemon dial）；`inventory.go` 抽出 `newLocalRegistry` 供 inventory 與 doctor 共用；`config.go` 拆出不建立目錄的 `socketPath`（`DefaultSocketPath` 行為不變）；`run.go` 加 `doctor` dispatch；測試 `cmd/asura/doctor_test.go`。
  - 紅燈：以回傳 -1／零值的骨架執行 `go test -count=1 -run 'TestDoctor' -v ./cmd/asura` → 9 個（子）測試 FAIL（狀態判定、模式、DEGRADED 輸出、四種 NOT_READY、用法錯誤）。
  - 綠燈：`go test -count=1 -race -run 'TestDoctor' -v ./cmd/asura` 全 PASS。單元：`levelWarn`→DEGRADED/0、`levelFail`→NOT_READY/1、全 ok/info→READY/0；只有 workspace_write 的 runtime 只得 direct，加上 read_only_sandbox 得 direct＋three-face。CLI（stub runtime、隔離 HOME／XDG）：codex 可用＋opencode 版本不符 → `DEGRADED` exit 0，列出 `executable=<stub 路徑>`、`reason=version_failed`、omp／antigravity 原因、`systemctl not found`、daemon not running、`permission check only; nothing was written` 與不驗證登入的聲明；stub 只收到 `--version`（無模型呼叫或 Task）。無 runtime、壞 config（含 sentinel，不回顯）、DB 目錄不可寫、allowed root 不存在 → `NOT_READY` exit 1；多餘參數 exit 2。
  - 過程中發現並修正：opencode 版本不符時 `executable=unknown`——RoleRouting 先探唯讀角色並提前返回，4.1 只回報 striker；改為 striker 無結果時回報唯讀角色。先強化 `TestProbeExecutableReportsProbedBinary`（RoleRouting 在唯讀角色 probe 失敗後仍須回報路徑），以 4.1 版程式取得紅燈（`role routing after read-only probe failure: ""`），修正後綠燈。
  - 回歸：`go vet ./...` exit 0、`go build ./cmd/asura` exit 0、`go test -count=1 -race ./...` 全部 ok（2026-10-09，go1.27.1 linux/amd64）；backend SKIP 同 4.1。
  - 精煉：no-op——檢查項以單一 `add` 收集、輸出統一格式，未見可再簡化之處；沿用上列綠燈。
  - 範圍說明：整體 10 秒 deadline、輸出上限與 secret sentinel 屬 4.3。READY 需四個 runtime 與 user service 皆可用，本機無法在測試中提供 OMP／Antigravity 的固定 hash binary，READY 由單元測試覆蓋。

### Task 4.3 — Doctor secret 與唯讀邊界
- 產出與範圍：整體 10 秒 deadline、輸出上限、只輸出固定 reason、ENV／config 只顯示是否設定與路徑；不開 SQLite、不寫 config／DB／credential、不建立 unit；runtime 探測暫存只在私有 temp 並清理。
- 相依：4.2
- 驗收編號：DIST-008
- 完成判準：ENV、credential 檔與 probe stderr 植入 sentinel，probe 失敗與逾時（含掛起的 daemon peer）下 stdout／stderr 皆無 sentinel；doctor 在 deadline 內結束；前後 config／DB／credential hash 與 user unit 目錄不變；私有 temp 無殘留。
- 驗證：CLI 外迴圈；先以掛起 peer／洩漏 stub 取得紅燈。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`doctor.go` 以 10 秒整體 context 涵蓋 inventory、systemctl 與 daemon dial；systemctl 以獨立 process group 執行，逾時 SIGKILL 整組並設 `WaitDelay`；每項 detail 上限 512 bytes 並標 `...(truncated)`。測試 `cmd/asura/doctor_safety_test.go`。
  - 紅燈：`go test -count=1 -run 'TestDoctorSecretSafetyAndReadOnly|TestDoctorBoundsLongDetails' -v ./cmd/asura` → 前者 `doctor exceeded its deadline: 33.005956786s`（stub systemctl 的子行程 sleep 持有輸出管線，`exec.CommandContext` 逾時後仍等待管線關閉），後者 `unbounded line: 2074 bytes`。首輪因 stub 使用的 `sleep` 不在測試 PATH 上而未真正卡住、以錯誤原因失敗，改用 `sleep` 絕對路徑後才取得上述紅燈。
  - 綠燈：`go test -count=1 -race -run 'TestDoctor' -v ./cmd/asura` 全 PASS；安全測試 5.01 秒完成（≤ 10 秒期限）。情境：`OPENAI_API_KEY`／`ASURA_WEB_KEY`、`~/.codex/auth.json` 與失敗 probe 的 stdout／stderr 都植入 `SECRET-SENTINEL-d0c7`，opencode probe 與 systemctl 掛住、daemon socket 只接受不回應；輸出不含 sentinel、回報 `asura.service status unavailable (timeout)`、NOT_READY exit 1；HOME／XDG 整棵目錄（config、DB fixture、credential、runtime dir）前後路徑與內容雜湊一致、未建立 `~/.asura` 或 systemd unit 目錄、cwd 無新檔、私有 TMPDIR 無殘留；每行 ≤ 600 bytes。超長 `ASURA_SOCKET` 的 detail 被截斷並標記。
  - 回歸：`go vet ./...` exit 0；`go test -count=1 -race ./...` 全部 ok（2026-10-09，go1.27.1 linux/amd64）。
  - 精煉：no-op——期限以單一 context 傳遞，截斷集中在 `add`；沿用上列綠燈。

## Phase 5 — User service 與 setup

### Task 5.1 — `asura service` 子命令與 unit
- 產出與範圍：`asura service install|start|stop|restart|status|uninstall`；unit 含 binary 絕對路徑（systemd 規則跳脫、不經 shell）、`KillMode=mixed`、`TimeoutStopSec=60s`、`Restart=on-failure`、`RestartSec=5s`、`StartLimitIntervalSec=60s`、`StartLimitBurst=3`；僅 setup 時非預設 `XDG_CONFIG_HOME` 寫入 `Environment=`；無 secret、不設 linger；非 Asura 管理的同名 unit 拒絕處理；無 systemd／user bus 時明確原因；uninstall 只移除受管理 unit。
- 相依：3.2
- 驗收編號：DIST-014、DIST-015
- 完成判準：systemctl stub 記錄的 argv 與 unit 內容符合上列；無 systemd 時 service 回明確原因且 standalone daemon 依 config 可啟動、doctor 為 DEGRADED exit 0；uninstall 前後 config 與 SQLite／Task／Memory 可讀且 hash 不變；不啟動其他 supervisor。
- 驗證：CLI 外迴圈 + unit 生成單元測試；systemctl stub 不等於 lifecycle 證據（見 5.3）。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`cmd/asura/service.go`（`renderUserUnit`、`checkManagedUnit` 以首行標記辨識受管理 unit、同目錄暫存檔加 rename 的 `writeUnit`、獨立 process group 且有逾時的 `systemctl --user` 呼叫；install 需有效 config，寫入 unit 後 daemon-reload 並 enable、不啟動；start／stop／restart 最多等 90 秒；status 只輸出 is-active／is-enabled 的第一個字；uninstall 執行 `disable --now`、刪 unit、daemon-reload）；`run.go` 將 `service` 納入 config 載入與 dispatch；`doctor.go` 改用共用的 `serviceUnit`／`userUnitPath`。測試 `cmd/asura/service_test.go`。
  - 紅燈：以空骨架執行 `go test -count=1 -run 'TestService|TestRenderUserUnit' -v ./cmd/asura` → 9 個（子）測試 FAIL。
  - 綠燈：`go test -count=1 -race -run 'TestService|TestRenderUserUnit|TestDoctor' -v ./cmd/asura` 全 PASS。unit 字面比對含 `KillMode=mixed`、`TimeoutStopSec=60s`、`Restart=on-failure`、`RestartSec=5s`、`StartLimitIntervalSec=60s`、`StartLimitBurst=3`，路徑中的空白、`"`、`%`、`$`、`\` 依 systemd 規則跳脫且不經 shell；非預設 `XDG_CONFIG_HOME` 寫入 `Environment=`，預設值不寫；unit 0644、無 linger／secret；重跑 install 不產生重複 unit；systemctl stub 記錄的 argv 為 `show-environment`→`daemon-reload`→`enable asura.service`、各 verb、`disable --now`→`daemon-reload`；非 Asura 的同名 unit 在 install／uninstall 皆被拒且內容不變、未呼叫 enable／disable；無 config 時 install 提示 `asura setup`；uninstall 後 config 與 DB 雜湊不變、`kept-task` 與 `kept-memory` 可讀，再次 uninstall 回報 not installed；用法錯誤 exit 2。
  - DIST-014：無 systemctl 時六個子命令皆 exit 1 並提示 `systemctl not found; run \`asura daemon\` standalone`；user bus 不可用時 install 失敗且未寫入 unit 目錄。standalone daemon 依 config 啟動由 3.2 `TestConfigDaemonPrecedence` 證明，無 systemctl 時 doctor 為 DEGRADED exit 0 由 4.2 `TestDoctorDegradedWithOneRuntime` 證明（同一被測版本，證據重用）；service 程式只呼叫 systemctl，未啟動其他 supervisor。
  - 回歸：`go vet ./...` exit 0；`go test -count=1 -race ./...` 全部 ok（2026-10-09，go1.27.1 linux/amd64）。
  - 精煉：no-op——systemctl 呼叫集中於單一 helper，unit 字串由單一函式產生；沿用上列綠燈。
  - 限制：systemctl 為 stub，真實 user bus lifecycle 由 5.3 驗證。

### Task 5.2 — `asura setup` 互動／腳本模式與冪等
- 產出與範圍：`asura setup`：TTY 互動選 allowed roots 與 service；非 TTY 需 `--allowed-roots`，僅 `--service` 安裝並啟動 service；建立 XDG config（0600）／data（0700）；顯示 doctor 結果與下一步；重跑保留設定與資料，參數衝突明確失敗並提示手動修改；不安裝／登入 runtime、不改 shell rc、不啟動 Web、不搬移舊 DB，偵測舊 DB 時顯示指定舊路徑的指引；半途失敗不宣稱完成。
- 相依：5.1
- 驗收編號：DIST-011、DIST-012
- 完成判準：fresh 非互動建立的 config 為 `schema_version = 1` 且只含指定 root；互動以 pty 驅動；非 TTY 缺 root 立即失敗不等待 stdin；重跑前後 config／DB／credential／unit hash 不變且無重複 unit；衝突參數非零；注入寫入失敗後重跑可完成。
- 驗證：CLI 外迴圈（含 pty）+ 合併／衝突判定單元測試。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：`cmd/asura/setup.go`（終端機判定用 `TCGETS`；新建 config 以 toml encoder 產生 `schema_version`／`allowed_roots`，0700 目錄、0600 檔案，經 link 發佈不覆寫既有檔；既有 config 僅比對 allowed roots，不同即拒絕並指出檔案；以 `prepareDirs` 建 data 目錄但不初始化 SQLite；偵測 cwd `asura.db` 只提示設定 `db`；`--service` 或互動同意才呼叫 service install＋start；最後顯示 doctor 結果）；`run.go` 將 `setup` 納入 config 載入與 dispatch。測試 `cmd/asura/setup_test.go`。
  - 紅燈：以回傳 -1 的骨架執行 `go test -count=1 -run 'TestSetup' -v ./cmd/asura` → 6 個測試 FAIL。
  - 綠燈：`go test -count=1 -race -run 'TestSetup' -v ./cmd/asura` 6 PASS。fresh 非互動：config 只含指定 root、未設 db／socket，config 0600、目錄 0700、data 目錄 0700；輸出含 doctor 結果、`codex: available` 與未啟用 service 的下一步；除 config 與 data 目錄外 HOME／XDG 無其他變更（credential 未動、無 DB、無 unit、無 shell rc），未呼叫 enable／start。非 TTY 缺 root 立即失敗且未建 config；root 不存在被拒。`--service`：呼叫 enable 與 start；重跑（含使用者追加 `workers = 4` 與既有 DB）後 config、DB、unit 雜湊皆不變，unit 只有一個；不同 root 的重跑 exit 1，指出 config 路徑且不改寫。enable 失敗時輸出 `Setup incomplete`、未宣稱完成，修正後重跑完成且 unit 不重複。cwd 舊 `asura.db` 只提示、內容不變。互動模式以 util-linux `script` 提供 pty，提示 allowed roots 與 `[y/N]`，回答 root 與 `n` 後建立 config 且未安裝 service。
  - 過程記錄：首輪 fresh 測試把 systemctl stub 的呼叫紀錄檔（setup 結尾執行 doctor 時查詢 service 狀態所寫）當成使用者狀態變更；該檔為測試設施，比對時排除後通過，其餘比對範圍不變。
  - 回歸：`go vet ./...` exit 0；`go test -count=1 -race ./...` 全部 ok（2026-10-09，go1.27.1 linux/amd64）。
  - 精煉：no-op——流程依序一次執行、失敗統一經 `incomplete` 回報；沿用上列綠燈。

### Task 5.3 — 真實 systemd user bus lifecycle
- 產出與範圍：在可用 user bus 的主機，以正式 unit 名 `asura.service` 與暫時 config（db／socket 指向暫存目錄）執行 install／start／stop／status／uninstall；systemd user manager 只從自身搜尋路徑載入 unit，故不採暫存 XDG unit 路徑，改以事前確認路徑不存在、事後全部移除保持隔離；驗證 60 秒停止期限、異常退出 5 秒重啟與 60 秒內 3 次上限、手動 stop 不重啟、中斷任務下次啟動標記 failed。
- 相依：5.1
- 驗收編號：DIST-013
- 完成判準：`systemctl --user show` 與 journal 記錄的狀態轉換、時間與重啟次數符合核准值；長任務 fixture 逾時被回收且下次啟動為 failed、未重跑；測試後無殘留 unit。缺 user bus 時記未驗證，不以 stub 代替。
- 驗證：手動／腳本化真實環境驗證，記錄主機、systemd 版本與命令輸出；不屬 `go test` 預設套件。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 授權：2026-10-09 使用者對「可以在這台機器上跑嗎？」選擇「在本機執行並清理（建議）」。
  - 腳本：`docs/issues/issue-45/evidence/systemd-lifecycle.sh`（需 `ASURA_LIFECYCLE_CONFIRM=1`；`~/.config/asura`、unit 或 drop-in 已存在即拒絕執行）。daemon 由 `go build` 產出的 branch binary 執行；測試專用 drop-in 只設定 PATH 以使用 stub codex（版本探測回 0.155.0，執行任務時 `sleep 600`），正式 unit 不含 PATH。
  - 環境：本機 systemd 259（259.5-0ubuntu3.4）user bus，x86_64；執行前 `asura*` unit 0 個、Linger=no。
  - 結果：`ASURA_LIFECYCLE_CONFIRM=1 sh docs/issues/issue-45/evidence/systemd-lifecycle.sh` exit 0、`== LIFECYCLE OK`。`systemctl --user show` 實測 `KillMode=mixed`、`TimeoutStopUSec=1min`、`Restart=on-failure`、`RestartUSec=5s`、`StartLimitIntervalUSec=1min`、`StartLimitBurst=3`、`UnitFileState=enabled`、`FragmentPath=~/.config/systemd/user/asura.service`；unit 無 linger／key／token／secret 字樣。長任務 running 時 `asura service stop` 耗時 61 秒後 unit 停止，stub 進程全數被回收；再次 start 後該 Task 為 `failed` 且未重跑。手動 stop 後 8 秒仍 inactive、NRestarts 不變。`reset-failed` 後 start，連續 SIGKILL 主進程：第 1、2 次各 5 秒後重啟，第 3 次後停在 `failed`、`MainPID=0`、`NRestarts=3`，journal 有 `Start request repeated too quickly`。uninstall 移除 unit，config 與 DB 雜湊不變、Task 仍可讀，Linger 未變。
  - 判準修訂（如實記錄）：首輪以 `Result=start-limit-hit` 判定限流，systemd 259 在此情境記為 `Result=signal`（unit 已停在 failed、未再啟動），判準因此誤報失敗；改為「failed 且 MainPID=0 維持 8 秒」加上 journal 限流訊息。反向自檢：若第 4 次啟動成功，unit 會是 active 且有 MainPID，新判準會失敗。另修正腳本中 `wait_for` 計數變數覆蓋外層迴圈變數的錯誤，以及 `ask` 未指定 `--backend codex` 的問題，修正後整段重跑。
  - 清理：執行後確認 `~/.config/asura`、`~/.config/systemd/user/asura.service`、`asura.service.d` 與 `/tmp/asura-lc.*` 皆不存在，`systemctl --user list-unit-files 'asura*'` 為 0。
  - 證據分類：真實 systemd user bus＋stub runtime。

## Phase 6 — 文件、clean onboarding 與完整回歸

### Task 6.1 — 常青文件與使用指南
- 產出與範圍：README（安裝、指令一覽）、`docs/guides/cli-reference.md`（version／doctor／setup／service 與 config precedence）、安裝／設定／service／移除與資料保留說明、`docs/guides/runtimes.md`（doctor 與 inventory 關係）、`docs/development.md` issue 索引、`CHANGELOG.md` Unreleased 條目（版本節於發佈 v1.1.0 時建立）。
- 相依：2.3、4.3、5.2
- 驗收編號：交付成果（支援 DIST-015）
- 完成判準：文件中的命令、flag、路徑與實作一致；相對連結存在；移除步驟與資料位置明列。
- 驗證：不改變可觀察行為；以連結與命令存在性的靜態檢查腳本為等價證據。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 產出：新增 `docs/guides/setup.md`（installer、version、doctor 狀態與 exit、config 欄位／XDG／相容模式／precedence 對照表／fail-closed 規則、setup、service lifecycle 與 60 秒停止期限／重啟政策、移除步驟與資料位置）；更新 `README.md`（安裝改為 installer＋doctor＋setup、v1.0.0 手動下載說明、指令一覽新增 version／doctor／setup／service、文件清單）、`docs/guides/cli-reference.md`（config precedence 說明與四個新子命令）、`docs/guides/runtimes.md`（doctor 與 inventory 的關係、executable 不進封存契約）、`docs/development.md`（Issue 45 索引）、`CHANGELOG.md`（Unreleased 新增功能、建置與相容性條目）。
  - 不改變可觀察行為：純文件。等價證據為 2026-10-09 從 repo 根目錄執行的靜態檢查（Python 內嵌腳本）：掃描上列 6 份文件的 102 個相對連結，目標檔案皆存在，`#錨點` 依 GitHub slug 規則（小寫、移除非字母數字、空白轉連字號）皆對應到實際標題；以 `go build` 產出的 binary 核對文件提到的 `version -json`、`setup -allowed-roots／-service`、`Usage: asura doctor`、`service install|start|stop|restart|status|uninstall` 皆出現在實際 usage；`install.sh` 含 `--version`、`--install-dir` 與文件所列入口 URL。結果：`PASS: links, anchors, commands and flags`。
  - 內容依據：各段行為對照 2.1～5.2 的測試與實作（例如 `version --json` 以 Go map 序列化，鍵依字母排序）；未宣稱尚未驗證的真實 systemd lifecycle 或正式 Release。

### Task 6.2 — Clean Linux onboarding 與 release 契約
- 產出與範圍：無 Go／repo 的 clean Linux 容器，以 1.2 相同命令建出的 HTTP fixture release 執行 install → doctor → setup → standalone daemon；核對 asset 名稱、archive 路徑、tag／version／commit／arch 與 checksum。正式 v1.1.0 Release 發佈後以真實 URL 重跑 install 與 version 核對。
- 相依：1.2、2.3、5.2
- 驗收編號：DIST-016
- 完成判準：fixture 流程全通且證據標明 HTTP fixture／stub runtime；發佈後真實 Release 驗證另記，未發佈前狀態標為等待外部驗收窗（窗口為 v1.1.0 tag 發佈日），不以 fixture 代替。
- 驗證：容器端到端腳本；記錄映像、命令與輸出。
- 狀態：⏳ 進行中（2026-10-09：HTTP fixture 端到端已通過；正式 v1.1.0 Release 驗證待發佈後執行）
- 證據：
  - 產出：`scripts/release/onboarding-e2e.sh`——以 `build-archives.sh` 建出 `v1.1.0` fixture Release（commit 為當下 HEAD），由本機 `python3 -m http.server` 提供；`alpine:3.20.3` 容器（busybox，無 Go、無 repo、無 curl、無 systemd）以 uid 1000 執行，GitHub URL 經容器內 wget wrapper 改寫到 fixture，runtime 為 codex stub。
  - 執行：`sh scripts/release/onboarding-e2e.sh`（2026-10-09，主機 x86_64，docker 28.1.1）→ 結尾 `== ONBOARDING OK`。觀察：`wget … install.sh | sh` 以 busybox wget／tar／sha256sum 完成安裝（請求依序為 install.sh、releases/latest、checksums、amd64 archive，未請求 arm64）；`version --json` 為 `{"arch":"amd64","commit":"abed0dfa21cb30926060fa64791357cc3762a5d0","os":"linux","version":"v1.1.0"}`，與 tag／commit／arch 一致；doctor 為 DEGRADED exit 0（無 bwrap、無 systemctl、只有 stub codex）；`setup --allowed-roots ~/project` 建立 config 並顯示 doctor 結果與 `Setup complete.`；standalone daemon 啟動於 `~/.local/state/asura/asura.sock (workers: 2, queue: 10)`，`runtime inventory` 經 config socket 取得結果，SIGTERM 後 exit 0。
  - 過程記錄：首輪容器 HOME 使用 Docker 預設 `noexec` 的 tmpfs，installer 以 `staged binary cannot report its version` 拒絕替換（防護如預期生效）；改為 `exec` tmpfs 後通過。
  - 2026-10-09 腳本新增平台參數與 `arch` 比對後重跑：amd64 預設平台通過（commit `929bda4…`），arm64 結果見 2.2。
  - 證據分類：HTTP fixture＋stub runtime；asset 名稱、archive 路徑與 checksum 由 1.2 測試與本流程的 installer 校驗共同核對。尚未以正式 GitHub Release 與真實 runtime 驗證。
  - 待辦（等待外部驗收窗）：v1.1.0 tag 發佈後，以 `curl -fsSL https://raw.githubusercontent.com/raybird/Asura/main/install.sh | sh` 在 clean Linux 安裝，核對 `version --json` 的 tag／commit 與 Release asset、checksum 一致，並確認同 tag 重跑 workflow 不覆寫 asset。窗口為 v1.1.0 實際發佈日（尚未排定）。

### Task 6.3 — 完整回歸與相容矩陣
- 產出與範圍：無 config、有 config、CLI override 三種狀態下 daemon／ask／run／status／cancel／audit／inventory／Cron／memory／Web 的相容矩陣；Phase 4 authority（StageBindingPlan、ExecutionContract、ExecutionAudit）不變；`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`；SKIP 逐項記名稱與原因。
- 相依：0.1、2.3、3.2、4.3、5.2、5.3、6.2（HTTP fixture 部分；正式 Release 驗證依 README Gate 豁免紀錄於發佈後補）
- 驗收編號：DIST-017
- 完成判準：三條命令 exit 0；0.1 基線全綠；矩陣每格有結果；SKIP 清單不計為通過；觸及 `internal/web` 或 memory lifecycle 時依 ci.yml 執行對應探測。
- 驗證：回歸證據彙整，固定被測提交。
- 狀態：✅ 已完成（2026-10-09）
- 證據：
  - 新增 `cmd/asura/compat_matrix_test.go`：有 config 時 daemon 啟動於 config socket，`cron list`、`memory list` 經 config socket 成功、加 `--socket` 指向不存在路徑即失敗，`web -h` 的預設 socket 為 config 值。行為已由 3.2 實作，屬回歸刻畫無紅燈；變異實驗（`socketPath` 忽略 config，事後還原）→ 首版只有 web 斷言轉紅（daemon 與 client 同步改用舊路徑而仍連得上），補上 daemon 啟動行斷言後於啟動行即轉紅。
  - 相容矩陣（指令 × 無 config／有 config／CLI 覆寫 → 測試）：daemon → `TestNoConfigDaemonDefaults`／`TestConfigDaemonPrecedence`（config-over-builtin、xdg-when-config-omits-paths）／`TestConfigDaemonPrecedence`（env-over-config、cli-over-env-and-config）；ask → `TestNoConfigDaemonDefaults`、`TestNoConfigFlagDefaults`／`TestConfigDaemonPrecedence`／cli-over 子測試的 `--socket`；run → `TestNoConfigRunDefaults`／`TestConfigRuntimePriorityAndContext`／同測試的 ENV 覆寫與 `--db`；status、cancel、audit → `TestNoConfigLocalFallbackUsesCwdDB`／`TestConfigLocalFallbackUsesConfigDB`（含 `--db` 覆寫）；runtime inventory → 同上兩測試；cron、memory → `TestNoConfigFlagDefaults`／`TestCompatClientsUseConfigSocket`（含 `--socket` 覆寫）；web → `TestNoConfigFlagDefaults`／`TestCompatClientsUseConfigSocket`；壞 config 對全部指令 → `TestConfigInvalidFailsClosed`。
  - Phase 4 authority：`git diff --stat 1be939b -- internal/engine internal/pipeline internal/audit internal/storage internal/daemon internal/auth` 無輸出（StageBindingPlan、ExecutionContract、ExecutionAudit、SQLite schema 未變）；`internal/backend` 僅新增 `ExecutableReporter` 與各 adapter 的回報方法，inventory／contract 結構未變（4.1）。
  - 完整回歸（2026-10-09，HEAD `3311bb4` 加上本 Task 的測試檔，go1.27.1 linux/amd64）：`go vet ./...` exit 0；`go build ./cmd/asura` exit 0；`go test -count=1 -race -v ./...` exit 0，23 個 package ok，無 FAIL。
  - SKIP（不計為通過）：`TestCodexProbe_BinaryVersion`、`TestCodexProbe_NonInteractive_JSONL`、`TestCodexProbe_ReadOnlySandbox`、`TestCodexProbe_WorkspaceWriteSandbox`、`TestCodexProbe_ContextCancellation`、`TestAdapter_Run_Live`、`TestCodexProbe_Live`（本機 codex 0.160.1 ≠ 固定 0.155.0）；`TestINT001`、`TestINT007`、`TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly`（本機 opencode 1.18.31 ≠ 固定 1.18.23）；`TestLiveAdapter` ×2（live OAuth model call opt-in）；`TestUtilityLiveProbe`（需 `ASURA_UFL_PROBE_DIR`）；`TestUtilityCapacityProbe`（opt-in 10k/100k SQLite probe）。
  - 本次未修改 `internal/web` 與 storage memory lifecycle，依 project.md 不需加跑 ci.yml 的 Web UI／lifecycleperf 探測；CI 會照常執行。

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
