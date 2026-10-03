# Issue 33 完整交付獨立審查報告

- 審查日期：2026-10-03
- 範圍：完整 issue 33／PR 交付變更；RTCAP-001～016 全部驗收，包括已核准保留的本機 shell run 相容路徑
- Reviewed BASE SHA：41b5220b2100ad85fdb4913c7c8e73ea57657464
- Reviewed HEAD SHA：c5500485b9921ac3dc7871282fbbaa7c60fe6295
- Reviewed patch-id：750d09dd17d8048af60420635dc4860c70590b66
- 獨立 reviewer：Codex 隔離 reviewer task `/root/review_runtime_contract`；不是原實作者
- Review artifact：`docs/issues/issue-33/review-c550048.md`
- PR：[Asura #34](https://github.com/raybird/Asura/pull/34)；協調者於 2026-10-03 確認遠端 source HEAD 與上述 HEAD 相同，CI Test & Build 執行中，未將進行中的 CI 當成通過證據
- 風險：High；接受前能力權限、immutable snapshot、執行前安全退化、版本探測期限及 shell/runtime 稽核區分

## 問題與風險

**MUST FIX：無。SHOULD FIX：無。**

原 `a9ba9b9` 的 OpenCode version deadline MUST FIX 已修復並驗證：Version→resolve→validate 的 preferred／isolated／PATH 分支全程傳遞 caller context，兩段版本 subprocess 都使用 `exec.CommandContext`。真 subprocess fixture 的 50ms deadline 原本實際耗時 `2.00130274s`，修復後針對性 race 與完整回歸通過。公開 helper 保持既有 API 與背景 context，未變更 runtime 能力或唯讀隔離政策。

保留 **NICE TO HAVE**：`internal/web/page.go:654` 可以補完整結構的查詢負向 fixture，讓壞掉的 role／requirements／match 同樣明示 unavailable。正常 admission 產生的契約已受共同驗證，執行前亦完整 Validate；未證明可由產品入口寫入不實契約，此項屬稽核防禦與證據持久力建議。原 Phase 摘要狀態建議已同步解除。

## 已查核維度

### 固定範圍與核准

已讀 AGENTS、docs 流程契約 2.0、acceptance、verification、review-evidence、project，以及 issue README／technical-analysis／implementation-plan。完整 BASE..HEAD 的 patch-id 已重新取得，符合上述識別。

RTCAP-001～015 的規格核准基線為 `665e409`；2026-10-03 使用者原話「核准」由協調者提供，文件來源相符。2026-10-03 使用者再回覆「保留」，核准 RC-TBD-11 相容方案及 RTCAP-016 修訂；修訂基線為 `fcb58947607759801f38d999504cfaf2e1289565`。比較該修訂提交與交付 HEAD 的 README，只有回填核准 SHA，條件／動作／結果沒有再改變。16 個 Scenario 與核准表逐項相等，全部已核准，沒有待確認或豁免。

本次完整審查承接前兩次固定版本逐檔查核，並覆核其全部新增差異；不是把局部 PASS 更名為完整 PASS。`b5d2d91..c550048` 的 Go diff 僅 `capability_status_test.go` 新增 47 行 shell characterization，所有 production source 不變。其餘差異為核准規格、常青文件、計畫／證據與歷史報告，均已讀取；本機 shell 子範圍依新核准規格及新增實際入口測試重新判定。

### 全部驗收對照

下列測試原始碼、紅綠紀錄及最終證據均位於本固定 HEAD；既有紅綠與精煉命令／結果統整於 `evidence/implementation-notes.md`。

| 驗收 | 查核證據與可觀察結果 | 結論 |
|---|---|---|
| RTCAP-001 | engine local runtime 與既有 Direct 回歸；共同 matcher 在接受／Run 前完成，原 prompt 與輸出相容，Striker 單次 | 通過 |
| RTCAP-002 | backend requirements policy 使用核准角色／能力的字面值，重複建立結果一致且不呼叫模型 | 通過 |
| RTCAP-003 | 真 IPC／SQLite rejects、所有 backend Run=0、workspace sentinel 不變；沒有拒絕 Task 持久化 | 通過 |
| RTCAP-004 | Three-Face Oracle／Critic read-only、Striker write、union 共同预檢；真舞台測試固定同一 backend instance | 通過 |
| RTCAP-005 | OMP／Antigravity 既有 capability matrix 未擴張；Three-Face 缺 read_only_sandbox 在 Stage 0 拒絕，零 Run | 通過 |
| RTCAP-006 | explicit 缺能力 backend 身分保留，其他可用 backend 零呼叫，不使用 Registry.Default／fallback | 通過 |
| RTCAP-007 | IPC／engine 無記憶封存、queue upgrade／drift、restart 保存 JSON byte equality，status 讀 V1 | 通過 |
| RTCAP-008 | 已存在 request 在 descriptor 前去重，runtime 移除後仍回原 Task；並行 SQLite winner 快照相同且單次 Run | 通過 |
| RTCAP-009 | unknown backend 在 CreateTask／Run 前拒絕；unknown 身分可辨識，無 fallback | 通過 |
| RTCAP-010 | matcher capability 順序／重複不影響 canonical required／available／missing／verdict，slice mutation 不 alias | 通過 |
| RTCAP-011 | CLI 真 SQLite 查詢前後 bytes 不變，Web legacy renderer；舊資料沒有 runtime probe 或回填 | 通過 |
| RTCAP-012 | 最終完整 race 19 packages、vet／build 成功；新增 CLI characterization 後完整 CLI race 再驗，來源差異與證據重用已核對 | 通過 |
| RTCAP-013 | unknown mode／role／access／capability 負向，固定 JSON 未知 contract／policy／invalid match；Prepare／Run 前拒絕 | 通過 |
| RTCAP-014 | Version error／空值 fail-closed 實作，真 OpenCode subprocess caller deadline 紅綠，修正後 Version／Resolve 及完整回歸 | 通過 |
| RTCAP-015 | runtime 消失、能力退化與相容升級；guard 在 Prepare／Run 前拒絕安全退化，保留 admission 快照及分離 guard error | 通過 |
| RTCAP-016 | runtime engine／IPC／Web／Cron 接受時封存與三 surface 稽核；新 shell 真 handleRun／SQLite／status 兩 backend cases、零 runtime sentinel、無 contract、unavailable、查詢保存 bytes 不變；既有 Web legacy renderer 支援相同無契約狀態 | 通過 |

### 重要失敗面

- **未知／不一致的契約**：共同 builder／matcher 拒絕 unknown，Execute Validate policy／version／identity／requirements／match。未知或不相容時在 stage Prepare 前退出，trace 不偽裝成功。
- **explicit A 缺能力而 B 足夠**：固定 lookup A；拒絕包含 required／available／missing 及 backend identity，所有 Run 為零，無自動切換。
- **接受後 V1→V2／能力退化／runtime 消失**：worker 優先解碼 persisted context，guard 使用目前能力對 sealed requirements 查核；升級能力足夠可執行，退化拒絕且 original JSON 不變，guard error 分開記錄。
- **佇列與 Cron**：接受前 preflight／授權，CreateTask 保存契約與無記憶上下文；wake worker 不重算 admission。Cron canonical workspace 授權及 capacity／dispatch 順序未繞過 guard。
- **重送、fingerprint conflict、撤權、取消及並行首次請求**：去重在 descriptor 前，保留既有授權／cancel／fingerprint 規則。SQLite unique insert 敗者回 winner，不覆寫其 snapshot；既有全回歸及新增並行 fixture 支持最多一個 Task／execution。
- **舞台間 registry 替換**：BoundBackend 固定同一 instance，角色序列 Oracle／Striker／Critic，A=3、B=0，不形成 mixed runtime 或 stage fallback。
- **版本 subprocess 卡住／caller 取消**：修正後全程 context-aware；50ms／2s fixture 的真紅燈及修復綠燈可捕捉原缺口，並非僅檢查 context 變數存在。
- **shell flags 與 runtime 不相同**：真 handleRun 的 opencode／codex 仍執行 `printf shell-proof`；PATH fake runtime 若被呼叫會留下 marker 且 exit 99，兩 case 均 marker 不存在。Task completed、結果與 backend flag 正確，context／trace 無 execution_contract，status 明示 unavailable，前後保存值不變。
- **查詢歷史／runtime 已改變／缺契約**：CLI、IPC、Web 讀保存資料，不 probe 或回填；Web textContent 沒有新增 HTML 注入。shell 無 contract 與舊任務共用核准的 legacy/unavailable 路徑。

### 證據有效性與測試真偽

Reviewer 實際執行 `sha256sum -c evidence/final-tested-source.sha256`：19 個來源一致；**唯一不一致為 `cmd/asura/capability_status_test.go`**。已逐行查核差異，僅新增 shell characterization，不修改 production 或削弱既有斷言。不能聲稱交付 HEAD 的 20 檔全數符合舊 checksum。

`final-race.log` 為 `go test -count=1 -race ./...` exit 0，19 個 packages 通過；final-vet／build 為無診斷的成功輸出，實際命令／結果與 source baseline 保存於 notes。新增測試後 `go test -count=1 -race ./cmd/asura` exit 0（4.047s）保存於 `shell-cli-race.log`，notes 明列被測來源 `fcb58947607759801f38d999504cfaf2e1289565`；該來源到本交付 HEAD 的 Go diff 為空。定向 `^TestCapability` race exit 0（1.631s）另存 `shell-compatibility-green.log`。其餘 18 packages 的被測程式與相關條件沒有改變，沿用最終 race；production 沒有再變更，vet／build 證據仍有效。沒有重跑全 suite，也沒有把舊 hash 冒充新測試來源。

新的 shell 子範圍是已核准保留既有行為，production diff 為空，紅燈不適用；真入口 characterization 與保存 bytes 比對為等價證據。原 runtime 新行為仍有 admission／matcher／guard／audit 真紅燈。guard 使用固定獨立 JSON，核心 matcher／storage 沒被 mock；新 shell marker、字面值 shell-proof、completed 與 unavailable 斷言不由被測程式重算。既有測試改為同步拒絕有核准契約支持且保留零 Run 判準，不構成假綠燈。

Web Node DOM seam 執行實際 renderer，未替代核心 render，未跑真人瀏覽器的限制已揭露。公開探針與局部測試的持久力建議與本次真實行為證據分開判定。

### 架構、範圍與跨 Task 品質

契約與共同 matcher 集中 backend，policy 由控制平面組裝；engine／daemon 在各接受入口封存，pipeline guard 消費 persisted 值。沿用 resolved_context／pipeline_trace JSON，沒有 schema、routing、fallback、health／認證輪詢、skills／MCP discovery 擴張。四 adapter 能力語意與既有隔離維持不變；OpenCode 的必要修正僅傳 context。

三 stage 的短 BoundBackend 分支一致，沒有已分歧的重複行為；daemon／engine 序列化對應各自接受責任，沒有需要新增抽象的實質重複。新增 shell table cases 沿既有測試方式；scope 與功能說明已更新常青文件。未發現架構違規或阻塞性的過度設計。

### 豁免、待確認與限制

沒有 gate 豁免或未決驗收；RC-TBD-11 已依使用者明確「保留」定案。舊報告與 notes 的歷史待決敘述由各自固定版本／日期定位，不覆寫現存核准表狀態。

本次未新增真模型呼叫、真人瀏覽器、新 live binary／認證 probes 或額外大規模壓力實驗。固定 capability fixture、真 IPC／SQLite、真 shell 入口、真版本 subprocess 與既有回歸是此次驗證範圍；不宣稱 production 即時認證或二進位檔不可變。限制已誠實揭露，不構成不實回報。

## 流程判定

**PASS（完整 issue 33／PR 交付範圍）**：16 項現存驗收皆已核准並有有效證據，原版本期限 MUST FIX 已解除，shell 子範圍依新核准內容完成獨立查核；固定交付 HEAD 的完整變更已審查，報告已持久化，無 MUST FIX。

此判定綁定上述 BASE／HEAD。若後續只新增本份報告的單一提交，須通過 review-evidence 的 artifact 提交有效性檢查；其他產品、規格或測試變更不自動沿用本 PASS。
