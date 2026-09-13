# 提交 3cfa259 審查報告

建立日期：2026-09-13。

## 審查版本與範圍

- Reviewed HEAD SHA：`3cfa259413e8cfd52fbeca56317441bda379eac6`
- Reviewed patch-id：`e041a43d8248f2e2a02da0a150bcd166f8e7b04a`（`git diff main...HEAD | git patch-id --stable`）
- 單一提交 patch-id：`fc88365ea989413aac05356383248e135e662258`（`git diff HEAD^ HEAD | git patch-id --stable`）
- 獨立 reviewer：本次 Codex 審查工作階段，未參與受審提交實作。
- 審查範圍：最新提交相對父提交的 8 個變更檔案，以及 Storage、Daemon、Engine、OpenCode Adapter 與 CLI 呼叫端；不代表審查整個分支。
- 方法：依 gitnexus-pr-review 工作流程檢查 diff 與影響範圍；GitNexus 回報 Asura 未建立索引，改用原始碼搜尋及呼叫端交叉檢查，未使用圖譜作為證據。

## 整體評估

**patch is incorrect — RETURN TO execute-task**。新增的唯一約束能阻止新資料重複，但未處理前一版已允許寫入的重複 Request ID，會阻斷這類資料庫升級。另兩項修復未發現可確認的新缺失。

## 問題與風險

### [P1] 建立唯一索引前先處理既有重複 Request ID

位置：[internal/storage/sqlite.go:129](../../../internal/storage/sqlite.go#L129)。若資料庫已由前一版的並行重送產生相同非空 `request_id` 的多筆任務，這個新增的唯一索引會報 `UNIQUE constraint failed: tasks.request_id`，使 `NewSQLiteStore` 直接失敗；Daemon 啟動與直接開庫的 status、cancel 等 CLI 路徑都無法使用。前一版只有非唯一索引，因此這是合法既有資料在升級時新增的阻斷，而非單純延續舊去重缺陷。應先提供保留任務歷史且明確決定 Request ID 對應的遷移策略，再建立唯一索引，並補上含重複資料的升級回歸測試。

## 重現證據

2026-09-13 以 Python SQLite 執行父提交的原始 schema，插入兩筆不同 task ID、相同 Request ID 的資料，再執行受審提交的原始索引 SQL：

```bash
python3 - <<'PY'
import sqlite3, subprocess, re
old = subprocess.check_output(['git', 'show', '3cfa259^:internal/storage/sqlite.go'], text=True)
new = subprocess.check_output(['git', 'show', '3cfa259:internal/storage/sqlite.go'], text=True)
c = sqlite3.connect(':memory:')
c.executescript(re.search(r'schema := `(.*?)`', old, re.S).group(1))
for task_id in ['task-a', 'task-b']:
    c.execute("INSERT INTO tasks(id,request_id,prompt,workspace,status,created_at,updated_at) VALUES (?, 'retry-1','p','/tmp','completed',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)", (task_id,))
print(c.execute('SELECT id,request_id FROM tasks').fetchall())
try:
    c.executescript(re.search(r'indices := `(.*?)`', new, re.S).group(1))
except sqlite3.IntegrityError as e:
    print(type(e).__name__, str(e))
else:
    raise AssertionError('expected migration failure')
PY
```

實際觀察結果（重現腳本捕捉預期例外，退出碼 0）：

```text
[('task-a', 'retry-1'), ('task-b', 'retry-1')]
IntegrityError UNIQUE constraint failed: tasks.request_id
```

這是 SQL 遷移重現，不冒稱已以 Go driver 跑過完整升級路徑；`initSchema` 將索引錯誤回傳、`NewSQLiteStore` 隨即關庫的影響由原始碼確認。

## 測試與限制

2026-09-13 執行：

```text
$ GOCACHE=/tmp/asura-review-gocache go test -count=1 ./internal/storage ./internal/backend/opencode -run 'TestSQLiteStore_|TestAdapter_Run_BackendErrorWithExitZero'
ok  asura/internal/storage  0.051s
ok  asura/internal/backend/opencode  0.013s
退出碼：0
```

此為明確限定範圍的測試，不是全套回歸。既有新增升級測試覆蓋缺欄位資料庫，但未覆蓋已有 Request ID 且重複的資料庫。

2026-09-13 另啟動 `GOCACHE=/tmp/asura-review-gocache go test -count=1 ./...`，已觀察到以下失敗，整理報告時尚未取得整個命令的結束結果：

- `TestINT001Unit`：真實 OpenCode 無法開啟 `/home/raybird/.local/share/opencode/log/opencode.log`。
- Daemon 多項測試（包含新增並行去重測試）：Unix socket 的 `setsockopt: operation not permitted`。
- 已顯示通過的套件包含 Storage、Engine、Auth 與 CLI；不能據此宣稱全套通過。

上述環境限制不列為提交缺失；本次未驗證 race 全套回歸，也未重新產生實作者的紅綠重構歷程或核准證據。

## 流程判定

**RETURN TO execute-task**：修正既有重複資料的遷移策略，補足升級回歸，再於允許 Unix socket 與 OpenCode 執行的環境驗證完整測試。本次只輸出審查文件，未修改 production code 或測試。
