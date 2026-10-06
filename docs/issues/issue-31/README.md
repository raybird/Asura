# Issue 31 — Memory Lifecycle & Supersession Foundation

## 概述
建立 active / superseded、durable 與單一 successor chain，保留歷史 Task、Git provenance 與 UFL identity。來源：[GitHub issue 31](https://github.com/raybird/Asura/issues/31)。基準 main：`1b21d587a738b871e1dde48abceb17a8ce8c1bb2`。

2026-10-01 來源狀態為「立項提案；先完成 L-TBD 與 MLIFE 驗收核准，再進 production implementation」。初次 dev-cycle 先調查建檔；使用者於 2026-10-01 回覆「審查通過」後核准全部規格並開始實作。

## 文件清單
- [requirement-analysis.md](requirement-analysis.md)
- [technical-analysis.md](technical-analysis.md)
- [implementation-plan.md](implementation-plan.md)

## 關鍵差異
| 項目 | 現況 | 提案 |
|---|---|---|
| 記憶狀態 | 五欄位，無 lifecycle | active / superseded 與 durable |
| Auto recall | scope / source 篩選 | SQL candidate scan 前排除 superseded |
| 歷史版本 | delete 為硬刪除 | 保留 relation identity，禁止復活 |
| Sealed context | 保存內容快照 | 新 Task 加入接受時 lifecycle 快照 |

## 涉及檔案
- `internal/storage/sqlite.go`、`sqlite_memory.go`、`sqlite_memory_page.go`：migration、lifecycle、bounded lookup。
- `internal/storage/sqlite_gitmem_tx.go`：共用交易內建立 successor，保留 candidate provenance。
- `internal/daemon/daemon.go`、`recall.go`、`gitmem_handlers.go`、`gitmem_bounded.go`：canonical authorization、IPC 與封存。
- `internal/engine/engine.go`、`internal/recall/recall.go`：快照與 recall audit；不改 ranking 政策。
- `cmd/asura/memory.go`：最小 CLI 操作。
- 上述套件對應測試、新 lifecycle 測試與 probe；`internal/web/` 既有測試供回歸，不新增 UI。
- 根目錄 `README.md`、`docs/superpowers/specs/2026-09-11-asura-architecture-design.md`：核准並實作後更新使用與架構契約。

## Gherkin 驗收劇本
以下 Given / When / Then 於 2026-10-01 核准。


## MLIFE-001 — Legacy compatibility

Given Phase 3K database 升級至 Phase 3L
When 尚未使用 lifecycle 新能力
Then 所有現有 memory 都視為 active / non-durable
And bounded recall 結果與 Phase 3K 相同

## MLIFE-002 — Atomic supersession

Given active memory A
When 新增 memory B 並指定 supersedes=A
Then B 建立成功
And A 原子轉為 superseded
And A.superseded_by=B
And 不存在只成功一半的狀態

## MLIFE-003 — Auto recall excludes superseded memory

Given A 已被 B supersede
When 新 bounded recall 查詢同時匹配 A 與 B
Then A 不進 auto candidate
And B 依正常 lexical + utility policy 參與排序

## MLIFE-004 — Explicit historical recall

Given A 已被 B supersede
When 使用者明確指定 A
Then A 仍可加入 sealed context
And audit 明確標示 superseded
And 顯示 successor B

## MLIFE-005 — Supersession chain

Given `A <- B <- C`
When 執行新的 auto recall
Then A/B 均不參與
And C 為唯一 active version
And A/B/C 歷史 chain 可完整 audit

## MLIFE-006 — No resurrection

Given A 被 B supersede
When B 後續被 delete
Then A 不自動恢復 active
And A 仍維持 superseded historical state

## MLIFE-007 — Durable does not change ranking

Given 兩筆 lexical / utility 完全相同的 memories
And 其中一筆 durable
When bounded recall 執行
Then durable 不取得額外 score boost
And 既有 deterministic tie-breaker 不改變

## MLIFE-008 — Durable can be explicitly superseded

Given durable memory A
When 使用者明確建立 B supersedes A
Then 操作允許
And A 保留 durable historical metadata
And B 成為 active successor

## MLIFE-009 — Workspace isolation

Given workspace A/B
When A 嘗試 supersede B 的 memory ID
Then fail-closed
And 不洩漏該 ID 是否存在
And 兩邊 lifecycle 均不改變

## MLIFE-010 — Sealed Task reproducibility

Given Task T 已使用 memory A
When 之後 A 被 B supersede
Then T 的 sealed memory snapshot 保持 A
And status / redelivery 仍重現原 Task
And 不以 B 重算歷史 Context

## MLIFE-011 — UFL isolation

Given A 已累積 utility observations
When B supersedes A
Then A 的歷史 telemetry 保留
And B observation count=0
And B weight=neutral
And 不繼承 A 的 utility

## MLIFE-012 — Full regression

Then：

```text
go vet ./...
go test -count=1 -race ./...
go build ./cmd/asura
```

全部通過。

並且：

- MEM
- CTXMEM
- RECALL
- WEBMEM
- GITMEM
- WEBGIT
- UFL
- CTXWIN
- TFACE
- MBACK
- BADAPT
- Cron
- queue / redelivery / cancel

均無 regression。


## Gherkin 核准紀錄
- **核准 commit**: 56871b8de89bb8dc774185780fd48e4667b10d88
- **核准來源**: 使用者於 2026-10-01 對本地 MLIFE 與 L-TBD 提案回覆「審查通過」。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| MLIFE-001 | 2026-10-01 | 已核准 |
| MLIFE-002 | 2026-10-01 | 已核准 |
| MLIFE-003 | 2026-10-01 | 已核准 |
| MLIFE-004 | 2026-10-01 | 已核准 |
| MLIFE-005 | 2026-10-01 | 已核准 |
| MLIFE-006 | 2026-10-01 | 已核准 |
| MLIFE-007 | 2026-10-01 | 已核准 |
| MLIFE-008 | 2026-10-01 | 已核准 |
| MLIFE-009 | 2026-10-01 | 已核准 |
| MLIFE-010 | 2026-10-01 | 已核准 |
| MLIFE-011 | 2026-10-01 | 已核准 |
| MLIFE-012 | 2026-10-01 | 已核准 |

## 風險與首要驗證
- **最大風險**：跨 scope relation 洩漏與非原子更新造成歷史錯寫或第二個 successor。
- **風險等級與理由**：High；涉及 authorization、持久化 migration 與 sealed Task 契約。
- **首要驗證**：核准後先用真實 SQLite 與 Daemon characterization tests 覆蓋跨 scope／不存在 ID 的相同錯誤、交易中斷／並行／重試，以及歷史 context 原文對比。
- **選擇理由**：直接檢查權限與交易失敗面，比先建完整 CLI 流程更能降低最大風險。
- **完成證據**：失敗後 successor 與 relation 均無半套資料；同 operation 重試只有一筆 successor；異 scope／不存在錯誤一致；已封存 context 位元組不變。此為待執行判準，非已通過結果。

## 待確認事項
| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| TBD-1 | 核准 MLIFE-001～012 及 technical-analysis 的 L-TBD 提案 | 已解決 | 2026-10-01「審查通過」，可依計畫執行 |
| TBD-2 | 確認 retry operation ID 與刪除後 relation audit 的外部語意 | 已解決 | 2026-10-01 核准 technical-analysis 提案 |
| TBD-3 | 確認 bounded detail 上限與性能驗收門檻 | 已解決 | 2026-10-01 核准提案上限與本機 probe 門檻 |

## CI 審查回退
2026-10-01 使用者於 [GitHub review](https://github.com/raybird/Asura/pull/32#pullrequestreview-5375935986) 指出最新正式 CI 失敗；先前本地 PASS 不足以放行最新版本。回退 P5-T1，修正 [性能 blocker](https://github.com/raybird/Asura/pull/32#discussion_r4152671417) 與 [取消 fixture blocker](https://github.com/raybird/Asura/pull/32#discussion_r4152671420)。採使用者允許的分離方案，10k/100k 性能門檻改為獨立非 race CI 步驟，30 秒／2 秒門檻不變；standard race 保留全部 correctness 測試。

## Timeline
| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-10-01 | PR #32 合併至 main（merge commit `41b5220`），issue 結案 | Raybird |
| 2026-10-01 | 使用者「審查通過」核准 MLIFE-001～012 與 L-TBD 提案 | 使用者 |
| 2026-10-01 | 依立項提案完成現況調查與候選規格建檔，保留待核准狀態 | Codex |

---
**建立日期**: 2026-10-01
**分級**: Large — 跨 storage、daemon、sealed context、recall 與 CLI，新增持久化契約
**風險**: High
**狀態**: 已完成 — PR #32 於 2026-10-01 合併（merge commit `41b5220`）
