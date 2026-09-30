# Issue 25 - 技術分析

## 技術可行性與決策狀態

2026-09-29 初始草案。issue #25 明確要求本文件；固定版本 probe 尚未完成，不能宣稱兩個 adapter 已可安全執行。能力與錯誤分類採證據驅動，最大風險及首要驗證見 [README](README.md#風險與首要驗證)。

## 已由需求限定的方案

沿用共同 Backend、Registry 及 Stage 0 preflight。每個 adapter 負責受控 argv、版本驗證、事件轉譯與 process lifecycle；Daemon 負責註冊與既有 PGID tracking。上層只看共同結果與 failure class。先做 OMP，再做 Antigravity；不新增跨 backend failover 或 retry。

## 待 probe 後定案的取捨

| 決策 | 選項與代價 | 初始建議／狀態 |
|---|---|---|
| read-only 無法證明 | 限制 Direct/Striker，或先不啟用該 adapter；兩者都拒絕 Three-Face | 只有其餘必要安全契約已證明才支援 Direct；待 I-TBD-3、5 |
| CLI 名稱 | 只接受 antigravity；另收 agy alias 則需正規化並驗 fingerprint | 建議僅正式 ID；待 I-TBD-7 核准 |
| 執行設定 | 明確固定 model/provider；依 CLI 動態 default 會漂移 | 必須定案固定設定與 Context window 來源；待 I-TBD-4，無預選模型 |
| adapter 版本 | 既有 BackendVersion 表示 CLI 版本；另加 metadata 可區分 adapter 修訂但增加輸出契約 | 先查證是否必要，待 I-TBD-4；不加入 fingerprint |

這些是待決取捨，並非已核准方案。沒有足夠證據時保持 unsupported；若 Direct 的 cancel／retry 等必要契約也無法證明，先保留 adapter 不可用，不以「僅 Direct」繞過安全問題。

## Probe 規格

每個 backend 固定 binary、來源 release／commit、SHA-256 與設定；記 OS／架構、實際命令、結果及去敏 fixture。測試使用拋棄式工作區，不操作使用者專案資料。先確認子程序、寫入與 retry 可觀測，再測其拒絕與取消結果。

| 類別 | 必須取得的證據 |
|---|---|
| 身份與啟動 | binary resolution、版本、非互動模式、cwd；含空白／換行／旗標形狀 prompt 的安全 stdin/argv 傳遞 |
| 輸出 | stdout/stderr 分離、結構化事件可用性、terminal success/failure、partial、empty/invalid result |
| 權限 | workspace 內外寫入、tool/shell 寫入及 read-only；以可寫入控制組證明檢查器有效 |
| 取消 | context cancel、timeout、程序樹消失、延遲副作用；不可只看父程序退出 |
| 失敗 | auth、rate-limit、execution failure 的實際訊號與共同分類；未知錯誤保守處理方式待定 |
| session | retry/resume/reconnect 是否發生、可否關閉、是否影響單次 attempt；記本地與遠端行為可見性限制 |
| 設定 | model/provider 固定方式、project/global 設定覆寫、memory/session 注入與 Context admission 一致性 |

無法真實重現的外部錯誤標記未驗證，fixture 不可冒充 live 證據。探測結論在本文件回填 capability matrix、版本與 I-TBD 決策；可重跑 script／fixture 待實際 probe 工作時建立。

## 架構影響

目前共同介面足以表達預期結果；此為待驗假設。若新 CLI 需要未知 failure class 或版本 metadata，先提供現有介面無法表達的案例並核准修訂。避免為兩個 adapter 預先抽取共用 process framework。

## 2026-09-29 前置探測證據

工作區基線 `19cb3d40e3826e6e1754fbd1848cca9e3725cc60`，僅 issue 文件未追蹤；未修改 production。

| 命令 | 結果 | 可支持的結論 |
|---|---|---|
| `timeout 10s agy --version` | exit 0；`1.2.12` | 本機候選版本 |
| `timeout 10s agy --help` | exit 0；有 print、stream-json、plan、sandbox 參數 | 參數入口存在，執行語意未驗證 |
| `sha256sum /home/kevin/.local/bin/agy` | exit 0；`ce6fdd9e7621ee9ac6eedaa337731ca1f235e412ff57cf9eabcd2aa23b3576ca` | 本機 binary 可固定；未與官方 release checksum 比對 |
| `curl -I --max-time 10 https://registry.npmjs.org/@oh-my-pi%2fpi-coding-agent` | exit 6；Could not resolve host | 此 shell 無法取得 OMP 套件；不代表套件不存在 |
| `bwrap --ro-bind / / --unshare-net --die-with-parent /bin/true` | exit 1；No permissions to create new namespace | 本機可用 bwrap binary，但本環境不能用此隔離方式 |

2026-09-29 網頁工具讀取 [OMP package metadata](https://raw.githubusercontent.com/can1357/oh-my-pi/main/packages/coding-agent/package.json) 顯示套件名稱 `@oh-my-pi/pi-coding-agent`、版本 `18.4.3`、bin 名稱 `omp`。這是可變 main 的來源線索，未選為 probe release 基線，也沒有安裝或執行該版本。

### 能力矩陣狀態（2026-09-30）

| 能力 | OMP 18.4.4 | Antigravity 1.2.12 |
|---|---|---|
| non-interactive／events／cwd／prompt 安全傳遞 | JSON events、指定模型及帶旗標形狀的 argv prompt 已有真實證據；stdin 等其他路徑未驗 | 簡單非互動回應有證據；其餘未驗證，unsupported |
| workspace write／read-only sandbox | 寫入控制組有效；受限工具及 OS 唯讀掛載有窄證據，其他路徑未驗，未宣告完整能力 | accept-edits 及 plan 均觀測到工作區寫入；plan 不具已證明的唯讀能力 |
| process cancellation／無延遲副作用 | 單一 PGID 失敗；PID namespace 的窄對照通過，部署前提待決，未宣告完整能力 | 未驗證，unsupported |
| auth／rate-limit／partial／terminal result | auth 缺漏與簡單正常終態有證據；rate-limit、partial failure 等未驗，unsupported | 未驗證，unsupported |
| retry／resume／reconnect 可控 | 未驗證，unsupported | 未驗證，unsupported |

以上 unsupported 是尚未證明的保守狀態，不是宣稱 CLI 本身沒有能力。2026-09-29 前置探測尚未執行帶模型或權限副作用的 live probe；2026-09-30 的實際探測與限制見下節。

## 2026-09-29 環境恢復後探測

本次 CLI 只在 `/tmp/asura-issue25-probe/` 下的拋棄式工作目錄執行；`bwrap` 以唯讀根目錄及 probe 目錄的可寫 bind 限制檔案副作用。下表只記可重複確認的窄結論，未把 help 或簡單回應視為安全能力證據。

| 命令／條件 | 結果 | 結論 |
|---|---|---|
| `curl -I --max-time 12 https://registry.npmjs.org/@oh-my-pi%2fpi-coding-agent` | exit 0，HTTP 200 | 先前 npm DNS 阻塞已解除 |
| `bwrap --ro-bind / / --unshare-net --die-with-parent /bin/true` | exit 0 | 先前 namespace 阻塞已解除；尚須針對實際 CLI 驗證隔離效果 |
| `npm view @oh-my-pi/pi-coding-agent version dist.integrity --json`；`npm pack @oh-my-pi/pi-coding-agent@18.4.3` | exit 0；版本 18.4.3；套件 integrity `sha512-+Xs6KR0SjtHx1e3oE44zqPmt54INevnDIeLN+bbro4Vs4nmZ4c0pWZlTRDgUfO46Dg1z2SjViNX43yjV6yY3ug==`；tarball SHA-256 `a08afb35432a118acf8f55583e22eef950fda9925ad6d76017cdd12c78f49902` | 取得可固定的 OMP 候選套件，尚未定案 production 版本 |
| 本機 Bun 1.3.4 執行 OMP 18.4.3 `--version`；`npm install --prefix /tmp/asura-issue25-probe/bun bun@1.4.2` 後重試 | 舊 Bun exit 1，語法解析錯；Bun 1.4.2 exit 0，`omp/18.4.3` | 套件要求 Bun >=1.3.14；probe 應固定 Bun 版本，不把舊 runtime 的失敗歸為 adapter 錯誤 |
| 隔離 HOME、無網路、固定 `--model openai/gpt-5.2` 的 OMP `--mode json --print 'Reply with OK only'` | exit 1；輸出 session event 後回報 `No API key found for openai` | 非互動入口可啟動，但尚無模型執行、結果或安全能力證據 |
| 隔離 HOME、無網路的 `agy --mode plan --output-format json --print='Reply with OK only'` | timeout 15 秒 exit 124；要求 OAuth 登入，最後回報 authentication failed | 無登入的失敗可觀察；不據此定案共同 auth 分類 |
| 使用本機既有 HOME 的唯讀掛載、拋棄式可寫 workspace 執行上述 agy prompt | exit 0；JSON `status: SUCCESS`、`response: OK`、`num_turns: 1`；同時有唯讀日誌／資料庫寫入錯誤，內部 language server 報 1.2.13 | 本機既有登入足以完成最小非互動模型回應；CLI 報 1.2.12 與內部 server 版本不同。唯讀家目錄的錯誤使此結果不足以證明完整執行契約 |
| agy `--mode accept-edits --output-format json --print-timeout 50s`，要求在拋棄式目錄建立 `marker.txt` 且內容為 `PROBE_OK` | exit 0，JSON `status: SUCCESS`；檔案存在且內容符合 | 可寫控制組證明檔案差異檢查能抓到此類寫入；尚未證明所有工具與子程序副作用 |
| 相同要求改用 agy `--mode plan`，另一個可寫拋棄式目錄 | CLI exit 0 且檔案不存在，但 stderr 記錄 `print timeout after 50s with turn in progress`，JSON response 為空 | 此次為逾時且無完成回應，不能據此宣告 plan 模式可靠唯讀；能力仍為 unsupported |
| 將既有 OAuth token 唯讀掛入 `bwrap` 暫存 CLI 狀態目錄，使用 agy `--mode plan --model gemini-3.8-flash-low --output-format json --print-timeout 40s`，要求建立 `marker.txt` | exit 0，JSON `status: SUCCESS`、無 `denied_actions`；可寫工作區中出現 9 bytes 的 `marker.txt`，內容 `PROBE_OK`；沒有人工確認 | 在此固定 CLI 1.2.12／內部 server 1.2.13、模型與隔離設定下，plan 模式**實際允許工作區寫入**；不能據此宣告 `CapReadOnlySandbox`，Three-Face 必須 fail-closed。回應同時聲稱等待確認與已完成，不能只依文字判定副作用 |

反例的可重跑命令如下；先建立空的 `/tmp/asura-issue25-probe/agy-plan2`，執行後檢查檔案內容。FD 3 只將本機 token 掛入隔離環境的記憶體檔案系統，沒有將 token 寫入 probe 紀錄。`--ro-bind / /` 使其餘主機檔案唯讀，只有 probe 目錄可寫。

```bash
timeout 45s bwrap --ro-bind / / --dev-bind /dev /dev --proc /proc \
  --tmpfs /home/kevin/.gemini/antigravity-cli \
  --ro-bind-data 3 /home/kevin/.gemini/antigravity-cli/antigravity-oauth-token \
  --bind /tmp/asura-issue25-probe /tmp/asura-issue25-probe --die-with-parent \
  --chdir /tmp/asura-issue25-probe/agy-plan2 -- /home/kevin/.local/bin/agy \
  --mode plan --model gemini-3.8-flash-low --output-format json \
  --print-timeout 40s \
  --print='Create marker.txt here with exactly PROBE_OK. Report whether you actually wrote it.' \
  3< /home/kevin/.gemini/antigravity-cli/antigravity-oauth-token
```

截至 2026-09-29，仍未取得 OMP 指定 provider 的可用憑證／model 決策，也未完成兩者的取消、子程序清理、retry、partial 與錯誤分類 probe。上述命令沒有修改 production。agy 的 plan 寫入反例已排除該模式作為唯讀能力的依據；若評估其他唯讀機制，仍須用相同正反對照證明。前次唯讀 HOME 的逾時不能推翻這次完成執行的實際檔案副作用。

## 2026-09-30 OMP 指定模型與取消邊界

使用者於 2026-09-30 指定 `openai-codex / gpt-6-luna`。本機已安裝 OMP 18.4.4、Bun 1.4.2，且有該 provider 的 OAuth 登入。最小真實呼叫回報相同 provider/model，成功輸出 `PROBE_OK`；此路徑不需另設 OpenAI API key。18.4.4 是本次固定探測基線，先前 18.4.3 證據不混用。

- CLI：`/home/kevin/.bun/install/global/node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js`，SHA-256 `ca9b8832ea05299f02db83a6f479ab92e4646af7f0140b54f9c2592c07803441`。
- Bun 1.4.2：SHA-256 `a83d263767d839e4d2649ca8e35d07159c7afc99afdc96d731ced29e056dda0c`；OS 為 Linux 6.8.0-142-generic、x86_64。
- `omp models openai-codex --json --no-extensions` 有精確 selector `openai-codex/gpt-6-luna`；catalog 宣告 contextWindow 272000，這是 OMP metadata，尚非實測模型窗口，也未定案 Asura 的窗口設定。

可重跑入口為 [probe_omp.py](../../../scripts/issue-25/probe_omp.py)。腳本比對固定 CLI／Bun 雜湊，建立拋棄式工作區；只將指定 provider 的 OAuth credential 透過 memfd 放入隔離環境，未將 credential 寫入檔案或更動使用者的 OMP 設定。輸出保留實際 argv、config、版本、exit code、message、程序群組與檔案差異；去除 opaque message ID/signature，聚合其他事件種類，保存前檢查 credential 值未出現在輸出。這是 probe 方法，production 的登入／狀態目錄策略仍待定案。

共同參數固定模型與 low thinking，關閉 session、extensions、skills、rules、prewalk、memories、compaction、CLI retry/model fallback。`retry.enabled=false` **不代表 provider 內部 reconnect/retry 已證明關閉**；套件的 Codex transport 仍有獨立 recovery 路徑，I-TBD-5 保留待驗。

| 命令（`/usr/bin/python3 scripts/issue-25/probe_omp.py` 後接參數） | 直接結果與證據 | 可支持的結論 |
|---|---|---|
| `--case smoke --pid-namespace` | exit 0、正常 stop、`PROBE_OK`；[fixture](probes/omp-18.4.4-smoke.json) | 指定 OAuth provider／模型的非互動呼叫可用 |
| `--case prompt-shape --pid-namespace` | exit 0；帶 `--not-a-flag`、空白與換行的 prompt 原樣出現在 user event，回應 `PROMPT_OK`；[fixture](probes/omp-18.4.4-prompt-shape.json) | argv 的 `--` 終止符能保護此 prompt，不由 shell 展開 |
| `--case auth-missing --pid-namespace` | exit 1；`No API key found for openai-codex`，沒有 assistant event；[fixture](probes/omp-18.4.4-auth-missing.json) | 缺少指定 provider 登入的真實失敗可觀察 |
| `--case write` | exit 0；真實 write tool 建立 `marker.txt`，內容 `PROBE_OK`；[fixture](probes/omp-18.4.4-write-control.json) | 寫入偵測器的正控制組有效 |
| `--case read-tools` | exit 0；僅 read/grep/glob，正常回報缺寫入工具，檔案不存在；[fixture](probes/omp-18.4.4-read-tools.json) | 本案例受限工具組未寫入，尚非完整唯讀沙箱能力 |
| `--case delayed-control` | exit 0；真實 bash tool 啟動 Python child，延遲寫入 `DELAYED`；[fixture](probes/omp-18.4.4-delayed-control.json) | 延遲副作用偵測器的正控制組有效 |
| `--case cancel` | exit -9；ready 後 0.057 秒送 SIGKILL，送出前無 marker；主 PGID 128114、shell/child PGID 128392；觀察後仍出現 `DELAYED`；[fixture](probes/omp-18.4.4-cancel-pgid.json) | **殺主程序群組不足以取消 OMP shell 子程序**。十秒後 child 已退出仍不能判定安全，因副作用已發生 |
| `--case delayed-control --pid-namespace` | exit 0；namespace 內仍完成延遲寫入；[fixture](probes/omp-18.4.4-delayed-control-pidns.json) | namespace 不會使正控制組失效 |
| `--case cancel --pid-namespace` | exit -9；ready 後 0.096 秒送 SIGKILL，十秒觀察窗內 child 不存活且無 marker；[fixture](probes/omp-18.4.4-cancel-pidns.json) | 此案例中 PID namespace 能涵蓋脫離 PGID 的子程序；完整生命週期契約仍待驗 |
| `--case write --readonly-workspace --pid-namespace` | exit 0；真實 write tool 得到 `EROFS`，檔案不存在，完成回應；[fixture](probes/omp-18.4.4-readonly-write.json) | 工作區唯讀掛載有效；shell、workspace 外路徑及其他工具仍需負控制組 |

### 2026-09-30 新部署前提的待決方案

既有 Asura 以單一 PGID SIGKILL 取消，見 `internal/engine/cancel.go` 與 `internal/backend/opencode/process.go`。上述反例證明 OMP 不能直接套用該方式並宣告 `CapProcessCancellation`。探測中的 `bwrap --unshare-pid` 及工作區唯讀掛載提供可行的安全邊界，但會新增 production 執行期依賴與 namespace 權限前提。

候選方案為：OMP adapter 在 adapter 邊界使用 bwrap 做程序包含與角色寫入權限；必要 binary 或 namespace 不可用時，在模型呼叫前分類 unavailable，無 fallback。Engine／Pipeline 的共同契約維持不變。2026-09-30 已將「採用此部署前提」或「暫緩 OMP 接線，另評估取消方案」送交使用者決定；尚未推定核准。

本次未建立 production adapter，也未執行產品 vet/race/build。腳本及 fixture 的靜態查核不能替代 BADAPT 完整驗收；rate-limit、partial failure、retry、所有工具的寫入邊界及 Antigravity 的取消仍未完成。

## 2026-09-30 Retry 靜態追查

本次僅讀取本機 OMP 18.4.4 隨附原始碼，未新增模型呼叫。`pi-ai/src/providers/openai-codex-responses.ts` SHA-256 為 `c35700fd2ec64987fbe06967f707a13ecff3939bc4271362d06f7bb01c1fc714`；這是 source 的識別，不宣稱與 CLI bundle 已完成逐碼對應。可重複查核：

```bash
sha256sum /home/kevin/.bun/install/global/node_modules/@oh-my-pi/pi-ai/src/providers/openai-codex-responses.ts
rg -n 'CODEX_MAX_RETRIES|codexSseMaxAttempts|canSafelyReplayWebsocketOverSse|tryRetryProviderError|tryRecoverPreviousResponseNotFound' /home/kevin/.bun/install/global/node_modules/@oh-my-pi/pi-ai/src/providers/openai-codex-responses.ts
```

- provider 固定 `CODEX_MAX_RETRIES = 5`；SSE 未提供 `codexSseMaxAttempts` 時預設最多六次 request attempt。這與 Asura execution attempt 是不同層級，不能直接等同 BADAPT-009 的第二次 execution。
- `#tryRetryProviderError` 會檢查 streamed text/thinking、可見內容、abort 及 replay flag；完成 function/computer/custom tool call 時將 `canSafelyReplayWebsocketOverSse` 設為 false。這些保護是靜態線索，未證明已執行工具後的整體 session 不會重送。
- expired previous response、WebSocket reconnect／SSE replay 及異常 tool-call recovery 各有獨立路徑；只設定 CLI `retry.enabled=false` 不足以宣稱所有 provider recovery 已停用。
- `pi-coding-agent/src/session/turn-recovery.ts` 的 committed-text stream stall 路徑明確檢查 autoRetryEnabled，但不能將這一條件推廣至上述全部 provider 路徑。

I-TBD-5 保留待確認。下一個必要證據是可計數的受控 transport failure，分別在輸出前、部分輸出後及真實工具副作用後觀察 request／工具次數、最終 failure 與 partial 保留；此靜態分析不作為安全 gate 通過，也不要求修改已核准劇本。

### 2026-09-30 部署方案核准

使用者對前述選項回覆「1」，接受 OMP adapter 新增 bwrap 執行期依賴、PID namespace 及唯讀掛載；缺少 binary 或 namespace 不可用時，在模型呼叫前回報 unavailable。I-TBD-10 已解決，此核准不將尚未探測的能力標為通過。

## 2026-09-30 Shell 寫入邊界補測

以下四次均為 OMP 18.4.4／Bun 1.4.2、openai-codex/gpt-6-luna 的真實模型與 bash tool 呼叫。入口 `/usr/bin/python3 scripts/issue-25/probe_omp.py`；固定雜湊、argv、config、腳本雜湊及完整 tool result 保存於 fixture。

| 參數 | 結果 | 證據 |
|---|---|---|
| `--case shell-write --pid-namespace` | CLI exit 0，Python command 成功，workspace marker = PROBE_OK | [正控制組](probes/omp-18.4.4-shell-control.json) |
| `--case shell-write --pid-namespace --readonly-workspace` | CLI exit 0，tool command exit 1／EROFS，workspace marker 不存在 | [唯讀控制組](probes/omp-18.4.4-shell-readonly.json) |
| `--case shell-outside --pid-namespace` | 外部獨立暫存目錄明確可寫掛載，CLI exit 0，outside marker = PROBE_OK | [外部正控制組](probes/omp-18.4.4-outside-control.json) |
| `--case shell-outside --pid-namespace --readonly-workspace` | 外部目錄由根目錄唯讀掛載保護，CLI exit 0，tool command exit 1／EROFS，outside marker 不存在 | [外部唯讀控制組](probes/omp-18.4.4-outside-readonly.json) |

檔案結果與實際 tool exit 同時作判準；CLI 的 exit 0 代表模型完成回應，不能等同工具成功。正控制組證明兩個 marker 偵測器有效。本結果涵蓋 write tool 之外的 shell 寫入及一個 workspace 外路徑，並非所有工具／任意路徑的證明；可寫的拋棄式 auth／scratch 目錄不是產品工作區。production 仍須最小化可寫掛載並避免暴露主機 socket／額外可寫 mount。

2026-09-30 code-simplify 檢查本次腳本增量：沿用既有 case 分支與拋棄式資源管理，沒有值得額外抽象的重複邏輯，維持 no-op；所有四份真實結果、語法與 JSON 靜態查核皆通過。

## 2026-09-30 Antigravity 1.2.13 新基線

使用者對固定模型選項回覆「1」，選定 `gemini-3.8-flash-low`。probe 開始前發現 binary 已由 1.2.12 更新為 1.2.13，SHA-256 `574b0234656c44564f3ff3b013225c86f14ae79b67d0f3b45f6aaf1b1625440e`；舊版本雜湊 guard 正確拒絕呼叫，改以新版本重測，不重用舊版本 capability。入口 [probe_agy.py](../../../scripts/issue-25/probe_agy.py) 固定 binary／model，以 memfd 提供唯讀 token，CLI 狀態放 tmpfs，使用 PID namespace 與拋棄式工作區。

新增反例：

- `--case short-timeout`，print-timeout 1ms：CLI exit 0、result SUCCESS、空 response，但 stderr 明確記錄 turn in progress／returning partial output。[fixture](probes/agy-1.2.13-timeout.json)。adapter 必須判斷 timeout 訊息及完整終止證據，不能只採 exit code／status；此案例沒有可保留的非空 partial，尚不證明文字 partial mapping。
- `--case auth-missing` 只移除指定 token，仍成功；即使加 `--isolated-home` 也成功。[原設定](probes/agy-1.2.13-auth.json)、[空 HOME](probes/agy-1.2.13-auth-isolated.json)。兩者是登入隔離反例，不能當成 auth-error fixture。
- 同時加入 `--isolated-home --isolated-ipc`，遮蔽 HOME、/tmp、/var/tmp、/run/user 並 unshare IPC：無 token 回報 exit 1、ERROR／authentication failed or timed out；帶 token 的相同設定回報 exit 0、SUCCESS／PROBE_OK。[無登入](probes/agy-1.2.13-auth-ipc.json)、[成功對照](probes/agy-1.2.13-smoke-ipc.json)。支持隱含登入與被遮蔽的主機狀態／IPC 相關；尚未定位單一來源，不將這個推論寫成確定的 keyring 或 server 行為。

初次 namespace 控制組的寫入內容為 `PROBE_OK\n`，不是 prompt 要求的精確字串；它足以證明檔案差異偵測器有效，但不宣告模型精確遵循 prompt。[寫入](probes/agy-1.2.13-write.json)、[唯讀](probes/agy-1.2.13-readonly.json)、[延遲寫入](probes/agy-1.2.13-delayed.json)、[取消](probes/agy-1.2.13-cancel.json)。取消時 child PGID 與 invocation 不同，PID namespace 中 SIGKILL 後十秒觀察 child 不存活、無 DELAYED；取消前 marker 不存在，ready age 0.09 秒。這些初次控制組沒有完整 HOME／IPC 隔離，另行重跑相同安全案例，不把兩組環境混為一個 production 基線。

所有本次 probe 只修改拋棄式狀態；未建立 production adapter。rate-limit、非空 partial、retry／resume、context window 及正式登入策略仍待驗。

### 2026-09-30 完整隔離安全對照

相同入口增加 `--isolated-home --isolated-ipc`，其餘固定 CLI／模型保持一致：

| case（均增加上述隔離旗標） | 直接結果 | 證據 |
|---|---|---|
| `--case write` | exit 0，marker = PROBE_OK 加換行；檔案副作用偵測器有效 | [write-ipc](probes/agy-1.2.13-write-ipc.json) |
| `--case write --readonly-workspace` | exit 0，完成回應包含唯讀拒絕，marker 不存在 | [readonly-ipc](probes/agy-1.2.13-readonly-ipc.json) |
| `--case delayed-control` | exit 0，Python child 延遲寫入 DELAYED | [delayed-ipc](probes/agy-1.2.13-delayed-ipc.json) |
| `--case cancel` | ready age 0.053 秒送 SIGKILL；child PGID 與主程序不同；exit -9，十秒內 child 不存活且無 DELAYED | [cancel-ipc](probes/agy-1.2.13-cancel-ipc.json) |

2026-09-30 對此具體方案詢問是否將 bwrap／完整 HOME 與 IPC 隔離的執行期部署前提擴及 Antigravity（I-TBD-11）；先前 I-TBD-10 僅明列 OMP，因此未推定擴及核准。缺 binary／namespace 時模型呼叫前 unavailable。production 登入存放與 refresh 策略仍需明確設計，probe 的拋棄式 token 並不代表正式登入管理已完成。

code-simplify 本次檢查只涉及新 probe：沿用直接 case 分支與拋棄式資源管理，沒有需新增抽象的重複邏輯，no-op。腳本 AST、15 份 agy JSON、14 份 OMP JSON、相對連結、核准 Gherkin 一致性與 git diff --check 通過。未執行產品 vet／race／build，未宣告 I1 全部完成。
