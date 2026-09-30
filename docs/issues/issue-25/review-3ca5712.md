# Issue 25 獨立審查報告

- 審查日期：2026-09-30。
- 範圍：完整交付 diff，目標分支 main。
- Reviewed BASE SHA：`19cb3d40e3826e6e1754fbd1848cca9e3725cc60`。
- Reviewed HEAD SHA：`3ca571208b7b3f3a9f2338f375d0d4d8f2de47b0`。
- Reviewed patch-id：`6e07f1a826af2df12bc221dc329ec00f8b49c9d4`。
- 獨立 reviewer：Codex subagent `/root/review_issue25`；未參與 production 實作。
- Review artifact：`docs/issues/issue-25/review-3ca5712.md`。

## 問題與風險

1. **MUST FIX：OMP 外部 runtime 依賴未固定。** `internal/backend/omp/adapter.go:92` 僅 pin Bun 與 cli.js，卻把整個主機 node_modules 以唯讀 mount 提供。固定 cli.js 確實有外部 `@oh-my-pi/pi-natives`、`@oh-my-pi/omptype` 等 import，技術分析亦記錄缺少依賴時不能啟動。RO mount 只能防止沙箱寫入，不能阻止主機更新依賴；入口 SHA 不變時仍可能執行未經 probe 的依賴 bytes。違反 BADAPT-002 的固定版本能力基線，且削弱 BADAPT-009 已測 retry 行為的有效性。應驗證必要依賴 manifest，並執行已驗證、不可被主機更新影響的依賴快照；不符合時 unavailable、零模型呼叫。
2. **MUST FIX：工作區位於 runtime 目錄內時會覆蓋 RO mount。** `internal/backend/clisandbox/run.go:97` 只檢查 workspace 包含 protected path；`run.go:168` 在 RO runtime 後掛載 RW workspace。若 workspace 是 node_modules 的子目錄，檢查通過且該依賴子目錄變為可寫。2026-09-30 reviewer 使用真實 bwrap 與 `/bin/sh` 重現：protected runtime 的子工作區內 `printf MUTATED > dependency.js` 成功，output `STARTED`，預期拒絕斷言失敗。應禁止 runtime 目錄與 workspace 兩方向重疊；保留一般 HOME 下專案的合法使用，不把整個 HOME 當作不能包含 workspace 的 runtime。
3. **MUST FIX：啟動前 I/O 失敗錯誤分類為取消。** `run.go:198–201` 對 BeforeStart 的所有錯誤使用 cancelled；實際 CLI hook 可能回傳鎖檔／flock／SQLite 讀取失敗，這些不是使用者取消。2026-09-30 reviewer 注入 `errors.New("database unavailable")`，實際得到 cancelled，預期非取消斷言失敗。只有 context cancellation／明確取消 intent 使用 cancelled，其餘應 execution failure。另請一起核對 preparation 期間 context 取消或 auth 到期，在 cmd.Start 前應保留正確 cancelled／authentication failure，而非 unavailable。

### 非阻塞建議

- **SHOULD FIX（證據持久力）：** `scripts/issue-25/probe_agy.py` 的輸出去敏 guard 只掃描頂層字串與整份 token JSON，不掃描巢狀 access_token／refresh_token。現有 artifact 另經實作者巢狀掃描，未以此推定有已保存的憑證洩漏；但日後單獨重跑腳本，模型若只輸出 access token，就可能漏過 guard。建議在列印 artifact 前遞迴收集原始與 access-only credential 中的敏感欄位。
- **NICE TO HAVE：** daemon 身份參數化測試中幾個 assertion 錯誤訊息仍寫 opencode／codex；斷言本身比較 backendID 正確，不阻塞。

## 已查核維度

### 驗收與證據

核准來源為 README 的 2026-09-29 使用者原話及 commit `9195c4d419ec7906aeeecc2700d7e11b821b75bc`；12 個 Gherkin 皆已核准。未要求變更規格才能修復上述實作問題。

| 驗收 | 證據與結論 |
|---|---|
| BADAPT-001 | CLI 預設保留 opencode；既有 codex 路徑不變，final-race.log 含完整 backend／MBACK／integration 回歸。可接受。 |
| BADAPT-002 | OMP 真實 access-only smoke/write/delayed/cancel 與受控故障 fixture 支持已測能力；runtime 依賴與 mount 缺口見 MUST FIX 1、2，交付尚不能通過。 |
| BADAPT-003 | ask/backend flag、Daemon 註冊 fail-closed、Pipeline 四後端計數及正式 OMP live 各驗不同責任。非選定後端零 fallback，有證據。正式 fixed runtime 仍受 MUST FIX 1、2 影響。 |
| BADAPT-004 | Antigravity 1.2.13 官方 hash、access-only/keyring 成功與 auth 反例、完整隔離取消正反控制有直接 fixture。唯讀未證明，unsupported 揭露正確。 |
| BADAPT-005 | Daemon 正式註冊測試驗證 fake binary 未被模型啟動；agy-new-login-live.log 實際正式 adapter 18.49 秒 PASS。缺版本／登入不 fallback。可接受。 |
| BADAPT-006 | TestNewBackendsThreeFaceStageZero 用正式能力矩陣並計數四後端，Unsupported、全部 0 calls；shared role guard 同樣拒絕 Oracle/Critic。可接受。 |
| BADAPT-007 | native fixture parser 覆蓋 OMP 429／partial／tool 後故障、Agy timeout SUCCESS／terminal carrier、auth snapshot expiry 與 shared cancellation。BeforeStart 分類有已重現錯誤，見 MUST FIX 3。 |
| BADAPT-008 | fingerprint 衝突/redelivery、queue wake、Daemon/SQLite 重開保持 BackendID；status 讀 persisted backend/trace version。使用 mock 驗身份層，未冒充 native capability。可接受。 |
| BADAPT-009 | OMP 巢狀設定關閉 CLI retry，平面設定反例保留；工具副作用後受控 transport failure 計數一次；Agy 真實服務 TLS 故障工具副作用一次，provider reconnect 與 Asura attempt 分開揭露。OMP 執行時依賴漂移必須修復才能沿用固定契約結論。 |
| BADAPT-010 | Production 新 adapter imports/特例只在 CLI/Daemon composition；Pipeline/Engine/Memory/Context 無新 raw adapter format 或特例。共用 process runner 有實際重複生命週期需求，未要求不必要的上層抽象。可接受。 |
| BADAPT-011 | Context reject/observe 與 SEALED_APPROVED_MEMORY 測試、既有 RECALL/GITMEM 全回歸；固定 CLI 關閉原 CLI memories/extensions，private HOME 限制原使用者設定。未虛構 Context window。可接受。 |
| BADAPT-012 | evidence/final-race.log 全 package 通過，final-vet/build 命令與 exit 0 由 implementation-plan 記錄；unknown backend 正式 composition 的 marker 不存在。review 新增邊界測試重現兩個原 suite 未覆蓋的錯誤，整體測試綠燈不免除修復。 |

### 相關失敗面與安全

- 未安裝／hash mismatch／namespace 不可用：模型前拒絕，有版本拒絕整合與實際 namespace 對照；OMP 依賴 hash 尚未涵蓋。
- 取消：shared 真實 helper 包含 setsid escaped child；native CLI 取消含 ready age、child identity、10 秒無副作用與正控制。PID namespace 加上 kill process group 的組合有實際支撐，不只驗父程序退出。
- auth：原 SQLite readonly、access-only 快照、refresh 不複製、memfd、輸出 exact-secret 遮蔽；Agy keyring/file 當前來源及 expiry 有負例。未看到 production 將原 token 輸出或落入 repository；probe guard 持久力建議如上。
- runtime：private scratch pin copy 的來源更新測試有效，但不能擴大為所有 OMP dependencies 已 pin；RW scratch 後有 RO file overlays，主 binary 別名已有封鎖。
- native output：Agy SUCCESS 與 nonempty response 仍被 timeout/AGY_ERROR 覆蓋；OMP invalid/incomplete/empty conservatively failure；partial 保留、不由上層 raw string 決策。Rate-limit 真實 vendor 未重現，合成 grpc carrier 沒冒充 live。
- context：未知窗口沿用既有 observe/explicit override；未新增猜測 catalog。
- Cron 測試：跨月舊月份 prefix 斷言錯誤，修正以獨立日曆計算下一次 03:00 日期，不修改 scheduler production；不是弱化失敗來制造假綠燈。

### 證據真偽、範圍與品質

檢查持久化 red/green、live 負例與最後新登入正例、fixture provenance 與技術分析限制。auth tests 明確揭露為實作後補，不宣称先紅；parser fixture 格式修正不冒充產品 red。mock dispatch、原生 probe、正式 adapter live 的責任區分合理。沒有 gate 豁免。2026-09-30 reviewer 針對性測試命令 `go test ./internal/backend/clisandbox -run TestReviewer -count=1` exit 1，兩個失敗原因即 MUST FIX 2、3；臨時 reviewer test 檔已移除，未改 production。

reviewer 讀取完整異動清單與 production diff、測試、probe scripts、生成 fixture 的來源/代表性正反控制、核准文件及完整 gate artifact。未重跑需真實登入的模型呼叫，沿用被審查 production 一致的已持久化正例；未宣稱 reviewer 自行執行完整 race。跨 Task duplicated capabilities/constructor 欄位屬兩個 adapter 的小型正常接線，沒有另列重構要求。

## 流程判定

**RETURN TO execute-task**。三項 MUST FIX 需修復、取得針對性紅綠證據及最終必要 gate 後，以新的固定 HEAD 重新獨立審查。本報告不代表 PR 可合併。
