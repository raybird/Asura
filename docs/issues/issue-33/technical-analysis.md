# Issue 33 — 技術方案與 RC-TBD 提案

提案日期：2026-10-03。2026-10-03 使用者核准 RC-TBD 全部方案，驗收狀態以 README 為準。

## 技術可行性與 ownership（RC-TBD-1／2／3／5／10）

沿用 `internal/backend` 的 Role／Capability，新增純值契約；engine 可引用 backend 而不形成 engine↔pipeline cycle。mode 在契約內使用受驗證的字串 direct／three-face，保留既有 pipeline.ExecutionMode 與 IPC wire value，避免大規模搬型別。

```go
type ExecutionRequirements struct {
    Role backend.Role
    WorkspaceAccess string // read-only 或 workspace-write
    RequiredCapabilities []backend.Capability
}

type RuntimeDescriptor struct {
    ID string
    Version string
    Capabilities []backend.Capability
    CapturedAt time.Time
}

type CapabilityMatch struct {
    Compatible bool
    Required []backend.Capability
    Available []backend.Capability
    Missing []backend.Capability
    BackendID string
    BackendVersion string
}

type ExecutionContract struct {
    ContractVersion string
    PolicyVersion string
    Mode string
    Requirements []ExecutionRequirements
    RequestedBackend string
    Runtime RuntimeDescriptor
    Match CapabilityMatch
}
```

型別實際位於 backend package，片段的限定名稱僅用來說明沿用型別。JSON 使用 snake_case；captured_at 使用 UTC。初版 contract_version／policy_version 固定 `capability-v1`，版本表示解讀規則與 deterministic policy，而非 binary version。

政策由 Asura 組裝，不由 adapter 決定。Direct 只有 Striker；Three-Face stage 順序 Oracle、Striker、Critic。Striker 為 workspace-write／workspace_write；其他兩者為 read-only／read_only_sandbox。structured_events 與 process_cancellation 仍是 descriptor 能力，不新增為必要條件，保持既有需求強度。

共同 matcher 概念簽名：`MatchCapabilities(requirements []ExecutionRequirements, runtime RuntimeDescriptor) (CapabilityMatch, error)`。純函式不 lookup registry、不讀 Version、不執行模型。能力集合按字串排序、去重、複製 slice；required 為所有 stage union。未知能力（需求與 available 兩側）、role、workspace access，或 role/access/capability 不一致都拒絕，不能忽略。policy builder 拒絕未知 mode；contract reader 在執行時拒絕未知版本。查詢未知版本則顯示 unsupported/unavailable 並保留原 JSON，不能當成相容。

能力不足回傳 incompatible 結果，admission error 包含 match 與 requested backend，並 unwrap 既有 ErrUnsupportedCapability；未知 backend 保留 ErrBackendNotFound。無效契約另有可辨識錯誤，不偽裝成 missing capability。

## Descriptor 與版本（RC-TBD-4）

提案沿用 Backend 介面，由共同入口呼叫 ID、Capabilities、Version 建立 descriptor。Registry 繼續 lookup；四個 adapter 不各自實作 matcher，不新增 health/auth probe。

比較兩種有實質行為差異的方案：

| 方案 | 優點 | 代價 |
|---|---|---|
| Version 失敗保存 unavailable 並繼續 | 最接近現有忽略錯誤行為 | 無法回答接受時確切版本；必須新增 version provenance 語意 |
| Version 失敗拒絕（已核准） | 接受時 descriptor 完整可稽核 | 新任務提早失敗，已核准 RTCAP-014 |

提案採 Version error 或空值拒絕，不把 adapter 的 expected version 當成已安裝版本。保留版本原始字串，不在控制平面重新猜測 runtime 版本；現有 adapter binary／隔離驗證仍有效。版本探測沿 caller context 並有有限期限，取消後不可接受任務。

## Snapshot、稽核與舊資料（RC-TBD-6／7）

選擇 `ExecutionContext.ExecutionContract *backend.ExecutionContract` additive 欄位，接受時深複製值並以既有 CreateTask 的 resolved_context INSERT 原子封存。無記憶／Cron 也序列化。所有 status/audit 以 persisted context 為接受時權威。

另一方案是新增 Task SQL 欄位或 audit table：可獨立查詢，但本期沒有跨 Task capability 搜尋需求，額外 migration 與寫入一致性成本不必要。只放 PipelineTrace 則要等執行才有資料，不能證明排隊任務接受時契約。

trace 可附接受時契約的相同值供既有 trace surface 閱讀，但不能以重新取得 descriptor 產生此副本。execution guard 結果分開記錄，StageTrace.BackendVersion 仍是執行資訊，不取代 admission snapshot。sealed JSON 寫入失敗須拒絕派送。

舊 Task 缺欄位：查詢 legacy/unavailable，零 backend 呼叫、不回填。舊 pending／queued 任務若沿既有路徑執行，仍執行目前共同安全預檢，結果只屬本次 execution trace，不偽造接受時契約。既有記憶 sealed context 不重組、不修改。

## Identity、preflight 與安全退化（RC-TBD-8／9）

raw requested_backend 保存請求原值；空值表示沿用現有 opencode 預設，resolved ID 仍明確為 opencode，不受 Registry.Default 改變。既有 CLI 已填 opencode 時，只能記錄收到的 opencode，不宣稱可重建旗標是否曾被省略。既有 fingerprint 維持 normalized backend，不能加入 version／capabilities 或區分空值與 opencode。

```diff
 workspace authorization + request validation + fingerprint
 existing request lookup / redelivery
+ common policy + registry lookup + descriptor + capability match
 memory resolution / recall
 capacity acquisition
- conditional memory context serialization
+ always serialize context with immutable contract
 atomic CreateTask
 workspace queue / dispatch
+ execution guard against sealed requirements
 stage Prepare / Run
```

IPC 跟 Web 共用 daemon 接受點；Cron、local run／engine 新 Task 也使用同一 admission service。pipeline 提供 preflight 方法／可選 engine admission 介面，避免把 backend policy 複寫在 daemon；runner-only 測試 seam 保留，但正式 runtime wiring 不得略過 admission。engine 消費已有 Task 時優先讀 persisted contract，不採用可覆寫既有快照的呼叫端 context。

同 request 並行時可能各自做一次尚未接受的 descriptor 探測；CreateTask 唯一限制決定勝者，敗者回傳 persisted 勝者 Task。驗收要求最多一個 Task／execution，不假裝 registry 探測是原子單次；已存在 Task 的正常 redelivery 則完全不探測。

接受後能力可能退化，因此執行前仍對同一 resolved backend 的目前能力做共同 matcher；只與 sealed requirements 比較，不能依新版 policy 重建需求。消失、unknown capability 或不相容時在 Prepare／Run 前拒絕並記 execution guard。版本升級但能力仍足夠不因版本不同而拒絕；adapter 自身版本驗證照常。歷史接受時 match 與快照保持不變。

接受前拒絕以 IPC／CLI 錯誤回傳完整 match；本期不新增 failed-admission Task／audit table。RTCAP-006 的 requested backend 從拒絕結果查核；已接受任務才有 durable sealed contract。

## 架構影響與待定事項

改動限於既有 admission、context、pipeline 與 audit surface，使用一個共同 matcher。read-only boundary、OpenCode role routing、No Automatic Failover、Memory／Recall／UFL／Lifecycle 都保持。2026-10-03 核准的 RC-TBD-1～10 與 RTCAP-001～016 為 production 實作契約。

## RC-TBD-11 — 本機 shell run 相容性差異（2026-10-03）

進一步閱讀 cmd/asura/run.go 證實 opencode/codex flags 都仍使用 sh -c shell runner；只有 omp/antigravity 使用 Backend.Run。原規劃誤將所有本機 run 視為 runtime 呼叫。要求它保存 OpenCode/Codex descriptor 會造成錯誤稽核，直接改成 runtime 則改變既有命令執行契約。

具體提案：保留 shell run 行為，runtime capability contract 明示 unavailable；真正 runtime 的 local engine、Daemon/IPC/Web/Cron 路徑完整封存。另一方案為切換成實際 runtime Run，會改變命令輸出、認證與模型調用。2026-10-03 使用者回覆「保留」，核准相容方案及 RTCAP-016 修訂。既有 status／Web 缺快照分支已明示 legacy/unavailable，無須新增 shell 標記或更動 production；新增真 CLI／SQLite characterization 驗證輸出、零 runtime 探測與零歷史回填。
