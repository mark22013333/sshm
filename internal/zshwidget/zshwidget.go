// Package zshwidget 產生 Ctrl+O 開啟 sshm 的 zsh 設定片段。
package zshwidget

// Script 是 `sshm zsh-widget` 印出的內容，由使用者自行貼進 ~/.zshrc。
const Script = `# sshm：在提示列按 Ctrl+O 開啟 sshm，提示列上已輸入的文字保留不動。
# 建議一併開啟 HIST_IGNORE_SPACE，讓 sshm 開頭帶空白的連線指令不進 history：
# setopt HIST_IGNORE_SPACE
_sshm_widget() {
  zle -I
  command sshm </dev/tty
  zle reset-prompt
}
zle -N _sshm_widget
bindkey '^O' _sshm_widget
`
