# Asura（阿修羅）系統架構與設計規範 (Architecture Design Spec)

- **文件建立日期**：2026-09-11
- **專案狀態**：架構規劃完成，進入實作準備階段
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
- **Arm 1 (Telegram Arm)**：Long-Polling 連線、MarkdownV2 AST 渲染、置頂釘選狀態訊息（Pinned Status）、分段防破損傳輸。
- **Arm 2 (Web Console Arm)**：輕量嵌入式 Web 介面、SSE 打字機即時串流、會話歷史導覽。
- **Arm 3 (Scheduler Arm)**：Cron 排程時鐘，支援定時觸發與閒置靜默反思（Silence Reflection）。
- **Arm 4 (Terminal CLI Arm)**：本地命令列直接互動，支援標準輸入輸出管線。
- **Arm 5 (Memory & Knowledge Arm)**：本地記憶庫、Git 唯讀增量掃描、`BRIEF.md` 靜態導覽編譯。
- **Arm 6 (Agent Backend Pool Arm)**：CLI 進程生命週期管理、429 退避降頻、進程組強殺回收。

---

## 4. 全域 Worker Pool 與並發調度 (Global Worker Pool)

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
- `sessions`：會話資料表（包含 scope 隔離，如 `project:Asura`）。
- `memories`：結構化記憶本體（category, title, content, rationale, search_text, importance, recall_count）。
- `memories_fts`：FTS5 外部內容虛擬表。
- `memory_attributes`：長期屬性（is_durable, superseded_by, sensitivity）。
- `recall_telemetry`：UFL 效用回報與遙測（query, hit_memory_ids, utility_score, used）。
- `scheduled_jobs`：排程任務表（cron_expr, prompt, scope, channel, is_active）。

---

## 6. Context Window 五大工程防禦體系 (Context Defense)

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
- **Opencode Adapter（主力）**：驅動 `opencode run --format json`，支援 stdout JSON 事件流解析、即時 Emoji 與自動帶入 `--dangerously-skip-permissions`。
- **Codex Adapter**：封裝 Codex CLI 之 stdin/stdout 通訊。
- **Agy Adapter**：原生適配本地 Antigravity CLI (`agy`)，提供無人值守指令封裝。

### 7.2 異常防禦與故障轉移
1. **429 速率限制 1.1 秒快速失敗 (Fail-Fast)**：監聽 stderr 命中 `"statusCode":429` 立即取消子進程，禁止長時間指數退避卡死 Worker。
2. **410 模型下架精確攔截**：深層分析 JSON `error` 結構，消滅 Exit code 0 的「假成功」。
3. **進程組 (PGID) 樹狀強殺**：`Setpgid: true` 啟動，取消時以 `syscall.Kill(-pgid, syscall.SIGKILL)` 徹底消滅所有衍生子行程與殭屍進程。
4. **逃逸守護進程主動清理**：任務收尾主動回收 `agent-browser close --all`。
5. **模型故障轉移池 (Model Failover Pool)**：主模型遭遇 429 或 410 時自動無縫切換備援清單。

---

*文件版本：v1.0.0*  
*建立日期：2026-09-11*
