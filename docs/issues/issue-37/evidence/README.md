# Issue 37 驗證證據

2026-10-04，核准基線 d27ace3；最終被測程式與測試內容以 [tested-source.sha256](tested-source.sha256) 固定。可用 `sha256sum -c docs/issues/issue-37/evidence/tested-source.sha256` 驗證同一工作區；文件／log 後續更新不改此程式內容。紅燈取得於 production 修改前或缺口修復前，不以環境／編譯錯誤冒充行為紅燈。

## 紅綠循環

- [admission-red.log](admission-red.log)：backend 單元與 daemon IPC 外層均因缺少 atomic stage plan 失敗；AtomicReject 已為綠，保留原安全門檻。
- [execution-red.log](execution-red.log)：pipeline 外層真 Run 未依 mixed plan，及 Oracle 後能力退化仍執行全部三階段。
- [audit-red.log](audit-red.log)：真 SQLite CLI 與真 admission contract 的 JavaScript renderer 缺 stage audit。
- [projection-red.log](projection-red.log)：persisted Task.Backend 不符 sealed plan 仍執行；修復後 failed、零 Run。
- [focused-green.log](focused-green.log)：最小實作、投影修復及 code-simplify 後，同一目標驗收集合與 legacy guard 刻畫在 race detector 下全綠。

純 planner 的 determinism、ordering、bounds/validation 是同一可觀察層級，合併單迴圈；daemon／pipeline／audit 涉及真整合，保留各自外層與 backend 單元。非新增行為的既有 AtomicReject/explicit 拒絕不強造紅燈；保留既有行為刻畫及新增 stage plan 紅燈。精煉只移除 bool map 的顯示技巧，沿原條件分支；focused-green.log 同時為最小實作與精煉後有效綠燈。

## 驗收映射

| 驗收 | 可重複命令中的測試名稱與獨立斷言 | 證據 |
|---|---|---|
| STGBIND-001 | backend Direct 單 Striker plan；engine 既有 local runtime/Direct、pipeline legacy contract/handle、CLI shell 相容 | focused-green.log 與 final-race.log |
| STGBIND-002 | TestStageBindingAdmission／MixedExecution／DaemonMixed，字面 codex/omp/codex 的 decision、實際 Run、trace | admission-red.log／execution-red.log → focused-green.log |
| STGBIND-003 | TestStageBindingDeterminismAndValidation，OMP 僅 Striker eligible，Oracle/Critic eligible=[codex] | focused-green.log |
| STGBIND-004 | TestStageBindingAtomicReject：真 IPC/SQLite 不建立 Task，Run=0，workspace sentinel 不變 | focused-green.log |
| STGBIND-005 | DeterminismAndValidation explicit 三 stage 同 ID；既有 explicit/省略預設回歸 | focused-green.log／final-race.log |
| STGBIND-006 | AtomicReject explicit OMP、既有 admission explicit 不 fallback | focused-green.log／final-race.log |
| STGBIND-007 | 固定 captured_at descriptors 重複純 planner，完整 JSON 相等且字面 runtime 正確 | focused-green.log |
| STGBIND-008 | 同 snapshot 反向 descriptors insertion 順序，canonical JSON 相等；原 Registry Snapshot 排序測試 | focused-green.log／final-race.log |
| STGBIND-009 | MixedExecution 改 priority；Queue 新增候選；sealed bytes 不變、實際 Run 仍原 IDs | focused-green.log |
| STGBIND-010 | MixedExecution／Queue 移除 Striker：任何 stage Run 前 failed，calls=0，trace 留失敗 stage | execution-red.log → focused-green.log |
| STGBIND-011 | GuardBetweenStages Oracle 後能力退化、MixedExecution 初始退化／版本升級：不 fallback、歷史 bytes 不變 | execution-red.log → focused-green.log |
| STGBIND-012 | DaemonMixed／Queue：重送回同 ID/plan，Version counters 無增加；不同 intent 拒絕 | focused-green.log |
| STGBIND-013 | RestartRecovery：真 SQLite close/reopen，pending/queued/running 均 failed 且 context bytes 相同，IPC 重送不探測/Run | focused-green.log |
| STGBIND-014 | CLI TestStageBindingStatus；Web TestStageBindingAudit 執行真 admission JSON 的 JavaScript renderer；Pipeline MixedExecution trace IDs 正確 | audit-red.log → focused-green.log |
| STGBIND-015 | backend mutation/unknown version/missing/order/limits；engine TaskProjection；pipeline NoConstructorFallback：Run=0 | projection-red.log／focused-green.log |
| STGBIND-016 | go vet ./...；go test -count=1 -race ./...；go build -o /tmp/asura-issue37 ./cmd/asura | final-vet.log／final-race.log／final-build.log |

## 限制與規格邊界

2026-10-04 不呼叫付費外部模型；runtime 用真能力組合 fixture，整合驗證不 mock matcher/planner/SQLite/pipeline。Web renderer 的 Go fixture 由真 AdmitRuntime 建構；status 查詢時 registry 已清空，fixture Run 會 panic，未改保存內容。CLI/Web 查詢實作無 registry／Version 入口。四 adapters 的既有 sandbox/取消測試由全套回歸涵蓋，外部 live tests 沿既有 opt-in 規則，不冒稱外部帳號或模型實測。

2026-10-04 restart 不 resume、版本升級不要求 exact equality、候選版本失敗整次拒絕、非密碼學一致性檢查的邊界均已核准。原子 admission 保證拒絕前不執行；執行 stage 之間 runtime 變更只能擋後續 Run，不能回滾先前完成工作。未新增健康／可用性／fallback/retry 或 stage override。
