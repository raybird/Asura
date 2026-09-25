# Git 安全與邊界探測報告（Task 1.1）

2026-09-25 11:25 CST。工作區基準：Commit `04441ec`。執行測試套件：`tests/integration/probe_git_boundary_test.go`。
本探測使用隔離的臨時目錄環境，未修改任何生產程式碼或既有資料。

---

## 一、探測目標與方法

為驗證 H-TBD-2、H-TBD-3 之安全防護與有界讀取假設，構建包含以下負向情境的測試環境：
1. **工作樹唯讀保護**：執行 `rev-parse`、`log`、`diff` 後比對 HEAD SHA、`.git/index` SHA-256 雜湊與 `git status --porcelain`。
2. **惡意 External Diff 阻斷**：在儲存庫 `.git/config` 設定 `[diff] external = <malicious_script>`，比對無防護與帶安全旗標時金絲雀檔案（Canary）是否產生。
3. **二進位檢測**：探測含 `\x00` 之二進位檔案在 `git diff --numstat` 的輸出特徵。
4. **子程序回收**：探測 Process Group（`Setpgid: true`）與 `syscall.Kill(-pgid, syscall.SIGKILL)` 在超時取消時的回收效率。
5. **記憶體防護**：驗證 `io.LimitReader` 是否精確阻斷超限輸出（256 KiB）。
6. **GitDir 越界逃逸**：探測 `.git` 以指標檔案（`gitdir: ...`）指向 `allowedRoots` 外部時之檢測機制。

---

## 二、實際執行結果與日誌

執行命令：
```bash
go test -v -run TestProbe_GitBoundary ./tests/integration/
```

輸出紀錄：
```text
=== RUN   TestProbe_GitBoundary_ReadOnlyWorktree
--- PASS: TestProbe_GitBoundary_ReadOnlyWorktree (0.02s)
=== RUN   TestProbe_GitBoundary_ExternalDiffBlocked
    probe_git_boundary_test.go:186: Confirmed: unsafe git diff triggered external diff script!
    probe_git_boundary_test.go:216: PASS: External diff script was completely suppressed by safe flags.
--- PASS: TestProbe_GitBoundary_ExternalDiffBlocked (0.02s)
=== RUN   TestProbe_GitBoundary_BinaryAndStats
    probe_git_boundary_test.go:253: PASS: Git numstat accurately reports binary file with '-' sentinel.
--- PASS: TestProbe_GitBoundary_BinaryAndStats (0.02s)
=== RUN   TestProbe_GitBoundary_ProcessGroupKill
    probe_git_boundary_test.go:288: PASS: Process group killed cleanly in 200.515695ms
--- PASS: TestProbe_GitBoundary_ProcessGroupKill (0.20s)
=== RUN   TestProbe_GitBoundary_LimitReader
    probe_git_boundary_test.go:303: PASS: LimitReader strictly bounds output to 256 KiB.
--- PASS: TestProbe_GitBoundary_LimitReader (0.00s)
=== RUN   TestProbe_GitBoundary_GitDirEscape
    probe_git_boundary_test.go:358: PASS: Successfully detected external gitdir escape (/tmp/.../outside/external.git outside /tmp/.../allowed)
--- PASS: TestProbe_GitBoundary_GitDirEscape (0.00s)
PASS
ok  	asura/tests/integration	0.268s
```

---

## 三、關鍵實證發現與設計規範

1. **旗標傳遞位置之語意要求**：
   - 全域旗標（如 `-c diff.external=`、`-c core.fsmonitor=`、`--no-replace-objects`）必須位於 `git` 與子命令（如 `diff`）之間。
   - 子命令旗標（如 `--no-ext-diff`、`--no-textconv`）若置於 `git` 之前會引發 Git exit status 129（`unknown option: --no-ext-diff`），必須作為 `diff` 之後的 argv 參數傳遞。
2. **外部 Diff 抑制有效性**：
   - 對照組：未帶保護旗標時，Git 確實呼叫了外部 shell 腳本並產生命中 canary 檔案，證實該攻擊面確實存在。
   - 實驗組：帶入 `-c diff.external=` 與 `diff --no-ext-diff --no-textconv` 後，canary 檔案未產生，Git 採用內部 diff 引擎，阻斷率 100%。
3. **工作樹零修改驗證**：
   - 執行多輪安全讀取後，HEAD SHA、Index Hash 與 Worktree porcelain 輸出皆為 0 變更，確認安全旗標不會產生暫存鎖或未提交異動。
4. **二進位檢測可靠度**：
   - `git diff --numstat` 對二進位變更精確回傳 `-       -       <file>`，可作為檢測與略過二進位檔案之確定性依據。
5. **行程樹安全退出**：
   - Process Group 終止機制於 200ms 內完成 kill，無殭屍行程殘留。
6. **GitDir 越界判定**：
   - 透過 `git rev-parse --git-dir` 結合 `filepath.EvalSymlinks`，能精確求值真實物件庫路徑，可直接套用現有 `allowedRoots` 判定邏輯。

---

## 四、結論

Task 1.1 所規劃的各項安全防護手段均獲得實證支持。實作 `internal/gitmem/git.go` 時應嚴格遵循上述旗標排列與路徑求值規範。
