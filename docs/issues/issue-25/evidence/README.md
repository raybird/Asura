# 2026-09-30 實作驗證紀錄

此目錄保存真實執行結果。red 表示先建立對應斷言、於修復前失敗；green 為修復後相同檢核。parser 初次測試組裝的 JSON 格式錯誤已修正，並非產品錯誤。auth 單元測試於實作後補上，不宣稱個別 auth 測試先紅。

- i2a：唯讀角色必須拒絕；真實 bwrap 子程序取消另由 green 測試涵蓋。
- i2b／i2c：native fixture parser 的成功、partial 與失敗共同契約。
- i3：CLI 接受新增 backend 身份。
- carrier：Antigravity 終止錯誤不可被 SUCCESS carrier 蓋過。
- runtime-pin：來源在驗證後更新，仍只執行已驗證的 runtime bytes。
- cron-date：既有整體 race 在跨月日期斷言失敗，改以次日實際日期核對；未修改 Cron production。
- integration-final：Daemon／CLI／Pipeline 整合測試。
- omp-final-live：正式 adapter 真實模型成功。
- agy-final-live：正式 adapter 45 秒 deadline 未取得結果，記為取消失敗。
- agy-refreshed-live：2026-09-30 14:27 仍讀到 14:13 到期的登入，模型呼叫前 authentication failure；待原 CLI 更新後重驗。

固定 CLI 探測與能力正反對照見 ../probes/。fixture、mock dispatch counter 與原生模型路徑各自驗證不同責任，不把 mock 當作安全能力證據。Antigravity vendor quota 僅有合成 carrier 測試，未宣告真實服務 quota 已重現。

2026-09-30 更新：原 CLI keyring 到期日更新為 2026-09-30 15:33，正式 adapter `go test -c -o /tmp/issue25-agy-final.test ./internal/backend/antigravity` 後以 `ASURA_ISSUE25_LIVE=1 ASURA_ISSUE25_AGY=/tmp/asura-issue25-fixed-agy/antigravity /tmp/issue25-agy-final.test -test.run TestLiveAdapter -test.v` 直接執行，18.49 秒 PASS；見 agy-new-login-live.log。final-race／final-vet／final-build 對應 implementation-plan.md 的完整命令；測試內容為本次交付 production，後續只補文件與證據。

2026-09-30 獨立審查修復：review-3ca5712.md 的三項 MUST FIX 已局部修正。OMP 全 dependency tree 複製後摘要固定（含 relative path、檔案 bytes、execute bit 與內部 symlink；跨樹連結拒絕），唯讀掛載私人副本；工作區與 runtime 雙向重疊拒絕；BeforeStart I/O 為 execution failure，context 取消／access expiry 分別分類。review-fix-red 是原 runner 的實際重疊反例，green 為同一斷言加啟動前失敗與副本測試。`go test ./internal/backend/clisandbox -count=1`、新 OMP 正式 adapter live 7.54 秒 PASS（omp-tree-live.log）。同組完整命令另保存 review-final-race／vet／build；準備階段取消與到期分類亦已補檢查。
