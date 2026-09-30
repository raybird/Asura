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

2026-09-30：下表保留初始階段設計；目前任務與證據以本檔「細化任務與技術基線」為唯一來源。BADAPT-001～012 核准範圍不變。

| 階段 | 驗收映射 | 產出 | 相依 | 可檢查完成判準 | 狀態 |
|---|---|---|---|---|---|
| I1 固定版本 contract probe | BADAPT-002、004、006、007、009 | 可重跑 probe、去敏輸出、能力矩陣與 I-TBD-1～6 決策 | 取得固定 binary 與可隔離環境；不改 production | README 列出的首要證據齊備；每項標已證明或 unsupported，安全失敗明確阻擋對應路徑 | OMP 18.4.4 指定模型、主 PGID 取消失敗／PID namespace 回收及唯讀掛載證據已保存；retry、完整失敗與其他安全路徑待驗 |
| I1-G 規格定案 | BADAPT-001～012 | I-TBD-1～11 結論、核准表與技術基線 | I1；必要 model/provider 等決策有答案 | 已核准的行為維持一致；固定版本與能力有 probe 證據，實質修訂逐項重新核准並提交基線 | 行為已核准；2026-09-30 I-TBD-10、11 已核准；完整探測及技術基線仍待驗 |
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

### 2026-09-30 OMP probe 證據

固定 binary、實際命令與各結果見 [技術分析](technical-analysis.md#2026-09-30-omp-指定模型與取消邊界)及 `probes/omp-18.4.4-*.json`。這些是真實 CLI probe；不是產品驗收全綠，也未建立 production adapter。

2026-09-30 使用系統 `/usr/bin/python3` 查核：Python 語法、四份 Markdown 相對連結、核准 commit 的 Gherkin 區塊完全相等、Scenario 與核准表集合相等、十份 JSON fixture 及控制組／取消／唯讀結果；全部 PASS。此為文件與 probe artifact 靜態查核，沒有用「主程序退出」或「子程序最後已退出」代替副作用判準。可重跑的語法／JSON 基本查核：

```bash
/usr/bin/python3 -c "import ast,json; from pathlib import Path; ast.parse(Path('scripts/issue-25/probe_omp.py').read_text()); files=list(Path('docs/issues/issue-25/probes').glob('omp-*.json')); assert len(files)>=14; [json.loads(p.read_text()) for p in files]; print('PASS: syntax and JSON files')"
git diff --check
```

2026-09-30 查核 `/proc/*/cwd`，沒有 probe 工作區的存活程序。採用 bwrap 的新增部署前提 I-TBD-10 尚待使用者決定；未據此啟動 I2 或宣告 OMP 的完整 capability。

### 2026-09-30 I1 Retry 追查補充

已追查固定套件 source 的 provider 與 session recovery 邊界，命令、source 雜湊及限制見 [技術分析](technical-analysis.md#2026-09-30-retry-靜態追查)。結果：CLI retry 關閉不等於 provider 無 recovery；工具完成後有 replay guard 的靜態線索，但未取得 transport 故障時的真實 request／工具計數證據，I1 仍未完成。純調查未改 production，不適用產品紅綠測試；本次查核 source 路徑與符號存在，文件 diff 無 whitespace error。I-TBD-10 部署選項仍等待使用者答覆。

### 2026-09-30 I1 Shell 補測及決策更新

I-TBD-10 已獲使用者「1」核准，解除 bwrap 部署決策阻塞。新增 shell 在 workspace 及外部獨立暫存目錄的正／唯讀控制組，共四份真實 fixture，命令與結果見 [Shell 邊界補測](technical-analysis.md#2026-09-30-shell-寫入邊界補測)。兩個正控制組均寫入 PROBE_OK；兩個唯讀控制組均有 EROFS 與 marker 不存在，CLI 均正常完成。對應 BADAPT-002、006 的安全調查，未宣告完整能力；retry／partial／Antigravity 仍待驗。腳本精煉 no-op；Python AST、14 份 JSON 與 git diff --check PASS，核准版本 Gherkin 不變。尚未執行產品 vet／race／build。

Antigravity 的固定正式模型未選定，2026-09-30 已詢問使用者採用先前 probe 的 gemini-3.8-flash-low 或先查看清單；這會影響 I-TBD-4 及 BADAPT-011，不能將僅用於 probe 的模型直接視為正式設定。

### 2026-09-30 Antigravity 模型與新版本探測

使用者對固定模型選項回覆「1」，採 gemini-3.8-flash-low，解除模型選擇缺項。CLI 已更新至 1.2.13，舊 hash guard 拒絕後建立新探測腳本與去敏 JSON；新版本、命令、反例及限制見 [新基線](technical-analysis.md#2026-09-30-antigravity-1213-新基線)。發現 exit 0／SUCCESS 仍可能為逾時；移除 token／隔離 HOME 仍成功，直到完整 HOME／IPC 隔離才得到有效 auth 正反對照。對應 BADAPT-004、006、007 的調查；retry、非空 partial 等未完成，I1 仍進行中。

2026-09-30 完整 HOME／IPC 隔離後重跑寫入、唯讀、延遲與取消控制組，直接結果及15份 agy fixture 見新基線。控制組通過，僅支持已測案例；I-TBD-11 的 Antigravity 部署範圍決策待答。code-simplify no-op，AST／29份 JSON／Markdown links／核准 Gherkin／diff check PASS；產品測試與完整 I1 仍未完成。


### 2026-09-30 設定與失敗補測

I1 仍進行中。修正 OMP 平面設定無效反例，巢狀設定後受控 503／429／partial／工具副作用故障不再出現 CLI auto_retry_start；provider transport 仍多次請求。重跑真實模型寫入、唯讀、延遲與取消。Antigravity 固定官方 1.2.13 binary，取得非空 timeout partial 及真實服務工具副作用後的 TLS transport failure；副作用一次，transport reconnect 19 次。命令、scope、原始計數與限制見 [補測](technical-analysis.md#2026-09-30-設定修正與失敗契約補測)。BADAPT-002、004、006、007、009 的調查證據增加，未宣告 production 驗收完成。

2026-09-30 使用者回覆「1」核准 I-TBD-4 登入策略：沿用原 CLI 登入／唯讀快照／過期後手動更新。接續驗證 access-only 與過期拒絕，之後定案 I1-G 與拆解後續實作。未執行產品 vet／race／build。

2026-09-30 登入補測：OMP 缺 refresh 欄位 TypeError，保留空 refresh 字串則真實 PROBE_OK；Antigravity access token 已於 2026-09-18 過期，移除 refresh 後要求登入並失敗。已請使用者更新原 CLI 登入，僅 access-only 成功對照依賴此更新；不將舊 refresh-enabled 成功冒充新策略通過。

2026-09-30 最終靜態查核：兩份 Python AST、47 份 JSON、巢狀憑證字串掃描、相對連結、核准 Gherkin 不變、probe 子程序清理與 git diff --check PASS。probe 調查未修改 production，故不適用產品紅綠測試；此結果不是完整產品驗收或獨立 review。


## 2026-09-30 細化任務與技術基線

沿用 README 風險策略；固定 OMP 18.4.4／Bun 1.4.2、Antigravity 1.2.13 與已選模型。正式身份只用 omp／antigravity，不增加 agy alias。版本回傳已驗 binary 的 CLI version，不改 fingerprint。unknown Context window 沿用既有 admission 政策，explicit override 仍有效，不用 catalog metadata 虛構實测窗口。登入採原 CLI 對應來源的 access-only 快照；期限失效回報 authentication failure，不續期或自動切換來源帳戶。OMP HOME/tmp/IPC 隔離是已核准快照與 bwrap 方案的實作細節。

2026-09-30 第一版能力：workspace_write、structured_events、process_cancellation 有直接 probe；read_only_sandbox 保留 unsupported（主機 TCP／外部工具通道未證明）。Oracle／Critic 在 preflight 拒絕，Three-Face Stage 0 零模型呼叫，符合 BADAPT-006。native plan 或有限唯讀 fixture 不作 capability 替代。provider transport retries 與 Asura attempt 分開；Run 不自動重試、不 resume、不 fallback。Antigravity 未觀察到的 vendor quota carrier 不宣告已實測，unknown native failures 映射 execution failure。

### Task I1 — 固定契約與安全調查
- 產出與範圍：兩份 probe、去敏 fixture、設定反例及能力限制；不改產品。
- 相依：無。
- 驗收責任：BADAPT-002、004；支援 006、007、009。
- 完成判準：能力逐項有直接證據或 unsupported；登入與副作用偵測有正反對照。
- 驗證：technical-analysis.md 的真實 CLI／模型與受控故障證據；文件以 AST／JSON／連結／核准內容比對查核。
- 狀態：✅ 調查完成；不代表產品驗收完成。
- 證據：2026-09-30 access-only／keyring／完整隔離補測及既有失敗 fixture。

### Task I2-A — 共同行為測試與隔離程序
- 產出與範圍：adapter 共用的 Linux bwrap／memfd 程序邊界與共同失敗斷言，限 internal/backend 下；不增加上層分支。
- 相依：I1 與上述技術基線。
- 驗收責任：BADAPT-007、010；支援 003、005、009。
- 完成判準：禁止唯讀角色、namespace unavailable 模型零呼叫、真實子程序取消與部分输出保留；fixed binary 與憑證來源不外洩。
- 驗證：fixture parser 層與真實子程序層各自紅綠；probe 已有的能力證據不以 fake binary 取代。
- 狀態：✅ 已完成；2026-09-30 驗證見 [持久化證據](evidence/README.md) 與 final-race.log。

### Task I2-B — OMP adapter
- 產出與範圍：internal/backend/omp 的 argv、巢狀設定、OAuth SQLite access-only 快照與 parser。
- 相依：I2-A。
- 驗收責任：BADAPT-003、009；支援 002、007、010。
- 完成判準：固定模型、關閉 CLI retry/session、嚴格版本／雜湊，成功與失敗只產生一次執行，其他 backend 零呼叫。
- 驗證：native fixture 的成功／429／partial／after-tool failure；auth expiry 前置拒絕；實際 sandbox runner 整合與取消。
- 狀態：✅ 已完成；2026-09-30 驗證見 [持久化證據](evidence/README.md) 與 final-race.log。

### Task I2-C — Antigravity adapter
- 產出與範圍：internal/backend/antigravity 的原 CLI keyring/file access-only、argv、event/result 與 AGY_ERROR parser。
- 相依：I2-B。
- 驗收責任：BADAPT-005；支援 004、007、009、010。
- 完成判準：未登入／過期／缺 bwrap／version mismatch fail-closed；SUCCESS/exit 0 timeout 為失敗且保留 partial；不 resume 或續期。
- 驗證：真實 fixture 與已記錄 access-only 成功對照；parser／程序整合分層紅綠。
- 狀態：✅ 已完成；2026-09-30 正式 adapter 真實模型通過，見 evidence/agy-new-login-live.log。

### Task I3 — 註冊、CLI 與持久化身份
- 產出與範圍：Daemon composition、ask/run 選項、status backend/version；沿用既有 Task schema、identity fingerprint 與 Pipeline trace。
- 相依：I2-C。
- 驗收責任：BADAPT-001、008、012；支援 003、005。
- 完成判準：預設與 codex 契約不變；四 backend counters、unknown 0 external processes；重送／queue wake／restart 保留原身份且無新增 attempt；status/trace 顯示取得的版本。
- 驗證：CLI／Daemon／SQLite reopen／queue 與 MBACK 回歸，最後 vet／race／build。
- 狀態：✅ 已完成；2026-09-30 驗證見 [持久化證據](evidence/README.md) 與 final-race.log。

### Task I4 — Context、Memory 與交付
- 產出與範圍：Stage 0、sealed memory、admission 整合測試、README／架構常青文件、PR 與獨立 review。
- 相依：I3。
- 驗收責任：BADAPT-006、011；支援 010、012。
- 完成判準：Three-Face 不支援能力及 admission 拒絕均零模型呼叫，memory 不重新選取；完整回歸／交付版本證據及独立 review 持久化。
- 驗證：既有 TFACE／CTXWIN／RECALL／WEBMEM／GITMEM／Cron／queue／cancel 全部保留；fixture 不替代被驗安全能力。
- 狀態：⏳ Stage 0／Context／Memory 與完整回歸已通過；PR 及獨立 review 待交付。

## 2026-09-30 產品驗證

`go test -count=1 -race ./...`、`go vet ./...` 與 `go build -o /tmp/asura-issue25 ./cmd/asura` 均 exit 0，原始輸出保存於 [evidence](evidence/README.md)。build 路徑避免覆寫工作區 binary。code-simplify 已檢查本次範圍，僅將 runtime overlay 的巢狀 append 改為明確複製再追加；所有契約保留，最終 race 同組通過。Cron 的既有日期斷言於跨月時失敗，局部測試修正後完整回歸通過，未變更排程功能。

固定 runtime 在來源更新後仍執行已驗 bytes；每次呼叫建立私人 scratch 副本並驗 hash、唯讀掛載，結束即移除。新 adapter 不修改 Pipeline／Engine／Memory／Context 的 production 邏輯。2026-09-30 更新登入後，正式 Antigravity adapter live 通過，見 evidence/agy-new-login-live.log。
