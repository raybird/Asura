# Issue 45 — 實作計畫草案

建立日期：2026-10-07。風險與首要驗證引用 [README](README.md#風險與首要驗證)。本檔是唯一任務來源；以下只列候選 Phase，不是可實作 Task。DIST-TBD 決策與主要成功／重要失敗劇本核准後，再由 decompose 細化責任 Task、相依與證據位置。

## 設計方案

Installer 下載 → manifest 校驗 → archive 安全檢查 → 同目錄 stage → atomic rename。CLI 在原有 handler 之前解析 operator defaults，再以明確出現的 flag 覆寫；不修改全域 ENV。Doctor 共用 registry／inventory 的探測契約，setup／service 共用設定路徑與 user unit 邏輯。具體 API／函式簽名待 schema 與介面定案，不預建抽象。

## 候選實作階段

| Phase | 狀態／相依 | 產出 | 驗收對應與可檢查完成判準 |
|---|---|---|---|
| P0 行為刻畫與供應鏈故障 fixture | 待核准；先固定 DIST-TBD-2／7／8／12／14／15 | 現有 CLI／ENV／fallback 基線；sentinel binary、manifest／archive fixture | DIST-003／004／009／015／017：先證明故障測試抓得到破壞與錯讀；existing characterization 全綠，installer 缺行為取得有效紅燈 |
| P1 Version 與 release 契約 | 待核准；P0、DIST-TBD-3／14 | version 人類／機器介面與 build 注入 | DIST-001／002／016：字面 tag／commit／arch 與 build binary 一致，dev build 明確可辨 |
| P2 安裝器 | 待核准；P0／P1、DIST-TBD-1／2／12／14／15 | POSIX sh installer、雙架構與故障測試 | DIST-001～005／016：所有 install failure 保留 sentinel，同版／升降版與 path 空白行為可重現 |
| P3 Config／XDG 與 CLI 接線 | 待核准；P0、DIST-TBD-6／7／8／15 | 嚴格 config、純 path resolution、原 handler defaults 接線 | DIST-009／010／017：不同字面值證明逐層 precedence，明確 flag 勝出，無 config 基線相符，bad config 不啟動 |
| P4 Doctor | 待核准；P1／P3、DIST-TBD-4／5／15 | read-only readiness 與共用 inventory 接線 | DIST-006～008／014：必要失敗 NOT_READY、optional 缺失不致命、0 model calls、sentinel 零洩漏、probe bounded、持久資料不變 |
| P5 Setup／user service | 待核准；P3／P4、DIST-TBD-9／10／11／13／15 | 冪等 bootstrap 與 systemd wrapper／unit | DIST-011～015：fresh／重跑／半途失敗不破壞既有內容；真實 user bus lifecycle 與無 systemd standalone 分開驗證 |
| P6 文件、clean fixture 與完整回歸 | 待核准；P2～P5 | 使用指南、release E2E、完整測試證據 | DIST-001／002／016／017：無 Go／repo 完成安裝及啟動；兩架構證據；vet／race／build；每個 skip 記原因 |

## 使用方式對照

現況（2026-10-07）：手動 download／extract → 指定 daemon flags。建議草案：`curl .../install.sh | sh` → `asura doctor` → `asura setup` → user service 或 standalone → `asura ask ...`。未核准前不更新常青指南為已提供的功能。

## 測試策略

- Installer 外迴圈用真實 sh／HTTP fixture／archive／checksum／filesystem；底層重用同層 failure fixture 時可合併紅燈，記錄無另一層整合責任的理由。stub 網路驗故障，不宣稱正式 Release E2E。
- Config unit 覆蓋 parse／validation／merge／path；CLI 外迴圈驗 daemon/client 使用真正生效值與 v1.0 fallback，不只測 loader。
- Doctor runtime probe 用命令記錄及 secret sentinel fixture，禁止執行 prompt；必要的真實 runtime contract 證據與 stub inventory 分開。
- Setup/service 保存 config、DB／Task／Memory、credential 的前後值；冪等判準不是只看 exit 0。systemctl mock 驗 argv／錯誤，不等於 systemd lifecycle 已跑通。
- 純文件以相對連結、Scenario 集合／核准表一致與命令存在性靜態檢查取代紅燈，不記作程式測試。
- 原 Task 記錄外迴圈紅、unit 紅、最小實作綠、精煉後綠的命令／exit／原因／環境與提交；精煉 no-op 明列理由，不重複跑相同證據。

完整回歸入口（2026-10-07 依 project.md）：`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura`。backend 相關 runtime contract／live SKIP 必須記錄名稱／原因，不算通過；如影響 Web 或 memory lifecycle，另跑 ci.yml 對應檢查。

## 交付前檢查清單

- [ ] DIST-TBD 必要決策有來源；全部現存 Scenario 已核准或逐項豁免。
- [ ] 核准規格保存有效 commit；decompose 在本檔補齊每個 Scenario 的責任 Task。
- [ ] 每個 Task 完成且 evidence 對應固定交付版本；沒有把 stub／skip 當真實證據。
- [ ] 常青文件符合實際介面；不改 Phase 4 authority 或 SQLite schema。
- [ ] 依 create-commit／create-pr 固定交付 BASE／HEAD；獨立 reviewer 報告持久化。
- [ ] PASS 後等待使用者合併；本流程不自行 merge。

2026-10-07：本次僅完成盤點與規格草案；沒有可執行的已核准 Task，沒有程式驗收通過的宣稱。
