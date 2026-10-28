# Issue 74 實作計畫：取消時回收整棵行程樹

## 設計方案

### 共用實作

`opencode.KillProcessGroup(pgid int) error` 是所有取消路徑的共用入口，簽名不變，行為改為：

1. 以 `/proc/<pid>/stat` 找出 process group `pgid` 的所有成員，再沿 PPID 找出這些成員的所有子孫。子孫另開 session 或 process group 都一樣算。
2. 對找到的行程送 `SIGSTOP`，再重新掃描一次；有新的子孫就繼續凍結，直到集合不再變大。這樣可以避免掃描和 kill 之間又 fork 出新行程。
3. 對整個集合送 `SIGKILL`，最後送 `kill(-pgid, SIGKILL)`。Codex 的 PID namespace init 也在集合裡；它被 kill 時，核心會一併回收 namespace 內的所有行程。
4. `ESRCH`（行程已經結束）視為正常。group 已經沒有成員時，維持現行行為：回傳 nil。

身分檢查（`VerifyTaskProcessGroup`、J-01／K-01／L-01／M-01）維持在呼叫端，順序不變：先確認 group 屬於該任務，再呼叫 `KillProcessGroup`。子孫只從已確認的 group 成員往下找，不會擴及無關行程（SCN-003）。

Codex adapter 行程內取消的 `syscall.Kill(-pgid, SIGKILL)` 改呼叫同一個函式。

### cancellation probe

模型執行的命令從 `echo started > F; sleep N` 改為留下兩個子行程：一個前景 `sleep N`，一個另開 session 的背景 `sleep M`（`setsid`），然後 `wait`。判定改為：Run 返回後 5 秒內，兩個 sleep 都消失，而且 group 已不存在。

不增加模型呼叫次數。OMP、Antigravity 的命令在 clisandbox 的 PID namespace 裡，預期照樣通過，以 live 實測確認。

## 實作步驟

### 步驟 1 — 首要驗證：重現與 OpenCode 行程樹
- 產出：
  - `KillProcessGroup` 的確定性重現測試：假 runtime 留下以下子孫，先用現行實作證明會有殘留（紅燈）。
    - 另開 session 的子孫
    - 另開 process group 的子孫，底下再有子孫
  - 無關行程的對照測試：另一個不是 group 子孫的行程，必須保留。
  - live 觀察：用 OpenCode 1.18.31 執行一次加強後的命令（1 次模型呼叫），以 `ps -o pid,ppid,pgid,sid` 記錄行程樹。
- 相依：無
- 驗收編號：SCN-001、SCN-003（重現與回歸網）
- 完成判準：
  - 重現測試以「殘留」為理由失敗。
  - 對照測試通過。
  - OpenCode 的行程樹已記錄，結論為以下其中一個：
    - 取消當下所有命令都是 group 成員的子孫：照計畫進行。
    - 不是：停下來，向使用者回報，不擅自擴大範圍。
- 驗證：單元紅燈；live 觀察記錄於 `evidence/step1.md`
- 狀態：📝 待實作
- 證據：

### 步驟 2 — 共用實作回收整棵子孫樹
- 產出：`KillProcessGroup` 依設計方案實作；Codex adapter 改用它
- 相依：步驟 1
- 驗收編號：SCN-001、SCN-003
- 完成判準：
  - 步驟 1 的重現測試轉綠，對照測試維持綠燈。
  - 既有的取消、身分檢查、H-01 測試全部通過。
  - 凍結與重新掃描的部分有測試涵蓋：取消期間仍在 fork 的子孫也會被回收。
- 驗證：單元測試；`go test -race` 全套
- 狀態：📝 待實作
- 證據：

### 步驟 3 — 外部取消路徑
- 產出：外部 `asura cancel` 回收同一棵行程樹的測試，以假 runtime 留下另開 session 的子孫
- 相依：步驟 2
- 驗收編號：SCN-002
- 完成判準：
  - 拿掉步驟 2 的修正時，測試會失敗。
  - 加回修正後通過，任務狀態為已取消。
- 驗證：`cmd/asura` 整合測試（現有的 cancel 測試架構）
- 狀態：📝 待實作
- 證據：

### 步驟 4 — 加強 cancellation probe
- 產出：probe 命令與判定依設計方案修改；單元測試涵蓋「背景 setsid 子行程殘留 → 失敗」
- 相依：無，可與步驟 2 並行；live 驗收需要步驟 2
- 驗收編號：SCN-004
- 完成判準：
  - 單元測試：假 runtime 只回收前景 sleep 時，probe 失敗，訊息列出殘留的 PID。
  - live 測試：
    - Codex 0.163.0、OpenCode 1.18.31 的 cancellation probe，在步驟 2 之後通過。
    - 用步驟 2 之前的實作（失效對照）執行時會失敗。
    - OMP、Antigravity 的 cancellation probe 照樣通過。
- 驗證：單元紅綠；live 結果記錄於 `evidence/step4.md`
- 狀態：📝 待實作
- 證據：

### 步驟 5 — Codex exec 唯讀對應（#73 TBD-7）
- 產出：一個 live 測試，以 `codex exec -s read-only` 讓模型執行 sleep，在命令執行期間讀取 `codex-linux-sandbox` 的 `--permission-profile`，與 `codex sandbox -P :read-only` 的權限設定比較
- 相依：無
- 驗收編號：SCN-006
- 完成判準：
  - 除了路徑之外，兩份權限設定的欄位一致，workspace 都不是 `write`。
  - 不一致時：測試失敗，並向使用者回報。
- 驗證：live（1 次模型呼叫），結果記錄於 `evidence/step5.md`
- 狀態：📝 待實作
- 證據：

### 步驟 6 — Codex 端到端與文件
- 產出：
  - Codex 0.163.0 的真實端到端：驗證前拒絕；verify 後 daemon、inventory、doctor、本機 run 都放行；remove 後回到拒絕。做法同 #73 的 `evidence/e2e.md`。
  - `docs/guides/runtimes.md`：probe 表與已知限制，移除 Codex 的 #74 限制，補 TBD-1。
  - CHANGELOG。
- 相依：步驟 2、4、5
- 驗收編號：SCN-005、常青文件
- 完成判準：
  - 端到端四個階段都有實際輸出。
  - 結束後 `~/.local/share/asura/` 還原到開始前的狀態。
  - 文件與程式一致。
- 驗證：live 端到端，記錄於 `evidence/e2e.md`；文件人工核對
- 狀態：📝 待實作
- 證據：

## 使用方式對照

使用者操作不變。取消任務（`asura cancel`、Ctrl-C、daemon 取消）之後，Codex 與 OpenCode 由模型啟動的命令不再繼續執行。Codex 新版本可以透過 `asura runtime verify codex` 驗證後使用。

## 測試策略與驗收對應

| 驗收編號 | 層級 | 證據 |
|---|---|---|
| SCN-001 | 單元（確定性，假 runtime）＋ live probe | 步驟 1、2、4 |
| SCN-002 | `cmd/asura` 整合測試 | 步驟 3 |
| SCN-003 | 單元對照測試；既有身分檢查測試 | 步驟 1、2 |
| SCN-004 | probe 單元紅綠＋ live 失效對照 | 步驟 4 |
| SCN-005 | live 端到端 | 步驟 6 |
| SCN-006 | live 觀察 | 步驟 5 |

live 測試需要 `ASURA_VERIFY_LIVE=1` 與登入憑證。各步驟的證據要列出被 skip 的測試，skip 不算通過（project.md「綠燈不等於有跑到」）。

## 風險與首要驗證

見 [README.md](./README.md)「風險與首要驗證」；首要驗證即步驟 1。

## 檢查清單

- [ ] 步驟 1：重現紅燈、對照綠燈、OpenCode 行程樹已記錄
- [ ] 步驟 2：共用實作，全套測試綠燈
- [ ] 步驟 3：外部取消測試先紅後綠
- [ ] 步驟 4：probe 加強，live 四個 runtime 結果與失效對照
- [ ] 步驟 5：Codex exec 唯讀對應
- [ ] 步驟 6：Codex 端到端、文件、CHANGELOG
- [ ] 專案 gate 通過，skip 已列出
