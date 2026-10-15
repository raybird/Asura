# Issue 47 實作計畫 - Release 發布者身分驗證

驗收規格與核准見 [README](./README.md)；風險與首要驗證只在 README 維護，本檔不複寫。

## 現況（2026-10-15 盤點）

- `install.sh`：下載 checksum manifest 與 archive，核對唯一項目與 SHA-256、archive 內容與型別、暫存 binary 的 `version --json` 後才替換。前置工具檢查都在下載前完成；v1.1.0 以前的版本直接拒絕
- `.github/workflows/release.yml`：tag push 或 workflow_dispatch（輸入 tag）觸發，`permissions: contents: write`；以 `scripts/release/build-archives.sh` 建置，`gh release create` 不帶 prerelease 旗標（含 `-` 的 tag 也會成為 Latest），上傳不覆寫既有 asset
- `scripts/installer/`：harness 以 PATH 前置的 curl／wget／uname stub 把 URL 對應到本地 fixture，驅動真實 `sh`；系統工具白名單不含 `gh`
- `docs/guides/setup.md`「信任邊界」明寫目前沒有簽章或 attestation 驗證

## 外部契約探測（2026-10-15，gh 2.74.2，以 cli/cli v2.74.2 的公開 attestation）

| 探測 | 結果 |
|---|---|
| `gh attestation verify gh_2.74.2_linux_amd64.tar.gz --repo cli/cli`（已登入） | exit 0 |
| 同上，未登入（空的 `GH_CONFIG_DIR`、無 token） | 失敗，要求 `gh auth login` |
| `gh attestation download` 取得 bundle 後，未登入加 `--bundle <file>` | exit 0 |
| 同上，archive 末端附加 1 byte | exit 1 |
| 加 `--signer-workflow cli/cli/.github/workflows/deployment.yml --source-ref refs/tags/v2.74.2` | exit 1：`expected SourceRepositoryRef to be refs/tags/v2.74.2, got refs/heads/trunk`（cli/cli 以 workflow_dispatch 從 trunk 發佈） |
| 改 `--source-ref refs/heads/trunk`，加 `--deny-self-hosted-runners` | exit 0 |
| 未登入加 `--bundle`，全新快取，以失效 proxy 阻斷網路 | exit 1：`tuf refresh failed: Get "https://tuf-repo-cdn.sigstore.dev/13.root.json"` |
| 未認證 `GET /repos/cli/cli/attestations/sha256:<digest>` | HTTP 200 |

結論：
- 驗證檔必須在本機（`--bundle`），installer 才能在未登入時驗證；因此驗證檔隨 Release 上傳。未認證 REST API 雖可取得，但需在 sh 中解析 JSON，不採用
- source ref 是 workflow 的觸發 ref，不是 checkout 的 ref：workflow_dispatch 從 `main` 觸發會簽出 `refs/heads/main`，導出 SCN-007 的 tag ref 檢查
- 驗證需連線 Sigstore 的 TUF CDN，導出 SCN-002「gh 無法完成驗證」列與文件限制

## 設計方案

### 機制取捨（Q1，2026-10-15）

| 方案 | 使用者端需求 | 金鑰管理 | 判定 |
|---|---|---|---|
| GitHub artifact attestation + `gh attestation verify --bundle` | gh（不需登入） | 無（keyless，身分來自 workflow OIDC） | 採用 |
| Sigstore cosign keyless 簽 checksum manifest | cosign（一般開發機比 gh 少見） | 無 | 未採用 |
| 自管金鑰（minisign／openssl），公鑰固定在 `install.sh` | minisign 或 openssl | 需保管與輪替；私鑰放 CI 時防護力與 keyless 相同 | 未採用 |

### Release workflow

- `permissions` 加 `id-token: write`、`attestations: write`
- 建置前檢查 `github.ref == refs/tags/$TAG`，不符時以錯誤訊息說明需以該 tag 為觸發 ref（`gh workflow run release.yml --ref <tag> -f tag=<tag>`）並失敗（SCN-007）
- 建置後以 `actions/attest@v4` 對 `dist/*.tar.gz` 產生 SLSA build provenance attestation，將輸出的 bundle 複製為 `dist/asura_<tag>_attestation.sigstore.json` 一併上傳。2026-10-15 查證：`actions/attest-build-provenance` 自 v4 起只是 `actions/attest` 的包裝，官方建議新實作直接使用後者（最新 v4.2.2）；`artifact-metadata: write` 只用於 registry storage record，本專案不需要
- `TAG` 含 `-` 時 `gh release create` 加 `--prerelease`
- workflow_dispatch 的 input 說明補上需以該 tag 為 ref 觸發

### installer

- 新旗標 `--require-attestation`，usage 同步更新
- 工具檢查階段判定「支援的 gh」：`gh attestation verify --help` 成功，且說明含 `--bundle`、`--signer-workflow`、`--source-ref`。不支援時記下原因（找不到 gh／gh 不支援）；加了 `--require-attestation` 則在此失敗（SCN-004）
- 版本判定：tag 恰為 `v1.1.0` 時免驗（D1）。指定 `--version v1.1.0` 加 `--require-attestation` 在下載前失敗；latest 解析為 v1.1.0 時在下載 archive 前失敗（SCN-005）
- 需驗證時另下載 `asura_<tag>_attestation.sigstore.json`，下載失敗即拒絕（SCN-002）
- checksum 與 archive 內容檢查後、取出 binary 前執行：

  ```sh
  gh attestation verify "$tmp/$asset" --bundle "$tmp/attestation.json" --repo raybird/Asura \
    --signer-workflow raybird/Asura/.github/workflows/release.yml \
    --source-ref "refs/tags/$tag" --deny-self-hosted-runners
  ```

  非零即拒絕，不退回只核對 checksum（SCN-002）
- 訊息：驗證通過時 stdout 註明已驗證發布者；未驗證的警告寫 stderr，前綴 `asura-install: warning:`，說明原因（SCN-003、SCN-005）

## 使用方式對照

| 情境 | 變更前 | 變更後 |
|---|---|---|
| `curl … \| sh`（有支援的 gh，新版本） | 只核對 checksum | 另驗證發布者，失敗即拒絕 |
| `curl … \| sh`（沒有 gh） | 只核對 checksum | 只核對 checksum，stderr 警告 |
| `curl … \| sh -s -- --require-attestation` | 不支援此旗標 | 沒有支援的 gh 或版本為 v1.1.0 時拒絕 |
| 維護者手動補發 | `gh workflow run release.yml -f tag=<tag>` | `gh workflow run release.yml --ref <tag> -f tag=<tag>`；其他 ref 在建置前失敗 |

## 實作步驟

1. ✅ **步驟 1：release.yml 與真實 prerelease 契約驗證**（SCN-006、SCN-007；首要驗證）
   - 產出：release.yml 修改並推送到 issue 分支；從該分支推送 tag `v1.1.1-rc.1`，執行真實 workflow。需修正重跑時遞增為 `rc.2`…，已發佈的 tag 與 prerelease 保留並記錄
   - 紅燈／對照：變更前的 v1.1.0 archive 以同一組身分鎖定驗證失敗（沒有 attestation）；以目前 `main` 的 release.yml 執行 `gh workflow run release.yml --ref main -f tag=v1.1.0`，確認沒有 ref 檢查、會進入建置（上傳因 asset 已存在而失敗，沒有副作用）
   - 相依：無
   - 完成判準：README「風險與首要驗證」的完成證據全部成立；Release 驗證檔含兩個 archive 的 subject；`gh workflow run release.yml --ref issue-47-release-attestation -f tag=v1.1.1-rc.1` 在建置前失敗，且沒有新增 asset 或 attestation；記錄支援所需旗標的最低 gh 版本來源
   - 完成證據（2026-10-15，release.yml 變更 commit `ed4e81f`，本機 gh 2.74.2）：
     - 紅燈 SCN-006：`gh attestation verify asura_v1.1.0_linux_amd64.tar.gz --repo raybird/Asura --signer-workflow raybird/Asura/.github/workflows/release.yml --source-ref refs/tags/v1.1.0 --deny-self-hosted-runners` exit 1，`HTTP 404 … /attestations/sha256:9bee57eb…`（v1.1.0 沒有 attestation）
     - 紅燈 SCN-007：變更前 `main` 的 release.yml 以 `gh workflow run release.yml --ref main -f tag=v1.1.0` 觸發 [run 38696913383](https://github.com/raybird/Asura/actions/runs/38696913383)：Checkout、Build archives 皆 success（沒有 ref 檢查），Upload 以 `asset under the same name already exists` 失敗；v1.1.0 三個 asset 的 updatedAt 仍為 2026-10-09T23:55:40Z
     - 綠燈 SCN-006：tag `v1.1.1-rc.1`（指向 `ed4e81f`）觸發 [run 38697033205](https://github.com/raybird/Asura/actions/runs/38697033205) success；Release `isPrerelease=true`，`releases/latest` 仍為 v1.1.0；asset 為兩個 archive、checksums 與 `asura_v1.1.1-rc.1_attestation.sigstore.json`。驗證檔 predicateType `https://slsa.dev/provenance/v1`，subject 為 amd64（`9ac7ff32…`）與 arm64（`5c424de5…`），與下載檔 sha256 相同
     - 未登入驗證（空 `GH_CONFIG_DIR`、無 token，`gh auth status` 顯示未登入）：`gh attestation verify <archive> --bundle asura_v1.1.1-rc.1_attestation.sigstore.json --repo raybird/Asura --signer-workflow raybird/Asura/.github/workflows/release.yml --source-ref refs/tags/v1.1.1-rc.1 --deny-self-hosted-runners`，amd64、arm64 皆 exit 0
     - 對照組（各只改一個變數）：archive 末端附加 1 byte → exit 1；`--source-ref refs/tags/v1.1.1-rc.2` → exit 1（`expected SourceRepositoryRef to be refs/tags/v1.1.1-rc.2, got refs/tags/v1.1.1-rc.1`）；`--signer-workflow …/ci.yml` → exit 1
     - 綠燈 SCN-007：`gh workflow run release.yml --ref issue-47-release-attestation -f tag=v1.1.1-rc.1` 觸發 [run 38697274131](https://github.com/raybird/Asura/actions/runs/38697274131)：Require tag ref 失敗（`release must be triggered from refs/tags/v1.1.1-rc.1 (got refs/heads/issue-47-release-attestation)`），其後步驟全部 skipped；amd64 digest 的 attestation 數量前後皆為 1，rc asset 的 updatedAt 未變
     - 最低 gh 版本：`attestation` 指令與 `--bundle` 始於 v2.49.0、`--signer-workflow` 始於 v2.51.0、`--source-ref` 始於 v2.68.0（2025-03-05，cli/cli PR #10308），以各版 release notes 為來源。installer 以 `--help` 是否列出所需旗標判定，不寫死版本號
2. ✅ **步驟 2：installer 驗證流程與 stub 測試**（SCN-001～SCN-005）
   - 產出：`install.sh` 修改；harness 加 gh stub（記錄參數，可設為通過、失敗、不支援 attestation）與驗證檔路由；新增 `scripts/installer/install_attestation_test.go`
   - 紅燈：新測試對現行 `install.sh` 因缺少目標行為而失敗
   - 相依：步驟 1（驗證檔名稱與 gh 參數以真實結果為準）
   - 完成判準：
     - SCN-001：以字面期望值斷言 gh 收到的 repo、signer workflow、`refs/tags/<tag>`、`--deny-self-hosted-runners`，且 `--bundle` 指向的內容等於發佈的驗證檔；驗證發生在 checksum 之後、替換之前
     - SCN-002：四種狀況皆非零，sentinel 顯示舊 binary 未變、暫存已清除
     - SCN-003：找不到 gh、gh 不支援兩種狀態皆完成安裝，stderr 含警告與對應原因
     - SCN-004：非零，且 stub 紀錄中沒有 archive 下載請求
     - SCN-005：三列各自成立，前兩列沒有驗證檔下載請求
     - 既有 installer 測試全綠；既有斷言因核准的新行為（警告、usage 文字）需調整時，逐項記錄理由
   - 完成證據（2026-10-15）：
     - 紅燈：新測試對未修改的 `install.sh` 執行 `go test -count=1 -run 'TestAttestation' -v ./scripts/installer/`，exit 1。TestAttestationVerifiedInstall 為 `gh verify not called`；TestAttestationFailureKeepsExistingBinary 兩種狀況皆 `code=0` 並完成安裝；TestAttestationUnavailableGhWarns 三種狀態 stderr 都沒有警告；TestAttestationRequiredWithoutGh 與 TestAttestationV110 的旗標情境為 usage error（exit 2），v1.1.0 情境沒有警告。TestAttestationAfterChecksum 在舊程式也通過，它是「checksum 不符時不呼叫 gh」的順序保護，不計為目標行為紅燈
     - 紅燈（workflow 契約）：`TestReleaseWorkflowUsesContract` 新增驗證檔名稱、`actions/attest@v4` 與 tag ref 檢查三項斷言；換回 `8f4b56f` 的 release.yml 執行為 FAIL（`release.yml missing uses: actions/attest@v4`），還原後 ok
     - 綠燈：`install.sh` 修改後同一命令 6 項全部 PASS；`go test -count=1 -v ./scripts/...` 的 installer 與 release 兩個 package 皆 ok，既有 16 項 installer／release 測試全部 PASS，未調整任何既有斷言（usage 斷言為前綴比對，新增旗標後仍成立）
     - gh 參數以字面字串斷言；受驗 archive 與 `--bundle` 的內容由 stub 複製後與發佈的 fixture 比對，不由測試重組 installer 的指令
     - 精煉 no-op：新增的是一段工具偵測、一個版本判定與一個驗證呼叫，沿用既有 `have`／`err`／`fetch` 寫法，沒有重複或可再簡化的結構
3. ✅ **步驟 3：以真實 prerelease 端對端驗證新 installer**（SCN-001、SCN-002、SCN-003、SCN-005）
   - 產出：命令、exit code 與輸出摘要寫回本步驟
   - 相依：步驟 1、2
   - 完成判準：
     - 本分支 `install.sh` 以真實 gh、未登入安裝 `--version v1.1.1-rc.1` 成功，`asura version` 回報該 tag，輸出註明已驗證發布者
     - 攻擊模擬：archive 換成另一份內容不同的合法建置、manifest 同步改寫使 checksum 相符、驗證檔用真實的；真實 gh 驗證失敗、拒絕安裝、舊 binary 不變
     - PATH 沒有 gh 時安裝 rc 成功並警告；預設 latest 仍安裝 v1.1.0 並提示沒有發布者驗證
   - 完成證據（2026-10-15，本分支 `install.sh`、本機 gh 2.74.2；未登入指空的 `GH_CONFIG_DIR`、無 token，`gh auth status` 確認未登入）：
     - 真實安裝：`sh install.sh --version v1.1.1-rc.1 --install-dir <dir>` exit 0，輸出 `Verified publisher attestation for asura_v1.1.1-rc.1_linux_amd64.tar.gz`，`asura version` 為 `asura v1.1.1-rc.1 (commit ed4e81f268be15f40d41f8398caa526115271060, linux/amd64)`
     - 攻擊模擬：以 `build-archives.sh v1.1.1-rc.1 attacker0000` 建出內容不同的 archive，連同它自己的 checksum manifest（`sha256sum -c` 為 OK）與 Release 上真實的驗證檔，由 PATH 前置的 curl stub 提供；真實 gh 驗證失敗，installer exit 1（`publisher attestation verification failed for asura_v1.1.1-rc.1_linux_amd64.tar.gz: Error: verifying with issuer "sigstore.dev"`），舊 binary 的 sha256 不變、安裝目錄沒有殘留
     - 連不到 Sigstore：同上以 stub 提供真實的 rc 檔案，gh 走失效 proxy、全新快取；exit 1（`… tuf refresh failed: Get "https://tuf-repo-cdn.sigstore.dev/13.root.json" …`），舊 binary 不變。對照組只拿掉失效 proxy，驗證通過並完成安裝
     - PATH 沒有 gh：安裝 rc exit 0，stderr `asura-install: warning: publisher not verified (gh not found); only the SHA-256 checksum was checked`；同條件加 `--require-attestation` exit 1（`--require-attestation: gh not found`），安裝目錄未建立
     - 預設 latest（有 gh）：安裝 v1.1.0 exit 0，stderr `publisher not verified (v1.1.0 has no publisher attestation)`
     - 重現腳本：本機 scratchpad 的 `e2e.sh`（不入版控），命令即上列各項
4. ✅ **步驟 4：常青文件**（SCN-001～SCN-007 的使用者與維護者說明）
   - 產出：`docs/guides/setup.md` 改寫信任邊界與驗證行為，補 `--require-attestation`、gh 版本需求、需連線 Sigstore、防護範圍不含 repo 被改寫，更新最後驗證日期；`CHANGELOG.md` Unreleased 新增條目
   - 相依：步驟 2、3
   - 完成判準：人工逐一核對每個 Scenario 在文件中有一致的敘述；README「版本範圍」表不動（project.md 規定發佈時才更新）
   - 完成證據（2026-10-15，人工核對）：
     - `docs/guides/setup.md` 新增「發布者驗證」一節：表格四列對應 SCN-001（有 gh 時驗證與身分鎖定）、SCN-002（失敗或缺驗證檔即拒絕、不退回）、SCN-003（沒有或太舊的 gh 警告後安裝）、SCN-004（`--require-attestation`）、SCN-005（v1.1.0 免驗與 prerelease 不免驗）；限制列出 Sigstore 連線（SCN-002 第四列）與防護範圍；「替換前的檢查」加入 attestation 一步；最後驗證日期改為 2026-10-15 並連到本 issue
     - SCN-006、SCN-007 屬維護者流程：驗證檔名稱寫在 setup.md，prerelease 與 tag ref 檢查寫在 `CHANGELOG.md` Unreleased 與 release.yml 的 workflow_dispatch input 說明
     - README「版本範圍」表未改
5. 📝 **步驟 5：回歸與交付驗證**
   - 產出：本機最小回歸結果與 SKIP 清單；PR CI 一次全綠
   - 相依：步驟 1～4
   - 完成判準：`go vet ./...`、`go test -count=1 -race -v ./...`、`go build ./cmd/asura` 全數通過；SKIP 項目逐一列出、不計為通過
   - 完成證據（2026-10-15，本機，含步驟 2～4 的工作區內容）：
     - `go vet ./...` 通過；`go test -count=1 -race -v ./...` exit 0，23 個 package 全部 `ok`、0 FAIL；`go build ./cmd/asura` 成功
     - 被 SKIP 的 14 項不計為通過，都是需要固定版本 runtime 或登入憑證的 live／contract 測試，與本次修改無關：TestLiveAdapter（2 處）、TestCodexProbe_BinaryVersion、TestCodexProbe_NonInteractive_JSONL、TestCodexProbe_ReadOnlySandbox、TestCodexProbe_WorkspaceWriteSandbox、TestCodexProbe_ContextCancellation、TestAdapter_Run_Live、TestCodexProbe_Live、TestDaemon_ThreeFace_HostileWorkspaceConfig_CannotEscapeReadOnly、TestUtilityLiveProbe、TestUtilityCapacityProbe、TestINT001、TestINT007
     - PR CI：待 PR 建立後回填

## 測試策略與驗收對應

| 驗收 | 層級 | 證據 |
|---|---|---|
| SCN-001～SCN-005 | installer 整合測試：真實 `sh` 搭配 curl／wget／uname／gh stub | 步驟 2 紅綠燈 |
| SCN-001～SCN-003、SCN-005 | 真實 Release 與真實 gh 的端對端 | 步驟 3 |
| SCN-006、SCN-007 | 真實 GitHub Actions 執行，無本機等價層 | 步驟 1 的 run 連結、驗證輸出與對照組 |

- 單迴圈理由：installer 是單一 sh 腳本，harness 以真實 `sh` 驅動即外部行為層，沒有獨立的內部單元；gh stub 只取代外部驗證程式，因此加上步驟 3 的真實 gh 驗證補足密碼學驗證本身
- 避免同義反覆：gh 參數以字面字串斷言，不由測試重組 installer 的指令
- gh stub 通過不代表簽章有效；簽章有效性只以步驟 1、3 的真實驗證為證據

## 檢查清單

- [ ] 步驟 1 的真實 workflow、對照組與 SCN-007 失敗皆有紀錄
- [ ] SCN-001～SCN-007 都有對應證據
- [ ] `build-archives.sh`、archive 結構、checksum 與版本核對流程未改變
- [ ] setup.md 與 CHANGELOG 已更新
- [ ] 本機最小回歸與 PR CI 通過
