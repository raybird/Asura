# Issue 29 技術分析與核准方案

分析日期：2026-10-01。以下方案於 2026-10-01 經使用者「核准」，尚待實作驗證；來源與基準見 README。

## 方案與取捨

比較把所有事件保存在 Task JSON、獨立 ledger 加 transactionally maintained aggregate 兩種方案。前者查詢／重建需掃描大量 JSON，且缺少 assessment 唯一鍵；建議後者，以明確唯一鍵及交易保障重送。不另建立非同步 worker 或背景模型。

```diff
 admission
- CreateTask(sealed context)
+ CreateTaskWithRecall(sealed context, immutable event) in one transaction
 terminal outcome
+ PersistUtility(validated sealed auto IDs) in one transaction
 future bounded recall
+ read aggregate for the same bounded candidate IDs
```

聚合採持久化 cache，與新增 assessment 同交易更新；以 ledger 重建作為獨立對照。只用即時計算雖簡單，apply 每次需掃歷史，難以維持既有 250ms 上限。

## K-TBD-1：schema、migration、retention、recovery

建議新增 recall_events（recall_id primary key、task_id unique、request identity、workspace、policy/version、created_at、必要 budget/skip metadata）、recall_selections（recall_id+memory_id unique、kind、lexical score/rank、weight、final score/rank）與 utility_assessments（recall_id+memory_id unique、state、provenance、reason、observed_at）。aggregate 以 workspace+memory_id 為鍵，保存各 count 與 last observation。

selection 只保存選中的 explicit/auto metadata，不复制 content、不保存未選候選；bounded auto 不超過 4。explicit metadata 沿用既有 Task 邊界，不新增無限制 IPC 回傳。事件與初始 Task 同交易，admission 失敗不留下孤兒，zero-auto bounded Task 仍有事件。

assessment 不更新、不覆寫；不同 payload 的 retry 明確 conflict。聚合只在真正 INSERT 成功時累加。aggregate 重建採固定高水位、keyset batch，交易避免 concurrent insert 遺漏或重算；不得整批載入記憶體。重啟先沿用現有中斷任務政策，再以每批至多 100 events 的 bounded recovery 補 assessment，不重跑模型。retention 第一版保留 audit、不新增 GC；長期 DB 總大小仍隨事件增長，列為明確限制。

## K-TBD-2、3：訊號與終態語意

CriticReview 增 optional `memory_usage`：至多 4 筆 `{memory_id,state}`，state 僅 used/unused/unknown。只有 auto IDs 非空時附加 Critic 指令與 allowlist；不影響 off/explicit prompt。舊 review 無欄位合法，補 unknown。

建議 invalid、duplicate、fabricated、explicit-only、跨 scope ID 或過量陣列使整個 usage 區塊失效；所有 auto IDs 記 unknown，不接受其中部分 used/unused。不得使合法 verdict 因 optional usage 語法或型別錯誤失效；實作需分離 raw optional 欄位解析與核心 review 驗證。核心 review invalid 則維持既有 Task failure。

只有 Three-Face 的 Critic 完成且核心 review 有效、Task 未取消时接受 usage；verdict failed 可保留 estimated 訊號，任務成敗與「有使用」並非同義。Direct、Critic 前失敗、Critic admission timeout、cancelled、invalid/missing usage 皆 unknown。所有模型 used/unused/unknown 標 estimated；未有模型訊號的 unknown 標 unknown provenance。missing entry 個別補 unknown。

## K-TBD-4：apply 精確提案

有效樣本 n=used+unused；n<5 時 weight=1。n>=5 時 weight=1-0.5*(unused/n)，上下限 [0.5,1]。unknown 不進分母。採 millionths 整數：weight_ppm=1,000,000-floor(500,000*unused/n)，final_score_scaled=lexical_score*weight_ppm，無浮點排序；final score 同分則 ID 升序。需驗證乘法溢位與壞資料拒絕。

例如 used=0、unused=4 為 1；used=0、unused=5 為 0.5；used=5、unused=5 為 0.75；全部 unknown 為 1。observe 無條件 weight=1、完全沿用 lexical order。explicit 不讀取或套用 multiplier。僅 lexical score>0 候選能進排序。

## K-TBD-5：設定與版本

建議 Daemon 環境變數 `ASURA_MEMORY_UTILITY_POLICY=observe|apply`，預設 observe，非法值啟動即拒絕。utility policy version `utility-v1` 與既有 `lexical-v1` 分開封存。管理者設定不進 client fingerprint，redelivery 沿用 sealed policy；不新增 request-level policy 或 Web 開關。

## K-TBD-6：failure boundary

Task admission 的 event/Task 原子寫入失敗，observe/apply 均拒絕提交並釋放容量；不能宣稱 Task 已接受卻缺必要 audit。apply aggregate read timeout/error/invalid data 拒絕提交，不靜默降回 observe。observe 不需讀 aggregate 排序。

Task 執行後 assessment/aggregation 失敗，不改成功／失敗／取消主結果、不重跑 stage；utility query/status 明確顯示 pending/error，未保存不能冒充有效計數。重啟從已保存 trace 與 sealed auto IDs 補寫；trace 缺失只可 unknown。assessment+aggregate 同交易避免只寫一半。此產品失敗行為於 2026-10-01 核准。

## K-TBD-7：CLI／IPC bounds

建議 `asura memory utility --workspace <path> <memory-id> --limit N --cursor <opaque>` 與 IPC `memory_utility`。limit 預設 20、範圍 1..100；不合法值明確拒絕。cursor 綁 canonical workspace、memory ID、時間及 recall ID；上限 512 bytes。依 observed_at DESC、recall_id DESC keyset 分頁，aggregate 與近期 observation 一起回傳，禁止 OFFSET 全歷史掃描。

ID 上限 256 bytes；所有 query 欄位使用 SQL bounded projection，response 上限 64 KiB，超量或 malformed legacy 失敗而非隱藏截斷。status 顯示 sealed policy、score、weight、provenance 與 utility persistence 狀態，不用現況重算歷史 score。

## K-TBD-8：刪除與 legacy

不得以 memory foreign key cascade 刪 audit。刪 memory 後 future recall 自然不再選取，utility 查詢需先 canonical authorization 再 exact scope ledger lookup；同 scope 可查 tombstone audit，其他 scope 與不存在 ID 回覆相同錯誤。新 memory ID neutral，不繼承 candidate aggregate。legacy Task 無 recall_id 顯示 unavailable，不推測補建歷史 telemetry。

## K-TBD-9：容量實驗

索引至少包含 event(workspace,created_at,recall_id)、assessment(workspace,memory_id,observed_at,recall_id) 與 aggregate(workspace,memory_id)。10k／100k events 固定 seed SQLite fixture，記 DB bytes、query plan、query/write/recovery latency、每事件寫入量。以 explain 與 load 對照證明無全表掃描，測 100-row batch 與 64 KiB response。apply admission 整體仍須符合原 250ms hard deadline；性能數值待實測，不能宣稱既已通過。

## K-TBD-10：可信度 probe

使用獨立人工標註的正反例：Striker 明確引用只有某 memory 才有的具體規則為 used；另一個相關但未採用的規則為 unused；無法由輸出判定者為 unknown。保留人工判準與原始輸出，加入 fabricated/duplicate/explicit/跨 scope ID 故障對照。固定 backend、model、版本與 prompt，至少 3 組 fixture 各 3 次；used/unused 可判定例需至少 8/9 與標註一致，所有 invalid ID 寫入有效訊號次數為 0，Direct extra calls=0。

這只能提供有限 estimated 一致性證據，不能證明真實因果效用。stub 用於 ID validation／retry 契約，不能取代真實模型 probe。若一致性不足，先維持 observe，回報結果並重新決定 apply 範圍，不能自動放寬判準。
