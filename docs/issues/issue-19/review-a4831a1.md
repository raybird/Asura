# Issue 19 獨立審查報告

- 審查日期：2026-09-22。
- 範圍：完整 PR diff，24 個異動檔案；High 風險，重点為 scope 隔離、快照冪等及資源上限。
- Reviewed BASE SHA：8576dd0323750a1aa8f2c550c0f69e8edd30e55d
- Reviewed HEAD SHA：a4831a161e3d470cced17561f4653febe8449532
- Reviewed patch-id：eadb898640880c5cca967769052a3dc96d79b2a1
- 獨立 reviewer：Codex subagent `/root/review_issue19`；未參與產品實作，未修改產品程式碼。
- Review artifact：`docs/issues/issue-19/review-a4831a1.md`

## 問題與風險

MUST FIX：無。SHOULD FIX：無。

NICE TO HAVE：`internal/recall/recall_test.go:15` 可將規劃 probe 的完整混合語料及重複詞分數字面值搬入正式 selector 測試；正式測試已驗排名、同分 ID 順序、query 上限與無匹配，但完整 probe 使用獨立規劃實作。本項是回歸證據持久力建議，不影響本次 code inspection 與現有測試結論。

## 已查核維度

核准來源為 2026-09-22 使用者對 D1–D5／RECALL-001～010 回覆「核准」，承載規格提交 aeaa652。比較該提交至 reviewed HEAD 的 README，驗收條件未改變，僅補證據、檔案清單與核准 SHA。已讀流程契約、project、acceptance、verification、review-evidence，以及完整產品／測試／文件 diff。

| 驗收 | 查核與證據 | 結論 |
|---|---|---|
| RECALL-001 | CLI off 省略欄位，Daemon 正規化 off；沿用原 fingerprint；刪除 memories 表後 off 仍可執行；未知模式在保存／執行前拒絕。 | 通過 |
| RECALL-002 | 授權先於 redelivery／候選查詢；SQL 精確 workspace；canonical symlink、父子與清單外測試、foreign explicit 拒絕、撤權重送拒絕。 | 通過 |
| RECALL-003 | Unicode lowercase、漢字 bigram、query 去重、不同匹配詞計分、score 降序／ID 升序；正式 selector 與 fixture、無匹配及完整 rune 截斷測試。 | 通過 |
| RECALL-004 | workspace/id 索引、LIMIT 257、256 評分、四欄 byte sentinel；超大／非法資料整筆略過；4 筆／8192 bytes／2048 estimated tokens；真實慢 SQL timeout 拒絕，無 Task／backend。 | 通過 |
| RECALL-005 | explicit 先解析、去重／排序後完整保留；auto 排除 explicit、超量後繼續；ComposePrompt 真實增量；Direct 與首次 Oracle Prepare／reserve 一致；基底超限 auto 為零。 | 通過 |
| RECALL-006 | bounded domain-separated 指紋只含 payload；同 ID 先返回快照，不查已刪除表；mode 衝突、同時提交單次執行；cancelled 沿用原有拒絕分支。 | 通過 |
| RECALL-007 | 強制同 workspace 排隊後刪記憶，喚醒仍使用原快照；Start 恢復沿用 interrupted→failed，設定改變不重選；0-added 保存 recall。 | 通過 |
| RECALL-008 | 各 stage 使用 sealed memories；OraclePlan 與 StrikerOutput 超限分別阻擋後續 Striker／Critic，保留前序 trace；observe 繼續且模型呼叫數符合原管線。 | 通過 |
| RECALL-009 | 可選 recall 指標／omitempty 保持舊 JSON；explicit/auto 順序與選中分數、政策、预算、skip counts 可追溯；status 顯示；新增索引不改既有資料。 | 通過 |
| RECALL-010 | 真實 SQLite 10001 筆、1 MiB 單筆、索引計畫、等待連線與執行中取消；selector benchmark；已保存完整 race／vet／build，另獨立重跑目標 race。 | 通過 |

重要失敗面另核對如下：

- 未選候選隱私：Decision 僅保存選中 IDs／分數及彙總計數；未保存未選全文、片段、匹配詞或 ID 清單，錯誤回應固定文字。
- 預算與取消：每筆評分／預算嘗試及最終回傳前檢查 context；查詢與 selector 共用期限，SQL rows 關閉釋放單連線。取消期間不取得容量、不保存任務。
- 邊界與相容性：explicit 不受 auto 內容上限裁切；同分排序穩定；多位元字元 query 不切半；未知窗口標記 unknown；off 不觸發新查詢或新 JSON。
- 架構：storage 僅讀有界候選，selector 純文字與預算選取，Daemon 密封；不引入模型召回、backend 切換、額外套件或跨 scope fallback。首次 Direct 無 plan 的既有 Striker Prepare 確實等於 ComposePrompt；Oracle 直接共用 Prepare。
- 測試真偽：真實 SQLite／IPC／orchestrator 未 mock 核心召回；backend recorder 用於輸入與呼叫計數。排名與 wrapper 預算具有獨立字面值；紅燈、後續全綠與精煉紀錄已保存，未見弱化既有測試。

## 驗證證據

既有完整交付證據：`docs/issues/issue-19/evidence/implementation.log`、`verification.md`，涵蓋外層紅燈、storage 能力缺失紅燈、selector 空排名紅燈、目標綠燈、全套 race／vet／build，以及追加真實 SQL timeout。

2026-09-22 reviewer 在 reviewed HEAD 獨立執行（Go Snap 於核准的沙箱外環境）：

```sh
go test -race -count=1 ./internal/recall ./internal/storage ./internal/daemon ./cmd/asura ./tests/integration -run 'TestSelect|TestRecall|TestRECALL|TestAskRecall|TestStatusRecall'
```

exit=0：

```text
ok asura/internal/recall 1.011s
ok asura/internal/storage 6.381s
ok asura/internal/daemon 1.056s
ok asura/cmd/asura 1.063s
ok asura/tests/integration 2.207s
```

## 豁免、待確認與限制

無 gate 豁免，TBD-1～5 均有使用者核准。詞彙召回／前 256 筆導致漏召回是已核准取捨。250ms 為可取消期限而非硬即時 SLA；既有原始 prompt／explicit 成本不受本次上限約束；SQL 回傳有界不等於磁碟 I/O 完全固定。benchmark 是固定單機合成資料；重啟採持久化 queued fixture 模擬中斷，未 kill 主程序。上述限制已如實揭露，不構成假綠燈。

Task 4.1 的 PR／review 仍在協調者交付步驟中；本報告提供獨立審查證據，不宣稱 PR 已合併。後續若新增除單一 review artifact 之外的提交，需重新核對版本有效性。

## 流程判定

**PASS** — 完整範圍獨立審查完成、必要驗收與重要失敗面有證據、無 MUST FIX；報告已持久化。本結論綁定上述 reviewed HEAD，僅依 review-evidence 的單一新增報告提交例外可沿用。
