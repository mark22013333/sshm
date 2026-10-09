# sshm

macOS 上的 SSH 機器管理 TUI。按 Enter 在目前的 terminal 原地連線；在 Orca 裡按 Shift+Enter 會開新分頁連線。規格見 [docs/spec.md](docs/spec.md)。

## 安裝

```sh
go install github.com/mark22013333/sshm@latest
```

需要 Go 1.24 以上。密碼登入會用到 macOS 內建的 `/usr/bin/expect`。

## 使用

```sh
sshm              # 開機器清單
sshm tao          # 開清單並預填搜尋字
sshm add          # 直接開「新增機器」表單
sshm -- add       # 搜尋字剛好跟子指令同名時，加 -- 當成搜尋字
```

機器清單的按鍵：

| 鍵 | 動作 |
|----|------|
| ↑↓ | 移動 |
| Enter | 在目前的 terminal 原地連線 |
| Shift+Enter／Ctrl+T | 在 Orca 中開新分頁連線，不在 Orca 時改為原地連線（Orca 把 Shift+Enter 送成 ESC+Enter，等同 Alt+Enter；Cmd+Enter 不會送進 terminal，無效） |
| ←→ | 折疊／展開群組 |
| Ctrl+N／Ctrl+E／Ctrl+D／Ctrl+X | 新增／編輯／複製／刪除（刪除要按 y 確認） |
| Tab | 切換「機器」「Tunnel」頁 |
| Esc | 清空搜尋，再按一次離開 |

資料存在 `~/.config/sshm/hosts.json`（有設 `XDG_CONFIG_HOME` 時改用該目錄），檔案權限 0600、目錄 0700。同時開多個 sshm 也不會互相覆蓋：每次存檔都會先鎖檔（`hosts.json.lock`）、重讀最新內容再寫回；編輯時只套用你改動的欄位，若那台已被另一個 sshm 刪除會顯示錯誤。hosts.json 可以是 symlink，sshm 會寫到連結目標。

## 密碼自動輸入的規則

密碼登入由內嵌的 expect 腳本處理，只在「登入階段」對「這次要連的機器」自動送一次密碼：

- 提示必須指名這次的 user 與 host，支援 OpenSSH 的三種格式：`user@host's password:`、`(user@host) Password:`、`Password for user@host:`（不分大小寫，後面可接空白或色碼）。只有 `Password:` 而沒有 user@host 的提示一律不送。
- 跳板機（ProxyJump／ProxyCommand）的提示指名的是跳板機，不會送出；請自己輸入跳板機密碼，之後目標機的提示仍會自動送。
- 連線後 20 秒內、你還沒自己打字之前才會送。提示出現後要等輸出靜止 0.8 秒、確認仍停在提示上才送，這段期間你一按鍵就不送。
- 送過一次、超過 20 秒、或你已經開始自己打字之後，就不再比對，所以 `su`、`sudo`、`mysql -p`、從遠端再 ssh 到第三台時的密碼提示都不會被自動填入。密碼錯誤時的第二次提示也不會重送。
- 若 `~/.ssh/config` 用 `HostName` 改寫了主機名稱，提示上顯示的 host 會和 sshm 裡填的不同，這時不會自動送，請手動輸入（或把 sshm 的 host 改成提示上顯示的名稱）。

## 已知限制

- **密碼以明碼保存**：存在 hosts.json 裡，並以參數傳給 expect 腳本，連線期間同一使用者可用 `ps` 看到。
- **在 Orca 開新分頁時，密碼會出現在畫面上**：連線指令（含密碼）是打進新分頁 shell 的文字，會顯示在新分頁畫面與 scrollback 裡。
- **沒開 zsh 的 `HIST_IGNORE_SPACE` 時，連線指令（含密碼）會寫進 shell history**；分頁若是 bash，要另外設定 `HISTCONTROL=ignorespace`。
- **Orca 可能把 scrollback 存到磁碟**（依 Orca 原始碼推論，未實測），密碼可能因此留在磁碟上。
- **密碼不能含 emoji 等非 BMP 字元**：macOS 內建的 expect 是 Tcl 8.5，非 BMP 字元送出時會被重複編碼，導致密碼錯誤。中文等 BMP 字元正常。
- **新分頁的 shell 要是 POSIX 相容的 shell（zsh、bash、sh）**：連線指令用 POSIX 單引號規則跳脫。fish 對單引號內的 `\` 有不同解讀，密碼或參數含 `\`、`'` 時可能出錯。
- Tunnel 頁、iTerm2 匯入、JSON 匯出入尚未實作。
