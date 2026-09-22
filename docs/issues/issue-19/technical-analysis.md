# 技術提案與取捨

2026-09-22；D1–D5 於 2026-09-22 經使用者核准。探測只代表可行性，不代表產品實作已通過驗收。

## D1 — 詞彙召回與候選上界

建議 `lexical-v1`：Unicode lowercase；連續漢字取相鄰雙字詞，其他連續字母／數字取長度至少 2 的詞；標點切開。每個不同 query term 在候選出現即加 1 分，不累加重複次數。至少 1 分才進排名，分數降序、ID 升序。不做繁簡、同義詞、詞幹或 Unicode compatibility 正規化，不能稱為 semantic retrieval。

只對原始 prompt 前 4096 UTF-8 bytes 的完整 rune 前綴取詞，依出現順序最多 128 個不同詞；原始 prompt 本身完全保留。query 截斷保存旗標。fixture 已覆蓋中文、英文、混合、大小寫、tie、無匹配、單字與同義詞限制；核准後增加識別字、長 query 與 Unicode fixture。

候選讀取同 workspace 的 ID 升序前 257 筆，只評分前 256 筆，第 257 筆只用來標記 `candidate_limit`。不先用內容大小或 explicit IDs 過濾 SQL，避免為找滿 256 筆而掃描全表；explicit 在固定集合內排除。上限內超大記憶不補位，這是可稽核的漏召回取捨。

新增 `(workspace,id)` 索引，查詢形狀為：

```sql
SELECT substr(CAST(id AS BLOB),1,257),
       substr(CAST(content AS BLOB),1,8193),
       substr(CAST(source AS BLOB),1,129),
       substr(CAST(created_at AS BLOB),1,65)
FROM memories
WHERE workspace = ? ORDER BY id LIMIT 257;
```

單筆可接受 ID/content/source/created_at bytes 上限分別 256/8192/128/64；sentinel 超限或資料無法合法還原時整筆略過，不使用截斷快照。workspace 來自 canonical request，不讀入每列的任意長 workspace。最多回傳 257 × 8644 = 2,221,508 bytes 的欄位 payload（不含 driver/object 開銷）；超限理由只有彙總計數。

探測已以實際 SQLite driver 驗證 content sentinel、同 workspace 查詢及複合索引；metadata sentinel 與 context cancellation 還需要實作測試。SQL timeout 不是硬即時保證，作業系統排程可能延後取消；其目的是限制查詢占用，搭配固定列數與欄位上限控制工作量。

取捨：

| 方案 | 優點 | 限制／決定 |
|---|---|---|
| 全量 ListMemories 後排序 | 覆蓋所有候選 | 無界讀取與排序，拒用 |
| 有界集合＋Go 詞彙分數 | 無額外依賴、可重現、中文策略透明 | 會漏掉 ID 前 256 筆以外的記憶；建議首版 |
| FTS5 | 可先依索引檢索相關集合 | 漢字斷詞與 tokenizer 契約尚未驗證，須額外索引／同步策略；首版不採用、不宣稱環境不支援 |

## D2 — 啟用介面

新增 `asura ask --memory-recall off|bounded`，預設 off；IPCRequest 與 SubmitRequest 為 `memory_recall,omitempty`。省略、空字串與 off 正規化為同一語意；其他值拒絕，0 backend calls。第一版不提供 request 自訂額度，也不增加 Web/Cron/run 啟用入口。Web/Cron 的既有請求維持省略欄位。

## D3 — 有限額度與 admission

建議固定：auto 最多 4 筆，格式化增量最多 8192 bytes 及 2048 estimated prompt tokens。這是保守的產品額度提案，非 benchmark 推導的最佳值，也不是模型窗口。大型任務仍可 explicit 指定。

對每個排名候選，用真實 ComposePrompt 計算 `withAuto - explicitOnly` 的 bytes 與 EstimatedPromptTokens，包含標題、ID、換行；不對 raw content 計費。超任一上限整筆略過，繼續較小候選，不修改 explicit。回傳最終增量及 estimator method。

有 ContextLimit 時，另外準備首次實際 stage prompt（Direct 或 Oracle，含固定指令），含既有 reserveRatio 的 TotalEstimatedTokens 必須不超限才可加入 auto；observe/reject 均限制 auto 增量。若 explicitOnly 基底已超限，auto=0，任務仍接受並由原 stage policy warning/reject；不以召回 gate 刪掉使用者內容。無窗口時只套固定 auto 額度並標記 unknown。

首次 prompt 準備應與正式 Pipeline Prepare 共用同一組裝來源，避免複製指令；保持既有字串完全相同並以 characterization tests 保護。後续每個 stage 仍對實際動態產物獨立 admission，接受時不估造 OraclePlan/StrikerOutput。

有界候選查詢與選取共用 250ms context deadline（包括等待 SQLite 連線）；每筆／預算計算間檢查取消。逾時、資料庫錯誤拒絕接受，無 Task、無 backend 呼叫；不是靜默降級成 0 added。既有 prompt／explicit 的處理成本不因本期被重新定義為有界，對很大的使用者原始內容仍有原本的成本；此限制需揭露。

## D4 — 密封與稽核

在 ExecutionContext 新增 `recall,omitempty` 可選指標，包含 mode、policy_version、explicit_ids、auto_ids（排名順序）、選中 ID 的分數與 `lexical_overlap` 理由、候選讀取/評分數、截斷旗標、略過原因計數、formatted bytes、estimated delta、estimator method、首次 stage budget 與設定來源。保存有效 policy 額度及接受時 ContextConfig。

快照序列：既有 explicit ID 排序，後接 auto 排名；Source 保留原本 manual，不能拿 Source 冒充 selection origin。MemoryRefs 為同一快照 ID 順序，explicit/auto 由決策陣列分辨。未選候選不保存 ID 清單、全文、片段或匹配 query 詞；skip reasons 是 bounded aggregate（duplicate、oversize、invalid_metadata、no_match、item_limit、byte_limit、token_limit、stage_limit）。

bounded 即使 0 added 也序列化完整 context 與決策。off 不新增 JSON 欄位。舊資料缺 recall 視為 off、所有現有 snapshots 為 explicit。不需要新 Task 欄位或改寫舊資料，僅 idempotent 新建複合索引；索引建立耗時需用已有大資料測試量測。

Task 的 resolved_context 為稽核唯一來源；CLI status 顯示 compact 摘要。PipelineTrace 透過所屬 Task 指向同一決策，不複製候選；stage trace 保留現有 admission 數據。既有 Task 查詢授權不變。

## D5 — 冪等與政策漂移

```diff
 authorize / normalize / fingerprint / redelivery
 resolve explicit
+bounded candidate read / rank / budget / recall decision
 capacity / seal / persist / dispatch
```

off 完全沿用舊 fingerprint 函式與 bytes。bounded 用 domain-separated 固定結構 JSON（canonical workspace、原始 prompt、mode、backend、normalized explicit IDs、memory_recall=bounded）計算 SHA-256，避免擴充舊冒號串接造成欄位歧義。selector 版本與管理者設定不屬於 request identity；記在 snapshot。因此規則或設定升級不使舊 ID 衝突，也不重選。若未來增加使用者 policy 參數，另立規格。

CreateTask duplicate request 競態分支必須採同一指紋檢查。兩個同時首次提交可能在其中一個保存前各做一次只讀召回，但唯一 request_id 只允許一個 Task／一次執行；不得覆蓋勝出的 snapshot。已存在的重送必須 selector 0 calls。

## 待核准與限制

D1–D5 對應 README TBD-1～5。小型合成語料不是搜尋品質保證；benchmark 是單機排序結果，不是生產延遲 SLA。timeout 取消、metadata 上限、重啟／競態與所有 RECALL 驗收須核准後補齊紅綠證據。
