# sshm 規格書

> 狀態：2026-10-09 需求訪談定案；原型四項已驗證（見文末）。
> 本檔是 sshm 的唯一規格來源，實作與驗收都以此為準。

## 1. 定位

macOS 上的 SSH 機器管理 TUI，取代使用者在 iTerm2 Profiles 裡以 `login.exp` 管理的 49 台機器。
在 Orca terminal 中執行時，連線開成 Orca 新分頁；在其他 terminal（iTerm2 等）中執行時，原地連線。

- 語言：Go 1.24，Bubble Tea（`charmbracelet/bubbletea`、`bubbles`、`lipgloss`）。單一執行檔。
- 安裝：`go install github.com/mark22013333/sshm@latest`。
- 介面文字：繁體中文；技術名詞（host、port、tunnel、ssh）保留原文。
- 平台：先只做 macOS。程式結構預留跨平台（平台相關的程式碼集中在少數檔案）。
- 未來：上游 Orca plugin API 開放「面板呼叫 worker」後，另做 plugin 面板，沿用本檔第 3 節的資料格式。

## 2. 指令

| 指令 | 行為 |
|------|------|
| `sshm` | 開 TUI，「機器」頁 |
| `sshm <搜尋字>` | 開 TUI 並預填搜尋字；搜尋字與子指令同名時用 `sshm -- add` |
| `sshm add` | 直接開「新增機器」表單 |
| `sshm import-iterm` | 從 iTerm2 匯入（第 7 節） |
| `sshm export [--with-passwords] <檔案>` | 匯出 JSON，預設不含密碼 |
| `sshm import <檔案>` | 匯入 JSON（同 id 覆蓋前先確認） |
| `sshm zsh-widget` | 印出 Ctrl+O 的 zsh widget 設定，由使用者自行貼進 `.zshrc`；**程式不得自動修改 `.zshrc`** |

zsh widget 行為：在提示列按 Ctrl+O 開 sshm；提示列上已輸入的文字保留不動。

## 3. 資料

位置：`~/.config/sshm/hosts.json`（遵守 `XDG_CONFIG_HOME`）。檔案權限 `0600`，目錄 `0700`。
寫入一律原子化（寫暫存檔再 rename），並在 `hosts.json.lock` 上取 flock，鎖內「重讀→套用變更→寫回」，避免多個實例同時存檔遺失資料。
開檔時若權限比 0600／0700 寬就立即收緊。`hosts.json` 是 symlink 時寫到其目標，鎖檔放在目標旁（權限 0600）；懸空 symlink 時印出明確錯誤後結束，不自行建立目標。取鎖最多等 5 秒，逾時顯示「另一個 sshm 正在存檔」。rename 後 fsync 目錄。
編輯機器時在鎖內依 id 重讀，只套用表單中有改動的欄位；該 id 已被其他實例刪除則報錯，不得復活。另一實例在表單開啟後改了 host／port／user／auth，而本表單改了 password／identityFile（或反之）時，回報衝突並要求重開表單，不得靜默丟棄或把密碼配到別台。
連線前（組指令前）重新跑一次驗證，hosts.json 被手改出不合法內容時拒絕連線並顯示原因。
所有文字欄位（含密碼）拒絕 C0 控制字元與 DEL。
密碼以明碼存於此檔（使用者決策）。
驗證：`user`、`host` 不得以 `-` 開頭；`auth=key` 時 `identityFile` 必填；`auth` 改成非 `password` 時清除 `password`。

```json
{
  "version": 1,
  "groups": [
    { "name": "範例客戶-ACME", "color": "blue" }
  ],
  "hosts": [
    {
      "id": "h_<隨機>",
      "name": "範例客戶-PROD-VM-acme",
      "group": "範例客戶-ACME",
      "host": "10.0.0.24",
      "port": 22,
      "user": "helpdesk",
      "auth": "password",
      "password": "…",
      "identityFile": "",
      "color": "red",
      "extraArgs": "",
      "customCommand": ""
    }
  ],
  "tunnels": [
    {
      "id": "t_<隨機>",
      "name": "範例PROD DB",
      "hostId": "h_…",
      "rules": [
        { "type": "L", "bindPort": 13306, "targetHost": "127.0.0.1", "targetPort": 3306 },
        { "type": "D", "bindPort": 1080 }
      ]
    }
  ]
}
```

- `auth`：`password`（走 expect 腳本）、`key`（`-i identityFile`）、`none`（交給 ssh-agent／`~/.ssh/config`）。
- `color`：七色固定色盤 `red orange yellow green blue purple white`，對應 emoji `🔴🟠🟡🟢🔵🟣⚪`。機器的 `color` 為空時用群組色；群組也沒有就不顯示。
- `customCommand` 非空時，整個取代組出來的連線指令。
- `extraArgs`：一行文字，以 shell 規則拆分後附加在 ssh 參數裡（例 `-J jump -o ServerAliveInterval=30`）。
- tunnel 規則：`L`／`R` 需要 `bindPort`、`targetHost`、`targetPort`；`D` 只需 `bindPort`。`bindAddress` 可選，省略時使用 ssh 預設。
- 未知欄位讀入時保留、寫回時不丟（為未來的 plugin 預留）。

## 4. 連線

組指令的規則：
- `auth=password`：`<資料目錄>/sshm-login.exp <port> <user> <host> <password> [extraArgs…]`
- `auth=key`：`ssh -o StrictHostKeyChecking=accept-new -p <port> -i <identityFile> [extraArgs…] <user>@<host>`
- `auth=none`：同上但不帶 `-i`
- tunnel 另加 `-N -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -o ExitOnForwardFailure=yes` 與每條規則的 `-L/-R/-D`。

expect 腳本：程式內嵌 `assets/sshm-login.exp`，啟動時若資料目錄中的副本不存在或內容不同就寫出（權限 `0700`）。行為：
`spawn ssh -o StrictHostKeyChecking=accept-new -p <port> {*}<extra> -- <user>@<host>`，直接進 `interact` 並監看輸出。自動送密碼只在**登入階段**生效，同時符合以下條件才送、且只送一次：
- spawn 後 20 秒內（`SSHM_LOGIN_WINDOW` 可調）；
- 使用者尚未按過任何鍵（一按鍵就停止自動送，避免 su／mysql -p／再 ssh 到第三台的提示被填入）；
- 輸出靜止 0.8 秒後，緩衝區仍以密碼提示結尾（不分大小寫，容許尾隨空白與 ANSI 色碼），避免 MOTD 分段輸出時誤送；確認期間同時監看使用者輸入，一有按鍵就放棄；
- 提示必須指名**本次目標**：`<user>@<host>'s password:`、`(<user>@<host>) Password:`、`Password for <user>@<host>:` 三種格式之一，user／host 以字串比對（不當 regex）。不含 user@host 的 `Password:`、或指名其他主機的提示（例如 ProxyJump 的跳板機）一律不送、也不消耗「只送一次」的額度。
- 例外：出現非目標提示後，使用者回答它（到按下 Enter 為止）的按鍵不算「按過鍵」，以便跳板機手動輸入後，目標機仍能自動送。
- `~/.ssh/config` 的 `HostName` 改寫主機名稱時，提示中的 host 與 sshm 記錄的不同，會退化成手動輸入（不會誤送）。
- `SSHM_LOGIN_WINDOW` 必須是整數，否則用預設 20。

使用者按鍵從一開始就轉送。結束碼：ssh 正常結束時沿用其結束碼，被 signal 結束時回 128+n。key／none 的 ssh 指令同樣在目的地前加 `--`。
Orca CLI 參數一律用 `--title=<值>`、`--command=<值>` 形式，防止值以 `--` 開頭被當成旗標。

已知取捨（使用者決策，2026-10-09 兩度確認不改 askpass）：密碼出現在 Orca 新分頁的 `--command` 文字（畫面與 scrollback 可見；Orca 可能把 scrollback 存到磁碟，未實測）、expect 的 argv（`ps` 可見），未開 zsh `HIST_IGNORE_SPACE` 時也會進 history。macOS 內建 expect（Tcl 8.5）會把 emoji 等非 BMP 字元送錯，密碼不支援 emoji。

開在哪裡：
- 偵測 Orca：`TERM_PROGRAM=Orca` 且 `ORCA_CLI_BIN_DIR` 有值。
- **Enter**：在 Orca 中 → 執行 `$ORCA_CLI_BIN_DIR/orca terminal create --worktree active --title "<emoji> <名稱>" --command "<指令>" --focus --json`，成功後 TUI 結束；失敗則在 TUI 顯示錯誤，不改原地連線。不在 Orca 中 → 同 Alt+Enter。
- **Alt+Enter**：TUI 結束後以 `syscall.Exec` 原地執行連線指令。
- `--command` 是打進 shell 的文字：指令開頭加一個空白（搭配 zsh `HIST_IGNORE_SPACE` 不進 history），參數一律做 shell quoting。

## 5. 介面

兩頁：「機器」「Tunnel」，Tab 切換。

機器頁：
- 頂端搜尋框，開啟即取得焦點；比對名稱、host、user、群組（不分大小寫、子字串）。
- 清單依群組折疊，群組列顯示機器數；左側色條，名稱下方小字 `user@host:port`。
- 鍵：↑↓ 移動、Enter 連線（新分頁）、Alt+Enter 或 Ctrl+T 原地連線（Ctrl+T 為 Option 未設成 Alt 時的備援）、`ctrl+n` 新增、`ctrl+e` 編輯、`ctrl+d` 複製一台、`ctrl+x` 刪除（需二次確認）、←→ 折疊／展開群組、Esc 清空搜尋或離開。
- 表單欄位：名稱、群組（可選既有或輸入新的）、host、port（預設 22）、user、認證方式、密碼／金鑰檔、顏色、額外 ssh 參數、自訂指令。必填：名稱、host、user。

Tunnel 頁：
- 每條 tunnel 一列：狀態燈（● 執行中／○ 已停止）、名稱、機器名稱、規則摘要（例 `L 13306→127.0.0.1:3306`）。
- 鍵：Enter 啟動／停止、`ctrl+n` 新增、`ctrl+e` 編輯、`ctrl+x` 刪除。
- 啟動 = 開 Orca 專用分頁（標題 `⇄ <名稱>`），把回傳的 terminal handle 記錄在 `~/.config/sshm/state.json`。
- 狀態 = 以 handle 查 `orca terminal list --json` 的 `result.terminals[].handle` 是否存在（**不可用 title 判斷**，shell 會改寫 title）。
- 停止 = `orca terminal close --terminal <handle>`。不在 Orca 中時 tunnel 頁唯讀並提示。
- 不做自動重連。

## 6. 匯出／匯入

JSON 格式同第 3 節。匯出預設把每台的 `password` 清空，`--with-passwords` 才保留。匯入時同 id 的項目先列出差異再確認。

## 7. 從 iTerm2 匯入

- 讀法：`plutil -extract "New Bookmarks" json -o - ~/Library/Preferences/com.googlecode.iterm2.plist`（整份 plist 無法轉 JSON，必須只取這一段）。
- 只處理 `Initial Text` 含 `login.exp` 的 profile；以 shell 規則拆分，`login.exp` 之後 4 個參數依序為 port、user、host、password。
- 名稱取 profile 的 `Name`；群組取 `Tags`：單一 tag 直接用；多個 tag 在預覽畫面逐台挑選，預設第一個。
- 預覽畫面：列出全部候選，可逐台勾選（預設全選）；已存在同 host+port+user 的標為「重複」預設不勾。
- 處理過程中含密碼的資料只存在記憶體，不得寫入暫存檔。

## 8. 原型驗證紀錄（2026-10-09）

1. Orca 環境變數：`TERM_PROGRAM=Orca`、`ORCA_CLI_BIN_DIR=/Applications/Orca.app/Contents/Resources/bin`。✅
2. `orca terminal create --worktree active --title … --command … --json` 開分頁成功，回傳 `result.terminal.handle`；`terminal list --json` 以 handle 查得到、close 後消失；list 的 `title` 會被 shell 改寫。✅
3. expect 腳本以假 ssh 驗證：額外參數原樣傳遞、含空白與特殊字元的密碼完整送達。✅（真實 tunnel 未測）
4. `plutil -extract "New Bookmarks"` 成功；52 個 profile 中 49 個為 `login.exp` 標準 4 參數格式；10 個有多個 tag。✅
