# Issue 25 - Phase 3I：Backend Adapter Expansion & Conformance

## 概述

來源：[GitHub issue #25](https://github.com/raybird/Asura/issues/25)。2026-09-29 以工作區 `19cb3d40e3826e6e1754fbd1848cca9e3725cc60` 調查。目標是增加 OMP 與 Antigravity CLI adapter，同時保留 Asura 對 Task、Context、Memory、Pipeline、Scheduling 的所有權。

issue 原文將 BADAPT 列為候選；使用者於 2026-09-29 在本對話明確回覆「那麼 核准」，核准本文件當時的 BADAPT-001～012 行為範圍及失敗路徑。固定版本、能力矩陣及 fixture 尚待 probe 定案；若結果需要實質修改劇本，須對受影響項目重新核准。2026-09-29 網路與隔離環境已恢復，並完成 OMP 候選套件及 agy 最小非互動探測；完整安全契約仍待驗證，證據見技術分析。

2026-09-30 使用者指定 `openai-codex / gpt-6-luna`，OMP 18.4.4 的真實呼叫已成功。取消 probe 發現 shell 子程序脫離主 PGID，SIGKILL 後仍有延遲寫入；PID namespace 的對照則成功回收。採用 bwrap 作為 adapter 執行期依賴的新增部署前提待使用者決定，尚未開始 production 實作。

## 文件清單

- [需求分析](requirement-analysis.md)
- [技術分析](technical-analysis.md)
- [初始計畫](implementation-plan.md)：唯一階段／任務來源；核准後由 decompose 細化。

## 關鍵差異

| 項目 | 現況 | 目標 |
|---|---|---|
| 明確選擇 backend | opencode、codex | 增加 omp、antigravity |
| 能力依據 | 既有 adapter 契約 | 新 adapter 必須有固定版本直接證據 |
| status | 未輸出 backend 身份／版本 | 顯示實際 backend 及可取得版本 |

## 涉及檔案

以下為最小預期邊界；新檔須由 probe 結論確認，並非已建立。

```text
internal/backend/omp/、antigravity/    新增 adapter、parser、能力矩陣與測試
internal/backend/                    新增共同 conformance 測試；沿用 Backend 介面
internal/daemon/daemon.go             註冊及生命週期接線
internal/daemon/*_test.go             身份、queue、restart、memory 整合
cmd/asura/ask.go、run.go、status.go    選擇、提示及身份顯示
cmd/asura/*_test.go                   CLI 回歸
internal/pipeline/*_test.go           Stage 0、trace、0 calls 測試
internal/engine/*_test.go             cancel、redelivery 整合回歸
scripts/issue-25/                     固定版本 probe 與去敏證據（後續建立）
docs/issues/issue-25/                 規格、決策及驗證紀錄
README.md                            CLI、能力限制與安裝前提
```

架構常青文件 `docs/superpowers/specs/2026-09-11-asura-architecture-design.md` 在 adapter 接線與版本策略定案後更新。Engine／Pipeline／Memory／Context production 語意維持既有契約；probe 若證明需修改共同契約，先記錄具體缺口並修訂核准範圍。第一版不新增 schema、routing、failover、mixed-stage binding、ACP/A2A、raw flags、CLI session persistence 或 Git Memory AI draft adapter。

## Gherkin 驗收劇本

以下行為劇本已核准；BADAPT-007 的 unknown-result 精確分類、BADAPT-008 的 adapter version 表達待 probe 後定案。

```gherkin
Feature: 可替換的四後端執行契約

  @BADAPT-001
  Scenario: 既有後端相容性
    Given 既有 OpenCode 與 Codex 任務及 MBACK 基線
    When 省略 backend 或明確指定 codex 執行
    Then 省略時使用 OpenCode，Codex 維持既有契約，全部 MBACK 回歸通過

  @BADAPT-002
  Scenario: OMP 能力有直接證據
    Given 固定版本 OMP 與隔離 probe 環境
    When 檢查 invocation、events、cancel、failure、partial output 及權限邊界
    Then 每項宣告能力都有真實 probe 證據，未證明者標記 unsupported

  @BADAPT-003
  Scenario: 明確選擇 OMP
    Given OMP 通過必要契約且已註冊
    When ask 或 run 指定 --backend omp
    Then 只啟動 OMP，其他三個 backend 為 0 calls，結果、失敗及 trace 使用共同契約

  @BADAPT-004
  Scenario: Antigravity 能力有直接證據
    Given 固定版本 Antigravity CLI 與隔離 probe 環境
    When 檢查 invocation、events、cancel、failure、partial output 及權限邊界
    Then 每項宣告能力都有真實 probe 證據，未證明者標記 unsupported

  @BADAPT-005
  Scenario: 明確選擇 Antigravity
    Given Antigravity 通過必要契約且已註冊
    When ask 或 run 指定 --backend antigravity
    Then 只啟動 agy，其他三個 backend 為 0 calls；未安裝或未登入時分類 unavailable 或 authentication failure，不切換 backend

  @BADAPT-006
  Scenario: Three-Face 在 Stage 0 拒絕不支援能力
    Given 選定的 OMP 或 Antigravity 缺少 Oracle 或 Critic 所需唯讀能力
    When 提交 Three-Face 任務
    Then 在 Oracle 啟動前拒絕，0 stage model calls，且不 fallback

  @BADAPT-007
  Scenario: 共同失敗與取消契約
    Given 四個 backend 的已知 auth、rate-limit、execution failure、cancel、empty 或 invalid result 案例
    When 執行各案例並在取消案例檢查子程序及延遲副作用
    Then 結果映射共同 failure contract，保留可取得的 partial output，上層不依 raw error string 決策；取消能力以子程序清除及副作用證據判定

  @BADAPT-008
  Scenario: 執行身份封存
    Given Request ID 已綁定 BackendID，或任務已排隊及持久化
    When 改 BackendID 重送，或以相同 payload redelivery、queue wake、Daemon restart
    Then 變更 BackendID 被拒絕；其餘情境保留原 backend；status 與 trace 顯示實際身份及可取得版本

  @BADAPT-009
  Scenario: 無自動切換或重複副作用
    Given OMP 或 Antigravity 已產生部分輸出或工作區修改
    When 該次執行失敗
    Then 任務 Failed 並保存現場，其他 backend 0 fallback calls，不產生第二次 execution attempt

  @BADAPT-010
  Scenario: Adapter 邊界隔離
    Given 四個 backend 使用共同介面
    When 檢查依賴及執行路徑
    Then Pipeline、Engine、Memory、Context 不依賴新 adapter package 或 raw format，無 omp 或 antigravity 特例分支；解析、生命週期及能力偵測留在 adapter/probe

  @BADAPT-011
  Scenario: Context 與記憶相容
    Given 新 backend 任務帶 explicit memory、bounded recall 或 Git-approved memory
    When Asura resolve、seal 並執行 Context admission
    Then 只有核准且封存的選取內容交給 backend，backend 不重新選取 Asura memory；admission 拒絕時不呼叫模型

  @BADAPT-012
  Scenario: 完整回歸與未知後端拒絕
    Given 四後端整合完成且既有回歸案例保留
    When 執行完整 vet、race test、build 及 unknown backend 請求
    Then 所有指定回歸通過；unknown backend 在任何外部 process 啟動前拒絕
```

## Gherkin 核准紀錄

- **核准 commit**: `9195c4d419ec7906aeeecc2700d7e11b821b75bc`（承載獲核准的 BADAPT-001～012 劇本）
- **核准來源**: 2026-09-29 使用者在本對話針對 issue #25 初始文件與下一步回覆「那麼 核准」。核准範圍為當時文件中的 BADAPT-001～012；I-TBD 的 probe 事實及未寫入的實質設計決策不由此推定核准。

| Scenario | 核准日期 | 狀態 |
|---|---|---|
| BADAPT-001 | 2026-09-29 | 已核准 |
| BADAPT-002 | 2026-09-29 | 已核准 |
| BADAPT-003 | 2026-09-29 | 已核准 |
| BADAPT-004 | 2026-09-29 | 已核准 |
| BADAPT-005 | 2026-09-29 | 已核准 |
| BADAPT-006 | 2026-09-29 | 已核准 |
| BADAPT-007 | 2026-09-29 | 已核准 |
| BADAPT-008 | 2026-09-29 | 已核准 |
| BADAPT-009 | 2026-09-29 | 已核准 |
| BADAPT-010 | 2026-09-29 | 已核准 |
| BADAPT-011 | 2026-09-29 | 已核准 |
| BADAPT-012 | 2026-09-29 | 已核准 |

## 風險與首要驗證

- **最大風險**：CLI 的唯讀模式、retry/resume 與子程序回收尚未證明，可能越權寫入或在取消後繼續產生副作用。
- **風險等級與理由**：High；涉及權限、副作用與未知核心外部契約。規模 Large 另由兩個 adapter 及跨 CLI／Daemon／Pipeline 整合決定。
- **首要驗證**：固定版本、隔離工作區的真實 contract probe；先測寫入邊界、取消後 child process／延遲寫入、retry/resume，再測結果及錯誤格式。
- **選擇理由**：風險在 CLI 外部契約，fixture 或完整 Asura 垂直切片均不能直接證明 CLI 安全能力。
- **完成證據**：每項記 binary 來源／版本／雜湊、argv、設定與環境摘要、去敏 stdout/stderr、exit code、程序樹與檔案前後差異。以允許寫入的控制案例證明檢測可捕捉副作用，再驗唯讀拒絕；取消案例包含會延遲寫入的子程序及有界觀察窗。未證明能力一律 unsupported；無法證明必要安全性時不得開放該執行路徑。

## 待確認事項

| 編號 | 事項 | 狀態 | 影響 |
|---|---|---|---|
| I-TBD-1 | OMP 18.4.4／Bun 1.4.2 固定 probe，指定 openai-codex/gpt-6-luna 的 OAuth 呼叫已成功；完整契約待驗 | 待確認 | BADAPT-002、003、007；可重跑腳本及 fixture 已保存，production 技術基線未定案 |
| I-TBD-2 | Antigravity 本機 CLI 1.2.12 完成最小非互動回應；內部 server 1.2.13，完整契約待驗 | 待確認 | BADAPT-004、005、007；簡單回應不證明安全或失敗路徑 |
| I-TBD-3 | agy plan 已寫入；OMP 主 PGID 取消漏掉子程序；bwrap PID namespace 與唯讀掛載的窄對照已通過 | 待確認 | BADAPT-002、004、006、007；採用方案見 I-TBD-10；其餘工具／路徑及 agy cancellation 待驗 |
| I-TBD-4 | OMP provider/model 已由使用者於 2026-09-30 指定；CLI／adapter trace、登入狀態隔離與 Context window 待定 | 待確認 | BADAPT-008、011；OMP catalog 272000 尚非實測窗口，版本不改既有 fingerprint |
| I-TBD-5 | CLI retry/session/resume 關閉方式及 unknown-result 分類 | 待確認 | BADAPT-007、009；必要副作用 gate |
| I-TBD-6 | auth、rate-limit、partial output、cancel 精確 mapping | 待確認 | BADAPT-002、004、007；由真實 evidence 建 fixtures |
| I-TBD-7 | 正式 ID 為 omp／antigravity，是否額外接受 agy alias | 待確認 | BADAPT-003、005、008；建議先只接受正式 ID，避免新增同義身份 |
| I-TBD-8 | BADAPT-001～012 Gherkin 已核准；固定版本 fixture/probe 基線尚待建立 | 待確認 | 目前核准行為範圍；probe 若要求實質改劇本，受影響項標待重新核准 |
| I-TBD-9 | npm DNS、namespace 及 OMP 指定模型登入已可用；完整隔離契約待驗 | 待確認 | I-TBD-1～6 的前置條件已具部分證據，未解除全部安全 gate |
| I-TBD-10 | OMP adapter 採用 bwrap 執行期依賴／PID namespace；不符合環境時模型呼叫前 unavailable | 已解決 | BADAPT-002、003、006、007；2026-09-30 使用者對明列方案回覆「1」，核准部署前提；完整能力仍須探測 |

I-TBD-1～6 先由 probe 取得事實，需使用者選定的 model/provider 或無法隔離的環境條件再提問。I-TBD-7 及 I-TBD-8 的技術基線在 probe 後定案；若變更已核准行為，依核准紀錄逐項重新核准。沒有 gate 豁免。

## Timeline

| 日期 | 異動 | 負責人 |
|---|---|---|
| 2026-09-29 | 依 issue #25 建立初始文件與候選驗收，保留 probe 及核准門檻 | Codex |
| 2026-09-29 | 完成版本／雜湊及環境前置探測；新增 I-TBD-9，live probe 受環境限制 | Codex |
| 2026-09-29 | 使用者於本對話核准 BADAPT-001～012 當前劇本；probe 技術決策仍待定 | 使用者 |
| 2026-09-29 | 環境恢復後取得 OMP 18.4.3 候選套件及 agy 最小非互動回應；安全能力仍待 probe | Codex |
| 2026-09-29 | agy plan 模式於隔離工作區實際寫入，排除該模式的唯讀能力宣告 | Codex |
| 2026-09-30 | 使用者指定 OMP 的 openai-codex/gpt-6-luna；完成真實呼叫、寫入與取消 probe，新增 I-TBD-10 部署方案決策 | 使用者／Codex |
| 2026-09-30 | 使用者回覆「1」，核准 OMP bwrap 隔離部署方案及不支援環境的 unavailable 行為 | 使用者 |

---
**建立日期**: 2026-09-29
**分級**: Large
**風險**: High
**狀態**: BADAPT-001～012 已核准；contract probe 部分完成，bwrap 部署前提已核准，完整技術基線待驗
