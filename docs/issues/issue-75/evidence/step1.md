# 步驟 1 證據：真實 OpenCode run 在 bwrap 內的可寫路徑

- 日期：2026-10-29
- OpenCode：1.18.31（`~/.nvm/versions/node/v22.20.0/bin/opencode`），模型 `openai/gpt-5.6-sol`（OAuth 登入）
- bwrap：0.11.1；kernel 7.0.0-38-generic
- 工作目錄：`~/.cache/asura-75-spike/ws`（git repo），同層目錄 `~/.cache/asura-75-spike/sibling`
- 環境：`OPENCODE_PERMISSION` 為 `DefaultApprovedPermissionPolicy`，`OPENCODE_DISABLE_AUTOUPDATE=1`，`--auto`

## 1. 唯讀 HOME（Q1 原方案）

```
bwrap --unshare-pid --unshare-ipc --die-with-parent --ro-bind / / --dev /dev --proc /proc --tmpfs /tmp \
      --bind ~/.local/share/opencode ~/.local/share/opencode --bind ~/.local/state/opencode ~/.local/state/opencode \
      --bind $WS $WS --chdir $WS -- opencode run --format json --dir $WS --auto "<prompt>"
```

prompt 要模型以 bash 執行 `echo in > $WS/inside.txt && echo tmp > /tmp/asura75-tmp.txt && echo out > $SIB/outside.txt`。

| 執行 | 結果 |
|---|---|
| 01:52Z，不帶 `--print-logs` | 240 秒逾時，沒有輸出。log 停在 instance `init` 之後的 `Failed to fetch models.dev ... EROFS ... ~/.cache/opencode/models.json.*.tmp` |
| `Reply with the single word PONG.`，DEBUG log | exit 0，輸出 `PONG` |
| 同 01:52 的 prompt，DEBUG log | exit 0。`inside.txt` 存在；bash 工具輸出 `/bin/bash: line 1: .../sibling/outside.txt: Read-only file system`；主機 `/tmp/asura75-tmp.txt` 不存在；log 有 `server unavailable key=serena`、`key=mcp-memory-libsql` |

`opencode mcp list` 對照（不呼叫模型）：

| 環境 | serena（`uvx`） | mcp-memory-libsql（`npx`） |
|---|---|---|
| 主機 | connected | connected |
| 唯讀 HOME 沙箱 | 列出但沒有 connected | failed：`MCP error -32000: Connection closed` |

結論：邊界有效，但使用者設定的 MCP server 在唯讀 HOME 下起不來，`models.json` 也更新不了。已向使用者回報，Q5 改為 HOME 暫時 overlay。

## 2. HOME 暫時 overlay（Q5 採用）

在 `--tmpfs /tmp` 之後、data／state bind 之前加 `--overlay-src $HOME --tmp-overlay $HOME`。

不呼叫模型的檢查：沙箱內 `echo x > $HOME/asura75-overlay.txt`、`echo y > $SIB/o.txt` 都成功；`opencode mcp list` 三個 server（gitnexus、serena、mcp-memory-libsql）都是 connected；結束後主機上兩個檔案都不存在。

真實執行，prompt 要模型以 bash 執行 `echo in > $WS/inside.txt && echo tmp > /tmp/asura75-tmp.txt && echo home > $HOME/asura75-home.txt && echo out > $SIB/outside.txt && echo etc > /etc/asura75-etc.txt`：

| 執行 | 結果 |
|---|---|
| 09:35Z，`--print-logs`（INFO） | 240 秒逾時，沒有輸出，主機上沒有任何檔案。log 停在 instance `init`，60 秒後只多一行 `cleanup prune=7.days` |
| 09:39Z，DEBUG log | exit 0，約 15 秒。session `ses_e78f847c0ffeilzA5Vbqee1jVV` 建立；log 沒有 EROFS，也沒有 `server unavailable` |

## 3. 逾時的對照

兩次逾時都停在同一個位置（instance `init` 之後、建立 session 之前），都是閒置數小時後的第一次執行。接著的對照組（不帶 `--print-logs`）：

| 命令 | 沙箱（overlay） | 主機 |
|---|---|---|
| `Reply with the single word PONG.` ×2 | exit 0，6 秒、6 秒 | exit 0，7 秒、8 秒 |
| `OPENCODE_PERMISSION` + `--auto`，bash 只寫 workspace ×2 | exit 0，9 秒、9 秒；檔案內容正確 | exit 0，10 秒、15 秒；檔案內容正確 |

沙箱內共 10 次、逾時 2 次；主機 4 次都正常，但主機沒有一次是冷啟動，無法判定逾時與沙箱有關。記入 README TBD-3，步驟 5 的 live 執行繼續觀察。

## 結論

- 寫回主機的路徑只需要 workspace 與 OpenCode 的 data、state 目錄，不需要擴大 Q1／Q5 的範圍。
- 照計畫進行，設計方案改為 HOME 暫時 overlay。
- 主機上的探測檔案（`/tmp/asura75-tmp.txt`、`~/asura75-home.txt`、`~/asura75-overlay.txt`、`/etc/asura75-etc.txt`、同層目錄內的檔案）都不存在；`~/.cache/asura-75-spike` 在記錄後刪除。
