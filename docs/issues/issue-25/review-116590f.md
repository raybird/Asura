# Issue 25 獨立完整交付重審

- 審查日期：2026-09-30。
- 範圍：完整 PR，目標 main；包含前輪交付與後續修復。
- Reviewed BASE SHA：`19cb3d40e3826e6e1754fbd1848cca9e3725cc60`。
- Reviewed HEAD SHA：`116590f833948480dcbd7e9ff1576d795512d82a`
- Reviewed patch-id：`db228d3c78efee4fd3ac50cbcae3704daa0adc34`。
- 獨立 reviewer：Codex subagent `/root/review_issue25`；未實作 production。
- Review artifact：`docs/issues/issue-25/review-116590f.md`。
- 前輪報告：`review-3ca5712.md`；RETURN 歷史保留，不以後續 PASS 改写當時結論。

## 問題與風險

**MUST FIX：無。** 前輪三項已解決：

1. OMP 完整 node_modules 先複製，對實際副本 canonical SHA256 驗證，摘要不符模型前 unavailable。原始路徑及 scratch 副本別名皆在 RW workspace/scratch mount 之後覆蓋 RO tree；Bun 與入口另外 pin。SnapshotTree 拒絕絕對或越樹 symlink、特殊 entry，固定摘要包含檔案相對路徑、內容與 executable bits。主機在準備後更新依賴，不影響已驗副本。固定 Linux x64 安裝樹／增加 global package 即摘要不符／每次約 1.2 GB scratch 的限制已在 README 與技術分析明揭露。
2. ReadOnlyPaths 與 workspace 兩方向重疊皆 Unsupported；一般 HOME 下的專案仍合法。原先後置 workspace RW 覆蓋 runtime 子目錄的重現已被拒絕。
3. BeforeStart 非 context 錯誤為 execution failure，context error 保留 cancelled；使用 credential deadline 的 runCtx，啟動前再區分 parent cancellation 與 credential expiry。Auth 讀取期間 parent cancellation 也保留 cancelled。

前輪 SHOULD FIX 的 probe nested credential guard 已遞迴掃描 original 與 access-only payload；NICE TO HAVE 的參數化 daemon assertion 訊息仍有舊 backend 名稱，僅診斷文字，不影響判斷。

### 非阻塞建議

- **NICE TO HAVE（取消反應／驗證持久力）：** SnapshotTree 複製整樹期間目前不接受 context；模型尚未啟動且到期／取消會在 Start 前阻擋，沒有取消後模型副作用，但較大樹／慢磁碟會延後回報。可在有實際使用需求時增加準備階段取消檢核，並把下述 preparation deadline 及實際 tree RO mount 的 reviewer 測試保留為正式 regression。這不否定本次已驗證的 process cancellation 能力。

## 已查核維度

### 核准、驗收與證據

核對完整 BASE..HEAD 異動、前輪 findings 與新 production/test/doc diff。核准仍為 `9195c4d419ec7906aeeecc2700d7e11b821b75bc` 的 BADAPT-001～012，來源 2026-09-29 使用者「那麼 核准」；修復沒有新增行為規格。沒有 gate 豁免。

| 驗收 | 固定交付的證據與判定 |
|---|---|
| BADAPT-001 | CLI 預設 opencode、既有 codex 路徑保留；evidence/review-final-race.log 全 MBACK/backend/integration 回歸，PASS。 |
| BADAPT-002 | OMP 真實 probe 正反控制與受控 retry/partial/故障、access-only fixture；完整依賴 pin 修復加 evidence/omp-tree-live.log 7.54 秒正式模型 PASS。僅宣告有證據的 write/events/cancel，PASS。 |
| BADAPT-003 | ask/run 新 ID、Daemon 正式註冊 fail-closed、四後端計數的 dispatch、正式 OMP model path；非選定後端零 fallback。新 tree 實際執行與摘要拒絕補齊，PASS。 |
| BADAPT-004 | Antigravity 官方 1.2.13 hash、access-only/keyring auth 正反對照、完整 HOME/IPC/PID 隔離寫入與取消 probe；readonly unsupported 有明確邊界，PASS。 |
| BADAPT-005 | Daemon composition 版本拒絕、auth expiry 前置拒絕、agy-new-login-live.log 正式 adapter 18.49 秒 PASS；後續 shared preparation 修正不改 Agy invocation，成功證據可沿用，PASS。 |
| BADAPT-006 | TestNewBackendsThreeFaceStageZero 正式能力矩陣、Unsupported 及四後端全部 0 calls；Oracle/Critic runner guard，PASS。 |
| BADAPT-007 | native parser fixture 的 auth/429/partial/terminal timeout、合成 Agy quota carrier 明確標示、真實 child cancellation；BeforeStart I/O 分類補修及 preparation cancellation/expiry 獨立查核，PASS。 |
| BADAPT-008 | fingerprint conflict/redelivery、queue wake、Daemon/SQLite reopen 保留 backend、status 讀 stored trace/version；身份層 mock 未冒充 capability，PASS。 |
| BADAPT-009 | OMP 巢狀 retry 關閉、平面設定反例保存、工具副作用後故障一次；Agy TLS 故障副作用一次且 reconnect 分層揭露。完整 OMP 依賴快照防止未驗 runtime 漂移；上層一次派送、不 fallback，PASS。 |
| BADAPT-010 | Pipeline/Engine/Memory/Context production 無新增 adapter imports、raw event 或 backend 特例。變更只在 adapter 與 CLI/Daemon composition，PASS。 |
| BADAPT-011 | sealed memory/admission reject 零呼叫與 observe 一次選定派送，既有 RECALL/GITMEM 全回歸；固定 CLI settings 關閉外部 memory/extensions，未知 context window 未猜測，PASS。 |
| BADAPT-012 | review-final-race 全 packages 通過、review-final-vet/build exit 0 的完整命令記於 implementation-plan/evidence README；unknown marker 零外部模型啟動，PASS。 |

### 重要失敗面、權限及架構

- runtime unknown/missing/hash mismatch：版本與樹摘要模型前 unavailable；复制副本 SHA 驗證涵蓋主機 mutation。尚未 probe 的新版本不自動允許。
- protected path：兩方向 overlap；tree overlay 放在 RW mounts 後；入口及 tree 副本別名均 RO。真實 bwrap 另驗 source update 後仍讀 VERIFIED 且写原 runtime 被拒絕。
- auth：原 SQLite readonly、Agy keyring/file 對應來源、剔除可用 refresh、memfd、輸出 credential exact-value 遮蔽；原憑證不改寫、不持久化續期。probe guard 補齊 nested 原始 token 掃描。
- cancellation：native delayed positive control + namespace cancel 10 秒 child identity/無 marker，以及 shared setsid helper；prep 階段 context cancel/credential expiry 在 Start 前判定，不冒充 unavailable。
- failure/partial：Agy SUCCESS carrier 不能盖過 timeout/AGY_ERROR；OMP invalid/incomplete/empty failure；partial 經共同 contract 保留，上層不解析 raw string。vendor quota 僅合成 carrier，沒有冒充服務端真實 quota。
- 身份與 memory：Schema/fingerprint/routing 未改，version 不加入 fingerprint；核心只使用 Backend contract，封存記憶在 Asura 處理。
- 跨 Task 精煉：兩個 adapter 的小型 constructor/capabilities 重複沒有造成行為分歧；共用 sandbox/snapshot 因實際生命週期需求而存在。未見範圍外架構擴張。
- Cron 測試修復：原 current-month 斷言跨月失效，獨立日曆 next 03:00 替代，不改 scheduler；不是弱化測試來製造綠燈。

### 證據與 reviewer 實際查核

2026-09-30 reviewer 重讀修復 SHA 與原完整交付內容／證據：review-fix-red 重現舊 overlap、review-fix-green、review-final-race/vet/build、omp-tree-live。原 auth tests 實作後補與 parser fixture 組裝修正仍如實揭露，不宣稱每一項 unit test 都先紅。model live、mock dispatch、原生 probe 的責任持續區分。

2026-09-30 reviewer 另外建立臨時測試、實際執行 `go test ./internal/backend/clisandbox -run TestReviewerSecond -count=1`：exit 0，`ok asura/internal/backend/clisandbox 0.079s`。獨立檢核：

1. 真實 bwrap 下 source dep 在 BeforeStart 更新為 UPDATED，執行仍讀 VERIFIED；嘗試寫 RO tree 失敗，command 未產生 BAD。
2. preparation hook 取消 parent context，Start 前回 cancelled。
3. credential 30 ms 到期、hook 等待 60 ms，Start 前回 authentication failure。

臨時 reviewer test 已移除，未修改 production。`git diff --check` exit 0，工作區在新增此報告前為乾淨。沒有再跑模型及整份 suite；完整 gate 沿用相同交付 production 的持久化證據。修復只影響 Agy preparation 邊界，無必要重複取得其成功模型證據。

## 限制與交付影響

只支援已固定 Linux x64 binary/依賴樹與所選模型；需要 bwrap namespace。OMP 全樹複製約 1.2 GB scratch 成本及 global tree 必須完全相符已揭露。原 CLI OAuth expiry 需人工更新。read_only_sandbox unsupported、Three-Face Stage 0 拒絕；外部 TCP/tool 通道未證明唯讀，不將 filesystem 對照擴大為完整唯讀能力。vendor 所有故障／配額類型未實測，unknown conservatively execution failure。

## 流程判定

**PASS**。I4 交付與獨立審查已完成，目前等待合併；README／implementation-plan 保留審查前實作快照，本報告為最新審查結論。PR #26 source HEAD 已由協調者核對為本報告的 Reviewed HEAD。獨立完整交付審查已持久化，前輪 MUST FIX 全數解決，沒有新的阻塞缺口。PASS 綁定上列固定 HEAD；後續只能依 review-evidence.md 的單一新增 artifact 提交例外沿用，其他 production／test／設定或文件異動須重新核對。
