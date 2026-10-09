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

## 從 iTerm2 匯入

```sh
sshm import-iterm                       # 讀 ~/Library/Preferences/com.googlecode.iterm2.plist
sshm import-iterm --plist <路徑>        # 改讀指定的 plist
```

- 只處理 Initial Text 含 `login.exp` 的 profile：依 POSIX sh 規則拆分（與 iTerm2 把文字打進 shell 後的結果一致），`login.exp` 之後、`;` 等指令分隔符號之前的 4 個參數依序是 port、user、host、password。名稱取 profile 的 Name，群組取 Tags。
- 參數裡有會被 shell 展開的寫法（未加單引號的 `$`、反引號、`$'…'`、萬用字元、字首的 `~`）或引號未閉合時，標為「無法匯入」，請改成單引號後再匯入或手動新增。
- 先進預覽畫面：預設全選；已有相同 host＋port＋user 的標「重複」、預設不勾；參數數量不對、port 不是數字、user／host 以 `-` 開頭或含控制字元的標「無法匯入」並說明原因（不顯示密碼）。
- 預覽鍵位：↑↓ 移動、空白 勾選、←→ 切換群組（有多個 tag 時，預設第一個）、a 全選、n 全不選、Enter 匯入、Esc 取消。新群組會自動建立、不設顏色。
- 完成後顯示匯入幾台、略過幾台。讀取過程只透過 `plutil -extract "New Bookmarks"` 的輸出在記憶體中處理，不寫任何暫存檔。

## 匯出／匯入 JSON

```sh
sshm export backup.json                     # 匯出（密碼清空）
sshm export --with-passwords backup.json    # 連密碼一起匯出，請妥善保管
sshm export --force backup.json             # 目的檔已存在時要加 --force 才覆蓋
sshm import backup.json                     # 匯入
```

- 格式與 hosts.json 相同，匯出檔權限 0600。目的檔是 symlink、或就是 sshm 的資料檔本身（含硬連結）時一律拒絕。
- 匯入時，新的機器、群組、Tunnel 直接加入；同 id（群組為同名）但內容不同的項目會先列出差異（密碼只顯示「密碼不同」），再選 y＝覆蓋、n＝只匯入新項目、其他＝取消。
- 匯入檔的密碼是空的（例如預設匯出的檔案）時：同一台的 host、port、user 都沒變才沿用現有密碼；任一有變就不沿用，差異清單會註明「密碼將沿用」或「密碼將清空」，匯入完成後列出需要補密碼的機器。
- 匯入檔裡只要有一項不合法（機器的 user／host 以 `-` 開頭或含控制字元、tunnel 規則不合法或指向不存在的機器、檔內 id 重複），整份都不匯入。未知欄位會原樣保留。
- 差異清單不顯示密碼、額外 ssh 參數、自訂指令的內容，只註明已變更。

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
- Tunnel 頁尚未實作。
