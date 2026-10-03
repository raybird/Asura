# Issue 35 — 需求與現況盤點

2026-10-03 盤點基準：`37e034b667700a7ced3f22919fb26b2a0d824a6c`。需求來源、範圍與核准狀態見 [README](README.md)。

## 現況分析

- Registry `List()` 已持鎖並排序 ID，但逐一 List/Get 不能提供同一註冊集合的原子快照。`Default()` 受首次註冊與 SetDefault 影響，不宜當 auto 優先序來源。
- `RequirementsForMode` 建立 Direct Striker 或 Three-Face 三 stage 需求；`MatchCapabilities` 是共用純函式，對未知能力與 role/access 不一致 fail-closed。
- `AdmitRuntime` 只接受指定 backend，空值固定 opencode；先檢查 capability，再取得有期限的 Version 並封存 descriptor。`ExecutionContract.Validate` 假設 requested backend 等於 selected runtime，需要 additive selection 語意並保留 capability-v1。
- Daemon submission 先驗 workspace/mode、建立 fingerprint 與查既有 Request ID，後做 Preflight、記憶解析、容量與 CreateTask。這個順序保護正常重送不重新 admission。
- fingerprint 使用 normalized requested backend，空值與 opencode 等價；auto 可保留成獨立字串，不能改用 selected ID 作 fingerprint。
- Engine 同 ID 去重會比較 `Task.Backend`；auto intent 不能直接與已選 runtime 比較，須從 persisted contract 解讀請求，queue dispatch 則使用 selected ID。
- ExecutionContract 透過 `resolved_context` 與 CreateTask 同次 INSERT 保存。pipeline 已有接受時契約及執行前同 backend 能力 guard；status/Web 從 persisted JSON 查詢。
- Daemon 註冊 opencode、可找到 binary 的 codex，以及無條件註冊 omp/antigravity。列舉候選不等於確認其 binary／認證可用。
- local `run` 的 opencode/codex 分支實際使用 `sh -c`，Phase 4A 已核准保留此行為。OMP/Antigravity 才是 adapter Run；新增 auto 不能把 shell runner 偽裝成 AI runtime。
- Web `/api/tasks` 現有 submission 沒有 backend/mode 欄位；需 additive backend 欄位表達 auto，沿用 direct 預設。不需要新增 mode 功能或 management UI。

## 問題點與目標

需要解決候選一致快照、intent 與 selected ID 分離、政策排序、跨入口封存與 legacy 相容。最大新增產品決策是排序及 local run auto 的執行語意；版本取得失敗如何處理也不能無聲轉成 availability routing。

本文件因實際讀程式才發現 shell 與無條件註冊差異而建立。驗收與決策不在此重複維護；以 README 與 technical-analysis 為準。
