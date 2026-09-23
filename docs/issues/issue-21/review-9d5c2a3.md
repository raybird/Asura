# Issue 21 文件分析與規劃審查

- 審查日期：2026-09-23。
- 範圍：未提交文件的隔離快照審查；五份文件，不包含本報告及產品實作。
- Reviewed BASE SHA：`9d5c2a3ecae3a59341ec63b69fb9e573ef1b5c79`
- Reviewed HEAD SHA：`9d5c2a3ecae3a59341ec63b69fb9e573ef1b5c79`
- BASE／HEAD 都是對照程式基線。被審文件未提交，不存在承載這五份文件的 reviewed commit。
- Reviewed patch-id：不適用；本次是未提交文件快照審查，以以下 SHA256 綁定內容，不以空的 BASE..HEAD diff 代表文件。
- 獨立 reviewer：Codex 子代理 `/root/review_issue21`，未參與原規劃撰寫。
- Review artifact：`docs/issues/issue-21/review-9d5c2a3.md`。
- 風險：High，檢查授權、資源上限及重送副作用的規劃是否一致。

## 固定範圍

快照位於 `/tmp/asura-issue21-review-vDHNfc/issue-21/`，以下路徑相對於該目錄；問題位置也指此快照。2026-09-23 以 `sha256sum` 取得：

| 文件 | SHA256 |
|---|---|
| README.md | `6c415cd16f2814a63a2787db852be2b6ff7c2b639428745026df0d0734bd3810` |
| requirement-analysis.md | `44e9ed3cdbed411f5c76e4e5e37f7cafc5ef7ae4cd80b30037fbcbc6a7716831` |
| technical-analysis.md | `4062f19a193bdc472ebe5a45b470cd9d52b47b8654f5c3b45f500465cbe48b6b` |
| implementation-plan.md | `221839989c75bd6cfce52ecfc583c21ca7bdc17b3111def8a15a9af747063dbc` |
| evidence/probe-results.md | `6cb810f95f0c3889e7684fb7173f02e10802bea32ed41d0129548f056b2f7599` |

2026-09-23 以 `gh issue view 21 --repo raybird/Asura --json body,title` 讀取需求，並對照 Web、Daemon 與 SQLite 原始碼。工作區 `git status --short` 僅顯示未追蹤的 issue-21 目錄，程式無工作區修改。本次未執行產品測試，未重新產生原探測結果。

## 問題與風險

### MUST FIX 1：detail 的 IPC 選擇與資源上限承諾衝突

位置：`technical-analysis.md:22`、`:35`、`:51`、`:52`；`implementation-plan.md:21`。

提案明列 get 沿用既有 IPC，並維持 CLI get 的全量契約；另一方面承諾 detail 的 IPC／HTTP 回應受控。基線 `internal/daemon/daemon.go:974` 直接呼叫 `GetMemory`，`internal/storage/sqlite_memory.go:83` 讀取完整 content，Daemon 隨即序列化整筆資料。若 SQLite 已由 CLI 寫入超大內容，Web 即使最後拒絕 HTTP 回應，完整內容仍已在 SQLite → Daemon → Web 傳輸。這不是單純尚未選定限額，而是指定路徑無法達成同份提案的限制。

建議在 G-TBD-2 的具體提案中明列 Web 專用的有界 detail 查詢／IPC，或等價的服務端有界參數及拒絕語意，確保 CLI 契約保留且超限資料在進入 IPC 前被攔截。Phase 1 產出加入 detail 責任，驗證既有超大資料會得到固定錯誤，且未全量跨 IPC；不要只檢查 HTTP 大小。

### MUST FIX 2：WEBMEM-001 的跨 Origin 讀取結果與能力矩陣不一致

位置：`README.md:49`；`technical-analysis.md:11`。

Scenario 把跨 Origin 請求的「讀取或變更」都定為拒絕；矩陣則讓 GET 僅檢查 session／Host。基線 `internal/web/server.go:60` 的 Origin 拒絕只作用於非 GET／HEAD。帶有效 session、合法 Host、不同 Origin 的 GET 不會因 Origin 被共用入口拒絕。瀏覽器 SameSite／同源讀取限制與伺服器拒絕不是同一驗收結果，不能以既有變更方法測試證明此劇本。

建議在 G-TBD-1 將方法、session、Host、Origin 條件及可觀察結果列成明確矩陣，再統一 Scenario：若保留現有邊界，區分伺服器拒絕和瀏覽器不能取得內容；若要新增 GET Origin 檢查，明列為待核准行為並定義正常不帶 Origin 的 GET。兩者均不得默認為已核准。

### SHOULD FIX 1：隔離 SQL probe 缺少完整重跑入口

位置：`evidence/probe-results.md:8`、`:11`、`:24`；`implementation-plan.md:14`。

文件保留資料分布敘述與 EXPLAIN SQL，但缺建表型別、插入 fixture、第一次時間戳配置、第二頁及 bytes／scope 斷言的實際 SQL、執行命令與該 probe 的 exit code。讀者可自行重建相似案例，卻不能直接重跑原實驗。P0-2 完成判準明列命令與 fixture，建議補一份隔離腳本與真實執行紀錄，或精確標出目前缺少的部分。本項是證據可重現性／持久力改善；無證據認定原數據虛假，也不把已揭露的 CLI／Go driver 差異當缺失。

### SHOULD FIX 2：清除狀態尚未涵蓋延遲回應重新填入 UI

位置：`technical-analysis.md:39`；`implementation-plan.md:36`。

建議把「A 的 list/detail/task 查詢未完成 → 切到 B 或登出 → A 回應抵達」加入 WEBMEM-009 的明確案例。單次清空 DOM／選取仍可能被未完成請求重新填入。可用 workspace／session 世代判斷或等價方法丟棄過期回應，測試需控制回應順序並檢查下一次 payload。現有高層驗收已要求不殘留，本項補足具體失敗路徑，不要求增加產品範圍。

### SHOULD FIX 3：未知結果 request-ID 的可找回方式需成為 G4 決策的一部分

位置：`technical-analysis.md:43`。

提案同時要求登入／登出／切 workspace 清除待重送狀態，並稱未知結果可由 Request ID 查詢，尚未說明使用者如何取得清除前的 ID。建議至少在送出前顯示可複製 ID，或明列其他不保留敏感 payload 的找回方式；驗收涵蓋回應遺失後切換／重新登入。跨重新整理保留 payload 本來已揭露為待決策，無須因本項強制新增持久化儲存。

## 已查核維度

| 驗收／失敗面 | 規劃覆蓋與結論 |
|---|---|
| WEBMEM-001：未登入、登出、Host、Origin | 有共用入口與既有測試證據；GET Origin 語意須依 MUST FIX 2 統一。 |
| WEBMEM-002：A/B、父子、symlink、撤權 | 每次 canonical 授權及精確 scope 有原始碼依據；跨 scope 與不存在共用外部結果方向正確。 |
| WEBMEM-003、010：10k、長內容、同時間戳、傳輸上界 | keyset／索引與 SQL 截取方向合理；detail 路徑須依 MUST FIX 1 修正。候選上限、metadata、cursor 及變動資料仍明列待決策，未誤稱已驗收。 |
| WEBMEM-004：CRUD 失敗、重啟、刪除後舊任務 | 計畫有 SQLite 事實與已保存快照的獨立觀察來源；核准後需落到具體 Task。 |
| WEBMEM-005、006：跨 scope IDs、自填 context、預設 off／bounded | 獨立 DTO、Daemon 解析／selector、0 backend calls 與不擴大 backend／mode 的邊界正確；unknown field 拒絕相容性已揭露。 |
| WEBMEM-007：同 ID 重送、ID 排序、模式或內容衝突 | 凍結 payload 合理；既有 Daemon 先正規化 ID 再指紋判斷有原始碼依據。細化時明確區分集合等價與真正 payload 變動，補 SHOULD FIX 3。 |
| WEBMEM-008：記憶刪除後 audit、legacy／off／0 auto／unknown | 使用保存 Task，沒有重建歷史；現有 status 無界的限制已揭露。G5 是否另設有界 DTO 是待決策，不認定為既有已完成能力。 |
| WEBMEM-009：XSS、切 workspace、session 失效 | textContent 與清空狀態方向正確，補 SHOULD FIX 2 的競態案例。手機／桌機最小操作檢查可在後續 Task 細化。 |
| WEBMEM-010：既有回歸與 vet／race／build | 命令和必要回歸系列已列入，沒有把尚未執行的全套回歸宣稱通過。 |

需求與架構：維持 Web → IPC → Daemon → SQLite，沒有另建 Runner／檢索演算法。Large 與 High 分別以範圍與後果判定合理。任務來源單一，P0-3 清楚未完成；Phase 1→4 的相依大致合理。Phase 1 的真實 HTTP 拒絕紅燈應以目標失敗理由取得，Phase 2 才完成 HTTP 成功閉環，不應把路由不存在的 404 當授權成功。

核准與限制：原 issue 明確只授權調查、規劃與非產品探測；十項 Scenario 都保持待核准，沒有自行擴張授權。沒有產品 Task、未實作、未核准及未跑新功能測試均不列為缺失。原探測將已完成範圍與後續驗證分開，未發現假綠燈的充分證據。尚無可直接核准的固定 hard limits／DTO 全欄位／cursor 與 error payload；這是 P0-3 明列的剩餘工作，不能把目前草案稱為全部規劃完成。

## 流程判定

**RETURN TO execute-task**。依 review-evidence.md 使用此固定狀態；本次實際應退回 **new-issue 的規劃修訂／P0-3**，處理上述兩項 MUST FIX，再完成具體可核准提案。不是要求開始未授權的產品實作。

本報告是 2026-09-23 的獨立文件審查，**不是 PR 交付 PASS，也不是 WEBMEM 產品驗收證據**。後續文件改動以新內容重審；本次相同程式 BASE／HEAD 不使變更後文件自動沿用結論。
