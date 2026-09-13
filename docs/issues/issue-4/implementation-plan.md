# Issue 4 實作計畫

建立日期：2026-09-13。驗收唯一來源為 [README](README.md)；[細部任務](implementation-plan-decomposition.md) 為任務清單權威來源。此為規劃，不放行未核准行為。

## 流程與介面

瀏覽器 → Web 登入／同源 API → Daemon Unix Socket → 既有 Policy／WorkerPool／Engine／SQLite。

沿用 `Client.Submit(ctx, engine.SubmitRequest)`、`Client.GetTask(ctx, taskID)`、`Client.CancelTask(ctx, taskID)`。HTTP 路由草案為 `POST /api/login`、`POST /api/logout`、`GET /api/projects`、`POST /api/tasks`、`GET /api/tasks/{id}`、`GET /api/tasks/{id}/result`、`POST /api/tasks/{id}/cancel`。具體錯誤碼與分頁參數於 Phase 1／4 固定並測試；不新增通用插件抽象。

## 階段概要

| Phase | 工作包 | 交付 | 前置 |
|---|---|---|---|
| 1 | R-01 | 登入與全操作授權拒絕證據 | 完整劇本核准及核准 commit |
| 2 | R-02 | 接受識別與重送持久契約 | Phase 1 |
| 3 | R-03 | Web 提交、查詢、共同取消最小流程 | Phase 2 |
| 4 | R-04 | 輪詢、恢復與純文字分頁 | Phase 3 |
| 5 | R-01～04 | 瀏覽器驗收、回歸與操作文件 | Phase 4 |

## 驗證策略

先以未登入與越權對照降低最大風險，再擴展成功路徑。Go 整合測試承載 Scenario ID 並替代獨立 BDD runner；瀏覽器驗收補上 cookie、跨站行為、純文字呈現與頁面重開。外迴圈與內迴圈分開，不以模擬 Daemon 取代共同容量或副作用驗收。

每項保存外迴圈紅燈、內迴圈紅燈、最小實作全綠與重構後全綠；重構未發生時如實記錄並重跑。新增測試不得以編譯失敗或空選取作為紅燈。具體命令與證據欄位見拆解。

## 交付邊界

本次僅文件，無 production code 修改，不適用紅燈要求。文件靜態驗證不代表功能或安全驗收通過。實作後更新根目錄使用文件，獨立審查另留 artifact；本次未建立 PR、提交 commit 或執行部署。
