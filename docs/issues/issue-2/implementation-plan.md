# 實作計畫

2026-09-12 正式立項。行為、核准紀錄與雙迴圈驗證對應均以 [README](README.md) 為準。

## 分階段執行

2026-09-12 補充：[Phase 1～6 任務拆解](implementation-plan-decomposition.md) 為詳細執行清單，包含八項 Scenario 責任與雙迴圈命令；下表保留工作包摘要。本次只完成拆解，未執行 B-00 探測。

| 工作包 | 工作及完成證據 | 前置 |
|---|---|---|
| B-00 | 固定版本、CLI／事件／取消及權限契約探測；保存命令、樣本、退出碼，覆蓋 INT-001、002、007 的允許與拒絕邊界 | 核准規格提交 |
| B-01 | 最小 OpenCode adapter；INT-001、005、006、007 紅綠重構證據 | B-00 |
| B-02 | Daemon 啟動、共同容量與重啟收尾；INT-003、006 證據 | B-01 |
| B-03 | ask、接受識別碼、查詢及重送去重；INT-001、004、004-REDELIVERY 證據 | B-02 |
| B-04 | 共同取消入口與資源回收；INT-005、006 證據 | B-03 |
| B-05 | 全部八項 Scenario 回歸、真實後端與操作文件驗證 | B-04 |

最大未知是固定後端能否執行核准的權限邊界，先取得契約證據才進入 production code。版本不可取得或契約不符時，提出具體證據並逐題確認，不自行換版或降低權限要求。

## 驗證紀錄

2026-09-12：`opencode --version` 退出碼 0，輸出 `1.18.15`，與核准版本不符；尚未發送真實模型請求，也未變更全域安裝。B-00 未通過。

本次立項僅修改文件，不適用紅燈要求；以 Markdown 連結、Scenario 編號與 `git diff --cached --check` 靜態檢查替代。實際檢查結果於完成後補記，未執行 production 回歸。

2026-09-12：`npm view opencode-ai@1.18.23 version --fetch-retries=0 --fetch-timeout=10000` 退出碼 0，輸出 `1.18.23`。套件來源提供指定版本，但尚未證明該版本的執行及權限契約。

2026-09-12：Node 靜態檢查退出碼 0，輸出 `PASS: 6 documents; relative links, fences, 8 unique Scenario IDs`；檢查兩份規劃及四份 issue 文件的相對連結存在、程式碼圍欄成對、八個 Scenario ID 不重複。`git diff --cached --check` 退出碼 0、無輸出。核准規格已提交為 `1b92a60`；本次沒有 production code 變更，未執行 Go 測試。

2026-09-12：執行 `bash scripts/issue-2/probe-opencode.sh all` 完成 B-00 契約探測。隔離安裝 `opencode-ai@1.18.23` 驗證版本精確吻合（退出碼 0），全域環境維持 `1.18.15`；`events` 驗證輸出符合預期 JSON 事件格式，無效參數退出碼為 1；`permissions`（INT-007）驗證核准目錄檔案修改成功，且以 `OPENCODE_PERMISSION='{"bash":"deny"}'` 成功阻擋危險工具調用，零副作用；`cancellation`（INT-005）驗證獨立進程組 SIGTERM 取消後進程樹完全回收，不相關工作存活。探測日誌與樣本保存於 `docs/issues/issue-2/evidence/phase-1/`。B-00 通過，放行 B-01。

2026-09-12：完成 B-01 OpenCode Adapter 實作（`internal/backend/opencode/adapter.go`），prompt 不經 shell 解譯，支援 `OPENCODE_PERMISSION` 與進程組取消管理。完成 `TestINT001Unit`、`TestINT007Unit`、`TestINT001`、`TestINT007` 之紅綠重構雙迴圈驗證，`go test -count=1 -race ./...` 回歸全綠。證據記錄於 `docs/issues/issue-2/evidence/phase-2/test_results.log`。B-01 通過，放行 B-02。
