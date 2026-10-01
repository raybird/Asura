# Issue 29 — Memory Utility Feedback Loop Foundation

## 概述

2026-10-01：依 [GitHub issue 29](https://github.com/raybird/Asura/issues/29) 建立規劃。基準 `7785a204dc0de486938ee223342ae2af17eb6c2a`。建立 Recall → immutable telemetry → bounded utility assessment → aggregation → opt-in down-weight 閉環。預設 observe、Direct 不增加模型呼叫、explicit 不降權、歷史 sealed context 不變。

本期不包含 Web UFL UI、自動刪除／衰減／archive、embedding、boost、動態 backend routing 或背景新增 memory。

## 文件清單

- [需求分析](requirement-analysis.md)
- [技術分析與核准方案](technical-analysis.md)
- [唯一實作計畫](implementation-plan.md)

## 涉及檔案

```text
internal/
├── recall/       # 候選排序、utility policy 與 sealed audit
├── storage/      # ledger、migration、原子聚合與 bounded query
├── daemon/       # canonical authorization、Task admission、IPC
├── engine/       # terminal outcome 與 utility persistence 接合
├── pipeline/     # 既有 Critic 呼叫的 optional usage assessment
└── web/          # 本期不新增 UFL UI
cmd/asura/        # memory utility 與 status
 tests/integration/ # 跨層驗收
README.md         # 使用者 CLI 契約
 docs/superpowers/specs/2026-09-11-asura-architecture-design.md # ownership 與 UFL 決策
```

## Gherkin 驗收劇本

2026-10-01：以下契約與 technical-analysis.md 的 K-TBD-1～10 具體方案經使用者核准。


### UFL-001 — Default compatibility / observe-only

```gherkin
Given 既有 off／explicit／bounded recall 任務
When 升級到 Phase 3K 且未明確啟用 apply
Then off 與 explicit 行為完全不變
And bounded 的 selected IDs、lexical order、budget 與 model call 數維持 Phase 3F 契約
And UFL 可記錄 telemetry，但不改變排序

```

### UFL-002 — Recall identity and sealed reproducibility

```gherkin
Given 一個新的 bounded Task 被接受
When recall resolution 完成並保存 Task
Then 建立唯一 recall_id 並與 sealed ExecutionContext 綁定
And 後續 memory 新增／刪除／utility 更新不改寫該 Task 的 selected IDs、content 或排序證據
And 相同 Request ID redelivery 不建立第二個 recall event
```

### UFL-003 — Canonical workspace isolation

```gherkin
Given workspace A/B、父子目錄、symlink alias／escape 與撤權情境
When 寫入或查詢 recall telemetry／utility aggregate
Then 僅 exact canonical workspace 的資料可被存取
And 跨 scope ID 不洩漏存在性
And 拒絕路徑不修改 utility 統計
```

### UFL-004 — Critic utility signal validation

```gherkin
Given Three-Face Task 已封存 auto memory IDs
When Critic 回報 used／unused／unknown
Then 只接受該 Task 已封存的 auto IDs
And fabricated／explicit-only／跨 scope IDs 不得成為有效 utility assessment
And 所有 Critic 判定標記為 estimated provenance
And 無可驗證 Critic 結果時記為 unknown
```

### UFL-005 — No extra model calls for Direct

```gherkin
Given direct Task 使用 bounded recall
When Task 成功、失敗或取消
Then UFL 不新增任何模型呼叫
And 該次 utility assessment 可保持 unknown
And 既有結果、取消與 partial output 契約不變
```

### UFL-006 — Idempotent aggregation

```gherkin
Given Critic assessment 已成功持久化但 client 回應遺失，或 Daemon 在後續聚合前重啟
When 同一結果被重送或 recovery 重新處理
Then 同一 recall_id + memory_id 最多計數一次
And aggregate 可由 immutable events 重建
And 不因 retry 增加 used／unused 次數
```

### UFL-007 — Bounded apply weighting

```gherkin
Given utility policy=apply 且某 auto memory 已達最小有效 observation threshold
When 新的 bounded recall 對候選排序
Then utility multiplier 只在核准下限與 1.0 之間
And 不改變候選掃描、Top-K、prompt budget 與 timeout hard bounds
And explicit memories 不受 multiplier 影響
And final selected result 將 lexical score、weight 與 policy 封存在 Task audit
```

### UFL-008 — Low evidence stays neutral

```gherkin
Given memory 只有少量 observation、全部 unknown 或資料不足
When apply policy 計算權重
Then weight 保持 1.0
And 不因缺資料、失敗 Task 或 Direct mode 被懲罰
And 不把 missing/unknown 當 unused
```

### UFL-009 — Delete / Git-approved / history compatibility

```gherkin
Given manual 與 git-approved memories 已產生 utility telemetry
When memory 被刪除、Git candidate 後續變更或 Web 完成新的 approve
Then 已刪 memory 不再參與未來 recall 且不復活
And 歷史 Task 的 recall/utility audit 仍維持原 sealed identity
And 新核准 memory 以 neutral weight 開始，不繼承其他 candidate 的 utility
```

### UFL-010 — Bounded audit queries

```gherkin
Given workspace 有大量 recall events
When CLI／IPC 查詢 utility detail 或近期 observations
Then server-side query 有 page／limit／field bounds 與 deterministic order
And 不一次載入全部 telemetry
And 超大或 legacy 不相容資料明確失敗，不造成 Daemon／CLI 記憶體放大
```

### UFL-011 — Failure truthfulness

```gherkin
Given SQLite busy/error、Critic invalid output、聚合失敗、Task cancellation 或 context timeout
When UFL persistence／assessment／aggregation 發生錯誤
Then 不把 unknown 冒充 used/unused
And 不把 apply 當成功後靜默使用未保存的權重
And Task 主執行結果不因非必要 UFL telemetry failure被錯誤改寫成另一個結果；精確 fail-open/fail-closed 邊界須於技術分析定案並有測試
```

### UFL-012 — Full regression

```gherkin
Given Phase 3K 完成
When 執行完整驗證
Then `go vet ./...`、`go test -count=1 -race ./...`、`go build ./cmd/asura` 全綠
And MEM、CTXMEM、RECALL、WEBMEM、GITMEM、WEBGIT、CTXWIN、TFACE、MBACK、BADAPT、Cron、queue/redelivery/cancel 回歸無退化
```

## Gherkin 核准紀錄

- **核准 commit**: 95ac2347e2857aa9b7a6d7f8b7ad11e1b1165805
- **核准來源**: 2026-10-01 對話：使用者回覆「核准」，核准前一回合交付的 UFL-001～012 與 K-TBD-1～10 設計提案；依 dev-cycle 持續推進。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| UFL-001 | 2026-10-01 | 已核准 |
| UFL-002 | 2026-10-01 | 已核准 |
| UFL-003 | 2026-10-01 | 已核准 |
| UFL-004 | 2026-10-01 | 已核准 |
| UFL-005 | 2026-10-01 | 已核准 |
| UFL-006 | 2026-10-01 | 已核准 |
| UFL-007 | 2026-10-01 | 已核准 |
| UFL-008 | 2026-10-01 | 已核准 |
| UFL-009 | 2026-10-01 | 已核准 |
| UFL-010 | 2026-10-01 | 已核准 |
| UFL-011 | 2026-10-01 | 已核准 |
| UFL-012 | 2026-10-01 | 已核准 |

## 風險與首要驗證

- **最大風險**：估計訊號錯判形成長期降權，或 admission／crash retry 破壞 sealed identity、跨 scope 隔離與計數。
- **風險等級與理由**：High；涉及 scope 權限、不可變歷史與核心外部失敗契約。
- **首要驗證**：先用獨立人工標註的正反 fixture 做 telemetry-only Critic 契約 probe，再做 ledger 原子性／crash／隔離 fault injection；apply 最後實作。
- **選擇理由**：契約 probe 直接區分可驗證訊號與模型猜測，交易 fault injection 直接揭露重複與半寫入；單純跑通端到端不能證明這兩項。
- **完成證據**：記錄固定 backend／model／prompt、原始輸出、標註結果、ID 驗證與模型呼叫數；不合法訊號只能 unknown；重送與 crash 不重複計數、跨 scope 拒絕不修改資料。mock fixture 只證明契約，不證明模型可信度。

## 待確認事項

所有 K-TBD 的核准方案見技術分析；真實 probe 結果仍須取得。

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| TBD-1 | 核准 UFL-001～012 及 K-TBD-1～10 的具體方案 | 已解決 | 2026-10-01 使用者「核准」，可依相依順序執行 |
| TBD-2 | 真實 Critic probe 的 backend/model 與可用性 | 已解決 | 2026-10-01 沿用已設定 OpenCode 1.18.31 / openai/gpt-5.6-sol；可信度仍待 probe |

## 驗證證據

2026-10-01：P0～P4 完成，紅綠、固定模型 9/9、10k/100k 容量、精煉、vet/race/build 見 [verification.md](evidence/verification.md)。正式 CI 與獨立 review 於 P5 取得後回填。

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-01 | 使用者「核准」UFL-001～012 與 K-TBD-1～10；沿用現有模型設定 | 使用者／Codex |
| 2026-10-01 | 讀取 issue、盤點基準、建立待核准規格與設計方案 | Codex |

---
**建立日期**: 2026-10-01
**分級**: Large — 跨 storage、Daemon、engine、pipeline、recall 與 CLI
**風險**: High
**狀態**: 實作完成，交付驗證與獨立審查中
