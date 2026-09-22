# 交付驗證紀錄

2026-09-22，核准基線 aeaa652；被測版本為本紀錄同次實作提交的 Go 工作區。原始命令、輸出摘要及紅燈見 [implementation.log](implementation.log)。全套 race 後只新增 QueryDeadline 測試並單獨跑 race；production code 未再變動。文件及格式修正不改產品行為。

| 驗收 | 可觀察結果與測試 |
|---|---|
| RECALL-001 | TestAskRecall off wire 欄位省略；OffScopeExplicitAndNoRequery 刪除候選表後 off 仍成功、off/省略同指紋；Acceptance 未知模式拒絕；CTXMEM 全套相容 |
| RECALL-002 | OffScopeExplicitAndNoRequery：symlink、父子 scope、跨專案 explicit、清單外與重啟撤權；Acceptance foreign 內容不出現在 sealed JSON |
| RECALL-003 | TestSelect、SelectLimits、SelectDeadlineAndQueryRune：排序、混合語言、單字／同義詞無匹配、query caps；probe 固定 fixture 100 次同結果 |
| RECALL-004 | RecallCandidates 10001 rows + EXPLAIN + connection wait cancellation；MetadataAndLegacyMigration sentinel；BoundedAndExplicit 外層 256/4 邊界；QueryDeadline 真實慢 SQL 拒絕且未保存／執行 |
| RECALL-005 | RecallBudget 真實 ComposePrompt 76 bytes/33 tokens、無窗口與首次 Oracle Prepare 一致；SelectLimits 額度臨界；BoundedAndExplicit 長 explicit 不裁切；ExplicitOverrunAndCriticOverrun observe/reject 基底過量 |
| RECALL-006 | Acceptance mode 衝突及刪除後 snapshot；OffScopeExplicitAndNoRequery 刪表偵測不重查；ConcurrentRequest 一個 Task／一次呼叫；QueuedRestartSnapshot 設定漂移後仍回傳同 snapshot |
| RECALL-007 | QueuedRestartSnapshot 以 channel 強制排隊、刪記憶後喚醒；重啟 crash 遺留 fixture 依既有政策 failed 且 0 rerun；Acceptance 0-added 存 recall |
| RECALL-008 | StageAdmission reject 保留 Oracle trace、observe 三次模型呼叫；ExplicitOverrunAndCriticOverrun 動態 StrikerOutput 阻擋 Critic；每 stage 收到同記憶；CTXWIN/TFACE/MBACK 回歸 |
| RECALL-009 | RecallJSONCompatibility 舊 JSON 與 off omitempty；MetadataAndLegacyMigration 舊 DB 新索引不改原值；StatusRecallAudit 顯示 provenance；候選隱私由整合與 code inspection 核對 |
| RECALL-010 | 全套 race 包含 CTXMEM/CTXWIN/TFACE/MBACK/CRON/WEB/PGID cancel/redelivery；正式 selector benchmark、有界真實 SQL、vet、build |

## 紅綠與精煉

外層先以真實 IPC 接受未知欄位後缺少自動記憶產生紅燈；storage 以缺少新增查詢契約產生紅燈；selector 未連接產品的空實作先重現空排名紅燈。不同外部與底層失敗面保留雙迴圈。後續新增邊界測試支援相同驗收，並非每筆邊界都有獨立紅燈。

曾修正測試 fixture：time.Now 的 monotonic 字串不符合有限 timestamp 契約，改用 UTC；Critic fixture 的 passed 改為既有契約 pass。這兩次失敗不列為產品紅燈。

2026-09-22 code-simplify：固定 policy 使用具名欄位，避免 positional 數值錯置；移除本次 gofmt 帶來的不相關排版變更。既有 Oracle Prepare 直接重用，無需另建 prompt abstraction 或改指令。精煉後同組目標測試由完整 race 覆蓋，全部通過。

## 限制與相容性

- 詞彙召回及候選 ID 前綴有已核准的漏召回限制；不保證語意品質。
- 250ms 為可取消期限，不是硬即時 SLA；輸入 explicit／原始 prompt 本來的大小未在本期限制。
- benchmark 是固定合成資料的單機結果，非 worst-case／線上 SLA；SQL 回傳量受限不代表底層磁碟 I/O 完全固定。
- restart 沿用既有 failed-on-interruption，不新增自動重新執行。測試以持久化 queued fixture 模擬中斷後真實 Start，未強制 kill 測試主程序。
- query deadline 用真實 SQLite 遞迴 view 造成慢查詢，不 mock 選取或儲存；成功後同一 store 可執行 off 任務。
- 2026-09-22 [獨立 review PASS](../review-a4831a1.md) 已持久化；本紀錄仍僅為實作驗證證據。
