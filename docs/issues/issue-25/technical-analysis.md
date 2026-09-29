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

### 能力矩陣狀態

| 能力 | OMP | Antigravity 1.2.12 |
|---|---|---|
| non-interactive／events／cwd／prompt 安全傳遞 | 未驗證，unsupported | 未驗證，unsupported |
| workspace write／read-only sandbox | 未驗證，unsupported | 未驗證，unsupported |
| process cancellation／無延遲副作用 | 未驗證，unsupported | 未驗證，unsupported |
| auth／rate-limit／partial／terminal result | 未驗證，unsupported | 未驗證，unsupported |
| retry／resume／reconnect 可控 | 未驗證，unsupported | 未驗證，unsupported |

以上 unsupported 是尚未證明的保守狀態，不是宣稱 CLI 本身沒有能力。未執行帶模型或權限副作用的 live probe。下一個必要條件是能取得固定 OMP binary、允許隔離探測及存取指定 provider 的執行環境；不能以放寬權限或未隔離的實際工作區代替。
