# 實作計畫

2026-09-12 正式立項。行為、核准紀錄與雙迴圈驗證對應均以 [README](README.md) 為準。

## 分階段執行

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
