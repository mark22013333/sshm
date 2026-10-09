# README 截圖：iTerm2 匯入預覽。
# 在 repo 根目錄執行：vhs docs/tapes/import-iterm.tape
# 讀的是 testdata 裡的合成 plist（--plist），不讀真正的 iTerm2 設定；最後按 Esc 取消、不寫入。
Output "docs/tapes/.out/import-iterm.gif"
Require go
Set Shell "bash"
Set FontFamily "Hack Nerd Font Mono"
Set FontSize 15
Set Width 1600
Set Height 600
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 50ms

Hide
Type ". docs/tapes/demo-env.sh && clear"
Enter
Sleep 12s
Type "sshm import-iterm --plist testdata/iterm/iterm2-fixture.plist"
Enter
Sleep 2s
Show
Sleep 500ms
Screenshot "docs/images/import-iterm.png"
Sleep 500ms

Hide
Escape
Sleep 500ms
