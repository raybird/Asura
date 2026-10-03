# Issue 33 修正版獨立審查報告

- 審查日期：2026-10-03。
- 範圍：局部功能審查；固定 BASE..HEAD 的完整變更，判定限於 RTCAP-001～015 與 RTCAP-016 的真正 runtime 路徑。
- Reviewed BASE SHA：`41b5220b2100ad85fdb4913c7c8e73ea57657464`。
- Reviewed HEAD SHA：`b5d2d91d19e4c175eee637f6575cb5a5e1d639b4`
- Reviewed patch-id：`eda8c8244c5fd173fe5ee4183431e3503613b74a`，由完整 `git diff BASE HEAD | git patch-id --stable` 重新取得。
- 獨立 reviewer：Codex 隔離 reviewer task `/root/review_runtime_contract`；不是原實作者。
- Review artifact：`docs/issues/issue-33/review-b5d2d91.md`。
- 風險：High；接受前能力權限、immutable snapshot、執行前退化與版本探測期限。

## 問題與風險

**MUST FIX：無。** 原 HEAD 的 OpenCode version deadline 問題已修復。`Adapter.Version` 使用私有 `resolveOpenCodeBinary(ctx, ...)`，preferred／isolated／PATH 的所有驗證分支均呼叫 `validateOpenCodeVersion(ctx, ...)`，其 subprocess 使用 `exec.CommandContext`；後續讀取版本的 subprocess 也使用同一 context。公開 Resolve／Validate helper 保留既有簽章與背景 context 行為，未改 runtime 能力或隔離政策。

真 subprocess fixture 使用 `exec sleep 2`，caller deadline 為 50ms；原版實際等待 `2.00130274s` 的失敗保存於 `evidence/version-deadline-red.log`。修正版 `TestCapabilityVersionProbeDeadline` 要求錯誤且總耗時不超過 750ms，與 Version／Resolve 相關測試以 race 執行 exit 0，見 `evidence/version-deadline-green.log`。期望由 caller 期限及獨立 elapsed 上限決定，未 mock Version 或核心 admission，不是同義反覆。完整最終回歸亦已涵蓋此新增測試。

保留兩項 **NICE TO HAVE**，不阻塞局部判定：

- `internal/web/page.go:654` 的查詢 renderer 可補完整結構的負向 fixture，讓壞掉的 role／requirements／match 同樣明示 unavailable；現有正常契約由 admission 驗證，未證明產品入口可寫入不實契約。CLI 已有完整 Validate，Web 使用 textContent，沒有新增 HTML 注入。
- `implementation-plan.md` 上方 Phase 摘要與下方完成 Task 狀態仍不完全一致；完整交付時同步摘要。

## 已查核維度

### 版本、核准與證據

沿用已讀 AGENTS、流程契約 2.0、acceptance、verification、review-evidence、project 及 issue 規格。核准來源為協調者提供的 2026-10-03 使用者明確核准 `665e409` 方案及 16 項驗收；此次 README 增列版本期限涉及檔案及更新實作狀態，没有新增驗收行為。RC-TBD-11 與 RTCAP-016 shell 子範圍維持待決／待重新核准，明示排除。

先前完整 diff 查核詳見 [原版報告](./review-a9ba9b9.md)。本次比對舊 HEAD 與新 HEAD 的全部異動清單，原 runtime 契約、daemon／engine／pipeline／status／Web source 與原測試皆未改動；新增 production 差異僅為上列 OpenCode context 傳遞，已逐分支覆核。規格、全部新增證據及已提交原報告亦已查核，不以 patch-id 相同或原判定直接放行新 HEAD。

`sha256sum -c evidence/final-tested-source.sha256` 實際執行，20 個來源檔全數 OK；審查開始時工作區乾淨，與固定 HEAD 相符。`implementation-notes.md` 保存最終命令、exit code 與工作區 checksum，未冒用修復前 SHA。最終 `go test -count=1 -race ./...` exit 0，`final-race.log` 的 19 個 package 全部成功；`go vet ./...`／`go build -o /tmp/asura-issue33 ./cmd/asura` exit 0，final-vet／build 無診斷。未重跑完整 suite 或額外 /tmp 探測。

| 驗收 | 可觀察證據與責任 | 結論 |
|---|---|---|
| RTCAP-001 | engine local runtime：共同 preflight、原 prompt、單次 Striker；既有 Direct regression | 通過局部查核 |
| RTCAP-002／010 | backend 字面值 policy、排序／去重／slice mutation；共同 matcher 純函式 | 通過 |
| RTCAP-003／006／009 | 真 IPC／SQLite reject、零 Run、sentinel 不變、requested identity、無 fallback | 通過 |
| RTCAP-004／005 | union 全需求、既有 OMP／Antigravity 不含唯讀能力、Three-Face 真 stage 綁定角色及同 backend | 通過 |
| RTCAP-007／008 | 無記憶 persisted JSON、restart／redelivery／queue bytes equality、並行唯一 winner／單次 Run | 通過 |
| RTCAP-011 | CLI 真 SQLite 前後保存值不變、Web legacy／unsupported renderer；查詢零 runtime 探測 | 通過；Web 防禦建議如上 |
| RTCAP-012 | 最終 race 19 packages、vet／build、20 source checksum 全數有效 | 通過 |
| RTCAP-013 | unknown mode／role／access／capability 負向、固定 JSON 未知版本／policy／invalid match guard | 通過 |
| RTCAP-014 | Version error／empty fail-closed；真 subprocess caller deadline 紅綠與完整回歸 | 原 MUST FIX 已解除 |
| RTCAP-015 | runtime 消失／能力退化拒絕、升級能力足夠繼續；Prepare／Run 前 guard 保留原 snapshot | 通過 |
| RTCAP-016 真正 runtime 部分 | local runtime engine、IPC／Web 共用 daemon、Cron 接受時封存與三 surface 稽核 | 局部通過；shell 子範圍未驗收 |

### 重要失敗面、架構與品質

- 不可理解的契約：builder／matcher 拒絕未知輸入，執行核对 policy／version／identity／requirements／match；在 Prepare／Run 前拒絕，沒有 silent ignore。
- explicit A 缺少能力而 B 足夠：lookup 固定 A，不使用 default 或 fallback；required／available／missing 可辨識，所有 runtime Run 為零。
- 已接受後 runtime 消失／能力退化／升級：執行 guard 對 sealed requirements 比較目前能力，失敗保存獨立 guard error；相容升級允許執行且不改 V1 admission JSON。
- redelivery／並行 unique winner：既有請求查詢在 descriptor 前，授權／fingerprint／cancel 仍有效；insert 敗者讀取 winner，不覆寫 snapshot、不增加 execution。容量與 workspace queue 邊界沿既有契約。
- Cron／無記憶任務：授權 canonical workspace 與 preflight 在接受前，CreateTask 原子保存 context；worker 優先 persisted context，呼叫端 context 不覆寫。
- stage 間 registry 替換：BoundBackend 固定同一 instance，測試驗證三 stage 的角色及 A=3／B=0，沒有 mixed runtime。
- 版本 subprocess 逾時／取消：新 admission 探測全程使用 caller context；紅燈證明案例能捕捉原缺口，綠燈及回歸支持最小修復。
- 查詢舊資料／目前 runtime 已變：只讀保存值，不探測或回填；status SQLite bytes equality 與 IPC 原 Task 支持歷史不可變。
- 架構與 scope：backend ownership 的純契約與共同 matcher，pipeline 組裝 admission，engine／daemon 負責各接受入口，沿用既有 JSON storage；未新增 schema、routing、fallback、health、MCP discovery。
- 跨 Task 重複／過度設計：三 stage 的短 BoundBackend 分支一致，daemon／engine 序列化对应各接受責任，未發現已分歧的重複邏輯；此次私有 context helper 是相容公開 API 的最小變更，不要求額外抽象。
- 測試真偽與持久力：核心 matcher／storage／renderer 未被 mock 掉；guard 為獨立固定 JSON。既有同步拒絕測試更新有核准新契約支持，零 Run 判準保留。版本 test 使用真 subprocess。Web Node DOM seam 和未跑真人瀏覽器已揭露，不構成假綠燈。

### 豁免、待決與限制

沒有新增 gate 豁免。`shell-run-compatibility.log` 證明本機 codex run 仍執行 shell 並顯示 legacy/unavailable，該結果用於說明 RC-TBD-11 差異，不能當 RTCAP-016 通過。RTCAP-016 的 opencode/codex shell run 相容性與必要後續工作仍由使用者決策；本報告不替使用者核准。

沒有進行真模型、真人瀏覽器、新認證或 live binary probes；固定能力 fixture、真 IPC／SQLite、真版本 fixture subprocess 與既有完整回歸為本次證據。這些限制已揭露，不宣稱 production 即時認證或二進位檔不可變。

## 流程判定

**PASS（僅局部 runtime 範圍）**：固定新 HEAD 的獨立審查已完成並保存，原 MUST FIX 已解除，局部必要驗收與重要失敗面有有效證據，無其他 MUST FIX。

**不能作為整個 issue／PR 閉環 PASS**：RTCAP-016 shell 子範圍與 RC-TBD-11 決策尚未完成。交付仍須保持此邊界與核准狀態，後續產品或規格變更須按固定新版本重新判斷。
