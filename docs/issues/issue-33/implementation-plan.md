# Issue 33 — 實作計畫

提案日期：2026-10-03。設計見 [technical-analysis.md](./technical-analysis.md)，風險與首要驗證只引用 [README](./README.md#風險與首要驗證)。本檔為唯一任務來源；2026-10-03 使用者已核准全部驗收與方案，以下由 decompose 細化為可執行任務。

## 實作步驟

| 階段 | 狀態 | 驗收 | 產出 | 相依 | 完成判準 |
|---|---|---|---|---|---|
| P0 現況與定案 | ✅ | RTCAP-001～016 | 四份規劃文件、既有測試基線、RC-TBD 定案 | issue 33 | 文件靜態查核完成且必要方案／驗收核准有來源 |
| P1 最高風險契約保護 | ✅ | 003／007／008／009／015／016 | 接受前拒絕、封存、重送與 restart 的行為測試 | P0 核准 | 先保存因缺少新行為失敗的外迴圈紅燈；獨立字面值與 persisted JSON 比對，不 mock matcher |
| P2 純契約與 policy | ✅ | 002／004／010／013／014 | requirement builder、descriptor、共同 matcher、錯誤鏈 | P1 風險刻畫 | deterministic 與 unknown 負向測試先紅後綠；slice 無 alias、順序固定，無模型呼叫 |
| P3 Admission 與封存 | ✅ | 001／003～009／014～016 | engine/local、IPC/Web、Cron、queue admission 與原子 context 寫入 | P2 | 所有新任務接受前 match；無記憶保存；重送不探測；並行最多一次執行；拒絕無 workspace 副作用 |
| P4 Pipeline 與 execution guard | ✅ | 001／004／005／007／013／015 | 移除零散 hard-code、同 backend stages、安全退化 trace | P3 | same backend、stage policy 正確、退化時 Prepare/Run=0，接受時快照不變 |
| P5 Audit 與 legacy | ✅ | 007／011／016 | status、IPC Task、Web 稽核、兩份常青文件 | P4 | 三個 surface 讀同一保存值；legacy 查詢零 runtime 呼叫與零 DB 回填；保留舊欄位 |
| P6 交付驗證 | ⏳ | 012 與全體 | 全回歸、範圍精煉、commit／PR、持久化獨立 review | P5 | 必要證據齊備、所有條件核准，固定 BASE/HEAD review PASS 後等待合併 |

## 測試層級與策略

- RTCAP-002／010：backend 純函式測試；同層級外／內迴圈可合併，沒有 IPC 或 storage 責任。expected required/missing 用核准政策的字面值。
- RTCAP-004／013：policy/matcher 單元負向測試，加 Pipeline stage 順序與零 Prepare/Run 整合測試；保留不同失敗面。
- RTCAP-001／003／005／006／009／014／015：共同契約單元加 engine／daemon／pipeline 整合。可 fake runtime output，不能 fake 被驗收的 matcher、capability matrix 或 sealed storage；所有 backend 計數，拒絕前後 workspace sentinel 對比。
- RTCAP-007／008／016：真 SQLite、Daemon IPC、queued wake、restart、Cron 與 local run；接受後替換 registry 能力與版本，斷言預先保存的 JSON、Task ID 與執行次數。加入 duplicate insert、取消與 backend fingerprint conflict 保護。
- RTCAP-011：舊 JSON fixture、真 storage 讀取、CLI/Web 呈現，查詢前後比較原 bytes；缺欄位不能被序列化成新歷史。
- RTCAP-012：`go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura`；保存 command、exit code、被測 HEAD/工作區與 skipped live probe 揭露。固定 adapter contract 可用現有 fixtures；本期不依賴新模型呼叫作為 admission 判準。
- 純規劃／常青文件使用可重複靜態與人工查核，不宣稱有程式紅綠測試。後續 production Task 每項保存紅綠重構證據，code-simplify no-op 時記原因。

## 使用方式對照

`ask --backend codex`、`run --backend opencode`、`--mode three-face` 沿用。新增 status／audit 摘要，沒有新必填參數、auto backend 或 runtime 管理頁。版本不可取得、unknown 契約及能力退化的提早拒絕行為須先核准。

## 規劃查核

- [x] 已讀流程契約 2.0、acceptance、verification、project 與文件範本。
- [x] Large：需求分析由實際 admission／封存路徑調查觸發；技術分析由版本失敗與快照保存位置的方案取捨觸發。
- [x] 風險與涉及檔案只在 README 維護；文件連結只指向實際檔案。
- [x] RC-TBD-1～10 均有提案；候選驗收與核准表集合一致。
- [x] 2026-10-03 使用者核准必要設計與驗收，來源見 README。
- [x] 在本檔細化已核准 Phase／Task，再開始 production 實作。

## P0 證據

2026-10-03：原始碼盤點見 requirement-analysis；既有測試基線見 [planning-baseline.log](./evidence/planning-baseline.log)。規劃文件檢查見 [planning-doc-check.log](./evidence/planning-doc-check.log)。測試與靜態檢查的實際結果以 log 為準，尚未取得的核准不勾選。

## Task P1-T1 — 接受前拒絕與封存紅燈

- 產出與最小範圍：daemon IPC 真 SQLite 整合測試，涵蓋無記憶、拒絕與 redelivery；production 不改。
- 相依：P0。
- 驗收支援（風險紅燈責任）：RTCAP-003、007、008、009、015、016。
- 完成判準：先證明舊實作缺少接受前拒絕與無記憶封存；mutation／restart 後 byte equality 為後續實作任務的完成判準。
- 驗證：外迴圈先紅，內層契約另由 P2-T1 取得紅燈。
- 狀態：✅ 已完成。
- 證據：[admission-red.log](./evidence/admission-red.log)、[implementation-notes.md](./evidence/implementation-notes.md)。

## Task P2-T1 — 需求與 matcher

- 產出與最小範圍：backend 型別、canonicalization、descriptor、純 matcher 單元測試。
- 相依：P1-T1。
- 驗收責任：RTCAP-002、010、013、014。
- 完成判準：未知輸入拒絕、集合順序獨立、版本失敗拒絕。
- 驗證：同一純函式層合併外內紅燈，沒有 IPC/persistence 責任。
- 狀態：✅ 已完成。
- 證據：[implementation-notes.md](./evidence/implementation-notes.md)。

## Task P3-T1 — 所有入口 admission 與封存

- 產出與最小範圍：engine preflight seam、daemon submit/Cron、CLI wiring、真 storage。
- 相依：P2-T1。
- 驗收責任：RTCAP-003、006、009、016（支援 007、008、014）。
- 完成判準：接受前共同預檢、duplicate winner、persisted context 權威、無記憶與 Cron 保存。
- 驗證：IPC/engine/Cron 整合與單元錯誤路徑。
- 狀態：✅ 實作與驗證完成；RC-TBD-11 已於 2026-10-03 依「保留」定案。
- 證據：[implementation-notes.md](./evidence/implementation-notes.md)。

## Task P4-T1 — Pipeline 與安全 guard

- 產出與最小範圍：pipeline policy/preflight/execution guard/trace。
- 相依：P3-T1。
- 驗收責任：RTCAP-001、004、005、015（支援 007、013）。
- 完成判準：能力退化拒絕且不改原 contract，stage 同 backend。
- 驗證：既有 stage characterization 加新 guard 紅綠測試。
- 狀態：✅ 已完成。
- 證據：[implementation-notes.md](./evidence/implementation-notes.md)。

## Task P5-T1 — 歷史與稽核 surface

- 產出與最小範圍：CLI status、Web audit、legacy fixtures、常青文件。
- 相依：P4-T1。
- 驗收責任：RTCAP-007、008、011（支援 016）。
- 完成判準：三 surface 同值、不回填、重送不呼叫 backend。
- 驗證：CLI/Web/真 SQLite integration 與 legacy byte equality。
- 狀態：✅ 實作與驗證完成；RC-TBD-11 已於 2026-10-03 依「保留」定案。
- 證據：[implementation-notes.md](./evidence/implementation-notes.md)。

## Task P6-T1 — 全回歸與交付

- 產出與最小範圍：完整測試、精煉、commit、PR、獨立 review artifact。
- 相依：P5-T1。
- 驗收責任：RTCAP-012。
- 完成判準：vet/race/build 通過且 review PASS。
- 驗證：全 suite 與固定 BASE/HEAD independent review。
- 狀態：⏳ 全回歸已通過；等待完整交付審查與 PR。
- 證據：[implementation-notes.md](./evidence/implementation-notes.md)。

2026-10-03：RTCAP-016 的 shell 子範圍已核准保留；P3／P5 沿用既有 shell 與 unavailable 稽核。相容性驗證見 [shell-compatibility-green.log](./evidence/shell-compatibility-green.log)，沒有 production 行為修改，紅燈不適用。
