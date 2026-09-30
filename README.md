# Asura (阿修羅)

> **Personal autonomous AI agent orchestrator and control plane**

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Asura（阿修羅）** 是一套以 Go 語言打造的個人自主 AI 控制平面與輕量編排中樞（Personal Autonomous AI Control Plane & Orchestrator）。

其核心神話隱喻為「三面六臂、非天非魔、極致戰力」：
- **三面（Three Faces）**：由 **智者之面 (Oracle)**、**執行之面 (Striker)** 與 **審查之面 (Critic)** 組成的動態協同管線，告別單一 Prompt 的混亂負擔。
- **六臂（Six Execution Arms）**：向外伸展的通訊與執行通道（本機 CLI、常駐守護進程、記憶中樞、Agent 執行池等）。
- **單一中樞守護（Unified Daemon）**：以 Go 語言編譯為單一靜態二進位檔案，徹底終結多進程對 SQLite 的檔案鎖與 Migration 競爭。

---

## 核心特色 (Key Features)

- 🚀 **極致輕量守護**：以 Go 靜態編譯單一二進位檔案（`asura`），具備常駐低資源消耗與高啟動速度。
- 🛡️ **POSIX 進程樹隔離與精確回收**：
  - 獨立進程組（PGID）管理，取消任務時直達整組進程樹強殺（SIGKILL）。
  - 不可變核心時間戳記（`StartTime`）身分驗證，徹底防禦 Linux PID 重用誤殺。
  - 三態進程組身分合約：組內孤兒進程生命週期追蹤，杜絕假成功與殭屍進程殘留。
- 🔒 **跨行程平坦排他鎖與狀態引擎**：
  - 啟動區間平坦排他鎖（`.task-<hash>.lock`），徹底消除啟動與取消間的競態。
  - 單一 SQLite 狀態引擎，統一管理任務佇列、執行狀態、日誌與追蹤資料。
- 🧩 **後端契約與異常防禦**：
  - 抽象化 Runner 契約，無縫對接 OpenCode、Claude 等執行環境。
  - 內建 429 速率限制 1.1 秒快速失敗、410 模型下架精確攔截、空輸出防禦與部分失敗保護。

---

## 快速開始 (Quick Start)

### 1. 編譯建置

需求環境：Go 1.22+，Linux 環境。

```bash
# 複製專案
git clone https://github.com/raybird/Asura.git
cd Asura

# 建置單一二進位檔案
go build -o bin/asura ./cmd/asura
```

### 2. 常用 CLI 指令

#### 啟動 Daemon 服務中樞 (daemon)
```bash
# 啟動常駐執行中樞（監聽 Unix Domain Socket，集中管理排程與執行容量）
./bin/asura daemon --socket /tmp/asura.sock --allowed-roots /path/to/project --workers 2 --queue 10
```

2026-09-13：經 Daemon 提交、查詢、取消或重送任務時，都套用服務端 `--allowed-roots`。專案移出清單後，其歷史任務仍保存在資料庫，但不能透過 Daemon 讀取或取消；指向清單外的符號連結亦遭拒絕。此清單是入口授權政策，不是作業系統沙箱。

#### 提交自然語言 AI 任務 (ask)
```bash
# 透過 Daemon 中樞提交 AI 任務（非同步接受立即回傳 Task ID，斷線不中斷，預設 direct 模式、opencode 後端）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project "分析專案代碼並整理函式呼叫圖"

# 啟用 Three-Face 固定管線模式（Oracle 規劃 → Striker 執行 → Critic 審查）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project --mode three-face "重構認證模組並補齊測試"

# 指定後端執行器（支援 opencode、codex，預設 opencode）
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project --backend codex "修復單元測試"

# 提交並等待任務完成輸出結果
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project --wait "執行測試並回報結果"
```

#### 本機直接執行指令 (run)
```bash
# 本機直接執行指令並追蹤任務（預設 direct 模式、opencode 後端）
./bin/asura run \
  --workspace /path/to/project \
  --allowed-roots /path/to/project \
  "echo 'Hello Asura'"

# 指定以 Three-Face 模式與指定後端執行
./bin/asura run \
  --workspace /path/to/project \
  --allowed-roots /path/to/project \
  --backend codex \
  --mode three-face \
  "make test"
```

2026-09-30：`ask` 與 `run` 支援 `--backend opencode|codex|omp|antigravity` 旗標（未指定預設為 `opencode`）。指定未知後端時將立即 fail-closed 拒絕，0 模型調用且絕不自動切換。Three-Face 模式下後端須支援唯讀沙箱（`CapReadOnlySandbox`），若能力不符立即拒絕。任務執行期間若部分失敗，保存現場並標記 Failed，絕無第二次自動容錯嘗試（No Automatic Failover）。

2026-09-30：OMP 固定支援 CLI 18.4.4、Bun 1.4.2 與 `openai-codex/gpt-6-luna`；Antigravity 固定支援 CLI 1.2.13 與 `gemini-3.8-flash-low`。OMP 還會驗證並複製固定的完整 node_modules 樹（約 1.2 GB 暫存空間）；增加其他 global 套件也會使摘要不符，需使用相符的獨立安裝。兩者需要 Linux `bwrap` 的 PID／IPC namespace；版本或隔離條件不符會在模型呼叫前拒絕。沿用原 CLI 登入，只傳入 access-only 快照，登入過期須在原 CLI 手動更新。OMP 不傳入可用的 refresh token；Antigravity 先讀原 CLI keyring，再讀原 CLI token 檔。

2026-09-30：這兩個 backend 目前僅支援 Single Striker，不宣告唯讀 sandbox 能力，Three-Face 在 Stage 0 拒絕且不呼叫模型。Context window 未知時沿用既有政策；可用 `ASURA_CONTEXT_LIMIT` 指定限制。`status` 顯示任務保存的 backend 與 trace 中的版本。

#### 查詢任務狀態 (status)
```bash
# 向 Daemon 或儲存層查詢指定任務狀態、執行模式與 Pipeline 各階段歷程
./bin/asura status <task-id>
```

#### 安全取消任務 (cancel)
```bash
# 從共同入口發送取消請求，終止任務進程組並確保進程樹安全回收
./bin/asura cancel <task-id>
```

#### 啟動單人 Web 遠端入口 (web)
```bash
# 啟動 Web 控制台（預設監聽 loopback 127.0.0.1:8080，單人私人網路使用）
ASURA_WEB_KEY="your-secret-key" ./bin/asura web \
  --listen 127.0.0.1:8080 \
  --socket /tmp/asura.sock
```

2026-09-13：
- **存取安全**：單一存取密鑰換取 12 小時工作階段，強制 `HttpOnly` 與 `SameSite=Strict` Cookie，驗證所有請求的 Host 與變更方法的 Origin。
- **中樞整合**：共用 Daemon 授權政策與容量限制，網頁端專案下拉選單來自服務端政策，支援輸入既有 CLI Task ID 查詢與共同取消。
- **純文字分頁與恢復**：長輸出結果以 Unicode Rune 進行安全字元切片分頁（`GET /api/tasks/{id}/result`），以 `textContent` 渲染防止腳本執行；關閉頁面重開後登入仍可恢復查詢同一任務，唯讀輪詢不重跑副作用。
- **外部 Tunnel 邊界**：若使用 Cloudflare Tunnel、Tailscale 等第三方工具臨時對外曝露，外部流量仍必須通過密鑰驗證與 Origin 防護；Asura 本體不內嵌 Tunnel 供應商 SDK 或自動管理穿透。

2026-09-23：登入 Web Console 後可在授權專案下管理手動記憶：分頁列出摘要、按 ID 查看詳情、新增內容，以及確認後刪除。清單預設 20 筆、每頁最多 50 筆；摘要最多 512 UTF-8 bytes，Web 詳情最多 64 KiB，新增內容最多 32 KiB。超限或舊資料格式不支援時會明確回報，CLI 的完整記憶查詢仍可使用。

提交任務時可選取最多 32 個同專案記憶 ID，或明確開啟 `bounded` 自動召回；未選取且未開啟時保持原本的 off 行為。送出前顯示 Request ID，回應遺失可用「原請求重試」送出相同內容；若關閉頁面，請使用先前保存的 ID 重新登入查詢。Task 查詢頁會顯示當次保存的記憶與召回決策，刪除現存記憶不會改寫舊 Task 快照。

2026-09-30：Web 的「Git 候選記憶」區域可指定固定 range 或 since-last 擷取、依 pending／approved／discarded 狀態分頁、查看來源與完整草稿，以及編修後確認核准或確認捨棄。Complete／Partial、reused 與拒絕結果會明確顯示；Partial 由服務端禁止核准。核准後顯示正式 `git-approved` Memory ID，可由既有「專案記憶」查看與召回；候選本身不進入 recall，舊 Task 快照不變。

2026-09-30：候選每頁預設 20 筆、最多 50 筆，摘要最多 512 UTF-8 bytes；Web 草稿／核准內容最多 32 KiB、來源 facts 最多 256 KiB、HTTP JSON 最多 512 KiB，超限 detail 明確拒絕，可改用 CLI 查核。Capture 有 10 秒期限，離線會取消尚未完成的 Git 工作；回應遺失時重新載入候選確認真實狀態。切換 workspace、登出或 session 失效會清除未送出的編修；第一版不提供 force、ai_draft 或 bulk clear。

#### 定時排程管理 (cron)
```bash
# 新增定時排程（標準 5 欄位 Cron：分 時 日 月 週）
./bin/asura cron add \
  --name "nightly-audit" \
  --workspace /path/to/project \
  --schedule "0 3 * * *" \
  --prompt "執行靜態檢查與測試"

# 查詢所有排程清單與下次執行時間
./bin/asura cron list

# 查詢特定排程歷史觸發紀錄
./bin/asura cron runs <cron-id> --limit 10

# 暫停排程（不再自動觸發）
./bin/asura cron pause <cron-id>

# 恢復排程（重新計算下次執行時間並啟用）
./bin/asura cron resume <cron-id>

# 刪除排程（連帶清理歷史觸發紀錄）
./bin/asura cron remove <cron-id>
```

2026-09-13：
- **同專案排他佇列**：排程任務與互動任務共用專案互斥鎖，同一工作目錄絕不並行執行，避免檔案衝突。
- **保留互動通道**：保留獨立執行槽位供互動任務（`ask`），排程暴增時 CLI 互動請求絕不被餓死。
- **重疊略過與漏跑不補**：前次任務執行中若再度到期自動略過（`skipped`）；Daemon 重啟時過期任務自動重新對齊未來時刻，不補跑歷史。
- **未授權目錄自動暫停**：排程工作目錄若未在 Daemon `--allowed-roots` 內，觸發時嚴格拒絕並自動轉為 `paused`。
- **不可達日期安全防護**：如 2 月 31 日等日曆上不可達之排程在新增時即刻拒絕，防止計時器陷入零值雪崩循環。
- **日與週匹配語意**：若表達式中同時指定「日（Day of Month）」與「週（Day of Week）」，採用交集（AND）條件匹配（即「當日且當週」同時成立才觸發）。

#### 專案級基礎記憶管理 (memory)
```bash
# 新增專案記憶（純文字內容，固定 manual 來源與建立時間）
./bin/asura memory add \
  --workspace /path/to/project \
  --content "專案重要架構決策與業務規則"

# 列出專案所有記憶（五欄位完整顯示）
./bin/asura memory list \
  --workspace /path/to/project

# 查詢指定 ID 記憶之詳細五欄位（ID、Workspace、Content、Source、CreatedAt）
./bin/asura memory get \
  --workspace /path/to/project <memory-id>

# 刪除指定記憶（依 ID 與專案精確比對刪除）
./bin/asura memory remove \
  --workspace /path/to/project <memory-id>

# 擷取 Git 變更事實產生待審候選草稿（支援固定 --range 或增量 --since-last）
./bin/asura memory capture \
  --workspace /path/to/project --since-last

# 檢視專案所有待審候選記憶草稿（包含原始變更事實與 Provenance 來源）
./bin/asura memory candidates \
  --workspace /path/to/project

# 核准指定候選草稿轉入正式記憶（可選 --content 就地修訂內文）
./bin/asura memory approve \
  --workspace /path/to/project <candidate-id>

# 捨棄指定候選草稿（標記為 rejected，不可逆且防復活）
./bin/asura memory discard \
  --workspace /path/to/project <candidate-id>

# 清理專案所有已處理完畢之候選紀錄（清除 approved/rejected，保留 pending）
./bin/asura memory clear-candidates \
  --workspace /path/to/project
```

2026-09-15：
- **五欄位完整回傳**：查詢與寫入均完整回傳 `ID`、`Workspace`（標準化正規路徑）、`Content`（完整文字不截斷）、`Source`（固定為 `manual`）與 `CreatedAt`（建立時間）。
- **精確 Workspace 隔離**：記憶嚴格限定於該專案的標準化路徑；同一授權 root 下之不同專案嚴格隔離；子目錄不繼承父目錄記憶；有效符號連結別名共用相同專案記憶集合。
- **撤權與重新授權政策**：專案自 `--allowed-roots` 撤權後，原記憶保留於儲存層，但所有讀寫刪除操作皆嚴格拒絕；重新獲得授權後立即恢復查詢與操作。
- **重啟持久化與指定刪除**：記憶持久化保存於 SQLite，重啟 Daemon 完整保留未刪資料；刪除指定記憶後重啟不恢復，且另一專案與同專案其他記憶完全不受影響。
- **失敗回報與零 AI 調用**：Daemon 不可用或操作失敗時，CLI 明確輸出錯誤至 stderr 並回傳非零退出碼，絕不冒充已保存或已刪除，不改走其他儲存方式；記憶管理操作直通儲存層，絕不啟動 AI 模型後端；既有任務提交不自動注入記憶內容。

2026-09-25：
- **Git 變更感知與候選隔離（Phase 3H）**：支援 `asura memory capture`，唯讀安全執行 Git 子程序讀取固定範圍（`base..head`）或 `--since-last` 增量變更。
- **實體儲存與召回隔離**：候選草稿存放於獨立 `memory_candidates` 表，未審核之草稿 100% 隔離，絕不進入 Task 記憶召回 prompt；僅在顯式 `asura memory approve` 後寫入正式記憶（`source='git-approved'`）並建立獨立 `memory_provenance` 來源記錄。
- **有界截斷與 Checkpoint 安全**：受硬性邊界（200 commits、500 files、256 KiB diff）限制；超出限制時標記 Partial 截斷，且嚴格禁止自動前進 Checkpoint，避免未分析歷史遺漏；不完整候選（Partial Candidate）嚴格禁止核准（Approve），杜絕殘缺事實污染正式專案記憶。
- **防復活與歷史密封性**：核准或拒絕紀錄持久化保存，防止重複 Range 重新擷取；手動刪除已核准記憶後禁止原 Candidate 復活；既有 Task 快照維持密封不變。

#### 有界自動記憶召回

2026-09-22：`ask` 可明確啟用詞彙召回，預設 `off`；Web/Cron/run 不新增啟用入口。

```bash
./bin/asura ask --socket /tmp/asura.sock --workspace /path/to/project \
  --memory-recall bounded --memory-id explicit-id "檢查 SQLite 隔離規則"
```

- 僅讀取已授權且相同 canonical workspace 的記憶，接受任務時封存；相同 request ID 重送沿用原快照，即使記憶或管理者設定改變也不重新選取。
- `lexical-v1` 使用英文等字詞與相鄰中文雙字詞比對，不支援同義詞／語意檢索。只評分 ID 升序前 256 筆；query 使用前 4096 bytes 完整字元前綴、最多 128 個不同詞，可能漏召回。
- 自動最多加入 4 筆，實際格式化增量最多 8192 bytes／2048 estimated prompt tokens；explicit 記憶不裁切或替換。候選內容超過 8192 bytes 或 metadata 過大時整筆略過。
- 有已設定窗口時，auto 也受首次 stage 含輸出保留的估算限制；原始 prompt／explicit 基底超限仍沿用 `observe`／`reject`。後續每個 stage 依實際產物重新 admission。未設定窗口時，只保證 auto 額度有限，不能保證模型不溢出。
- 查詢及選取期限 250ms，逾時或資料庫錯誤拒絕提交。`status` 的 `Recall` 提供選取來源、排序分數、政策、estimated budget 與略過原因計數，不保存未選候選全文。
- 重啟沿用既有政策：未完成任務標為中斷失敗，快照保留，不自動重跑。

#### 固定三面管線 (Three-Face Fixed Pipeline)
2026-09-17：
- **模式語意與向後相容**：
  - `direct`（預設）：直通既有單一 Striker / Runner，完全維持 Phase 3B 的 Prompt、Memory 與 Redelivery 契約，僅產生 1 次模型呼叫。
  - `three-face`：固定依序執行 Oracle（規劃）→ Striker（執行）→ Critic（審查），各執行 1 次，總計 3 次模型呼叫。
- **共用不可變 Sealed ExecutionContext**：三角色共用同一 Task execution identity 與快照記憶，執行期間外部新增、修改或刪除記憶絕不破壞既有任務上下文。
- **結構化契約與 Fail-Closed**：
  - Oracle 產出結構化 `OraclePlan`（包含 Goal, Constraints, Steps, ExpectedOutcome），Striker 依據該 Plan 執行。
  - Critic 審查 Striker 產出並產出結構化 `CriticReview`（包含 Verdict, Findings, Summary）。
  - 嚴格 Fail-Closed：任一 Stage 失敗（或 Critic Verdict 判定 Failed）立即標記任務為 Failed，絕不靜默 fallback，保存已發生 Stage 的 partial output 與錯誤原因。
- **審計與成本追蹤**：所有任務持久化 `ExecutionMode` 與 `PipelineTrace`（包含各 Stage 狀態、DurationMs、呼叫次數與結構化 Plan/Review），支援 CLI `status` 格式化檢視。
- **Redelivery 冪等防禦**：請求指紋計算納入 `Mode` 參數；相同 Request ID 與相同 Payload 重送時立即回傳既有 Task，絕不重複觸發三角色執行。

#### 上下文窗口管理與 Token 審計 (Context Window Management & Audit)
2026-09-21：
- **能力探測優先與真實邊界**：固定版本 OpenCode 與 Codex CLI 經過 Contract Probe 驗證，明確標記 unsupported 能力，不推測或假裝擁有模型內部 Context 編輯權。
- **動態 Stage-Aware 預估與准入 (Admission)**：
  - Oracle、Striker（帶入動態 OraclePlan）與 Critic（帶入 OraclePlan + StrikerOutput）各自進行執行前（Preflight）獨立上下文預估。
  - 透過環境變數 `ASURA_CONTEXT_LIMIT`（正整數 Token）與 `ASURA_CONTEXT_POLICY`（`observe` 觀測警示 或 `reject` 拒絕）進行程序級預算控制。
- **Fail-Closed 阻擋防禦**：在 `reject` 政策下，超量 Stage 於調用後端前直接拒絕，模型呼叫次數為 0，且絕不自動更換 Backend，前面已完成之 Stage 產物與 Trace 完整保留。
- **數據來源可信度 (Provenance) 與不可變性**：
  - 嚴格區分 `exact`（後端實測）、`estimated`（啟發式估算）與 `unknown`（缺失），絕不將未回報數值捏造為 0。
  - 預算檢查維持 Sealed Context 不變性，絕不私自刪剪記憶或 Prompt。

---

## 架構設計與議題文件

- **系統架構設計規範**：請參閱 [架構設計文件](docs/superpowers/specs/2026-09-11-asura-architecture-design.md)，涵蓋信任邊界、排程拓撲、進程模型與 D-01 至 D-09 技術決策。
- **議題追蹤與驗收劇本**：
  - [Issue 1 本機執行核心](docs/issues/issue-1/README.md) 與 [實作計畫](docs/issues/issue-1/implementation-plan.md)
  - [Issue 2 本機 AI 執行路徑整合 (Phase 1.5)](docs/issues/issue-2/README.md) 與 [實作計畫拆解](docs/issues/issue-2/implementation-plan-decomposition.md)
  - [Issue 4 單一 Web 遠端入口 (Phase 2A)](docs/issues/issue-4/README.md) 與 [實作計畫拆解](docs/issues/issue-4/implementation-plan-decomposition.md)
  - [Issue 6 定時排程與容量調度 (Phase 2B)](docs/issues/issue-6/README.md) 與 [實作計畫拆解](docs/issues/issue-6/implementation-plan-decomposition.md)
  - [Issue 9 專案級基礎記憶 (Phase 3A)](docs/issues/issue-9/README.md) 與 [實作計畫](docs/issues/issue-9/implementation-plan.md)
  - [Issue 13 Three-Face Fixed Pipeline (Phase 3C)](docs/issues/issue-13/README.md) 與 [實作計畫](docs/issues/issue-13/implementation-plan.md)
  - [Issue 19 Bounded Automatic Recall](docs/issues/issue-19/README.md) 與 [實作計畫](docs/issues/issue-19/implementation-plan.md)
  - [Issue 17 Context Window Management Foundation (Phase 3E)](docs/issues/issue-17/README.md) 與 [實作計畫](docs/issues/issue-17/implementation-plan.md)
  - [Issue 23 Git-Aware Project Memory Capture (Phase 3H)](docs/issues/issue-23/README.md) 與 [實作計畫](docs/issues/issue-23/implementation-plan.md)

---

## 基礎設施與 AI 開發規範

本專案之 AI 開發規範、文件體系與工作流程遵循 [`dev-rules-kit`](../dev-rules-kit) 標準架構：

- **核心行為準則**：請參見根目錄 [AGENTS.md](AGENTS.md)，涵蓋「編寫前先思考」、「簡單優先」、「手術式修改」、「目標驅動執行與驗證」及「BDD + TDD 雙迴圈」等原則。
- **Claude Code 導覽**：請參見根目錄 [CLAUDE.md](CLAUDE.md)。
- **文件規範與開發閉環**：請參見 [docs/AGENTS.md](docs/AGENTS.md)，詳細規範 issue 的動態分級（Small / Medium / Large）、驗收標準形式（輕量驗收條件 vs Gherkin 劇本）與紅綠重構證據要求。

### 目錄結構

```
Asura/
├── .gitignore             # Git 忽略設定（排除本地資料庫與暫存檔）
├── README.md              # 專案說明文件
├── CHANGELOG.md           # 變更修訂紀錄
├── AGENTS.md              # 跨平台 AI 核心開發規則
├── CLAUDE.md              # Claude Code 進入點與導覽
├── cmd/
│   └── asura/             # CLI 程式進入點 (run, cancel, status, ask, cron, memory, web)
├── internal/
│   ├── auth/              # 工作目錄與授權政策 (Policy)
│   ├── backend/           # 執行後端整合 (opencode process, parser)
│   ├── contextwin/        # 上下文窗口管理、預算、估算器與准入引擎
│   ├── cron/              # 定時排程器與日曆計算
│   ├── daemon/            # 守護進程服務中樞
│   ├── engine/            # 任務狀態引擎 (Engine, Worker Pool)
│   ├── pipeline/          # Three-Face 管線核心 (Oracle, Striker, Critic, Orchestrator)
│   ├── storage/           # SQLite 持久化儲存層
│   └── web/               # 單人 Web 遠端入口 (SPA 控制台與同源 API)
├── tests/
│   └── integration/       # 端到端整合測試 (SCN, CRON, MEM, TFACE, CTXWIN 驗收)
└── docs/                  # 專案技術文件與議題紀錄
    ├── AGENTS.md          # docs/ 目錄規範與開發閉環指引
    ├── superpowers/       # 架構設計規格書 (Architecture Spec)
    └── issues/            # 議題閉環追蹤 (Issue 1 ~ N)
```

### 開發工作流程

日常開發遵循以 Issue 為中心的開發閉環：

```
new-issue ──► decompose (僅 Large) ──► execute-task (BDD + TDD) ──► create-commit ──► create-pr ──► review
```

可直接搭配全域工作流程 `/dev-cycle {ID}` 或各階段專屬技能推進。

---

*最新更新日期：2026-09-25*
