# 步驟 7 證據：PR #86 審查修正（2026-11-03）

審查報告：[PR #86 留言](https://github.com/raybird/Asura/pull/86#issuecomment-6594459489)，判定 RETURN TO execute-task。使用者 Q7～Q10 都照建議，規格修訂提交於 `e0ffc67`（SCN-004 修訂、新增 SCN-008）。

## 確認審查的主要說法

- **M1**：CI run 41568584565（`b28ea30`）的 `Install bubblewrap` 步驟顯示 `Setting up bubblewrap (0.9.0-1ubuntu0.3)`。測試輸出 `backend opencode failed (backend_unavailable): bwrap namespaces unavailable`，`TestAdapter_Run_*` 與 `TestRuntimeVerificationAcrossPaths/opencode` 失敗。本機是 0.11.1，所以本機全綠。
- **M2**：讀 `internal/backend/clisandbox/run.go:80-85`，clisandbox 會以 `FailureUnsupported` 拒絕 workspace 蓋住 HOME 的情況；本 PR 原本沒有這項檢查。

## 紅燈（修改前）

`go test -count=1 -run TestStrikerSandbox ./internal/backend/opencode/`：

```
--- FAIL: TestStrikerSandbox_FailsClosedWithoutBwrap
    sandbox_test.go:257: striker error does not carry bwrap stderr: backend opencode failed (backend_unavailable): bwrap namespaces unavailable
--- FAIL: TestStrikerSandbox_InventoryRequiresOverlayCapableBwrap
    sandbox_test.go:281: bubblewrap 0.9.0: reason=none err=<nil>, want dependency_not_found
    sandbox_test.go:281: garbage: reason=none err=<nil>, want dependency_not_found
--- FAIL: TestStrikerSandbox_RejectsWorkspaceCoveringMaskedPaths
    sandbox_test.go:302: workspace .../home: opencode was executed
    sandbox_test.go:302: workspace .../asura-opencode-sandbox-1909323157: opencode was executed
    sandbox_test.go:302: workspace /: opencode was executed
```

workspace 是 `/tmp` 的那一筆只有 FailureClass 判定失敗：opencode 有執行，但它寫的標記在 HOME overlay 裡，被丟棄了。

修改後這三個測試都 PASS。

## 修改

| 審查項目 | 處理 |
|---|---|
| M1（Q7） | inventory 在找到 bwrap 之後檢查 `bwrap --version`，低於 0.10 或無法解析時回 `dependency_not_found`。寫入角色的沙箱預檢失敗時，訊息帶上 bwrap 的 stderr。CI 改從 bubblewrap 0.11.1 的 release 原始碼建置，固定 sha256 `c1b7455a…6f71`，並多一步 overlay 自我檢查 |
| M2（Q8） | `sandboxArgs` 在 workspace 等於或包含 HOME（解析 symlink 後）、`/tmp`、`/var/tmp`、`/run` 時回 `errWorkspaceCoversMask`，adapter 以 `FailureUnsupported` 拒絕 |
| S1（Q9）、S3（Q10） | 只改文件：README TBD-5、TBD-1，runtimes.md，CHANGELOG。S1 補在 #85，S3 另開 #87 |
| S2 | 只唯讀掛回執行檔本身，不掛它所在的目錄 |
| S4 | 加 `--new-session` |
| S5 | 取消測試等待 pgid 時，同時監看 Run 是否已經返回 |
| N1 | 加 `--tmpfs /var/tmp` |
| N2 | 只掛回 resolv.conf 的目標檔案，不掛整個 `/run/systemd/resolve` |
| N4 | `TestStrikerSandbox_HidesHostRunSockets` 每次都寫一個正向 sentinel，沒寫出來就失敗 |
| N3、N5、N6 | 不改，理由見 [PR #86 回覆](https://github.com/raybird/Asura/pull/86)（2026-11-03 第二輪審查後補上，第二輪審查 N-D） |

## CI 建置步驟的本機重現

本機沒有 meson，改用 docker 的 `ubuntu:24.04`（與 CI runner 同版）執行相同的建置步驟。tarball 先在本機以 `sha256sum -c` 核對過，以唯讀方式掛進容器：

```
apt-get install -y meson ninja-build libcap-dev pkg-config gcc xz-utils
meson setup _build --prefix=/usr -Dman=disabled -Dtests=false -Dselinux=disabled -Dbash_completion=disabled -Dzsh_completion=disabled
ninja -C _build && ninja -C _build install
bwrap --version  →  bubblewrap 0.11.1
```

容器只用來重現建置步驟，沒有實際跑 CI 的 AppArmor 與 namespace 檢查；那兩項以 push 後的 CI 結果為準。為此拉下來的 `ubuntu:24.04` image，執行後已刪除（本機原本沒有這個 image）。

## 沙箱參數變更後的直接探測（不呼叫模型）

用 `sandboxArgs` 產生的參數，在真實 bwrap 內執行：

```
20.27.177.113   github.com        ← getent hosts，DNS 正常
https=200                          ← curl https://api.github.com/
stub-resolv.conf                   ← /run/systemd/resolve 底下只有 resolv.conf，沒有 varlink socket
DOCKER-BLOCKED
      1 ?                          ← session id 1、沒有控制終端（--new-session）
```

## live 重跑（03:35:13Z～03:36:30Z，OpenCode 1.18.31）

```
output                        6.6s  PASS
workspace-boundary            9.4s  PASS
read-only                     7.6s  PASS
cancellation                  6.6s  PASS
--- PASS: TestVerifyLive_OpenCode (31.38s)

workspace-boundary           11.5s  FAIL: security violation: wrote outside the workspace
--- PASS: TestVerifyLive_OpenCodeSandboxControl (12.61s)
```

暫時測試（執行後已刪除）：

- 沙箱內 `opencode mcp list`：三個 MCP server 都是 `connected`，含 serena（uvx）與 mcp-memory-libsql（npx）。
- 內建 1.18.23 寫入角色：`version=1.18.23 err=<nil> output="DONE" file="after"`。

端到端（[e2e.md](./e2e.md) SCN-005）沒有重跑。這次修改的是沙箱參數與 inventory 的版本檢查，admission 與驗證紀錄的邏輯沒變。版本檢查在本機 0.11.1 上的結果，已由上面的 live verify 確認（verify 會先經過 inventory）。

## 收尾

- 暫時測試檔都已刪除，`git status` 沒有 `zz_*`。
- 第一次擷取參數失敗時留下一個空目錄 `~/.cache/zzargs-269987123`，確認是空的之後刪除。
- `ps` 沒有本次留下的 opencode 或 bwrap。列出的 `wk28-probe` 容器與 `opencode serve` 屬於另一個 session，沒有動。

## Gate（本機）

`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...`：exit 0，24 個套件 ok，沒有 FAIL。skip 28 個，與 [e2e.md](./e2e.md)「Gate」相同。都是沒有設 `ASURA_VERIFY_LIVE` 的 live 測試，不算通過。build 產生的 `asura` 已刪除；`gofmt -l` 對本分支的 Go 檔沒有輸出。

## CI（`9593a63`）

[run 41573766211](https://github.com/raybird/Asura/actions/runs/41573766211)（pull_request）結論 success。同一個 commit 的 push run 41573760635 被 workflow 的 concurrency 設定取消，這是既有行為。

- `Install bubblewrap` 步驟：`/home/runner/work/_temp/bubblewrap.tar.xz: OK`，`bubblewrap 0.11.1`，overlay 自我檢查通過。
- `TestStrikerSandbox_*`、`TestOpenCodeWithoutBwrapFailsClosed`、7 個 `TestAdapter_Run_*` 與 `TestRuntimeVerificationAcrossPaths/opencode` 都沒有 skip，也沒有失敗。
- CI 的 skip 都是 live 或環境相依的測試（`TestVerifyLive_*`、`TestCodexProbe_*`、`TestINT00*` 等），不算通過。

## 第二輪審查後（2026-11-03）

[第二輪審查](https://github.com/raybird/Asura/pull/86#issuecomment-6595150687)判定 PASS。另外指出 S-A：SCN-008 的 `/tmp`、`/var/tmp`、`/run` 沒有核准來源。Q11 選「保留擴大的範圍」，記入 README 核准來源，程式不變。

N-A：`TestStrikerSandbox_RejectsWorkspaceCoveringMaskedPaths` 原本用 `os.TempDir()` 代表 `/tmp`，設了 `TMPDIR` 就會紅燈；`/` 又同時蓋住 HOME，所以拿掉 `/var/tmp` 或 `/run` 的檢查也抓不到。改用字面路徑，每個遮蔽目錄各測一筆。以 `TMPDIR=$HOME/.cache` 執行，三個突變各自被對應的那一筆抓到：

| 突變（從遮蔽清單拿掉） | 結果 |
|---|---|
| `/tmp` | FAIL：`workspace /tmp: res=&{... FailureClass:...}`，沒有拒絕 |
| `/var/tmp` | FAIL：`workspace /var/tmp: ...` |
| `/run` | FAIL：`workspace /run: ...` |
| 無（最終版） | PASS |

N-C：runtimes.md 把「主機不允許建立沙箱」的例子補上核心不支援非特權 overlay 的情況。N-D：在 PR 補上 N3、N5、N6 不修改的理由。N-B 不修改，理由也寫在同一則 PR 回覆。

Gate：`go vet ./... && go build ./cmd/asura && go test -count=1 -race -v ./...` exit 0，沒有 FAIL；skip 28 個，與前一次相同，不算通過。`asura` 已刪除。
