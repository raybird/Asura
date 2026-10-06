# Web Console

單人使用的 Web 遠端入口，與 Daemon 共用授權政策與容量限制。

```bash
# 啟動 Web 控制台（預設監聽 loopback 127.0.0.1:8080，單人私人網路使用）
ASURA_WEB_KEY="your-secret-key" ./bin/asura web \
  --listen 127.0.0.1:8080 \
  --socket /tmp/asura.sock
```

## 存取與任務

2026-09-13：
- **存取安全**：單一存取密鑰換取 12 小時工作階段，強制 `HttpOnly` 與 `SameSite=Strict` Cookie，驗證所有請求的 Host 與變更方法的 Origin。
- **中樞整合**：共用 Daemon 授權政策與容量限制，網頁端專案下拉選單來自服務端政策，支援輸入既有 CLI Task ID 查詢與共同取消。
- **純文字分頁與恢復**：長輸出結果以 Unicode Rune 進行安全字元切片分頁（`GET /api/tasks/{id}/result`），以 `textContent` 渲染防止腳本執行；關閉頁面重開後登入仍可恢復查詢同一任務，唯讀輪詢不重跑副作用。
- **外部 Tunnel 邊界**：若使用 Cloudflare Tunnel、Tailscale 等第三方工具臨時對外曝露，外部流量仍必須通過密鑰驗證與 Origin 防護；Asura 本體不內嵌 Tunnel 供應商 SDK 或自動管理穿透。

## 專案記憶管理

2026-09-23：登入 Web Console 後可在授權專案下管理手動記憶：分頁列出摘要、按 ID 查看詳情、新增內容，以及確認後刪除。清單預設 20 筆、每頁最多 50 筆；摘要最多 512 UTF-8 bytes，Web 詳情最多 64 KiB，新增內容最多 32 KiB。超限或舊資料格式不支援時會明確回報，CLI 的完整記憶查詢仍可使用。

提交任務時可選取最多 32 個同專案記憶 ID，或明確開啟 `bounded` 自動召回；未選取且未開啟時保持原本的 off 行為。送出前顯示 Request ID，回應遺失可用「原請求重試」送出相同內容；若關閉頁面，請使用先前保存的 ID 重新登入查詢。Task 查詢頁會顯示當次保存的記憶與召回決策，刪除現存記憶不會改寫舊 Task 快照。

## Git 候選記憶

2026-09-30：Web 的「Git 候選記憶」區域可指定固定 range 或 since-last 擷取、依 pending／approved／discarded 狀態分頁、查看來源與完整草稿，以及編修後確認核准或確認捨棄。Complete／Partial、reused 與拒絕結果會明確顯示；Partial 由服務端禁止核准。核准後顯示正式 `git-approved` Memory ID，可由既有「專案記憶」查看與召回；候選本身不進入 recall，舊 Task 快照不變。

2026-09-30：候選每頁預設 20 筆、最多 50 筆，摘要最多 512 UTF-8 bytes；Web 草稿／核准內容最多 32 KiB、來源 facts 最多 256 KiB、HTTP JSON 最多 512 KiB，超限 detail 明確拒絕，可改用 CLI 查核。Capture 有 10 秒期限，離線會取消尚未完成的 Git 工作；回應遺失時重新載入候選確認真實狀態。切換 workspace、登出或 session 失效會清除未送出的編修；第一版不提供 force、ai_draft 或 bulk clear。

## Runtime 與稽核查詢

已登入使用者可唯讀查詢「Runtime 可用性清冊」（`GET /api/runtime-inventory`）與任務執行稽核（`GET /api/tasks/{id}/audit`），語意見 [Runtime 與執行控制](runtimes.md#可用性清冊)。
