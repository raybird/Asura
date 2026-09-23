# Issue 19 獨立審查報告

- 審查日期：2026-09-22。
- 範圍：PR #20 完整差異；資料隔離、快照冪等及資源上限屬 High 風險。
- PR：https://github.com/raybird/Asura/pull/20
- Reviewed BASE SHA：8576dd0323750a1aa8f2c550c0f69e8edd30e55d
- Reviewed HEAD SHA：9bd04e4d880b34b5cd21081696c7a31dfcbf92e2
- Reviewed patch-id：9313c734ecb00c79ddb73515f4f232c8c984e505
- 獨立 reviewer：Codex subagent `/root/review_issue19`，未參與產品實作。
- Review artifact：`docs/issues/issue-19/review-9bd04e4.md`

## 問題與風險

MUST FIX：無。SHOULD FIX：無。

NICE TO HAVE：`internal/recall/recall_test.go:15` 可將規劃探測的完整混合語料及重複詞分數字面值加入正式 selector 測試。現有正式測試已覆蓋排名、同分順序、查詢上限與無匹配；此項屬回歸證據持久力建議，不阻塞交付。

## 版本與證據有效性

本次重新審查完整固定範圍，重用同一位獨立 reviewer 對 a4831a161e3d470cced17561f4653febe8449532 完成的產品、測試與文件審查，詳見 [原始完整報告](review-a4831a1.md)。2026-09-22 已另讀完該版本至本次 HEAD 的全部新增差異，確認僅有四份文件：

- `README.md`：加入 PR／審查連結、交付事件，狀態改為等待合併。
- `implementation-plan.md`：以已取得證據更新完成狀態與檢查清單，保留文件收尾另核對審查有效性的說明。
- `evidence/verification.md`：加入已持久化獨立審查連結。
- `review-a4831a1.md`：加入原始獨立審查報告。

上述相對路徑均位於 `docs/issues/issue-19/`。核准的 RECALL-001～010、D1–D5、核准來源與規格基線 aeaa652 均未改變；產品、測試、設定、套件與測試環境未改變。這次不直接套用「只有新增報告」例外，而是重新查核文件差異後對新 HEAD 作出結論。已有程式驗證可重用，不需重跑。

## 已查核維度

| 驗收 | 保持有效的查核結論與證據 |
|---|---|
| RECALL-001 | 預設 off、未知模式拒絕、原有指紋及舊 JSON 相容；CLI／IPC 與刪表後 off 測試。 |
| RECALL-002 | 授權先於重送／查詢、精確 canonical workspace、跨專案 explicit 與撤權拒絕；整合隔離測試。 |
| RECALL-003 | 不同詞計分、固定排序與中文雙字詞，查詢完整字元前綴上限；selector 與探測證據。 |
| RECALL-004 | 256 候選／第 257 筆哨兵、各欄位 bytes 上限、最多 4 筆、增量預算；真實 SQL timeout 無保存／呼叫。 |
| RECALL-005 | explicit 完整保留且優先，超量 auto 整筆略過；真實 ComposePrompt／Oracle Prepare 預算及基底超限測試。 |
| RECALL-006 | 指紋包含 bounded 模式、不含政策，已存在請求不重查；同時提交唯一任務／執行與衝突拒絕。 |
| RECALL-007 | 排隊後資料變更仍用快照；重啟沿用中斷失敗政策；零選取仍保存決策。 |
| RECALL-008 | 動態 OraclePlan／StrikerOutput 逐 stage admission；拒絕保留前序 trace，observe 不增加召回模型呼叫。 |
| RECALL-009 | 舊 JSON／資料相容、索引不改原值、status 稽核；未選候選僅保存彙總計數。 |
| RECALL-010 | 真實 10001 筆資料、超大單筆、索引、取消釋放單連線、benchmark；完整回歸及獨立目標 race 證據。 |

需求、架構、安全、品質及各失敗面的詳細輸入／預期／覆蓋結論，沿用原始報告「已查核維度」，本次文件差異未使任何一項失效。未見弱化測試、變更斷言或新增產品行為。

驗證來源為 [實作命令紀錄](evidence/implementation.log)、[驗收對應](evidence/verification.md) 及 [原始 reviewer 命令輸出](review-a4831a1.md#驗證證據)。2026-09-22 已獨立執行下列目標 race，五個套件皆通過、退出碼 0：

```sh
go test -race -count=1 ./internal/recall ./internal/storage ./internal/daemon ./cmd/asura ./tests/integration -run 'TestSelect|TestRecall|TestRECALL|TestAskRecall|TestStatusRecall'
```

本次文件查核命令為 `git diff a4831a161e3d470cced17561f4653febe8449532 9bd04e4d880b34b5cd21081696c7a31dfcbf92e2`；結果與上述四份文件一致，未改驗收規格。完整範圍重新計算的 patch-id 如報告欄位所載。

## 豁免、待確認與限制

無 gate 豁免；TBD-1～5 已核准。已知限制仍為詞彙／前綴候選漏召回、250ms 非硬即時 SLA、原始 prompt／explicit 處理成本未限制、固定單機合成 benchmark，以及以 queued fixture 模擬中斷而非強制終止測試程序。原始報告與實作證據已揭露，不構成假綠燈。

PR 已建立，issue 等待合併與 Task 4.1 完成狀態符合實際證據；本報告不宣稱已合併。後續僅新增本報告的單一直接後繼提交，仍須依 review-evidence.md 執行 artifact 有效性驗證。

## 流程判定

**PASS** — 完整固定範圍的獨立審查及新增文件核對完成，產品驗證保持有效，必要驗收與失敗面均有證據，無 MUST FIX，報告已持久化。
