# 需求與現況分析

2026-09-25。來源：[GitHub #23](https://github.com/raybird/Asura/issues/23)。本檔記錄立項與現況調查結果；待核准之外部行為以 [README](README.md#gherkin-驗收劇本) 為準。

## 需求描述

為 Asura 提供**顯式觸發、有界唯讀、隔離且需人工審核**的 Git 變更感知與專案記憶擷取能力（Phase 3H）。
讓 CLI 使用者能針對授權 workspace 的 Git repository，提取固定範圍（commits／diffs）的架構與決策事實，產生待審候選（candidates），經使用者檢視／編修／核准後，以同 scope 的正式專案記憶寫入既有 SQLite，供後續任務召回使用。

明確不包含：
- 背景定時或自動掃描本機 repositories、安裝或修改 Git hooks、攔截 commit/merge 自動寫入、GitHub Webhook 或雲端同步。
- 自動核准、改寫、合併或刪除既有正式記憶；UFL／重要度評分／supersedes／embedding／向量搜尋；跨專案共享記憶。
- 在使用者原 repo 自動建立「記憶 commit」、改寫 Git 歷史或自動推送遠端；不改 Phase 3F lexical-v1 選擇器與既有自動召回額度。
- 自動重跑結果未知的 AI Task、Backend failover、角色權限放寬、無明確證據的 Context 壓縮。

## 現況分析

1. **工作區授權與 Canonical 鍵值 (`internal/auth/policy.go`)**：
   - 目前 `Policy` 僅維護 `allowedRoots` 清單，透過 `AuthorizeCanonicalWorkspace` 同時完成路徑存在性、祖先路徑解析（符號連結求值）、尾斜線清理與範圍比對。
   - 既有系統完全沒有檢驗工作區是否為 Git repository，亦無儲存或校驗 Git root、`.git` 目錄或 repository identity 的機制。若使用者傳入符號連結、子目錄或非 Git 工作區，可能引發路徑混淆或 Git 越界操作。

2. **守護進程 IPC 與記憶分發 (`internal/daemon/daemon.go`)**：
   - Daemon 提供 `memory_add`、`memory_list`、`memory_get`、`memory_remove`、`memory_page`、`memory_detail_bounded` 等 IPC 動作。
   - 每次記憶操作均嚴格呼叫 `AuthorizeCanonicalWorkspace` 確保 scope 精確性。
   - 目前 Daemon 完全不具備 Git 操作能力，沒有執行外部 Git 子程序的邊界與逾時機制，亦無候選草稿（candidates）與正式記憶（approved memories）的生命週期區分。

3. **記憶儲存與資料庫結構 (`internal/storage/sqlite.go`, `sqlite_memory.go`)**：
   - 記憶表 `memories` 結構固定為五欄位：`id` (TEXT PK), `workspace` (TEXT), `content` (TEXT), `source` (TEXT DEFAULT 'manual'), `created_at` (DATETIME)。
   - 索引包括 `(workspace)`、`(workspace, id)`、`(workspace, created_at, id)`。
   - 目前所有寫入的 `source` 均為 `manual`。系統尚無儲存來源 Git commit SHA、ref、diff 統計、審核狀態或 checkpoint 的專用欄位或獨立資料表。
   - 既有 `ListMemories`、`PageMemories` 與 `ResolveMemoriesForTask` 均依賴 `workspace` 欄位進行精確比對。

4. **CLI 記憶管理入口 (`cmd/asura/memory.go`)**：
   - 提供 `add`, `list`, `get`, `remove` 四個子命令，均透過 Unix Domain Socket 與 Daemon 通訊。
   - 尚無針對 Git 變更提取（capture）、候選預覽（candidates list/show）、候選審核（approve/discard/edit）的命令介面。

5. **任務密封與自動召回 (`internal/recall/recall.go`, `internal/pipeline/`)**：
   - Phase 3F 實作的 `lexical-v1` 召回引擎，直接從 `memories` 表讀取同 workspace 的候選清單（上限 256 筆），進行 BM25 式評分後選出最多 4 筆、8192 bytes、2048 tokens 注入任務。
   - 若未經審核的 Git 候選直接寫入 `memories`，將立即被自動召回引擎納入評分，破壞「草稿不進入 prompt」、「人工審核方可生效」的關鍵安全邊界。

## 問題點總結

1. **Git 信任邊界與安全性**：Git 差異資料、commit message、檔案路徑等可能包含惡意輸入、巨大檔案、二進位垃圾、敏感秘密（secrets）或符號連結逃逸，直接讀取或未經清洗可能造成 DoS、路徑穿越或資訊洩漏。
2. **原 Repo 唯讀保護**：任何操作若意外觸碰 `.git`、修改工作區檔案、建立 commit 或暫存檔，都會破壞使用者開發環境的一致性與信任。
3. **草稿與正式記憶混淆**：若無獨立候選暫存層，未核准的變更摘要可能污染專案記憶庫並直接被 Task 召回。
4. **增量狀態與歷史混淆**：多分支開發時，若 checkpoint 僅紀錄單一 SHA 而未關聯 ref 或未處理分支切換/force-push，會誤把不同歷史拼成一條線性增量，導致記憶摘要遺漏或錯誤。
5. **故障恢復與交易保證**：若擷取、草稿儲存、checkpoint 更新或核准過程中崩潰，先查後寫的競態或部分寫入可能造成重複記憶或 checkpoint 遺失。

## 目標與邊界

- **邊界 1（唯讀與身分）**：純 CLI 顯式觸發，在 Daemon 嚴格授權的 canonical workspace 內執行唯讀 Git 指令，驗證 Git root 與固定 SHA，絕不修改原 repo。
- **邊界 2（有界防護）**：設定 commits 上限、diff bytes 上限、檔案數上限、超時取消，略過二進位與 submodule，截斷揭露真實性。
- **邊界 3（候選隔離與人工審核）**：以獨立候選暫存（SQLite 候選表）隔離未核准資料；CLI 提供檢視／編修／核准流程，經審核後才寫入 `memories`（標註來源為 git-approved）。
- **邊界 4（一致性與冪等）**：重複執行不重複寫入候選或正式記憶；checkpoint 與審核具備交易保護與故障恢復能力。
- **邊界 5（零破壞性）**：完全相容現有五欄位 API、Web Console 與 Phase 3F lexical-v1 召回機制。
