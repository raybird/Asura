# Issue 35 — 技術提案

2026-10-03 提案，排序及設定入口已分批核准，其餘待核准。以 [現況盤點](requirement-analysis.md) 與 [驗收規格](README.md) 為依據。

## 選型與取捨

| 決策 | 提案 | 取捨 |
|---|---|---|
| policy priority | 預設 opencode 優先、其他 ID 字典序、omp 最後；既有四者為 opencode > antigravity > codex > omp | 2026-10-03 使用者指定 OMP 最後；其他排序避免無依據的品質排名；2026-10-03 使用者「好核准」確認可調入口 |
| policy 抽象 | 共同 package 的純函式與固定 ID/version | 可調 priority 以資料輸入純函式即可，不需要 plugin interface |
| persistence | 在 ExecutionContract additive SelectionDecision，沿 resolved_context 原子保存 | 新 SQL 欄位/table 需要 migration，本期沒有跨 Task selection 搜尋需求 |
| version error | 完整快照建構失敗則整次 auto fail-closed | 排除版本失敗候選可提高可用性，但實際引入 availability routing；提案代價是任一已註冊但缺 binary 候選會阻擋 auto |
| local run auto | 新 opt-in 路徑使用真實 adapters | 只支援 ask auto 不滿足 issue；改全部 run 為 AI 破壞既有 shell 契約 |

```diff
 workspace authorization / canonical request identity
 persisted request lookup / redelivery
- explicit runtime admission
+ explicit: 維持原 admission
+ auto: registry membership snapshot → descriptors → matcher → eligible → policy
 memory / context admission
- seal requested backend as execution backend
+ seal original intent + decision; execution backend = selected ID
 atomic CreateTask / queue / dispatch
 same selected runtime safety guard → stage Run
```

## RS-TBD 對照與實作細節

| ID | 具體提案 |
|---|---|
| RS-TBD-1 | `RuntimeSelectionRequest{Mode, RuntimeID}`：mode 僅 explicit/auto；explicit 非空 ID，auto 不含 ID；邊界由既有 backend 字串解析，空值 normalize explicit(opencode) |
| RS-TBD-2 | CLI `--backend auto` 明確 opt-in；省略仍 opencode，不採 Registry.Default；保留 requested_backend 原值 |
| RS-TBD-3 | Registry 新增 snapshot 入口，在同一 RLock 下複製 ID/backend references 並按 ID 排序，鎖外 capture descriptors；ID 是註冊唯一身分，不引入 timing-based rank；快照建立後不跟隨 registry 更新 |
| RS-TBD-4 | candidate 保存 RuntimeDescriptor 與共同 CapabilityMatch；eligible ID、rejected ID/reason、selected descriptor 可由同一快照核對；無 eligible 錯誤包含 inspected candidates 與能力摘要 |
| RS-TBD-5 | 純 selection 函式，無獨立 SelectionPolicy interface；不把 policy 放 Registry 或 adapter |
| RS-TBD-6 | 預設 opencode 優先、其餘 ID 字典序、omp 最後；可選使用者 priority 列表，未列者依預設規則補齊；registry audit ID 序與 policy priority 分開 |
| RS-TBD-7 | 封存 policy_id=`deterministic-priority`、policy_version=`v1` 與有效 priority 列表（含補齊項）；capability-v1 policy/version 仍保留，兩種政策分開解讀 |
| RS-TBD-8 | additive SelectionDecision 保存 intent、所有候選、eligible/rejected、selected、policy；requirements 引用同一 ExecutionContract。最多 32 candidates，decision JSON 最多 64 KiB，超限拒絕而不截斷；上限待核准，不冒稱既有契約 |
| RS-TBD-9 | fingerprint 以 intent 建立：沿用原 explicit hash，auto 的 backend 字串為 auto，絕不納入 registry/selected/version；engine 比 persisted intent，派送使用 selected ID；正常重送先 lookup，並行首次 admission 容許多次 capture 但唯一 Task／execution |
| RS-TBD-10 | IPC 既有 backend 欄位可傳 auto，Web API additive backend；舊 client 缺欄位不變。CLI/status/trace/Web audit 從 sealed contract 顯示 requested/selected/policy/candidates；不加入 Web management UI |
| RS-TBD-11 | admission 前建立 Three-Face 完整 requirements union，一次 matcher/policy，三 stage 使用同一 selected ID；執行期 guard 不重選 |
| RS-TBD-12 | 重用 RuntimeDescriptor 作 Task binding；不新增 stage binding list、future flag 或預留空 abstraction |

## 快照完整性與 fail-closed

snapshot 只固定 registry membership；descriptor capture 用 adapter ID/Capabilities/Version，不宣稱多個外部 binary 能在同一瞬間被讀取。selection 是捕獲完成後的值快照純函式，相同值快照必須產生相同結果；captured_at 不作排序輸入。

candidate enumeration 不呼叫 Run、模型、health 或 auth refresh。Version capture 與 enumeration 分開，沿 Phase 4A 有限期限及 caller cancellation；不新增 health probe。不完整 descriptor、未知 capability、無 eligible 或超限均在接受與 execution side effect 前拒絕。候選 Version failure 不假造 unavailable 版本繼續選擇，也不暗中減少候選。

explicit 不需要枚舉其他候選，維持 Phase 4A 的能力拒絕與版本探測順序；其 additive decision 記錄唯一指定候選及 explicit policy 身分，不要求其他 runtime 可用。新 reader 接受舊 capability-v1 無 SelectionDecision 的契約，status 顯示 selection unavailable，不重新封存。新 decision validate 核對 intent、candidate 唯一性、matches、eligible/rejected、policy 與 selected 身分；執行時不得拿現行政策替換已封存需求或決策。

## 架構影響與必要決策

控制平面仍只做 requirements/matcher/selection，adapter 不變成 router。local auto 必須接真實 registry/adapters，沿用 process lock、cancel、context budget 與 role sandbox。常青架構及 CLI 文件在實作交付時更新，不先描述尚未存在的功能。

README TBD-1～4 是核准阻塞；其中版本失敗提案限制可用性最明顯。若要改成「版本不明仍按 capability 選，僅 selected 版本失敗拒絕」，需同步修訂 candidate version provenance 與 RTSEL-013，不視為等價技術選擇。

## 使用者可調排序入口提案（2026-10-03）

既有 CLI 的設定多採旗標或環境變數，尚無統一 runtime 設定頁。本期建議提供 `ASURA_RUNTIME_PRIORITY` 環境變數，daemon 啟動與 local run 載入同一解析函式。例如：

```bash
ASURA_RUNTIME_PRIORITY=codex,opencode,antigravity,omp asura daemon
ASURA_RUNTIME_PRIORITY=codex,opencode,antigravity,omp asura run --backend auto 'prompt'
```

`ask --backend auto` 使用 daemon 的排序；ask process 的環境變數不覆蓋 daemon。daemon 變更後需重啟；local run 每次啟動讀取。未設定使用上述預設，使用者明確排序可將 omp 移到前面，預設最後不是不可覆寫的限制。

列表是偏好而不是候選白名單：未列者按預設規則補齊；偏好中的 runtime 未註冊時不新增候選。空項、重複 ID 與不支援的設定 ID 拒絕，不默默吞錯。設定 ID 第一版限既有四個 backend；其他已註冊 runtime 仍依預設字典序參與選擇。排序僅套用 eligible 集合，不改 explicit backend，不增加 fallback。

保存有效 priority 後，歷史 decision 可由 requirements、candidate snapshot、priority 與 policy version 重建；修改環境不改歷史或重送結果。補驗收需涵蓋自訂順序、未列候選、未註冊偏好、錯誤設定及設定變更後重送。2026-10-03 使用者回覆「好核准」，核准排序、環境變數入口及有效 priority 封存方向。其餘 admission 行為仍需確認；不視為整份規格核准。

Web 可在既有 audit 顯示有效排序。本期建議先提供上述設定入口；可編輯的 Web 排序頁需要持久化設定與變更 API，仍超出原 issue 的 audit-only Web 邊界，若使用者指定本期要做再修訂範圍。
