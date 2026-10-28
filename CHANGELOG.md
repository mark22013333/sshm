# 版本紀錄

每一版新增與修正的重點。完整的變更見 [GitHub Releases](https://github.com/mark22013333/sshm/releases) 或 `git log`。

## v0.4.1（2026-10-28）

修正

- 密碼登入的機器不再先送 ssh-agent 裡的金鑰：agent 有 6 把以上金鑰時，伺服器會在密碼提示前以 Too many authentication failures 斷線。金鑰登入的機器改為只送指定的金鑰（IdentitiesOnly）。

## v0.4.0（2026-10-13）

新增

- TUI 新增「關於」頁（Tab 輪流切換「機器」→「Tunnel」→「關於」），列出目前版本與最新版本、hosts.json 等檔案的實際位置、各頁快捷鍵與 AI agent 用法摘要。
- 自我更新：啟動時在背景查 GitHub 最新釋出版（一天最多一次，失敗不顯示）；有新版時機器頁狀態列會提示，在「關於」頁按 U 下載、比對 SHA-256 後取代執行檔。開發版、目錄不可寫或非 macOS 時改列手動更新指令。

修正

- 更新途中被中斷而留在執行檔目錄的暫存檔，會在下次啟動或下次更新時清掉；替換執行檔後確保寫入落盤。
- 從「關於」頁切回機器頁時清掉狀態訊息，不再蓋住新版提示。

## v0.3.0（2026-10-13）

新增

- 給 AI agent 使用：`sshm agent enable／disable／list／guide` 子指令，用 sshm 存的密碼登入一次，替機器裝上專用金鑰並寫好 ssh 別名，之後 agent 只需 `ssh <別名>`、不碰密碼。
- 機器清單按 Ctrl+O 開通／停用選取的機器（按 y 確認），已開通的機器顯示 `🤖 別名`。

修正

- 密碼改經環境變數交給 expect 腳本，原地連線、Orca 分頁、Tunnel 與 agent 開通的行程參數都不再含密碼。
- 原地連線時，環境中殘留的同名變數會被新值取代，不再因讀到舊密碼而登入失敗。
- 開通給 agent 的安全性與可靠度：遠端 `authorized_keys` 以公鑰本體辨識自己那一行（不會誤刪別台電腦或手寫的行）、改為原子寫入；ssh 別名設定寫入前先做白名單驗證並用 `ssh -G` 解析；記錄公鑰裝過的位置，機器改了 host／port／user 後停用時連舊位置一起清除；同一台被兩個 sshm 同時開通時不留孤兒金鑰。
- 文件說明安全界線：確認關卡只防誤開通，不防刻意繞過。

## v0.2.0（2026-10-13）

新增

- 機器表單在認證方式為 password 時，可按 Ctrl+R 切換顯示／隱藏密碼（每次開表單預設隱藏）。

## v0.1.0（2026-10-10）

第一個公開版本。

- 機器清單 TUI：依群組折疊、打字即搜尋；Enter 原地連線，在 Orca 裡按 Shift+Enter／Ctrl+T 開新分頁連線。
- 密碼登入由內嵌 expect 腳本自動送一次密碼；提示必須指名本次的 user@host（帳號區分大小寫），主機欄位不接受 `@`。
- zsh 提示列按 Ctrl+O 叫出 sshm（`sshm zsh-widget`）。
- 從 iTerm2 匯入（`sshm import-iterm`）、JSON 匯出／匯入（`sshm export`／`sshm import`）。
- Tunnel 頁：管理 `-L`／`-R`／`-D` 轉送，在 Orca 分頁中啟動／停止並顯示狀態燈；Ctrl+G 前往該 tunnel 的分頁；ssh 結束後分頁 3 秒自動關閉。
- 以 GoReleaser 發佈 macOS（Apple Silicon／Intel）執行檔，可用 `curl` 一行安裝。
