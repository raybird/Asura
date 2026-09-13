# Issue 4 現況分析

建立日期：2026-09-13。行為來源與修改邊界見 [README](README.md)。

## 需求與目標

把本人使用的 Web 接到既有任務中樞；延續逐題核准的授權、重送與結果交付，不新增執行平台。

## 程式現況

2026-09-13 靜態閱讀結果，未執行功能測試：

- `cmd/asura/daemon.go` 建立 Policy、SQLiteStore 與 Daemon；allowed-roots 未指定時使用工作目錄。
- `internal/auth/policy.go` 以絕對路徑與路徑前綴判斷 root／子目錄；未解析符號連結，遠端入口須以越界連結案例驗證，不能直接宣稱其安全。
- `internal/daemon/daemon.go` 的 submit 已授權 workspace，使用 RequestID 與 fingerprint 比對、SQLite 唯一限制及共同 WorkerPool。取消任務重送遭拒絕。
- 同檔 status 與 cancel 依 Task ID 或 RequestID 找任務，未套用專案 Policy；Web 必須在交付結果或執行取消前完成授權，不能只保護 submit。
- Client 同一連線直接讀寫編解碼器；Web 並行請求不能無同步地共用該連線。
- `internal/storage/sqlite.go` 保存完整 Result 並提供單筆查詢，沒有結果分頁介面。全量讀取再切片可避免瀏覽器大回應，但不代表服務端記憶體有界；需在分頁任務中處理。

## 差距

需新增 HTTP 登入與頁面、補上各操作一致的服務端授權、保存瀏覽器提交識別以支援回應遺失恢復，以及結果分頁。既有去重可沿用但仍須驗證並行重送與滿載接受競爭，不能以既有測試名稱推定 Web 路徑已通過。
