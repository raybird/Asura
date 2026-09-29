# Issue 25 - 初始實作計畫

## 設計與使用方式

2026-09-29 初始計畫；風險與首要驗證唯一來源為 [README](README.md#風險與首要驗證)。沿用 `Backend.Run(context.Context, *ExecutionRequest) (*ExecutionResult, error)`、Registry、共同 failure/event/trace 與能力 preflight。新 adapter 的 constructor、argv、parser 與版本常數待 probe 定案。

| 使用方式 | 現況 | 候選目標 |
|---|---|---|
| ask/run 省略 backend | opencode | 保持 |
| ask/run --backend codex | Codex | 保持 |
| ask/run --backend omp 或 antigravity | 拒絕 | 指定 adapter；缺能力或環境未就緒則 fail-closed |
| status | 任務與 pipeline 摘要 | 加 backend 身份與可取得版本 |

## 階段順序（唯一任務來源）

以下為規劃依賴，尚未由 decompose 細化為可實作 Task。2026-09-29 BADAPT-001～012 行為劇本已獲核准；固定版本、安全能力與 fixture 基線仍需 probe。先完成調查及必要技術決策，再由 decompose 在本檔細化 Phase／Task。

| 階段 | 驗收映射 | 產出 | 相依 | 可檢查完成判準 | 狀態 |
|---|---|---|---|---|---|
| I1 固定版本 contract probe | BADAPT-002、004、006、007、009 | 可重跑 probe、去敏輸出、能力矩陣與 I-TBD-1～6 決策 | 取得固定 binary 與可隔離環境；不改 production | README 列出的首要證據齊備；每項標已證明或 unsupported，安全失敗明確阻擋對應路徑 | OMP 候選版本及 agy plan 寫入反例已記錄；取消與錯誤路徑未完成，OMP provider/model 待定 |
| I1-G 規格定案 | BADAPT-001～012 | I-TBD-1～9 結論、核准表與技術基線 | I1；必要 model/provider 等決策有答案 | 已核准的行為維持一致；固定版本與能力有 probe 證據，實質修訂逐項重新核准並提交基線 | 行為已核准；技術基線待定 |
| I2-A 共同 conformance harness | BADAPT-001、002、004、006、007、009、010 | 共同 assertions、各 adapter fixture 入口 | I1-G、decompose | 測試能捕捉錯誤分類、partial、取消與重複呼叫；不 mock 掉被驗能力 | 待 probe 後拆解 |
| I2-B OMP adapter | BADAPT-002、003、006、007、009、010 | OMP adapter 與 parser／process 測試 | I2-A | 真實 probe 與 fixture 一致；先紅後綠，其他 backend 呼叫為 0 | 待 probe 後拆解 |
| I2-C Antigravity adapter | BADAPT-004～007、009、010 | Antigravity adapter 與相關測試 | I2-B 全綠 | auth/unavailable、partial、cancel 對共同契約，無 session 語意外洩 | 待 probe 後拆解 |
| I3 註冊、CLI 與身份 | BADAPT-001、003、005、008、012 | Daemon 接線、CLI/status、身份整合測試 | I2-C | 預設不變、未知 ID 零程序啟動；重送／queue／restart 保留原身份及無額外 attempt | 待 probe 後拆解 |
| I4 Context／Memory 與交付 | BADAPT-006、008～012 | 整合證據、常青文件、獨立 review | I3 | 所有回歸通過、文件與能力一致、獨立 review 完成後再提 PR | 待 probe 後拆解 |

## 測試策略

- BADAPT-002、004：真實固定版本 probe 為能力證據；help、README 與 fake binary 不足以宣告能力。安全邊界需正反控制組與可觀察副作用。
- BADAPT-003、005、007、009：adapter 單元驗 parser／argv／事件分類；外迴圈使用真實子程序或已記錄 fixture 驗執行整合、partial、cancel、attempt count。不同失敗層級保留雙迴圈。
- BADAPT-001、006、008、011、012：CLI／Daemon／Pipeline 整合驗 0 calls、持久化 SQLite 重開、queue wake、fingerprint 衝突、sealed memory 與 admission 拒絕。偵測器應計數所有 backend，不只斷言被選者有執行。
- BADAPT-010：依賴與原始碼靜態檢查加上共同契約測試；人工追蹤新增分支及 import，不能只搜尋某一種 if 字串。
- 每個行為變更先記目標紅燈，再最小實作與全綠；單元／外迴圈同層時說明合併理由。重構 no-op 記原因，純文件以靜態查核取代紅燈。
- 完整驗證入口：`go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura`。保留 MBACK、TFACE、CTXWIN、RECALL、WEBMEM、GITMEM、Cron、queue/redelivery/cancel 測試。2026-09-29 僅更新規格與 probe 紀錄，未執行上述產品驗證。
- 證據回填細化後的原 Task，含日期、版本／工作區、命令、exit code、原因及對應 BADAPT。測不到的真實外部行為揭露限制，不能填通過。

## 建檔檢查

2026-09-29 對照 issue-checklist：四份文件、編號映射、metadata、核准 gate 與唯一任務來源已建立。必要未知 I-TBD-1～9 尚未解決，行為規格已核准，但技術規劃仍未完成。尚無產品驗證或獨立 review 證據。

### 2026-09-29 文件靜態檢查

使用 Python 檢查四份 Markdown 的相對連結均存在、README 有 BADAPT-001～012 共 12 個 Scenario 且核准表完全相等、無 HTML placeholder；結果 PASS。`git diff --check` exit 0，但新文件未追蹤，不以此命令宣稱已檢查新檔內容。產品測試、live contract probe 與獨立 review 仍未完成。
