# 專案記憶

專案級記憶的管理、Git 候選審核、有界召回、效用回饋與版本取代。Web 上的對應操作見 [Web Console](web-console.md)。

## 基礎記憶管理

```bash
# 新增專案記憶（純文字內容，固定 manual 來源與建立時間）
./bin/asura memory add \
  --workspace /path/to/project \
  --content "專案重要架構決策與業務規則"

# 列出專案所有記憶（五欄位完整顯示）
./bin/asura memory list \
  --workspace /path/to/project

# 查詢指定 ID 記憶之詳細五欄位（ID、Workspace、Content、Source、CreatedAt）
./bin/asura memory get \
  --workspace /path/to/project <memory-id>

# 刪除指定記憶（依 ID 與專案精確比對刪除）
./bin/asura memory remove \
  --workspace /path/to/project <memory-id>
```

2026-09-15：
- **五欄位完整回傳**：查詢與寫入均完整回傳 `ID`、`Workspace`（標準化正規路徑）、`Content`（完整文字不截斷）、`Source`（固定為 `manual`）與 `CreatedAt`（建立時間）。
- **精確 Workspace 隔離**：記憶嚴格限定於該專案的標準化路徑；同一授權 root 下之不同專案嚴格隔離；子目錄不繼承父目錄記憶；有效符號連結別名共用相同專案記憶集合。
- **撤權與重新授權政策**：專案自 `--allowed-roots` 撤權後，原記憶保留於儲存層，但所有讀寫刪除操作皆嚴格拒絕；重新獲得授權後立即恢復查詢與操作。
- **重啟持久化與指定刪除**：記憶持久化保存於 SQLite，重啟 Daemon 完整保留未刪資料；刪除指定記憶後重啟不恢復，且另一專案與同專案其他記憶完全不受影響。
- **失敗回報與零 AI 調用**：Daemon 不可用或操作失敗時，CLI 明確輸出錯誤至 stderr 並回傳非零退出碼，絕不冒充已保存或已刪除，不改走其他儲存方式；記憶管理操作直通儲存層，絕不啟動 AI 模型後端；既有任務提交不自動注入記憶內容。

## Git 候選記憶

```bash
# 擷取 Git 變更事實產生待審候選草稿（支援固定 --range 或增量 --since-last）
./bin/asura memory capture \
  --workspace /path/to/project --since-last

# 檢視專案所有待審候選記憶草稿（包含原始變更事實與 Provenance 來源）
./bin/asura memory candidates \
  --workspace /path/to/project

# 核准指定候選草稿轉入正式記憶（可選 --content 就地修訂內文）
./bin/asura memory approve \
  --workspace /path/to/project <candidate-id>

# 捨棄指定候選草稿（標記為 rejected，不可逆且防復活）
./bin/asura memory discard \
  --workspace /path/to/project <candidate-id>

# 清理專案所有已處理完畢之候選紀錄（清除 approved/rejected，保留 pending）
./bin/asura memory clear-candidates \
  --workspace /path/to/project
```

2026-09-25：
- **Git 變更感知與候選隔離（Phase 3H）**：支援 `asura memory capture`，唯讀安全執行 Git 子程序讀取固定範圍（`base..head`）或 `--since-last` 增量變更。
- **實體儲存與召回隔離**：候選草稿存放於獨立 `memory_candidates` 表，未審核之草稿 100% 隔離，絕不進入 Task 記憶召回 prompt；僅在顯式 `asura memory approve` 後寫入正式記憶（`source='git-approved'`）並建立獨立 `memory_provenance` 來源記錄。
- **有界截斷與 Checkpoint 安全**：受硬性邊界（200 commits、500 files、256 KiB diff）限制；超出限制時標記 Partial 截斷，且嚴格禁止自動前進 Checkpoint，避免未分析歷史遺漏；不完整候選（Partial Candidate）嚴格禁止核准（Approve），杜絕殘缺事實污染正式專案記憶。
- **防復活與歷史密封性**：核准或拒絕紀錄持久化保存，防止重複 Range 重新擷取；手動刪除已核准記憶後禁止原 Candidate 復活；既有 Task 快照維持密封不變。

## 有界自動記憶召回

2026-09-22：`ask` 可明確啟用詞彙召回，預設 `off`；Web/Cron/run 不新增啟用入口。

```bash
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project \
  --memory-recall bounded --memory-id explicit-id "檢查 SQLite 隔離規則"
```

- 僅讀取已授權且相同 canonical workspace 的記憶，接受任務時封存；相同 request ID 重送沿用原快照，即使記憶或管理者設定改變也不重新選取。
- `lexical-v1` 使用英文等字詞與相鄰中文雙字詞比對，不支援同義詞／語意檢索。只評分 ID 升序前 256 筆；query 使用前 4096 bytes 完整字元前綴、最多 128 個不同詞，可能漏召回。
- 自動最多加入 4 筆，實際格式化增量最多 8192 bytes／2048 estimated prompt tokens；explicit 記憶不裁切或替換。候選內容超過 8192 bytes 或 metadata 過大時整筆略過。
- 有已設定窗口時，auto 也受首次 stage 含輸出保留的估算限制；原始 prompt／explicit 基底超限仍沿用 `observe`／`reject`。後續每個 stage 依實際產物重新 admission。未設定窗口時，只保證 auto 額度有限，不能保證模型不溢出。
- 查詢及選取期限 250ms，逾時或資料庫錯誤拒絕提交。`status` 的 `Recall` 提供選取來源、排序分數、政策、estimated budget 與略過原因計數，不保存未選候選全文。
- 重啟沿用既有政策：未完成任務標為中斷失敗，快照保留，不自動重跑。

## 記憶效用回饋與稽核

2026-10-01：bounded recall 建立唯一 `recall_id`，與 Task 的 sealed context 同交易保存；相同 Request ID 重送沿用原決策。Daemon 預設 `ASURA_MEMORY_UTILITY_POLICY=observe`，維持 `lexical-v1` 排序；設定為 `apply` 才對未來 auto recall 套用 `utility-v1` down-weight。非法 policy 會拒絕啟動。

```bash
ASURA_MEMORY_UTILITY_POLICY=apply ./bin/asura daemon --allowed-roots /path/to/project
./bin/asura memory utility --workspace /path/to/project <memory-id> --limit 20
```

2026-10-01：有效樣本為 used+unused；少於 5 筆或全部 unknown 時 multiplier=1。達 5 筆後 multiplier=1−0.5×unused/(used+unused)，以 millionths 向下取整降權量，範圍 0.5～1；final score 同分依 ID 升序。explicit 不降權，既有 256 候選、Top-K、query、prompt budget 與 250ms recall deadline 不變。`status` 的 Recall 保存 policy、lexical score/rank、weight、final score 與 estimated provenance，Utility 顯示 pending/error/complete；legacy 無事件顯示 unavailable。

2026-10-01：Three-Face 利用既有 Critic 呼叫回報 used/unused/unknown；屬模型 estimated 訊號，不能當作實際因果效用。捏造／重複／非 auto ID 或不合法 usage 區塊全部轉 unknown；Direct 不新增模型呼叫，取消／無可靠 Critic 訊號保持 unknown。assessment 與 aggregate 同交易、防重複，可由 ledger 重建。執行後 UFL 錯誤不改主 Task 結果、不重跑 stage；重啟以每批 100 events 補寫。admission audit 寫入失敗或 apply 權重讀取失敗拒絕提交。

2026-10-01：utility 查詢限定已授權 exact canonical workspace，回應明列 utility policy 與當前有效 weight（observe 固定 1.0），單頁預設 20、最多 100 observations，以 `--cursor` 接續；cursor 最多 512 bytes、回應最多 64 KiB。刪除記憶不復活，同 scope 保留歷史 audit。第一版無 GC，DB 總量仍隨事件增長。真實模型 probe 只提供固定版本、有限 fixture 的一致性證據，見 [Issue 29](../issues/issue-29/README.md)。

## 記憶生命週期與版本取代

最後驗證日期：2026-10-01。

正式記憶預設 `active`／non-durable。建立 successor 時使用：

```bash
./bin/asura memory add --workspace /path/to/project --content '新的決策' \
  --supersedes <old-memory-id> --operation-id <stable-operation-id> --durable
./bin/asura memory durable --workspace /path/to/project <memory-id>
./bin/asura memory detail --workspace /path/to/project <memory-id>
```

`--supersedes` 必須搭配穩定的 `--operation-id`；相同 operation 與 payload 重試回原 ID，不同 payload 拒絕。已刪 successor 的重試不會重建記憶。取代以單一交易建立新記憶並將舊記憶標為 `superseded`，舊內容保持不變；一條 chain 僅有一個 active 版本。`--reason` 可補取代理由，最多 1024 bytes；operation ID 最多 128 bytes，relation ID 最多 256 bytes。

Auto recall 在候選掃描前排除 superseded，不占 Top-K 或 prompt budget。顯式 `--memory-id` 仍可使用尚未刪除的 superseded 記憶，sealed snapshot 與 bounded recall audit 會保存接受時的 lifecycle／successor。後續 supersede、delete、重啟或重送不改舊 Task。

Durable 只保護未來自動 retention，不提高 lexical／utility 排名，也不固定進 Top-K；仍可顯式 supersede 或 remove。本期沒有自動 GC 或解除 durable 操作。successor utility 從零 observations／neutral weight 開始，predecessor 的 UFL 歷史保留。

Detail 回傳 lifecycle、durable、直接 predecessor／successor、Git provenance identity、utility aggregate 與時間。每次需 exact canonical workspace 授權；跨 scope 與不存在 ID 使用相同錯誤。content 最多 65536 bytes、完整 JSON 最多 131072 bytes，超限拒絕；查詢 deadline 2 秒。remove 沿用硬刪內容，relation ledger 保留無內容的歷史 ID／理由；可逐節 detail 稽核已刪 chain 節點，successor 消失不復活 predecessor。詳見 [Issue 31](../issues/issue-31/README.md)。
