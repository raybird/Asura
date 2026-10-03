# Asura（阿修羅）系統架構與設計規範 (Architecture Design Spec)

- **文件建立日期**：2026-09-11
- **專案狀態**：架構草案，關鍵契約待驗證（2026-09-11 修訂；原狀態為「架構規劃完成，進入實作準備階段」）
- **實作語言**：Go (Golang 1.22+)
- **專案路徑**：`/home/kevin/Documents/RCodes/Asura`

---

## 1. 專案願景與定位 (Vision & Positioning)

### 1.1 系統定位
**Asura（阿修羅）** 是基於 **TeleNexus (moltbot-lite)**、**Memoria** 與 **Wukong（孫悟空）** 的實戰經驗演進而成的第三代個人自主 AI 控制平面（Personal Autonomous AI Control Plane & Orchestrator）。

其核心神話隱喻為「三面六臂、非天非魔、極致戰力」：
- **三面（Three Faces）**：由 **智者之面 (Oracle)**、**執行之面 (Striker)** 與 **審查之面 (Critic)** 組成的動態協同管線，告別單一 Prompt 的混亂負擔。
- **六臂（Six Execution Arms）**：向外伸展的六大通訊與執行通道（Telegram、Web Console、Cron 排程、本機 CLI、記憶中樞、Agent 執行池）。
- **單一中樞守護（Unified Daemon）**：以 Go 語言編譯為單一靜態二進位檔案，徹底終結多進程對 SQLite 的檔案鎖與 Migration 競爭。

### 1.2 解決的上一代痛點

> [!NOTE]
> 2026-09-11 修訂：以下保留原始目標。「杜絕鎖死」「數月不凍結」尚非已成立的保證；30MB 與 1.1 秒亦未量測。驗證範圍與判準見第 8 節 D-09。

1. **擺脫常駐資源膨脹與記憶體洩漏**：摒棄 Node.js/Bun 常駐的高記憶體基準與 idle CPU 浪費，以 Go 實現閒置記憶體 < 30MB、CPU 趨近 0% 的極致輕量守護。
2. **終結記憶資料庫分裂**：將對話歷程、排程定義、長期知識與效用遙測收斂至單一 `asura.db`，杜絕跨容器 SQLite `SQLITE_BUSY` 鎖死。
3. **解決 Context 限制與長期退化**：建立五層防禦體系（工具輸出外部化、分層預算、無狀態探路、自動滾動微摘要、會話交接手冊），保證系統數月連續運行不凍結。
4. **健全無人值守與異常防禦**：建立 429 速率限制 1.1 秒快速失敗、410 模型下架精確攔截、以及進程組（PGID）樹狀強殺，杜絕假綠燈與殭屍進程。

---

## 2. 系統架構與進程模型 (Architecture & Process Model)

### 2.1 單一守護進程拓撲 (Unified Daemon)

```
                              ┌───────────────────────────────────┐
                              │            ASURA CORE             │
                              │        (單一二進位檔案 asura)       │
                              └─────────────────┬─────────────────┘
                                                │
                 ┌──────────────────────────────┼──────────────────────────────┐
                 ▼                              ▼                              ▼
      【Ingress & Interfaces】         【Core Orchestrator】           【Storage & Engine】
      - Telegram Bot (Long-Poll)       - Three-Face Pipeline          - SQLite WAL (Single Conn Pool)
      - Web Console (Chi + SSE)          (Oracle ➔ Striker ➔ Critic)   - CJK Bigram FTS5 全文檢索
      - Cron Scheduler Engine          - Global Worker Pool (Max 2)   - UFL 效用遙測與校準
      - Terminal CLI Subcommands       - Resiliency & 429/410 Guard   - SAR 預算式記憶召回
                 │                              │                              │
                 └──────────────────────────────┼──────────────────────────────┘
                                                ▼
                                    【Execution Backends】
                                 - Opencode CLI (預設)
                                 - Codex CLI
                                 - agy (Antigravity CLI)
```

### 2.2 運作模式
1. **`asura daemon`**：常駐背景守護行程。初始化單一 SQLite WAL 連線、啟動 Global Worker Pool、同時監聽 Telegram、Web Console (:8080) 與 Cron 排程。
2. **`asura run "<prompt>"`**：命令列單次調用。優先向 Daemon 送入 High Lane 執行；若 Daemon 未啟動則以 Standalone 模式直接加載執行。

### 2.3 原始碼目錄結構

> [!NOTE]
> 2026-09-11 補充：第 2.2 節的 Standalone 回退須先定義唯一擁有權與啟動競爭政策；「無法連線」不能直接等同「Daemon 不存在」。單一二進位不代表單一程序（D-02）。下列目錄為提案，尚未建立。

```text
Asura/
├── cmd/
│   └── asura/              # 主程式進入點 (main.go)
├── internal/
│   ├── daemon/             # 守護進程生命週期、信號捕獲與優雅關機
│   ├── config/             # 設定檔解析 (YAML / ENV / 預設值)
│   ├── ingress/            # 通訊輸入 (telegram, web, cli, cron)
│   ├── pipeline/           # 三面管線 (oracle, striker, critic)
│   ├── worker/             # 全域 Worker Pool 與雙 Lane (interactive, scheduled)
│   ├── backend/            # Agent 後端適配器 (opencode, codex, agy)
│   ├── memory/             # SQLite, CJK Bigram, FTS5, UFL, SAR
│   └── utils/              # 錯誤分析、PGID 程序清理、Markdown 渲染
├── web/                    # 內嵌 Web Console 靜態頁面 (HTML/JS/CSS, go:embed)
├── docs/                   # 技術文件、規範與議題閉環 (dev-rules-kit 體系)
│   └── superpowers/specs/  # 架構規格書存檔
├── go.mod
└── go.sum
```

---

## 3. 三面六臂協同管線 (Three-Face Pipeline & Six Arms)

### 3.1 三面動態協同機制

```
                     ┌─────────────────────────┐
                     │     使用者輸入 / 排程觸發    │
                     └────────────┬────────────┘
                                  │
                                  ▼
                      【意圖複雜度評估 (Router)】
                                  │
                 ┌────────────────┴────────────────┐
           (直接問答 / 簡單命令)              (多步驟工程任務)
                 │                                 │
                 ▼                                 ▼
          【快速通道】                       【完整三面流水線】
        Striker 直接執行              ┌───────────────────────────┐
                 │                    │ 1. 智者之面 (Oracle)       │
                 │                    │    - SAR 記憶回想 (專案脈絡)│
                 │                    │    - 任務拆解與架構規劃    │
                 │                    │    - 產生結構化執行計畫    │
                 │                    └─────────────┬─────────────┘
                 │                                  │ (帶入無狀態前序計畫)
                 │                                  ▼
                 │                    ┌───────────────────────────┐
                 │                    │ 2. 執行之面 (Striker)      │
                 │                    │    - 調度 opencode/codex/agy│
                 │                    │    - 工具執行、代碼修改    │
                 │                    │    - 實時輸出工具 Emoji    │
                 │                    └─────────────┬─────────────┘
                 │                                  │ (帶入執行日誌與測試結果)
                 │                                  ▼
                 │                    ┌───────────────────────────┐
                 │                    │ 3. 審查之面 (Critic)       │
                 │                    │    - 驗收標準與 TDD 驗證  │
                 │                    │    - 防空輸出修復 (Repair) │
                 │                    │    - 結算 UFL 記憶效用評分 │
                 │                    └─────────────┬─────────────┘
                 │                                  │
                 └────────────────┬─────────────────┘
                                  ▼
                     【整合最終回答 ➔ 回傳使用者】
                     【關鍵決策結構化落盤至 asura.db】
```

1. **智者之面 (Oracle)**：
   - 負責意圖分析、情境邊界劃分。
   - 透過 CJK Bigram + SAR 檢索專案歷史決策。
   - 複雜任務產出循序執行計畫（Step Plan），並以**無狀態（Ephemeral）執行**，避免探路過程污染主會話。
2. **執行之面 (Striker)**：
   - 驅動底層 CLI（`opencode` / `codex` / `agy`）。
   - 執行檔案編輯、終端機命令與瀏覽器操作。
   - 即時監聽與解析事件流，轉換為即時工具 Emoji（🔍, 📁, 📖, 💻, ✏️, 🌐）。
3. **審查之面 (Critic)**：
   - 依據 `dev-rules-kit` 驗證完成度與測試證據，嚴防無效產出。
   - 空輸出防禦（Empty Output Repair）：若 Striker 只調用了工具卻未產生總結文字，Critic 自動在同會話中發起追問總結，避免記憶空落盤。
   - 結算 UFL（Utility Feedback Loop）效用，自動向資料庫回報記憶實質利用狀況。

### 3.2 六臂執行通道 (Six Execution Arms)

> [!NOTE]
> 2026-09-11 補充：快速通道繞過 Critic，因此原案的空輸出修復與 UFL 並未覆蓋所有任務。建議將錯誤判定、結果保存與有界空輸出處理放入共同收尾；補總結不等同重跑已產生副作用的工作。模型審查與 UFL 另行驗證（D-06）。

- **Arm 1 (Telegram Arm)**：Long-Polling 連線、MarkdownV2 AST 渲染、置頂釘選狀態訊息（Pinned Status）、分段防破損傳輸。
- **Arm 2 (Web Console Arm)**：輕量嵌入式 Web 介面、SSE 打字機即時串流、會話歷史導覽。
- **Arm 3 (Scheduler Arm)**：Cron 排程時鐘，支援定時觸發與閒置靜默反思（Silence Reflection）。
- **Arm 4 (Terminal CLI Arm)**：本地命令列直接互動，支援標準輸入輸出管線。
- **Arm 5 (Memory & Knowledge Arm)**：本地記憶庫、Git 唯讀增量掃描、`BRIEF.md` 靜態導覽編譯。
- **Arm 6 (Agent Backend Pool Arm)**：CLI 進程生命週期管理、429 退避降頻、進程組強殺回收。

---

## 4. 全域 Worker Pool 與並發調度 (Global Worker Pool)

> [!NOTE]
> 2026-09-11 修訂：以下保留原案；優先排隊不等同中斷執行中的工作。「排程任務絕不卡死即時聊天」須由容量保留或其他核准策略支撐。滿載等待、背景飢餓、同專案寫入互斥，以及 Oracle／Critic／修復呼叫如何計入上限，皆待 D-03 決定。


```
                            【全域任務入口】
                                   │
                  ┌────────────────┴────────────────┐
                  ▼                                 ▼
        【Interactive Lane】              【Scheduled Lane】
        (Telegram 即時 / Web 點擊)          (Cron 定時排程 / 背景巡檢)
         優先級：HIGH                       優先級：LOW
                  │                                 │
                  └────────────────┬────────────────┘
                                   ▼
                     ┌───────────────────────────┐
                     │     Global Worker Pool    │
                     │   (Max Concurrent = 2)    │
                     │  * 支援 Context 逾時強殺   │
                     │  * 散熱與資源過載保護       │
                     └─────────────┬─────────────┘
                                   ▼
                    [Worker 1]           [Worker 2]
```

- **雙 Lane 分流**：`Interactive Lane`（高優先級）擁有搶佔排程權；`Scheduled Lane`（低優先級）排隊等候，排程任務絕不卡死即時聊天。
- **硬性並行上限（預設 2）**：嚴格限制本機同時運行的 Agent 進程數，防止主機 CPU 飆破極限導致凍結。
- **全鏈路 `context.Context` 穿透**：取消信號或逾時直接觸發進程組（PGID）級別的強制清理。

---

## 5. 記憶持久化與檢索模型 (Memory, CJK Bigram & UFL)

### 5.1 核心機制整合
1. **CJK Bigram 雙向擴展**：在寫入 `search_text` 與執行查詢時，將中文字串自動切分為二元片語（Bigram），結合 SQLite FTS5 虛擬表實現高精準度中文 BM25 檢索。
2. **UFL (Utility Feedback Loop)**：
   - 每次檢索生成唯一 `recall_id`；
   - Critic 自動分析模型產出是否重用檢索內容，回填效用分數；
   - 長期未被重用的冗餘記憶動態降權（Down-weight, 最低衰減至 50%），高效用記憶則豁免清理。
3. **長期記憶語意（Durable & Supersedes）**：
   - `durable` 記憶永久免除 90 天半衰期衰減與過期清理；
   - `supersedes` 實現決策版本更新，舊記憶退出檢索但歷史完全保留。
4. **SAR 嚴格字元預算控制**：
   - 檢索注入內容嚴格限制於 1,500 字元預算內，包含：核心 Durable 規範（500 字元）+ Top-K 歷史命中（600 字元）+ 近期對話脈絡（400 字元）。

### 5.2 資料庫 Schema 規格 (`asura.db`)

> [!NOTE]
> 2026-09-11 補充：以下是資料分類草圖。訊息、任務執行、重試、排程觸發與結果的持久化關係，以及狀態轉移、崩潰恢復、scope 隔離與 FTS 同步，尚待 D-07 定義；不視為完整 schema。

- `sessions`：會話資料表（包含 scope 隔離，如 `project:Asura`）。
- `memories`：結構化記憶本體（category, title, content, rationale, search_text, importance, recall_count）。
- `memories_fts`：FTS5 外部內容虛擬表。
- `memory_attributes`：長期屬性（is_durable, superseded_by, sensitivity）。
- `recall_telemetry`：UFL 效用回報與遙測（query, hit_memory_ids, utility_score, used）。
- `scheduled_jobs`：排程任務表（cron_expr, prompt, scope, channel, is_active）。

---

## 6. Context Window 五大工程防禦體系 (Context Defense)

> [!NOTE]
> 2026-09-11 修訂：以下五項為能力提案。D-08 須先證明後端可在工具結果進入模型前攔截、提供 Token 用量及替換會話歷史；讀取輸出事件不等同控制 Context。不支援時須縮減範圍或採用後端原生能力。另需定義字元與 Token 配額的優先關係、超長單行截斷、非同步摘要版本一致性及 HANDOVER 的隔離與保留政策。


1. **防線一：工具巨量輸出外部化 (Output Offload)**：
   - 單次工具輸出大於 4KB 或 100 行時，自動落盤至 `.asura/cache/tool_out_<hash>.log`；
   - Prompt 中僅保留首尾各 30 行與日誌路徑，提示 Agent 自主以 grep 檢索。
2. **防線二：嚴格分層預算制 (Context Budget Allocator)**：
   - 總預算嚴格配比：System Rules (15%) + SAR Memory (15%) + Recent Turns (25%) + 動態執行與思考餘量 (45%)。
3. **防線三：無狀態輔助棒與上下文隔離 (Ephemeral Execution)**：
   - Oracle 的調研與探路過程在臨時 session 進行，結束後立即物理銷毀，僅傳遞精華「前序計畫」至 Striker。
4. **防線四：主動微摘要與滾動壓縮 (Compaction)**：
   - 會話 Token 達到 75% 警戒水位時，由 Critic 非同步壓縮前序輪次為結構化事實，落盤入 DB，主會話只保留精簡摘要。
5. **防線五：自動會話交接手冊 (Session Handover Protocol)**：
   - 會話超過 20 輪時，系統自動產出 `HANDOVER.md` 備忘錄，封裝舊會話並以清爽狀態開立新會話。

---

## 7. 執行後端適配與異常防禦 (Backends & Resiliency)

### 7.1 後端適配優先級

> [!NOTE]
> 2026-09-11 修訂：以下 CLI 參數及事件格式尚未以固定版本驗證。略過權限的採用方式須依 D-01 的授權政策決定，不作為已核准預設值。

- **Opencode Adapter（主力）**：驅動 `opencode run --format json`，支援 stdout JSON 事件流解析、即時 Emoji 與自動帶入 `--dangerously-skip-permissions`。
- **Codex Adapter**：封裝 Codex CLI 之 stdin/stdout 通訊。
- **Agy Adapter**：原生適配本地 Antigravity CLI (`agy`)，提供無人值守指令封裝。

2026-09-30：目前 Registry 提供 opencode（預設）、codex、omp、antigravity；新 adapter 邏輯限於 backend 套件及 CLI／Daemon composition。OMP 固定 18.4.4／Bun 1.4.2，Antigravity 固定 1.2.13，hash 驗證後複製 runtime 並唯讀掛載，避免來源更新競態。bwrap PID／IPC namespace、私人 HOME／tmp／dev 與 memfd access-only 登入隔離為必要前提；無支援或登入過期即拒絕。新 adapter 不宣告 read_only_sandbox，Three-Face 在 Stage 0 拒絕。provider transport retry 不視為 Asura 新 attempt；不增加 failover、resume 或自動更新登入。最後驗證日期：2026-09-30，證據見 [issue 25](../../issues/issue-25/evidence/README.md)。

### 7.2 異常防禦與故障轉移

> [!NOTE]
> 2026-09-11 修訂：以下保留原案。「無縫切換」需區分未執行、部分執行與結果未知，並定義重試上限、冷卻、備援耗盡及副作用接續（D-04）。429／410 的事件來源、結構與退出碼待 D-08 驗證。PGID 強殺需另驗證逃逸程序與退出回收；發送信號不等同資源已回收。`agent-browser close --all` 的作用域與其他任務隔離待 D-05 驗證。

1. **429 速率限制 1.1 秒快速失敗 (Fail-Fast)**：監聽 stderr 命中 `"statusCode":429` 立即取消子進程，禁止長時間指數退避卡死 Worker。
2. **410 模型下架精確攔截**：深層分析 JSON `error` 結構，消滅 Exit code 0 的「假成功」。
3. **進程組 (PGID) 樹狀強殺**：`Setpgid: true` 啟動，取消時以 `syscall.Kill(-pgid, syscall.SIGKILL)` 徹底消滅所有衍生子行程與殭屍進程。
4. **逃逸守護進程主動清理**：任務收尾主動回收 `agent-browser close --all`。
5. **模型故障轉移池 (Model Failover Pool)**：主模型遭遇 429 或 410 時自動無縫切換備援清單。

2026-09-29 更新：外部 CLI 取消在 SQLite 初始化前先建立以 Task ID 雜湊命名的取消意圖 sidecar；runner 取得同樣以雜湊命名的排他鎖後，必須在 `cmd.Start` 前同時檢查 sidecar 與 DB 狀態。取消端先以 DB CAS 登記 `cancelling`，鎖握手完成且取得不可變進程身分後才回收 PGID，確認回收後才寫入 `cancelled` 並清除 sidecar；握手或身分驗證失敗時保留取消意圖以 fail-closed。

---

## 8. 範圍、信任邊界與決策追蹤（2026-09-11）

### 8.1 首版邊界（提案，待核准）

首個可交付核心建議包含本機 CLI、Daemon、單一後端、資料庫擁有權、任務狀態、取消與共同收尾。Opencode 為優先探測對象，契約驗證後再確認採用。

下一階段選定一個遠端入口，再加入 Cron。完整三面管線、UFL、進階 Context 管理、多後端、Git 掃描與靜默反思列為後續範圍。階段順序見[實作計畫](../../issues/issue-1/implementation-plan.md)，驗收候選與核准狀態見[規劃入口](../../issues/issue-1/README.md)。

### 8.2 信任與執行邊界（提案，待核准）

所有入口先辨識呼叫者及可操作專案，再依權限政策建立任務。D-01 須決定 Telegram 允許名單、Web 綁定位址與驗證、本機 IPC 存取控制、工作目錄限制，以及需要使用者確認的操作。

任務清理以該任務擁有的程序與瀏覽器資源為邊界。後端略過權限、任務中斷與資源回收須共同符合該政策；簡單命令的快速通道也需經過授權及共同收尾。

### 8.3 待驗證假設與決策

本表維護技術問題、狀態與證據；規劃文件引用 ID。2026-09-11 尚未執行技術探測，以下全部待確認。結果應記錄版本、實際命令、觀察值及證據位置，再更新狀態。

| ID | 尚待確認 | 完成證據 | 狀態 |
|---|---|---|---|
| D-01 | 入口授權、工作目錄與權限政策 | 核准的權限矩陣；允許／拒絕對照，拒絕時無 Agent 啟動及寫入副作用 | 待確認 |
| D-02 | Daemon／Standalone 唯一擁有權、Migration 與 IPC 失敗政策 | 雙 run、run 與 daemon 競爭啟動、Migration 失敗及 IPC 中斷案例均無雙重擁有者或突破全域上限 | 待確認 |
| D-03 | 滿載調度、寫入互斥與併發計數 | 核准策略與等待門檻；滿載時序、背景進度及同專案並行寫入驗證 | 待確認 |
| D-04 | 部分執行後的重試與故障轉移 | 在副作用前後注入失敗，以獨立外部計數確認符合核准重試政策 | 待確認 |
| D-05 | 程序與瀏覽器資源所有權 | 取消 A 後 A 資源回收，B 與使用者原有資源仍可操作；退出回收有實際證據 | 待確認 |
| D-06 | 快速／完整管線共同收尾與審查邊界 | 成功、結構化錯誤與空輸出皆有結果；補總結次數有界且不重做副作用 | 待確認 |
| D-07 | 任務、歷史、scope、FTS 與恢復契約 | 崩潰後結果未知可辨識；跨 scope 不外洩；重複排程符合核准政策；索引與來源一致 | 待確認 |
| D-08 | CLI 契約、Context 控制、預算與交接 | 2026-09-21 Phase 3E 完成 OpenCode (1.18.23/1.18.31) 與 Codex (0.155.0/0.155.1) 之 Context Contract Probe，建立能力矩陣與 unsupported 清單；實作 Stage-aware 預估與 Preflight Fail-closed 准入控制；模型內部 Context 編輯/動態歷史替換經驗證標記為 unsupported | 已驗證基礎契約（Phase 3E） |
| D-09 | 效能與穩定性門檻 | 定義硬體、版本、資料量、負載、觀測區間及延遲起訖；區分 Daemon 與子程序；已知失敗對照可被判準捕捉，再取得數值 | 待確認 |

### 8.4 修訂紀錄

| 日期 | 異動 |
|---|---|
| 2026-09-29 | 記錄外部 CLI 取消的 sidecar／flock／DB CAS 握手與 PGID 回收順序。 |
| 2026-09-21 | Phase 3E (Issue 17) 完成 OpenCode / Codex 固定版本能力探測與 unsupported 矩陣，建立 Stage-aware 預估與 Fail-closed Preflight 准入控制，更新 D-08 驗證狀態。 |
| 2026-09-11 | 依使用者授權整理文件；保留原案並將完成狀態與絕對保證改列待驗證，新增首版提案、信任邊界、D-01 至 D-09 及實作草案入口。技術行為尚未逐項核准。 |

*文件版本：v1.2.0*
*建立日期：2026-09-11*
*更新日期：2026-09-29*


## Phase 3F：有界自動專案記憶召回

最後驗證日期：2026-09-22。

Daemon 在授權與 request fingerprint/redelivery 後、explicit 解析後、容量取得及 Task 保存前執行 bounded recall。預設 off 完全沿用舊指紋；bounded 使用固定結構與獨立 domain 的指紋，只包含使用者選項，不含管理者政策版本。重送先回傳 Task，不查候選。

SQLite 新增 `(workspace,id)` 索引；前 257 筆中僅前 256 筆供 selector 使用，第 257 筆標示截斷。SQL 對 ID/content/source/created_at 只取上限加一的 byte sentinel，避免讀入完整超大欄位。selector 對同 scope、合法 metadata 進行確定性詞彙排序，服務端使用真實 ComposePrompt 與 Oracle Prepare 計算增量及首次 stage 預算。

ExecutionContext 的可選 `recall` 保存政策、explicit/auto IDs、選中分數、estimated budget 與略過計數；無匹配也保存。舊資料沒有 recall 即視為 off；不改寫 memories 或舊 Task。PipelineTrace 的所屬 Task 保有唯一決策，後續 stages 沿用密封內容並各自執行 admission。重啟的中斷失敗政策不變。

完整上限、排序限制與驗收見 [Issue 19](../../issues/issue-19/README.md)。

## Phase 3G：Web Console 專案記憶管理與召回操作

2026-09-23；2026-09-24 更新。Web 的 `/api/memories` list/detail/add/delete 經既有 session、Host 與變更方法 Origin 入口，透過 Unix IPC 交由 Daemon 每次重新授權 canonical workspace；Web 不直接讀寫 SQLite。CLI 的全量 `memory_list/get` 維持原契約，Web 另使用 `memory_page` 與 `memory_detail_bounded`。SQLite 的 `(workspace,created_at,id)` 索引支援 `(created_at,id)` keyset；SQL 以 bytes sentinel 限制欄位，單頁最多 50 筆，摘要 512 bytes、detail 64 KiB。新記憶 page/detail 的 IPC 及 HTTP JSON 回應均於編碼前檢查 512 KiB 上限。Daemon 保留同步 IPC JSON 解碼，另以 Unix socket `POLLHUP`／`POLLERR` 監看完整連線中斷；在 `RawConn.Control` callback 內複製 fd 供 watcher 使用。只有 page/detail 查詢使用該連線 context，並套用獨立 9 秒期限，比 Web IPC 10 秒等待上限早一秒。客戶端只關閉寫入方向時仍可收到回覆。SQLite 使用 `QueryContext`；現有 `busy_timeout=10000` 下，SQLite 鎖等待可能延後已取消查詢的返回，該限制未由此變更調整。

最後驗證日期：2026-09-24。

Web Task submit 僅接受 ID、workspace、prompt、explicit `memory_ids` 與 `memory_recall`，由 Daemon 依原指紋、記憶解析、bounded selector 與密封流程處理；預設 off。介面送出前顯示 Request ID，回應遺失時以同一 ID 和 payload 重送。專案或 session 切換會清除記憶內容、選取與待重送資料，並以世代值丟棄遲到的 UI 回應。歷史稽核只依 Task 保存的 `resolved_context` 與 `pipeline_trace` 呈現簡要決策，不重查現存記憶；首版沿用既有 Task status IPC，因此該舊路徑的完整 context/result 大小限制仍須另案處理。詳細驗收與證據見 [Issue 21](../../issues/issue-21/README.md)。


## Phase 3J：Web Git Memory Capture & Review

2026-09-30：`/api/git-memory/capture`、`/api/git-memory/candidates` 與 detail/approve/discard 共用 session、Host、變更方法 Origin 邊界。Web 僅透過五個 bounded Git IPC 呼叫 Daemon；Daemon 每次重新驗證 canonical workspace，並重用既有 capture service、candidate store、checkpoint 與 approve/discard 原子狀態轉移。Web 不直接執行 Git 或存取 SQLite。

2026-09-30：Candidate page 採 created_at DESC/id ASC keyset，cursor 綁定 scope/status，SQL LIMIT limit+1，單頁最多 50。SQL byte sentinel 投影限制 metadata、draft/facts；capture 重用只讀 metadata，不載入 legacy 大草稿。Detail draft 最多 32 KiB、facts 最多 256 KiB、escaped JSON 最多 512 KiB；Web IPC reader 另有 1 MiB 上限。Web approve 在交易內驗證 pending/Complete 與有效內容大小，重送保留原批准 ID，即使正式 memory 已刪除也不復活。CLI 既有完整查詢契約保持不變。

2026-09-30：HTTP context/deadline 關閉 request 專用 Unix socket，Daemon connCtx 傳入 capture，沿用 Git process group kill；尚未提交的交易取消不推進 checkpoint，已提交交易不倒退。UI 以 workspace/session generation、list/detail token 與 AbortController 隔離遲到回應，全部 Git/memory 字串僅以純文字呈現。既有 Task snapshot 與 lexical-v1 recall 不改寫。詳細驗收與證據見 [Issue 27](../../issues/issue-27/README.md)。


## Phase 3K：Memory Utility Feedback Loop Foundation

最後驗證日期：2026-10-01。

Daemon／storage 擁有 recall event、assessment、aggregate 與 policy；backend 僅於既有 Critic 呼叫提供受限 estimated usage。Task admission 交易原子保存 sealed context 與 metadata-only ledger，Request ID 去重先於 recall；不保存未選候選全文。explicit identity 保存在 selections，event audit 不重複複製完整 explicit ID 清單。

observe 預設完全沿用 lexical-v1 排序；apply 以 used+unused>=5 才使用 [0.5,1] multiplier，採整數 millionths 保存實際 weight/final score，final 同分 ID 升序。unknown 不進分母、explicit 不降權、歷史 Task 不重算。250ms recall context 同時涵蓋有界候選讀取、至多 256 個 aggregate lookup 與 selector。

Task/event admission 或 apply 權重讀取錯誤 fail-closed；執行後 UFL persistence 不改主結果。assessment 與 cache 聚合共用交易／唯一鍵；recovery 僅處理 terminal Task，每批至多 100 events，不重跑模型。重建在交易高水位內 keyset 讀取每批 100 observations，原子替換 aggregate。immutable audit 無 memory cascade，不因刪除正式 memory 失去歷史 identity。

CLI/IPC utility audit 每次經 canonical authorization，keyset cursor 綁定 workspace+memory ID。SQL bounded projection、單頁 100、cursor 512 bytes、server/client JSON 64 KiB 邊界；large/invalid legacy metadata 明確失敗。legacy Task 不補建事件。無 GC、事件保留使 DB 持續增長；重建的大交易仍會暫占單一連線，並非無成本操作。細節與可信度／容量證據見 [Issue 29](../../issues/issue-29/README.md)。


## Phase 3L：Memory Lifecycle & Supersession Foundation

最後驗證日期：2026-10-01。

Daemon 擁有 lifecycle transition，沿用 exact canonical authorization。memories 加 active/superseded、is_durable 與 lifecycle_changed_at；legacy 預設 active/non-durable，ID/content 不變。migration 在 BEGIN IMMEDIATE 寫鎖下探測欄位、加 schema 並回填時間，同交易保存 marker，可安全重開。

memory_relations 對 scoped predecessor 與 successor 各有唯一鍵；只允許新 memory 取代 active predecessor。manual operation ledger 保存 scoped operation ID 與 payload fingerprint，Git approval 以 candidate identity 冪等。memory、relation、predecessor CAS、operation ledger，以及 Git candidate/provenance 共用 transaction；失敗全回滾，不建立 DAG 或復活 API。

Recall SQL 使用 (workspace,lifecycle,id) 索引，在 bounded LIMIT 前排除 superseded；explicit recall 仍可使用未刪舊版本。新 sealed snapshot／recall audit 保存 lifecycle metadata；legacy JSON 在讀取時解讀為 active/non-durable，不改 persisted Task。UFL 以新 memory ID 隔離，durable 不加分。硬刪保留不含內容的 relation/operation identity；deleted chain 節點可由同 scope bounded detail 逐節稽核。

CLI/IPC 提供 add options、durable、detail；detail 不遞迴展開全鏈、不展開 provenance facts_summary。content 64 KiB、總 JSON 128 KiB、deadline 2 秒；超限拒絕。Web 不新增 lifecycle UI，Git service options 可安全指定 supersedes，Partial 仍拒絕。ledger 無自動 GC，資料總量持續增長；10k/100k 本地 probe 與驗收證據見 [Issue 31](../../issues/issue-31/README.md)。

## Runtime capability admission（2026-10-03）

Asura 作為 Agent Runtime Control Plane，只管理 runtime 執行契約；skills、MCP 與 workflow 由 runtime 內部處理。internal/backend 的 capability-v1 型別與 matcher 純函式分離需求與供給；pipeline 組合既有 registry lookup 及有期限的 version descriptor 探測。engine、daemon submit 與 Cron 共用接受前 preflight。

Direct 為 Striker workspace-write；Three-Face 的 Oracle/Critic 為 read-only，同一 explicit backend 必須滿足全部 stage union。Unknown mode／role／workspace access／capability／contract-policy version fail-closed；不新增 routing、fallback 或 mixed runtime。執行開始時綁定同一 Backend instance，stage 間 registry 改變不切換 runtime。

接受時 ExecutionContext.execution_contract 原子寫入既有 Task.resolved_context JSON，無記憶與 Cron 也保存，不需 schema migration。request fingerprint 保持原 normalized backend identity，不包含版本或能力。redelivery 先回傳已存在且授權有效的 Task；並行首次請求由 SQLite 唯一 request ID 決定勝者快照。worker 優先使用 persisted context，不能以呼叫端 context 覆寫。

PipelineTrace 的 execution_contract 複用接受時值，execution_guard／guard_error 則記錄執行前目前能力的安全查核；stage backend version 為執行資訊。能力退化／runtime 移除拒絕而不覆寫接受時判定。缺舊 snapshot 查詢 legacy/unavailable，零 runtime 探測與零歷史回填。這個契約不代表 runtime 認證、health 或實際二進位檔不可變。

本機 run 的 opencode/codex 仍是既有 shell runner，不將它偽稱為實際 runtime 執行；其 runtime snapshot 相容性揭露由 issue 33 的 RC-TBD-11 定案。真正 local runtime pipeline 使用共同 admission。

最後驗證日期：2026-10-03；相關測試與未決範圍見 docs/issues/issue-33。
