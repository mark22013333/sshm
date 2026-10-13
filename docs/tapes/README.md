# README 截圖的重製方式

`docs/images/` 的 PNG 都由這裡的 [VHS](https://github.com/charmbracelet/vhs) tape 產生，資料全部是虛構的。

## 需要

- `brew install vhs`（會一起裝 ttyd；另需 ffmpeg）
- Go（tape 會先 `go build` 出 sshm）
- 字型 Hack Nerd Font Mono；中文會由系統字型補上

## 重製

在 repo 根目錄執行：

```sh
vhs docs/tapes/sshm.tape          # host-list、search、host-form、tunnel-page、tunnel-form、about-page
vhs docs/tapes/import-iterm.tape  # import-iterm
```

錄製時順便產生的 GIF 放在 `docs/tapes/.out/`（已 gitignore）。

## 展示資料怎麼來

每個 tape 開頭（隱藏畫面）會執行 `. docs/tapes/demo-env.sh`，它會：

1. 用 `mktemp -d` 在 `/tmp` 建一個全新的暫存目錄（解析成 `/private/tmp/sshm-demo.XXXXXX`，讓「關於」頁顯示的路徑不含使用者名稱），把 `XDG_CONFIG_HOME` 指過去，`go build` 之後再把 `HOME` 換成其中的 `home/`，所以不會讀寫你的 `~/.config/sshm` 與 `~/.ssh`。
2. 複製 `demo/hosts.json`（虛構的 4 個群組、10 台機器、3 條 tunnel，密碼一律 `demo-only`）與 `demo/state.json`（記錄「ACME PROD DB」的 handle）。hosts.json 裡 acme-prod-web 與 qa-sandbox 帶有展示用的 `agent` 欄位（清單顯示 `🤖 別名`），金鑰路徑的 `__DEMO_HOME__` 會換成臨時 HOME；沒有真的產生金鑰或開通任何機器。
3. 把假的 `demo/orca` 放進 `ORCA_CLI_BIN_DIR`，並設 `TERM_PROGRAM=Orca`，讓 sshm 以為在 Orca 中。假 orca 對 `terminal list` 回傳含上述 handle、`hostScope` 完整的 JSON，所以 Tunnel 頁會顯示一個 ● 與兩個 ○；其他指令一律回 `{"ok":true}`，呼叫紀錄寫在暫存目錄的 `orca-calls.log`。
4. `go build` 出 sshm（以 `git describe --tags --abbrev=0` 的 tag 當版本號）並放到 `PATH` 最前面，再預寫當天的 `update-check.json`，所以「關於」頁顯示「已是最新版」，錄製時不連 GitHub。

iTerm2 匯入畫面讀的是 `testdata/iterm/iterm2-fixture.plist`（合成資料），最後按 Esc 取消、不寫入。

tape 全程不在機器清單按 Enter、不在 Tunnel 頁按 Enter，不會觸發任何 ssh。

## 注意

- VHS 的 `Screenshot` 是在下一個「有錄影」的畫格才擷取，所以每個 `Screenshot` 前後都要在 `Show` 狀態下留一點 `Sleep`，否則會拍到後面的畫面。
- 修改展示資料時，host 只用 `10.0.0.x`、`192.168.1.x` 或 `example.com` 子網域，不要放真實機器資料，這個 repo 是公開的。
